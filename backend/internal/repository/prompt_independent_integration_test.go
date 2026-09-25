//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func independentPromptFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	var config *pgx.ConnConfig
	connection, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	require.NoError(t, connection.Raw(func(driver any) error {
		connection, ok := driver.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("expected PostgreSQL fixture connection")
		}
		config = connection.Conn().Config().Copy()
		return nil
	}))
	require.NoError(t, connection.Close())
	schema := pgx.Identifier{fmt.Sprintf("prompt_core_%d", time.Now().UnixNano())}.Sanitize()
	_, err = integrationDB.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	config.RuntimeParams = maps.Clone(config.RuntimeParams)
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["search_path"] = schema + ", public"
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	for _, table := range []string{"system_prompt_templates", "system_prompt_template_versions", "system_prompt_runtime", "system_prompt_rule_policies", "system_prompt_skill_runtime", "system_prompt_skill_bundle_versions", "system_prompt_skill_prompt_versions"} {
		quoted := pgx.Identifier{table}.Sanitize()
		_, err := db.ExecContext(ctx, "CREATE TABLE "+schema+"."+quoted+" (LIKE public."+quoted+" INCLUDING ALL)")
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE accounts (id BIGINT PRIMARY KEY, extra JSONB, deleted_at TIMESTAMPTZ);
		CREATE TRIGGER fixture_prompt_immutable BEFORE UPDATE ON system_prompt_template_versions FOR EACH ROW EXECUTE FUNCTION public.protect_system_prompt_version_content();
		INSERT INTO system_prompt_runtime (id, enabled, expose_server_prompt, compact_enabled, revision) VALUES (1, TRUE, TRUE, TRUE, 7);
		INSERT INTO system_prompt_skill_runtime (id, revision) VALUES (1, 1)`)
	require.NoError(t, err)
	raw, err := migrations.FS.ReadFile("256_prompt_independent_content.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(raw))
	require.NoError(t, err)
	return db
}

type independentFixtureFiles struct {
	service.RemoteSkillRegistryFiles
	fail  bool
	loads int
}

func (f *independentFixtureFiles) LoadCandidate(ctx context.Context, version service.RemoteSkillBundleVersion, prompt service.RemoteSkillPromptVersion, changes []service.RemoteSkillFileChange) (service.RemoteSkillCandidate, error) {
	f.loads++
	if f.fail {
		return service.RemoteSkillCandidate{}, errors.New("fixture missing current file")
	}
	return f.RemoteSkillRegistryFiles.LoadCandidate(ctx, version, prompt, changes)
}

func TestPromptIndependentMigrationTransactionHistoryAndFrozenFilesIntegration(t *testing.T) {
	ctx := context.Background()
	db := independentPromptFixtureDB(t)
	repo := &businessSystemPromptRepository{db: db}
	files := &independentFixtureFiles{RemoteSkillRegistryFiles: service.NewRemoteSkillRegistryFilesystem(t.TempDir())}
	// Local, embedded test fixture only. Production startup never calls either
	// seed method: it reads the previously persisted active publication.
	seed, err := files.LoadSeed(ctx)
	require.NoError(t, err)
	require.NoError(t, files.InstallCandidate(ctx, seed))
	registry := &remoteSkillRegistryRepository{db: db}
	_, err = registry.EnsureRemoteSkillSeed(ctx, seed)
	require.NoError(t, err)
	structured := `{"blocks":[{"type":"text","text":"first","enabled":true,"cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"disabled","enabled":false}],"expansion_prompt":"literal {fp}"}`
	bodies := []string{"  ordinary managed text\nexact bytes  ", structured, "archived hybrid scaffold must never become active"}
	modes := []string{"inline", nativeapi.PromptContentAnthropicSystemBlocks, service.BusinessSystemPromptCompositionCodexSkillHybrid}
	policy := nativeapi.PromptRulePolicy{Version: 2, DefaultRuleIDs: []string{"plain", "structured", "hybrid"}}
	oldVersions := make([]int64, 3)
	for index, id := range policy.DefaultRuleIDs {
		var template, version int64
		managed := any(nil)
		if index == 0 {
			managed = "github-seed"
		}
		if index == 2 {
			managed = service.BusinessSystemPromptManagedSourceRemoteSkill
		}
		require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO system_prompt_templates (slug, name, managed_source) VALUES ($1, $1, $2) RETURNING id`, id, managed).Scan(&template))
		hash, size, err := service.ValidateBusinessSystemPromptBody(bodies[index])
		require.NoError(t, err)
		bundle := any(nil)
		if index == 2 {
			bundle = service.BusinessSystemPromptRemoteSkillBundleID
		}
		require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO system_prompt_template_versions (template_id,version,body,sha256,byte_length,composition_mode,bundle_id) VALUES ($1,1,$2,$3,$4,$5,$6) RETURNING id`, template, bodies[index], hash, size, modes[index], bundle).Scan(&version))
		oldVersions[index] = version
		rule := promptConfigFixtureRule(id, template, version)
		rule.Order = (index + 1) * 100
		if index == 1 {
			rule.Platforms = []string{"anthropic"}
			rule.AccountTypes = []string{"oauth"}
			rule.RequestProfiles = []string{"generic-mimic"}
			rule.ExcludeModelContains = []string{"fable"}
		}
		policy.Rules = append(policy.Rules, rule)
	}
	raw, err := json.Marshal(policy)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO system_prompt_rule_policies (id,policy) VALUES (1,$1::jsonb);`, string(raw))
	require.NoError(t, err)
	binding := `{"prompt_skills":{"mode":"custom","rule_ids":["hybrid","structured"]},"unrelated":"keep"}`
	_, err = db.ExecContext(ctx, `INSERT INTO accounts (id,extra) VALUES (123,$1::jsonb)`, binding)
	require.NoError(t, err)
	files.fail = true
	_, err = repo.InitializeIndependentPrompts(ctx, files)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptBundleUnavailable)
	var revision, templates, markers int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT revision FROM system_prompt_runtime WHERE id=1`).Scan(&revision))
	require.Equal(t, 7, revision)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM system_prompt_templates`).Scan(&templates))
	require.Equal(t, 3, templates)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM system_prompt_independent_state`).Scan(&markers))
	require.Zero(t, markers)
	files.fail = false
	frozen, err := repo.InitializeIndependentPrompts(ctx, files)
	require.NoError(t, err)
	require.Equal(t, seed.EffectiveFiles, frozen)
	loaded, err := repo.LoadBusinessSystemPromptRules(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(8), loaded.Revision)
	require.True(t, loaded.Enabled && loaded.ExposeServerPrompt && loaded.CompactEnabled)
	for index, rule := range loaded.RulePolicy.Rules {
		original := policy.Rules[index]
		require.NotEqual(t, original.TemplateID, rule.TemplateID)
		require.NotEqual(t, original.VersionID, rule.VersionID)
		original.TemplateID, original.VersionID = rule.TemplateID, rule.VersionID
		require.Equal(t, original, rule, "identity, order and all scope must be unchanged")
	}
	require.Equal(t, policy.DefaultRuleIDs, loaded.RulePolicy.DefaultRuleIDs)
	var preserved string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT extra::text FROM accounts WHERE id=123`).Scan(&preserved))
	require.JSONEq(t, binding, preserved)
	again, err := repo.InitializeIndependentPrompts(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, frozen, again)
	require.Equal(t, 2, files.loads, "completed migration never consults the former registry/files again")
	coreFiles := service.ProvideFrozenPromptFiles(repo, nil)
	prompts := service.NewBusinessSystemPromptService(repo, nil)
	prompts.SetFrozenPromptFiles(coreFiles)
	require.NoError(t, prompts.Initialize(ctx))
	state, err := prompts.PromptConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, bodies[0], state.Contents["plain"].Body)
	require.Equal(t, structured, state.Contents["structured"].Body)
	require.Equal(t, seed.Prompt.EffectiveBody, state.Contents["hybrid"].Body)
	for _, content := range state.Contents {
		require.False(t, content.Managed)
	}
	for _, path := range []string{"SKILL.md", "RULES.md", "README_AI.md"} {
		file, err := coreFiles.LoadPublishedFile(ctx, path)
		require.NoError(t, err)
		require.Equal(t, seed.EffectiveFiles[path], file.Body)
	}
	history, err := prompts.PromptRuleHistory(ctx, "hybrid")
	require.NoError(t, err)
	require.Len(t, history, 2)
	for _, version := range history {
		require.Equal(t, version.ID != oldVersions[2], version.Restorable)
	}
	baseline := state.Contents["hybrid"].VersionID
	echo, err := repo.PromptVersionPreserveEcho(ctx, baseline)
	require.NoError(t, err)
	require.True(t, echo)
	// Omitted hidden switches and ordinary edit: exact baseline remains public,
	// the new version becomes private without changing the global expose flag.
	update := func(body string, restore int64) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"expected_revision": state.Revision, "enabled": state.Enabled, "policy": state.Policy, "contents": map[string]service.PromptContentDraft{"hybrid": {Body: body, RestoreVersionID: restore}}})
		require.NoError(t, err)
		var input service.PromptConfigUpdate
		require.NoError(t, json.Unmarshal(raw, &input))
		state, err = prompts.SavePromptConfig(ctx, input, 1)
		require.NoError(t, err)
		require.True(t, state.ExposeServerPrompt && state.CompactEnabled)
	}
	update("private edited body", 0)
	echo, err = repo.PromptVersionPreserveEcho(ctx, state.Contents["hybrid"].VersionID)
	require.NoError(t, err)
	require.False(t, echo)
	update(seed.Prompt.EffectiveBody, baseline)
	echo, err = repo.PromptVersionPreserveEcho(ctx, state.Contents["hybrid"].VersionID)
	require.NoError(t, err)
	require.True(t, echo)
	update("edited from public history", baseline)
	echo, err = repo.PromptVersionPreserveEcho(ctx, state.Contents["hybrid"].VersionID)
	require.NoError(t, err)
	require.False(t, echo)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT body FROM system_prompt_template_versions WHERE id=$1`, oldVersions[2]).Scan(&preserved))
	require.Equal(t, bodies[2], preserved)
	_, err = db.ExecContext(ctx, `UPDATE system_prompt_frozen_public_files SET body='changed' WHERE path='SKILL.md'`)
	require.Error(t, err)
	_, err = db.ExecContext(ctx, `UPDATE system_prompt_template_versions SET body='changed' WHERE id=$1`, baseline)
	require.Error(t, err)
}

func TestPromptIndependentFreshInstallHasNoSeedOrPublicationIntegration(t *testing.T) {
	db := independentPromptFixtureDB(t)
	_, err := db.Exec(`INSERT INTO system_prompt_rule_policies (id,policy) VALUES (1,'{"version":2,"rules":[],"default_rule_ids":[]}'::jsonb)`)
	require.NoError(t, err)
	repo := &businessSystemPromptRepository{db: db}
	files, err := repo.InitializeIndependentPrompts(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, files)
	var templates int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM system_prompt_templates`).Scan(&templates))
	require.Zero(t, templates)
}
