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

type cindyMigrationAccountSnapshot struct {
	Credentials json.RawMessage
	Extra       json.RawMessage
	Status      string
	Deleted     bool
	RowVersion  string
}

func TestCindyOpenAIDefaultsMigrationIsStrictAndIdempotent(t *testing.T) {
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
			deleted_at TIMESTAMPTZ
		);
		INSERT INTO accounts (name, platform, type, credentials, extra, status, deleted_at) VALUES
			('cindy-active', 'openai', 'apikey',
			 '{"base_url":"  https://API.LAXAROUTER.AI/  ","api_key":"fixture"}',
			 '{"openai_responses_mode":"force_responses","openai_alpha_search_mode":"responses_web_search","openai_prompt_cache_key_mode":"sha256_64","ordinary":"kept"}',
			 'disabled', NULL),
			('cindy-deleted', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai"}',
			 '{"ordinary":true}', 'error', NOW()),
			('ordinary', 'openai', 'apikey',
			 '{"base_url":"https://api.openai.com"}',
			 '{"openai_alpha_search_mode":"responses_web_search","openai_prompt_cache_key_mode":"sha256_64"}',
			 'active', NULL),
			('cindy-path', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai/v1"}',
			 '{"openai_alpha_search_mode":"responses_web_search","openai_prompt_cache_key_mode":"sha256_64"}',
			 'active', NULL),
			('cindy-query', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai/?x=1"}',
			 '{"openai_alpha_search_mode":"responses_web_search","openai_prompt_cache_key_mode":"sha256_64"}',
			 'active', NULL),
			('cindy-oauth', 'openai', 'oauth',
			 '{"base_url":"https://api.laxarouter.ai"}',
			 '{"openai_alpha_search_mode":"responses_web_search","openai_prompt_cache_key_mode":"sha256_64"}',
			 'active', NULL),
			('cindy-uppercase-scheme', 'openai', 'apikey',
			 '{"base_url":"HTTPS://api.laxarouter.ai"}',
			 '{"openai_alpha_search_mode":"responses_web_search","openai_prompt_cache_key_mode":"sha256_64"}',
			 'active', NULL);
	`))

	migration, err := dbmigrations.FS.ReadFile("207_codexrip_restore_cindy_openai_defaults.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))

	active := readCindyMigrationAccount(t, ctx, db, "cindy-active")
	require.JSONEq(t, `{
		"openai_responses_mode":"force_responses",
		"openai_alpha_search_mode":"direct",
		"openai_prompt_cache_key_mode":"passthrough",
		"ordinary":"kept"
	}`, string(active.Extra))
	require.JSONEq(t, `{"base_url":"  https://API.LAXAROUTER.AI/  ","api_key":"fixture"}`, string(active.Credentials))
	require.Equal(t, "disabled", active.Status)
	require.False(t, active.Deleted)

	deleted := readCindyMigrationAccount(t, ctx, db, "cindy-deleted")
	require.JSONEq(t, `{
		"ordinary":true,
		"openai_alpha_search_mode":"direct",
		"openai_prompt_cache_key_mode":"passthrough"
	}`, string(deleted.Extra))
	require.Equal(t, "error", deleted.Status)
	require.True(t, deleted.Deleted)

	uppercaseScheme := readCindyMigrationAccount(t, ctx, db, "cindy-uppercase-scheme")
	require.JSONEq(t, `{
		"openai_alpha_search_mode":"direct",
		"openai_prompt_cache_key_mode":"passthrough"
	}`, string(uppercaseScheme.Extra))

	for _, name := range []string{
		"ordinary", "cindy-path", "cindy-query", "cindy-oauth",
	} {
		snapshot := readCindyMigrationAccount(t, ctx, db, name)
		require.JSONEq(t, `{
			"openai_alpha_search_mode":"responses_web_search",
			"openai_prompt_cache_key_mode":"sha256_64"
		}`, string(snapshot.Extra), name)
	}

	firstRowVersion := active.RowVersion
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))
	second := readCindyMigrationAccount(t, ctx, db, "cindy-active")
	require.Equal(t, firstRowVersion, second.RowVersion)
	require.JSONEq(t, string(active.Extra), string(second.Extra))
}

func readCindyMigrationAccount(
	t *testing.T,
	ctx context.Context,
	db interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	name string,
) cindyMigrationAccountSnapshot {
	t.Helper()
	var snapshot cindyMigrationAccountSnapshot
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT credentials, extra, status, deleted_at IS NOT NULL, xmin::text
		FROM accounts WHERE name = $1
	`, name).Scan(
		&snapshot.Credentials,
		&snapshot.Extra,
		&snapshot.Status,
		&snapshot.Deleted,
		&snapshot.RowVersion,
	))
	return snapshot
}
