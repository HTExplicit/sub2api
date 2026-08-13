//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
)

func TestCindyPromptCacheHashMigrationIsStrictAndIdempotent(t *testing.T) {
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
			 '{"openai_responses_mode":"force_responses","openai_alpha_search_mode":"direct","openai_prompt_cache_key_mode":"passthrough","ordinary":"kept"}',
			 'disabled', NULL),
			('cindy-deleted', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai"}',
			 '{"openai_alpha_search_mode":"direct","ordinary":true}', 'error', NOW()),
			('cindy-already-hashed', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai"}',
			 '{"openai_alpha_search_mode":"direct","openai_prompt_cache_key_mode":"sha256_64"}',
			 'active', NULL),
			('ordinary', 'openai', 'apikey',
			 '{"base_url":"https://api.openai.com"}',
			 '{"openai_alpha_search_mode":"direct","openai_prompt_cache_key_mode":"passthrough"}',
			 'active', NULL),
			('cindy-path', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai/v1"}',
			 '{"openai_alpha_search_mode":"direct","openai_prompt_cache_key_mode":"passthrough"}',
			 'active', NULL),
			('cindy-oauth', 'openai', 'oauth',
			 '{"base_url":"https://api.laxarouter.ai"}',
			 '{"openai_alpha_search_mode":"direct","openai_prompt_cache_key_mode":"passthrough"}',
			 'active', NULL);
	`))

	alreadyBefore := readCindyMigrationAccount(t, ctx, db, "cindy-already-hashed")
	migration, err := dbmigrations.FS.ReadFile("208_codexrip_use_cindy_prompt_cache_hash.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))

	active := readCindyMigrationAccount(t, ctx, db, "cindy-active")
	require.JSONEq(t, `{
		"openai_responses_mode":"force_responses",
		"openai_alpha_search_mode":"direct",
		"openai_prompt_cache_key_mode":"sha256_64",
		"ordinary":"kept"
	}`, string(active.Extra))
	require.JSONEq(t, `{"base_url":"  https://API.LAXAROUTER.AI/  ","api_key":"fixture"}`, string(active.Credentials))
	require.Equal(t, "disabled", active.Status)
	require.False(t, active.Deleted)

	deleted := readCindyMigrationAccount(t, ctx, db, "cindy-deleted")
	require.JSONEq(t, `{
		"openai_alpha_search_mode":"direct",
		"openai_prompt_cache_key_mode":"sha256_64",
		"ordinary":true
	}`, string(deleted.Extra))
	require.Equal(t, "error", deleted.Status)
	require.True(t, deleted.Deleted)

	alreadyAfter := readCindyMigrationAccount(t, ctx, db, "cindy-already-hashed")
	require.Equal(t, alreadyBefore.RowVersion, alreadyAfter.RowVersion)
	for _, name := range []string{"ordinary", "cindy-path", "cindy-oauth"} {
		snapshot := readCindyMigrationAccount(t, ctx, db, name)
		require.JSONEq(t, `{
			"openai_alpha_search_mode":"direct",
			"openai_prompt_cache_key_mode":"passthrough"
		}`, string(snapshot.Extra), name)
	}

	firstRowVersion := active.RowVersion
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))
	second := readCindyMigrationAccount(t, ctx, db, "cindy-active")
	require.Equal(t, firstRowVersion, second.RowVersion)
	require.JSONEq(t, string(active.Extra), string(second.Extra))
}
