//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	promptpolicy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// One PostgreSQL method covers the migration and real transaction boundaries
// introduced by prompt rules. The harness applies the production schema first;
// the private prompt schema keeps singleton changes and immutable fixture
// versions out of public. No Redis, plugin process or model request is used.
func TestPromptRulesMigrationAndAccountBindingIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NotNil(t, integrationDB)

	var pgConfig *pgx.ConnConfig
	connection, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	err = connection.Raw(func(driverConnection any) error {
		pgConnection, ok := driverConnection.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("expected harness PostgreSQL connection")
		}
		pgConfig = pgConnection.Conn().Config().Copy()
		return nil
	})
	require.NoError(t, connection.Close())
	require.NoError(t, err)

	suffix := fmt.Sprint(time.Now().UnixNano())
	schema := "prompt_rules_it_" + suffix
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	_, err = integrationDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema)
	require.NoError(t, err)
	pgConfig.RuntimeParams = maps.Clone(pgConfig.RuntimeParams)
	if pgConfig.RuntimeParams == nil {
		pgConfig.RuntimeParams = map[string]string{}
	}
	pgConfig.RuntimeParams["search_path"] = quotedSchema + ", public"
	db := stdlib.OpenDB(*pgConfig)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	var accountID, actorID int64
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("close fixture client: %v", closeErr)
		}
		// This exact schema was created above. Dropping it removes only synthetic
		// prompt versions, without disabling any immutable-version trigger.
		if _, cleanupErr := integrationDB.ExecContext(cleanup, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Errorf("remove fixture prompt schema: %v", cleanupErr)
		}
		if accountID > 0 {
			for _, query := range []string{`DELETE FROM scheduler_outbox WHERE account_id=$1`, `DELETE FROM accounts WHERE id=$1`} {
				if _, cleanupErr := integrationDB.ExecContext(cleanup, query, accountID); cleanupErr != nil {
					t.Errorf("remove fixture account rows: %v", cleanupErr)
				}
			}
		}
		if actorID > 0 {
			if _, cleanupErr := integrationDB.ExecContext(cleanup, `DELETE FROM users WHERE id=$1`, actorID); cleanupErr != nil {
				t.Errorf("remove fixture actor: %v", cleanupErr)
			}
		}
	})

	// Copy current production columns/checks/defaults/indexes. LIKE deliberately
	// excludes FKs; reattach their real definitions to the private prompt tables.
	tables := []string{"system_prompt_templates", "system_prompt_template_versions", "system_prompt_runtime", "settings"}
	for _, table := range tables {
		quoted := pgx.Identifier{table}.Sanitize()
		_, err := db.ExecContext(ctx, "CREATE TABLE "+quotedSchema+"."+quoted+" (LIKE public."+quoted+" INCLUDING ALL)")
		require.NoError(t, err)
	}
	for _, table := range tables {
		rows, err := integrationDB.QueryContext(ctx, `SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND contype='f' ORDER BY conname`, "public."+table)
		require.NoError(t, err)
		type foreignKey struct{ name, definition string }
		var keys []foreignKey
		for rows.Next() {
			var key foreignKey
			require.NoError(t, rows.Scan(&key.name, &key.definition))
			keys = append(keys, key)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		for _, key := range keys {
			for _, target := range tables {
				localReference := "REFERENCES " + quotedSchema + "." + pgx.Identifier{target}.Sanitize() + "("
				key.definition = strings.ReplaceAll(key.definition, "REFERENCES public."+target+"(", localReference)
				key.definition = strings.ReplaceAll(key.definition, "REFERENCES "+target+"(", localReference)
			}
			_, err := db.ExecContext(ctx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" ADD CONSTRAINT "+pgx.Identifier{key.name}.Sanitize()+" "+key.definition)
			require.NoError(t, err)
		}
	}
	_, err = db.ExecContext(ctx, `CREATE TRIGGER prompt_fixture_immutable
		BEFORE UPDATE ON system_prompt_template_versions
		FOR EACH ROW EXECUTE FUNCTION public.protect_system_prompt_version_content()`)
	require.NoError(t, err)

	actor := createEntUser(t, ctx, testEntClient(t), "prompt-rules-"+suffix+"@example.invalid")
	actorID = actor.ID
	_, err = db.ExecContext(ctx, `INSERT INTO system_prompt_runtime (id, enabled, expose_server_prompt, compact_enabled, revision, updated_by) VALUES (1, TRUE, TRUE, TRUE, 41, $1)`, actorID)
	require.NoError(t, err)
	prompts := &businessSystemPromptRepository{db: db}
	detail, err := prompts.CreateBusinessSystemPromptTemplate(ctx, service.BusinessSystemPromptTemplateCreate{
		Slug: "prompt_rules_fixture_" + suffix, Name: "Prompt rules integration fixture", Body: "Synthetic prompt content for this integration case.", CompositionMode: service.BusinessSystemPromptCompositionInline,
	}, actorID, 41)
	require.NoError(t, err)
	require.Len(t, detail.Versions, 1)
	versionID := detail.Versions[0].ID
	_, err = db.ExecContext(ctx, `UPDATE system_prompt_runtime SET active_template_id=$1, active_version_id=$2 WHERE id=1`, detail.Template.ID, versionID)
	require.NoError(t, err)
	readRuntime := func() string {
		t.Helper()
		var raw string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_jsonb(r)::text FROM system_prompt_runtime r WHERE id=1`).Scan(&raw))
		return raw
	}
	readPolicy := func() string {
		t.Helper()
		var raw string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT policy::text FROM system_prompt_rule_policies WHERE id=1`).Scan(&raw))
		return raw
	}
	runtimeBefore := readRuntime()
	migration, err := migrations.FS.ReadFile("253_prompt_rule_policies.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.JSONEq(t, runtimeBefore, readRuntime(), "migration preserves all legacy runtime fields")
	loaded, err := prompts.LoadBusinessSystemPromptRules(ctx)
	require.NoError(t, err)
	require.True(t, loaded.Enabled && loaded.ExposeServerPrompt && loaded.CompactEnabled)
	require.Equal(t, detail.Template.ID, loaded.TemplateID)
	require.Equal(t, versionID, loaded.VersionID)
	require.Equal(t, int64(41), loaded.Revision)
	require.NotNil(t, loaded.RulePolicy)
	require.Equal(t, []string{"legacy-default"}, loaded.RulePolicy.DefaultRuleIDs)
	require.Len(t, loaded.RulePolicy.Rules, 1)
	require.Equal(t, versionID, loaded.RulePolicy.Rules[0].VersionID)
	require.True(t, loaded.RulePolicy.Rules[0].FollowActive)

	// Each old domain keeps its own enabled state. The structured fixture also
	// exercises a literal placeholder inside expansion text and cache metadata.
	blocks := `[{"text":"before"},{"text":"{billing_header}"},{"text":"{claude_code_system_prompt}"},{"enabled":false,"text":"disabled"},{"text":"{claude_code_expansion_prompt}","cache_control":true},{"text":"after","cache_control":{"type":"ephemeral","ttl":"1h","fixture":"retained"}}]`
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES
		('enable_claude_oauth_system_prompt_injection','true',NOW()),
		('claude_oauth_system_prompt',$1,NOW()), ('claude_oauth_system_prompt_blocks',$2,NOW())`, "literal {fp} custom expansion", blocks)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE system_prompt_runtime SET enabled=false WHERE id=1`)
	require.NoError(t, err)
	v2Migration, err := migrations.FS.ReadFile("255_prompt_rule_policy_v2.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(v2Migration))
	require.NoError(t, err)
	loaded, err = prompts.LoadBusinessSystemPromptRules(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(42), loaded.Revision)
	require.True(t, loaded.Enabled && loaded.ExposeServerPrompt && loaded.CompactEnabled)
	require.Zero(t, loaded.TemplateID, "v2 snapshot must not use the former global selection")
	require.Equal(t, 2, loaded.RulePolicy.Version)
	require.Len(t, loaded.RulePolicy.Rules, 2)
	legacy, claudeRule := loaded.RulePolicy.Rules[0], loaded.RulePolicy.Rules[1]
	require.False(t, legacy.Enabled, "old OpenAI domain was disabled")
	require.False(t, legacy.FollowActive)
	require.Empty(t, legacy.Delivery)
	require.Equal(t, "auto", legacy.Role)
	require.Equal(t, []string{"openai", "cindy"}, legacy.Platforms)
	require.Equal(t, detail.Template.ID, legacy.TemplateID)
	require.Equal(t, versionID, legacy.VersionID)
	require.True(t, claudeRule.Enabled, "the independently enabled Claude domain must remain enabled")
	require.Equal(t, []string{"anthropic"}, claudeRule.Platforms)
	require.Equal(t, []string{"oauth", "setup-token"}, claudeRule.AccountTypes)
	require.Equal(t, []string{"generic-mimic"}, claudeRule.RequestProfiles)
	require.Equal(t, []string{"fable"}, claudeRule.ExcludeModelContains)
	var structured, archivedPolicy, archivedRuntime string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT body FROM system_prompt_template_versions WHERE id=$1`, claudeRule.VersionID).Scan(&structured))
	var envelope struct {
		Blocks    []map[string]any `json:"blocks"`
		Expansion string           `json:"expansion_prompt"`
	}
	require.NoError(t, json.Unmarshal([]byte(structured), &envelope))
	require.Len(t, envelope.Blocks, 6)
	require.Equal(t, "before", envelope.Blocks[0]["text"])
	require.Equal(t, false, envelope.Blocks[3]["enabled"])
	require.Equal(t, "literal {fp} custom expansion", envelope.Expansion)
	require.Equal(t, map[string]any{"type": "ephemeral", "ttl": "5m"}, envelope.Blocks[4]["cache_control"])
	require.Equal(t, map[string]any{"type": "ephemeral", "ttl": "1h", "fixture": "retained"}, envelope.Blocks[5]["cache_control"])
	require.NoError(t, db.QueryRowContext(ctx, `SELECT legacy_policy::text,legacy_runtime::text FROM system_prompt_rule_policies WHERE id=1`).Scan(&archivedPolicy, &archivedRuntime))
	require.Contains(t, archivedPolicy, `"follow_active": true`)
	require.Contains(t, archivedRuntime, `"enabled": false`)

	policy := *loaded.RulePolicy
	policy.Rules[0].Name = "Configured fixture rule"
	policy.Rules[0].Order = 17
	policy.Rules[0].Enabled = true
	require.NoError(t, prompts.UpdateBusinessSystemPromptRules(ctx, policy, 42, actorID))
	loaded, err = prompts.LoadBusinessSystemPromptRules(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(43), loaded.Revision)
	require.Equal(t, "Configured fixture rule", loaded.RulePolicy.Rules[0].Name)
	require.True(t, loaded.Enabled && loaded.ExposeServerPrompt && loaded.CompactEnabled)
	configuredPolicy, configuredRuntime := readPolicy(), readRuntime()
	_, err = db.ExecContext(ctx, string(v2Migration))
	require.NoError(t, err)
	require.JSONEq(t, configuredPolicy, readPolicy(), "replaying migration must not replace custom rules")
	require.JSONEq(t, configuredRuntime, readRuntime())
	conflicting := extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{}, DefaultRuleIDs: []string{}}
	require.ErrorIs(t, prompts.UpdateBusinessSystemPromptRules(ctx, conflicting, 41, actorID), service.ErrBusinessSystemPromptRevisionConflict)
	require.JSONEq(t, configuredPolicy, readPolicy())
	require.JSONEq(t, configuredRuntime, readRuntime())

	accounts := NewAccountRepository(client, db, nil).(*accountRepository)
	account := &service.Account{
		Name: "prompt-rules-fixture-" + suffix, Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Concurrency: 1, Schedulable: false,
		Credentials: map[string]any{"api_key": "synthetic-integration-only"},
		Extra:       map[string]any{"unrelated_fixture": map[string]any{"nested": []any{true, "retained"}}, "openai_responses_supported": true},
	}
	require.NoError(t, accounts.Create(ctx, account))
	accountID = account.ID
	stale, err := accounts.GetByID(ctx, accountID)
	require.NoError(t, err)
	_, present := stale.Extra[service.PromptAccountBindingExtraKey]
	require.False(t, present)
	var originalExtra, originalCredentials string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT extra::text, credentials::text FROM accounts WHERE id=$1`, accountID).Scan(&originalExtra, &originalCredentials))
	binding := extensionv1.PromptAccountBinding{Mode: "custom", RuleIDs: []string{"legacy-default"}}
	applied, err := accounts.UpdatePromptBindingIfRevision(ctx, accountID, stale.UpdatedAt, 43, binding)
	require.NoError(t, err)
	require.True(t, applied)
	assertBinding := func() {
		t.Helper()
		var raw, unrelated, credentials string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT (extra->'prompt_skills')::text, (extra-'prompt_skills')::text, credentials::text FROM accounts WHERE id=$1`, accountID).Scan(&raw, &unrelated, &credentials))
		expected, err := json.Marshal(binding)
		require.NoError(t, err)
		require.JSONEq(t, string(expected), raw)
		require.JSONEq(t, originalExtra, unrelated)
		require.JSONEq(t, originalCredentials, credentials)
	}
	assertBinding()
	applied, err = accounts.UpdatePromptBindingIfRevision(ctx, accountID, stale.UpdatedAt, 43, extensionv1.PromptAccountBinding{Mode: "off"})
	require.NoError(t, err)
	require.False(t, applied, "stale account revision cannot overwrite the binding")
	assertBinding()
	current, err := accounts.GetByID(ctx, accountID)
	require.NoError(t, err)
	applied, err = accounts.UpdatePromptBindingIfRevision(ctx, accountID, current.UpdatedAt, 41, extensionv1.PromptAccountBinding{Mode: "off"})
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptRevisionConflict)
	require.False(t, applied)
	assertBinding()
	require.ErrorIs(t, prompts.UpdateBusinessSystemPromptRules(ctx, conflicting, 43, actorID), service.ErrPromptRuleReferenced)
	require.JSONEq(t, configuredPolicy, readPolicy())
	require.JSONEq(t, configuredRuntime, readRuntime())

	// This is a real pre-binding whole-account snapshot, not a partial patch.
	stale.Name += " renamed"
	require.NoError(t, accounts.Update(ctx, stale))
	assertBinding()
	current, err = accounts.GetByID(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, stale.Name, current.Name)

	var versionCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_template_versions`).Scan(&versionCount))
	badPolicy := policy
	badPolicy.Rules = append([]extensionv1.PromptRule{}, policy.Rules...)
	badPolicy.Rules[1].VersionID = 9223372036854775807
	edit := service.PromptConfigUpdate{
		ExpectedRevision: 43, Enabled: true, ExposeServerPrompt: true, CompactEnabled: true,
		Policy: badPolicy, Contents: map[string]service.PromptContentDraft{"legacy-default": {Body: "Atomic replacement content."}},
	}
	require.ErrorIs(t, prompts.SavePromptConfig(ctx, edit, actorID), service.ErrBusinessSystemPromptVersionNotFound)
	require.JSONEq(t, configuredPolicy, readPolicy())
	require.JSONEq(t, configuredRuntime, readRuntime())
	var countAfterFailure int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_template_versions`).Scan(&countAfterFailure))
	require.Equal(t, versionCount, countAfterFailure, "a later bad reference rolls back an already inserted content version")
	edit.Policy = policy
	require.NoError(t, prompts.SavePromptConfig(ctx, edit, actorID))
	loaded, err = prompts.LoadBusinessSystemPromptRules(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(44), loaded.Revision, "content and rules share one revision increment")
	require.NotEqual(t, versionID, loaded.RulePolicy.Rules[0].VersionID)
	require.Equal(t, claudeRule.VersionID, loaded.RulePolicy.Rules[1].VersionID)
	var originalBody string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT body FROM system_prompt_template_versions WHERE id=$1`, versionID).Scan(&originalBody))
	require.Equal(t, detail.Versions[0].Body, originalBody)
	_, err = db.ExecContext(ctx, `UPDATE system_prompt_template_versions SET body='must fail' WHERE id=$1`, versionID)
	require.Error(t, err, "immutable version trigger remains active")
	assertBinding()

	// Exercise the fresh-install placeholder in the same fixture. Every change,
	// including this tiny account shadow table, is rolled back afterwards.
	freshTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = freshTx.Rollback() }()
	_, err = freshTx.ExecContext(ctx, `CREATE TABLE accounts (id BIGINT PRIMARY KEY, deleted_at TIMESTAMPTZ, extra JSONB);
		ALTER TABLE system_prompt_rule_policies DROP CONSTRAINT system_prompt_rule_policy_v2;
		DELETE FROM settings;
		UPDATE system_prompt_runtime SET active_template_id=NULL,active_version_id=NULL,enabled=false WHERE id=1;
		UPDATE system_prompt_rule_policies SET policy='{"version":1,"default_rule_ids":["legacy-default"],"rules":[{"id":"legacy-default","name":"Default","enabled":true,"template_id":0,"version_id":0,"follow_active":true,"delivery":"native_control","position":"control_append","models":[]}]}'::jsonb WHERE id=1;
		INSERT INTO accounts VALUES (1,NULL,'{"prompt_skills":{"mode":"custom","rule_ids":["legacy-default"]}}');
		SAVEPOINT referenced_empty_seed`)
	require.NoError(t, err)
	_, err = freshTx.ExecContext(ctx, string(v2Migration))
	require.Error(t, err, "an empty legacy rule with an account reference must not disappear")
	_, err = freshTx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT referenced_empty_seed; DELETE FROM accounts`)
	require.NoError(t, err)
	_, err = freshTx.ExecContext(ctx, string(v2Migration))
	require.NoError(t, err)
	var freshRules, freshDefaults int
	var freshRevision int64
	require.NoError(t, freshTx.QueryRowContext(ctx, `SELECT jsonb_array_length(p.policy->'rules'),jsonb_array_length(p.policy->'default_rule_ids'),r.revision
		FROM system_prompt_rule_policies p JOIN system_prompt_runtime r ON r.id=p.id WHERE p.id=1`).Scan(&freshRules, &freshDefaults, &freshRevision))
	require.Zero(t, freshRules, "empty default Claude settings must not create a custom rule")
	require.Zero(t, freshDefaults)
	require.Equal(t, int64(45), freshRevision)
	_, err = freshTx.ExecContext(ctx, string(v2Migration))
	require.NoError(t, err)
	require.NoError(t, freshTx.QueryRowContext(ctx, `SELECT revision FROM system_prompt_runtime WHERE id=1`).Scan(&freshRevision))
	require.Equal(t, int64(45), freshRevision, "replaying the v2 migration does not publish another revision")
	require.NoError(t, freshTx.Rollback())

	t.Run("full_legacy_policy_and_independent_claude_domain", func(t *testing.T) {
		// Both domains were independently valid before v2. Merging them must
		// preserve all 32 rule IDs and the additional Claude source. Keep the
		// migration and its DDL inside a transaction that is always rolled back.
		legacyCapacity := extensionv1.PromptRulePolicy{Version: 1}
		for i := 0; i < 32; i++ {
			id := fmt.Sprintf("legacy-capacity-%02d", i)
			if i == 0 {
				id = "legacy-default" // Preserve the fixture account's existing reference.
			}
			rule := extensionv1.PromptRule{
				ID: id, Name: fmt.Sprintf("Legacy capacity rule %02d", i), Enabled: i%3 != 0,
				TemplateID: detail.Template.ID, VersionID: versionID, FollowActive: i%2 == 0,
				Order: i, Delivery: "native_control", Position: "control_append", ModelMatch: "upstream", Models: []string{},
			}
			if rule.FollowActive {
				rule.TemplateID, rule.VersionID = 0, 0
			}
			legacyCapacity.Rules = append(legacyCapacity.Rules, rule)
			legacyCapacity.DefaultRuleIDs = append(legacyCapacity.DefaultRuleIDs, id)
		}
		legacyRaw, err := json.Marshal(legacyCapacity)
		require.NoError(t, err)
		capacityTx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = capacityTx.Rollback() }()
		_, err = capacityTx.ExecContext(ctx, `ALTER TABLE system_prompt_rule_policies DROP CONSTRAINT system_prompt_rule_policy_v2;
			UPDATE system_prompt_runtime SET enabled=true WHERE id=1`)
		require.NoError(t, err)
		_, err = capacityTx.ExecContext(ctx, `UPDATE system_prompt_rule_policies
			SET policy=$1::jsonb,legacy_policy=NULL,legacy_runtime=NULL WHERE id=1`, string(legacyRaw))
		require.NoError(t, err)
		_, err = capacityTx.ExecContext(ctx, string(v2Migration))
		require.NoError(t, err, "32 legacy rules and the separately configured Claude source must migrate together")

		// Read and validate the migrated v2 policy on this same transaction;
		// a separate repository connection cannot observe uncommitted DDL/data.
		var migratedRaw, preservedRaw []byte
		var capacitySnapshot service.BusinessSystemPromptSnapshot
		require.NoError(t, capacityTx.QueryRowContext(ctx, `SELECT r.enabled,r.expose_server_prompt,r.compact_enabled,
			r.revision,r.updated_at,p.policy,p.legacy_policy
			FROM system_prompt_runtime r JOIN system_prompt_rule_policies p ON p.id=r.id WHERE r.id=1`).Scan(
			&capacitySnapshot.Enabled, &capacitySnapshot.ExposeServerPrompt, &capacitySnapshot.CompactEnabled,
			&capacitySnapshot.Revision, &capacitySnapshot.UpdatedAt, &migratedRaw, &preservedRaw))
		var capacityPolicy extensionv1.PromptRulePolicy
		require.NoError(t, json.Unmarshal(migratedRaw, &capacityPolicy))
		capacityPolicy, err = promptpolicy.ValidateRulePolicy(capacityPolicy)
		require.NoError(t, err, "the migrated union must also pass the runtime's v2 policy validator")
		require.Equal(t, extensionv1.PromptRulePolicyVersion, capacityPolicy.Version)
		require.Len(t, capacityPolicy.Rules, 33)
		require.True(t, capacitySnapshot.Enabled && capacitySnapshot.ExposeServerPrompt && capacitySnapshot.CompactEnabled)
		require.Equal(t, int64(45), capacitySnapshot.Revision)
		require.JSONEq(t, string(legacyRaw), string(preservedRaw))
		for i, before := range legacyCapacity.Rules {
			after := capacityPolicy.Rules[i]
			require.Equal(t, before.ID, after.ID)
			require.Equal(t, before.Enabled, after.Enabled)
			require.Equal(t, before.Order, after.Order)
			require.Equal(t, []string{"openai", "cindy"}, after.Platforms)
			require.Equal(t, detail.Template.ID, after.TemplateID)
			require.Equal(t, versionID, after.VersionID)
			require.False(t, after.FollowActive)
			require.Empty(t, after.Delivery)
		}
		additionalClaude := capacityPolicy.Rules[32]
		require.NotContains(t, legacyCapacity.DefaultRuleIDs, additionalClaude.ID)
		require.True(t, additionalClaude.Enabled)
		require.Equal(t, []string{"anthropic"}, additionalClaude.Platforms)
		require.Equal(t, []string{"oauth", "setup-token"}, additionalClaude.AccountTypes)
		require.Equal(t, []string{"generic-mimic"}, additionalClaude.RequestProfiles)
		require.Equal(t, []string{"fable"}, additionalClaude.ExcludeModelContains)
		require.Equal(t, append(append([]string{}, legacyCapacity.DefaultRuleIDs...), additionalClaude.ID), capacityPolicy.DefaultRuleIDs)
		require.NoError(t, capacityTx.Rollback())
	})
	var serverVersion string
	require.NoError(t, db.QueryRowContext(ctx, `SHOW server_version`).Scan(&serverVersion))
	t.Logf("PostgreSQL %s: v2 migration preserved independent domain scope and structured Claude blocks; atomic content rollback, immutable versions, one revision, account CAS and references held", serverVersion)
}
