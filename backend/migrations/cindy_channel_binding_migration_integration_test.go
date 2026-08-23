//go:build integration

package migrations_test

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration236BindsStrictCindyGroupsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, cindyChannelBindingFixtureSQL))
	require.NoError(t, execRemoteSkillSQL(ctx, db, `
		INSERT INTO groups (id, name, platform, wire_platform, provider_profile) VALUES
			(11, 'strict-cindy', 'cindy', 'openai', 'cindy_laxa_v1'),
			(12, 'mixed-compatibility', 'openai', 'openai', '');
		INSERT INTO accounts (id, platform, wire_platform, provider_profile, type, credentials) VALUES
			(21, 'cindy', 'openai', 'cindy_laxa_v1', 'apikey', '{"base_url":"https://api.laxarouter.ai"}'),
			(22, 'openai', 'openai', '', 'apikey', '{"base_url":"https://api.openai.com"}');
		INSERT INTO account_groups (account_id, group_id) VALUES (21, 11), (21, 12), (22, 12);
		INSERT INTO api_keys (key, group_id) VALUES ('strict-key', 11);
	`))

	migration, err := dbmigrations.FS.ReadFile("236_bind_strict_cindy_groups_to_catalog_channel.sql")
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))
	}

	var channelCount, strictBindings, mixedBindings, pricingRows, schedulerEvents, authEvents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE features_config->>'cindy_catalog_managed' = 'cindy_laxa_v1'`).Scan(&channelCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_groups WHERE group_id = 11`).Scan(&strictBindings))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_groups WHERE group_id = 12`).Scan(&mixedBindings))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_model_pricing`).Scan(&pricingRows))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE group_id = 11 AND event_type = 'group_changed'`).Scan(&schedulerEvents))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_cache_invalidation_outbox`).Scan(&authEvents))
	require.Equal(t, 1, channelCount)
	require.Equal(t, 1, strictBindings)
	require.Zero(t, mixedBindings)
	require.Zero(t, pricingRows)
	require.Equal(t, 1, schedulerEvents)
	require.Equal(t, 1, authEvents)
}

func TestMigration236FailsClosedWhenStrictCindyGroupAlreadyHasAnotherChannel(t *testing.T) {
	ctx := context.Background()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, cindyChannelBindingFixtureSQL))
	require.NoError(t, execRemoteSkillSQL(ctx, db, `
		INSERT INTO groups (id, name, platform, wire_platform, provider_profile)
		VALUES (31, 'strict-cindy', 'cindy', 'openai', 'cindy_laxa_v1');
		INSERT INTO accounts (id, platform, wire_platform, provider_profile, type, credentials)
		VALUES (41, 'cindy', 'openai', 'cindy_laxa_v1', 'apikey', '{"base_url":"https://api.laxarouter.ai"}');
		INSERT INTO account_groups (account_id, group_id) VALUES (41, 31);
		INSERT INTO channels (id, name) VALUES (51, 'operator-channel');
		INSERT INTO channel_groups (channel_id, group_id) VALUES (51, 31);
	`))

	migration, err := dbmigrations.FS.ReadFile("236_bind_strict_cindy_groups_to_catalog_channel.sql")
	require.NoError(t, err)
	err = execRemoteSkillSQL(ctx, db, string(migration))
	require.ErrorContains(t, err, "strict Cindy groups already belong to another channel")
}

