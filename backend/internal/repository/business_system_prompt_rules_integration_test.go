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
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
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
	tables := []string{"system_prompt_templates", "system_prompt_template_versions", "system_prompt_runtime"}
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

	policy := *loaded.RulePolicy
	policy.Rules[0].Name = "Configured fixture rule"
	policy.Rules[0].Order = 17
	policy.Rules[0].FollowActive = false
	require.NoError(t, prompts.UpdateBusinessSystemPromptRules(ctx, policy, 41, actorID))
	loaded, err = prompts.LoadBusinessSystemPromptRules(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(42), loaded.Revision)
	require.Equal(t, "Configured fixture rule", loaded.RulePolicy.Rules[0].Name)
	require.True(t, loaded.Enabled && loaded.ExposeServerPrompt && loaded.CompactEnabled)
	configuredPolicy, configuredRuntime := readPolicy(), readRuntime()
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.JSONEq(t, configuredPolicy, readPolicy(), "replaying migration must not replace custom rules")
	require.JSONEq(t, configuredRuntime, readRuntime())
	conflicting := extensionv1.PromptRulePolicy{Version: 1, Rules: []extensionv1.PromptRule{}, DefaultRuleIDs: []string{}}
	require.ErrorIs(t, prompts.UpdateBusinessSystemPromptRules(ctx, conflicting, 41, actorID), service.ErrBusinessSystemPromptRevisionConflict)
	require.JSONEq(t, configuredPolicy, readPolicy())
	require.JSONEq(t, configuredRuntime, readRuntime())

	accounts := NewAccountRepository(client, db, nil).(*accountRepository)
	account := &service.Account{
		Name: "prompt-rules-fixture-" + suffix, Platform: service.PlatformOpenAI, WirePlatform: service.WirePlatformOpenAI,
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
	applied, err := accounts.UpdatePromptBindingIfRevision(ctx, accountID, stale.UpdatedAt, 42, binding)
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
	applied, err = accounts.UpdatePromptBindingIfRevision(ctx, accountID, stale.UpdatedAt, 42, extensionv1.PromptAccountBinding{Mode: "off"})
	require.NoError(t, err)
	require.False(t, applied, "stale account revision cannot overwrite the binding")
	assertBinding()
	current, err := accounts.GetByID(ctx, accountID)
	require.NoError(t, err)
	applied, err = accounts.UpdatePromptBindingIfRevision(ctx, accountID, current.UpdatedAt, 41, extensionv1.PromptAccountBinding{Mode: "off"})
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptRevisionConflict)
	require.False(t, applied)
	assertBinding()
	require.ErrorIs(t, prompts.UpdateBusinessSystemPromptRules(ctx, conflicting, 42, actorID), service.ErrPromptRuleReferenced)
	require.JSONEq(t, configuredPolicy, readPolicy())
	require.JSONEq(t, configuredRuntime, readRuntime())

	// This is a real pre-binding whole-account snapshot, not a partial patch.
	stale.Name += " renamed"
	require.NoError(t, accounts.Update(ctx, stale))
	assertBinding()
	current, err = accounts.GetByID(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, stale.Name, current.Name)
	var serverVersion string
	require.NoError(t, db.QueryRowContext(ctx, `SHOW server_version`).Scan(&serverVersion))
	t.Logf("PostgreSQL %s: migration retained runtime/active version, repeat retained policy, rule and account CAS held, referenced-rule delete rejected, stale account edit preserved binding", serverVersion)
}
