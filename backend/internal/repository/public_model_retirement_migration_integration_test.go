//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const publicRetirementMigration = "242_retire_public_model_manager.sql"

type retirementFixture struct {
	tx                                         *sql.Tx
	groupID, accountID, userID, keyID, usageID int64
}

func prepareRetirementFixture(t *testing.T) retirementFixture {
	t.Helper()
	f := retirementFixture{tx: testTx(t)}
	ctx := context.Background()
	oldSchema, err := dbmigrations.FS.ReadFile("240_admin_account_capabilities.sql")
	require.NoError(t, err)
	_, err = f.tx.ExecContext(ctx, string(oldSchema))
	require.NoError(t, err)
	require.NoError(t, f.tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance) VALUES($1,'fixture',42) RETURNING id`, t.Name()+"@example.invalid").Scan(&f.userID))
	require.NoError(t, f.tx.QueryRowContext(ctx, `INSERT INTO groups(name,platform,rate_multiplier,status,model_allowlist,managed_model_routes)
VALUES($1,'openai',0.2,'active','{"enabled":true,"models":["gpt-5.6-luna"]}','{"version":1,"enabled":true,"routes":[]}') RETURNING id`, t.Name()).Scan(&f.groupID))
	const credentials = `{"api_key":"fixture-not-a-live-credential","model_mapping":{"private":"private-target","Case":"upper","case":"lower","s2pub-g23-m0000000000000000":"gpt-5.6-luna-project"}}`
	require.NoError(t, f.tx.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES($1,'openai','apikey',$2::jsonb,'active',false) RETURNING id`, t.Name(), credentials).Scan(&f.accountID))
	_, err = f.tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, f.accountID, f.groupID)
	require.NoError(t, err)
	require.NoError(t, f.tx.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,$2,'retirement-fixture',$3) RETURNING id`, f.userID, fmt.Sprintf("retirement-%d", f.userID), f.groupID).Scan(&f.keyID))
	require.NoError(t, f.tx.QueryRowContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,model,total_cost,actual_cost) VALUES($1,$2,$3,'gpt-5.6-luna',1.58602,10.67113907) RETURNING id`, f.userID, f.keyID, f.accountID).Scan(&f.usageID))
	var channelID int64
	require.NoError(t, f.tx.QueryRowContext(ctx, `INSERT INTO channels(name,model_mapping) VALUES($1,'{"openai":{"gpt-5.6-luna":"s2pub-g23-m0000000000000000"}}') RETURNING id`, t.Name()).Scan(&channelID))
	_, err = f.tx.ExecContext(ctx, `INSERT INTO channel_groups(channel_id,group_id) VALUES($1,$2)`, channelID, f.groupID)
	require.NoError(t, err)
	var groupDigest, channelDigest, credentialsDigest string
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT encode(sha256(convert_to((to_jsonb(g)-'created_at'-'updated_at')::text,'UTF8')),'hex') FROM groups g WHERE id=$1`, f.groupID).Scan(&groupDigest))
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT encode(sha256(convert_to((to_jsonb(c)-'created_at'-'updated_at')::text,'UTF8')),'hex') FROM channels c WHERE id=$1`, channelID).Scan(&channelDigest))
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT encode(sha256(convert_to(credentials::text,'UTF8')),'hex') FROM accounts WHERE id=$1`, f.accountID).Scan(&credentialsDigest))
	plan := map[string]any{
		"schema": 1, "plan_digest": strings.Repeat("a", 64),
		"groups":    []any{map[string]any{"id": f.groupID, "digest": groupDigest, "account_ids": []int64{f.accountID}, "key_count": 1, "messages_dispatch_model_config": map[string]any{}, "default_mapped_model": ""}},
		"channels":  []any{map[string]any{"id": channelID, "digest": channelDigest, "group_ids": []int64{f.groupID}, "model_mapping": map[string]any{"openai": map[string]string{"gpt-5.6-luna": "gpt-5.6-luna"}}}},
		"accounts":  []any{map[string]any{"id": f.accountID, "credentials_digest": credentialsDigest, "group_ids": []int64{f.groupID}, "status": "active", "schedulable": false, "remove_mapping": []string{"s2pub-g23-m0000000000000000"}, "set_mapping": map[string]string{"gpt-5.6-luna": "gpt-5.6-luna-project"}}},
		"composite": []any{map[string]any{"group_id": f.groupID, "before": []any{}, "delete": []any{}, "add": []any{}}},
	}
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	_, err = f.tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('internal_rate_conversion_enabled','true'),('public_model_retirement_plan',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, string(encoded))
	require.NoError(t, err)
	return f
}

func TestPublicModelRetirementMigrationPreservesBillingKeysAndPrivateMappings(t *testing.T) {
	f := prepareRetirementFixture(t)
	ctx := context.Background()
	migration, err := dbmigrations.FS.ReadFile(publicRetirementMigration)
	require.NoError(t, err)
	_, err = f.tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var mapping, credential string
	var schedulable bool
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT credentials->'model_mapping',credentials->>'api_key',schedulable FROM accounts WHERE id=$1`, f.accountID).Scan(&mapping, &credential, &schedulable))
	require.JSONEq(t, `{"private":"private-target","Case":"upper","case":"lower","gpt-5.6-luna":"gpt-5.6-luna-project"}`, mapping)
	require.Equal(t, "fixture-not-a-live-credential", credential)
	require.False(t, schedulable)
	var rate, balance, cost float64
	var keyGroup int64
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT rate_multiplier FROM groups WHERE id=$1`, f.groupID).Scan(&rate))
	require.Equal(t, .2, rate)
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, f.userID).Scan(&balance))
	require.Equal(t, 42.0, balance)
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT actual_cost FROM usage_logs WHERE id=$1`, f.usageID).Scan(&cost))
	require.InDelta(t, 10.67113907, cost, 1e-9)
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT group_id FROM api_keys WHERE id=$1`, f.keyID).Scan(&keyGroup))
	require.Equal(t, f.groupID, keyGroup)
	var absent bool
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT to_regclass('admin_capability_runs') IS NULL AND to_regclass('admin_capability_items') IS NULL AND to_regclass('admin_capability_changesets') IS NULL`).Scan(&absent))
	require.True(t, absent)
	var settingsCount int
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key IN ('internal_rate_conversion_enabled','public_model_retirement_plan')`).Scan(&settingsCount))
	require.Zero(t, settingsCount)
	// Replaying a completed cleanup must not change its preserved data.
	_, err = f.tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
}

func TestPublicModelRetirementMigrationRejectsDriftWithoutPartialCleanup(t *testing.T) {
	f := prepareRetirementFixture(t)
	ctx := context.Background()
	_, err := f.tx.ExecContext(ctx, `UPDATE groups SET rate_multiplier=0.3 WHERE id=$1`, f.groupID)
	require.NoError(t, err)
	_, err = f.tx.ExecContext(ctx, `SAVEPOINT retirement_attempt`)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile(publicRetirementMigration)
	require.NoError(t, err)
	_, err = f.tx.ExecContext(ctx, string(migration))
	require.ErrorContains(t, err, "public_retirement_group_changed")
	_, err = f.tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT retirement_attempt`)
	require.NoError(t, err)
	var intact bool
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT credentials->'model_mapping' ? 's2pub-g23-m0000000000000000' FROM accounts WHERE id=$1`, f.accountID).Scan(&intact))
	require.True(t, intact)
	require.NoError(t, f.tx.QueryRowContext(ctx, `SELECT to_regclass('admin_capability_items') IS NOT NULL`).Scan(&intact))
	require.True(t, intact)
}
