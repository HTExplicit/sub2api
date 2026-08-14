//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
)

type cindyBalanceResetSnapshot struct {
	Credentials json.RawMessage
	Extra       json.RawMessage
	Status      string
	Schedulable bool
	Marked      bool
	Deleted     bool
	RowVersion  string
}

func TestCindyBalanceMarkerResetMigrationIsStrictAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, `
		CREATE TABLE accounts (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			platform TEXT NOT NULL,
			type TEXT NOT NULL,
			credentials JSONB NOT NULL DEFAULT '{}',
			extra JSONB NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active',
			schedulable BOOLEAN NOT NULL DEFAULT TRUE,
			cindy_balance_insufficient_at TIMESTAMPTZ,
			deleted_at TIMESTAMPTZ
		);
		CREATE TABLE ops_error_logs (
			id BIGSERIAL PRIMARY KEY,
			account_id BIGINT,
			status_code INT,
			upstream_status_code INT,
			provider_error_type VARCHAR(64),
			provider_error_code VARCHAR(64),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		INSERT INTO accounts (
			name, platform, type, credentials, extra, status, schedulable,
			cindy_balance_insufficient_at, deleted_at
		) VALUES
			('cindy-active', 'openai', 'apikey',
			 '{"base_url":"  HTTPS://API.LAXAROUTER.AI/  ","api_key":"fixture"}',
			 '{"ordinary":"kept"}', 'disabled', FALSE, NOW(), NULL),
			('cindy-deleted', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai"}',
			 '{"ordinary":true}', 'error', TRUE, NOW(), NOW()),
			('cindy-unmarked', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai"}',
			 '{"ordinary":"untouched"}', 'active', TRUE, NULL, NULL),
			('ordinary', 'openai', 'apikey',
			 '{"base_url":"https://api.openai.com"}', '{}', 'active', TRUE, NOW(), NULL),
			('cindy-path', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai/v1"}', '{}', 'active', TRUE, NOW(), NULL),
			('cindy-oauth', 'openai', 'oauth',
			 '{"base_url":"https://api.laxarouter.ai"}', '{}', 'active', TRUE, NOW(), NULL);

		INSERT INTO ops_error_logs (
			account_id, status_code, upstream_status_code,
			provider_error_type, provider_error_code, created_at
		)
		SELECT id, 429, 429, 'budget_exceeded', NULL, cindy_balance_insufficient_at
		FROM accounts WHERE name = 'cindy-active'
		UNION ALL
		SELECT id, 429, 429, 'budget_exceeded', '429', cindy_balance_insufficient_at
		FROM accounts WHERE name = 'cindy-deleted';
	`))

	beforeUnmarked := readCindyBalanceResetSnapshot(t, ctx, db, "cindy-unmarked")
	migration, err := dbmigrations.FS.ReadFile("210_codexrip_reset_legacy_cindy_balance_markers.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))

	active := readCindyBalanceResetSnapshot(t, ctx, db, "cindy-active")
	require.False(t, active.Marked)
	require.JSONEq(t, `{"base_url":"  HTTPS://API.LAXAROUTER.AI/  ","api_key":"fixture"}`, string(active.Credentials))
	require.JSONEq(t, `{"ordinary":"kept"}`, string(active.Extra))
	require.Equal(t, "disabled", active.Status)
	require.False(t, active.Schedulable)
	require.False(t, active.Deleted)

	deleted := readCindyBalanceResetSnapshot(t, ctx, db, "cindy-deleted")
	require.True(t, deleted.Marked)
	require.Equal(t, "error", deleted.Status)
	require.True(t, deleted.Schedulable)
	require.True(t, deleted.Deleted)

	for _, name := range []string{"ordinary", "cindy-path", "cindy-oauth"} {
		snapshot := readCindyBalanceResetSnapshot(t, ctx, db, name)
		require.True(t, snapshot.Marked, name)
	}
	unmarked := readCindyBalanceResetSnapshot(t, ctx, db, "cindy-unmarked")
	require.False(t, unmarked.Marked)
	require.Equal(t, beforeUnmarked.RowVersion, unmarked.RowVersion)

	activeRowVersion := active.RowVersion
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))
	require.Equal(t, activeRowVersion, readCindyBalanceResetSnapshot(t, ctx, db, "cindy-active").RowVersion)
	require.Equal(t, deleted.RowVersion, readCindyBalanceResetSnapshot(t, ctx, db, "cindy-deleted").RowVersion)
}

func readCindyBalanceResetSnapshot(
	t *testing.T,
	ctx context.Context,
	db interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	name string,
) cindyBalanceResetSnapshot {
	t.Helper()
	var snapshot cindyBalanceResetSnapshot
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT credentials, extra, status, schedulable,
		       cindy_balance_insufficient_at IS NOT NULL,
		       deleted_at IS NOT NULL, xmin::text
		FROM accounts WHERE name = $1
	`, name).Scan(
		&snapshot.Credentials,
		&snapshot.Extra,
		&snapshot.Status,
		&snapshot.Schedulable,
		&snapshot.Marked,
		&snapshot.Deleted,
		&snapshot.RowVersion,
	))
	return snapshot
}