func TestMigration236MaintainsBindingAcrossMembershipAndIdentityMutations(t *testing.T) {
	ctx := context.Background()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, cindyChannelBindingFixtureSQL))
	require.NoError(t, execRemoteSkillSQL(ctx, db, `
		INSERT INTO groups (id, name, platform, wire_platform, provider_profile) VALUES
			(61, 'future-cindy', 'cindy', 'openai', 'cindy_laxa_v1'),
			(99, 'fallback', 'openai', 'openai', '');
		INSERT INTO accounts (id, platform, wire_platform, provider_profile, type, credentials)
		VALUES
			(71, 'cindy', 'openai', 'cindy_laxa_v1', 'apikey', '{"base_url":"https://api.laxarouter.ai"}'),
			(72, 'openai', 'openai', '', 'apikey', '{"base_url":"https://api.openai.com"}');
	`))
	migration, err := dbmigrations.FS.ReadFile("236_bind_strict_cindy_groups_to_catalog_channel.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))

	var bindings int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_groups WHERE group_id = 61`).Scan(&bindings))
	require.Zero(t, bindings, "empty canonical group is not strict")

	require.NoError(t, execRemoteSkillSQL(ctx, db, `INSERT INTO account_groups (account_id, group_id) VALUES (71, 61)`))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_groups WHERE group_id = 61`).Scan(&bindings))
	require.Equal(t, 1, bindings)

	_, err = db.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id) VALUES (72, 61)`)
	require.ErrorContains(t, err, "mixed or cross-profile membership")
	_, err = db.ExecContext(ctx, `UPDATE groups SET fallback_group_id_on_invalid_request = 99 WHERE id = 61`)
	require.ErrorContains(t, err, "cannot configure fallback groups")
	_, err = db.ExecContext(ctx, `UPDATE accounts SET provider_profile = 'cindy_future_v2' WHERE id = 71`)
	require.ErrorContains(t, err, "mixed or cross-profile membership")

	require.NoError(t, execRemoteSkillSQL(ctx, db, `DELETE FROM account_groups WHERE account_id = 71 AND group_id = 61`))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_groups WHERE group_id = 61`).Scan(&bindings))
	require.Zero(t, bindings)
}

func TestMigration236RejectsMarkedChannelCustomData(t *testing.T) {
	ctx := context.Background()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, cindyChannelBindingFixtureSQL))
	require.NoError(t, execRemoteSkillSQL(ctx, db, `
		INSERT INTO channels (
			name, description, status, model_mapping, billing_model_source,
			restrict_models, features, features_config, apply_pricing_to_account_stats
		) VALUES (
			'renamed', 'custom', 'active', '{"cindy":{}}', 'channel_mapped',
			TRUE, '["custom"]', '{"cindy_catalog_managed":"cindy_laxa_v1","extra":true}', TRUE
		);
	`))
	migration, err := dbmigrations.FS.ReadFile("236_bind_strict_cindy_groups_to_catalog_channel.sql")
	require.NoError(t, err)
	err = execRemoteSkillSQL(ctx, db, string(migration))
	require.ErrorContains(t, err, "ambiguous name, mapping, pricing, features, or group topology")
}

const cindyChannelBindingFixtureSQL = `
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE groups (
    id BIGINT PRIMARY KEY,
    name TEXT NOT NULL,
    platform TEXT NOT NULL,
    wire_platform TEXT NOT NULL DEFAULT '',
    provider_profile TEXT NOT NULL DEFAULT '',
    fallback_group_id BIGINT,
    fallback_group_id_on_invalid_request BIGINT,
    status TEXT NOT NULL DEFAULT 'active',
    is_exclusive BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMPTZ
);
CREATE TABLE accounts (
    id BIGINT PRIMARY KEY,
    platform TEXT NOT NULL,
    wire_platform TEXT NOT NULL DEFAULT '',
    provider_profile TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    credentials JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'active',
    deleted_at TIMESTAMPTZ
);
CREATE TABLE account_groups (
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    PRIMARY KEY (account_id, group_id)
);
CREATE TABLE api_keys (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL,
    group_id BIGINT,
    deleted_at TIMESTAMPTZ
);
CREATE TABLE channels (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    model_mapping JSONB NOT NULL DEFAULT '{}',
    billing_model_source TEXT NOT NULL DEFAULT 'channel_mapped',
    restrict_models BOOLEAN NOT NULL DEFAULT FALSE,
    features TEXT NOT NULL DEFAULT '',
    features_config JSONB NOT NULL DEFAULT '{}',
    apply_pricing_to_account_stats BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE channel_groups (
    id BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    UNIQUE (group_id)
);
CREATE TABLE channel_model_pricing (
    id BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    platform TEXT NOT NULL
);
CREATE TABLE channel_account_stats_pricing_rules (
    id BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE
);
CREATE TABLE scheduler_outbox (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    account_id BIGINT,
    group_id BIGINT,
    payload JSONB,
    dedup_key TEXT
);
CREATE UNIQUE INDEX idx_scheduler_outbox_pending_dedup_key
    ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
CREATE TABLE auth_cache_invalidation_outbox (
    id BIGSERIAL PRIMARY KEY,
    cache_key CHAR(64) NOT NULL
);
CREATE OR REPLACE FUNCTION enqueue_group_api_key_auth_cache_invalidations(target_group_id BIGINT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys k
    WHERE k.group_id = target_group_id AND k.deleted_at IS NULL AND k.key <> '';
END;
$$;
`
