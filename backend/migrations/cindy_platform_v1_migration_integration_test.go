//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
)

func TestCindyPlatformProjectionRoundTripMigrationIsStrictAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, cindyPlatformPre229FixtureSQL))

	migration229, err := dbmigrations.FS.ReadFile("229_cindy_platform_wire_identity.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration229)))

	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-active", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "legacy-cindy-active", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "ordinary-openai", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "ordinary-openai", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-with-deleted-ordinary", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "legacy-cindy-with-deleted-ordinary", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "deleted-ordinary-companion", "openai", "openai", "")

	require.NoError(t, execRemoteSkillSQL(ctx, db, `
		INSERT INTO accounts
			(id, name, platform, wire_platform, provider_profile, type, credentials, deleted_at)
		VALUES
			(3, 'cindy-soft-deleted', 'cindy', 'openai', 'cindy_laxa_v1', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai","api_key":"fixture-deleted"}', NOW());
		INSERT INTO groups
			(id, name, platform, wire_platform, provider_profile, fallback_group_id, deleted_at)
		VALUES
			(3, 'cindy-empty', 'cindy', 'openai', 'cindy_laxa_v1', NULL, NULL),
			(4, 'cindy-soft-deleted', 'cindy', 'openai', 'cindy_laxa_v1', NULL, NOW());

		SELECT * FROM project_cindy_platform_v1_to_legacy();

		INSERT INTO accounts
			(id, name, platform, type, credentials, deleted_at)
		VALUES
			(5, 'new-legacy-cindy', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai/","api_key":"fixture-new"}', NULL);
		INSERT INTO groups
			(id, name, platform, fallback_group_id, deleted_at)
		VALUES
			(5, 'new-legacy-cindy', 'openai', NULL, NULL);
		INSERT INTO account_groups (account_id, group_id) VALUES (5, 5);
	`))

	for _, tc := range []struct {
		table string
		name  string
	}{
		{table: "accounts", name: "legacy-cindy-active"},
		{table: "accounts", name: "cindy-soft-deleted"},
		{table: "groups", name: "legacy-cindy-active"},
		{table: "groups", name: "cindy-empty"},
		{table: "groups", name: "cindy-soft-deleted"},
	} {
		assertCindyPlatformIdentity(t, ctx, db, tc.table, tc.name, "openai", "openai", "")
	}

	migration234, err := dbmigrations.FS.ReadFile("234_fix_cindy_platform_projection_round_trip.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration234)))
	assertCindyProjectionForwardState(t, ctx, db)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration234)))
	assertCindyProjectionForwardState(t, ctx, db)

	require.NoError(t, execRemoteSkillSQL(ctx, db, `SELECT * FROM project_cindy_platform_v1_to_legacy()`))
	for _, name := range []string{"legacy-cindy-active", "cindy-soft-deleted"} {
		assertCindyPlatformIdentity(t, ctx, db, "accounts", name, "openai", "openai", "")
	}
	for _, name := range []string{"legacy-cindy-active", "cindy-empty", "cindy-soft-deleted", "legacy-cindy-with-deleted-ordinary"} {
		assertCindyPlatformIdentity(t, ctx, db, "groups", name, "openai", "openai", "")
	}
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-with-deleted-ordinary", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "deleted-ordinary-companion", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "new-legacy-cindy", "openai", "", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "new-legacy-cindy", "openai", "", "")

	require.NoError(t, execRemoteSkillSQL(ctx, db, `SELECT * FROM project_cindy_platform_v1_from_legacy()`))
	assertCindyProjectionForwardState(t, ctx, db)

	var promotedAccounts, promotedGroups int64
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT promoted_accounts, promoted_groups
		FROM project_cindy_platform_v1_from_legacy()
	`).Scan(&promotedAccounts, &promotedGroups))
	require.Zero(t, promotedAccounts)
	require.Zero(t, promotedGroups)
}

func TestMigration235PreservesMixedOpenAIGroupsAfterCanonicalReplay(t *testing.T) {
	ctx := context.Background()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, cindyPlatformPre229FixtureSQL))

	migration229, err := dbmigrations.FS.ReadFile("229_cindy_platform_wire_identity.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration229)))
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-active", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "legacy-cindy-active", "cindy", "openai", "cindy_laxa_v1")

	require.NoError(t, execRemoteSkillSQL(ctx, db, `
		INSERT INTO accounts
			(id, name, platform, wire_platform, provider_profile, type, credentials, deleted_at)
		VALUES
			(3, 'cindy-soft-deleted', 'cindy', 'openai', 'cindy_laxa_v1', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai","api_key":"fixture-deleted"}', NOW());
		INSERT INTO groups
			(id, name, platform, wire_platform, provider_profile, fallback_group_id, deleted_at)
		VALUES
			(3, 'cindy-empty', 'cindy', 'openai', 'cindy_laxa_v1', NULL, NULL),
			(4, 'cindy-soft-deleted', 'cindy', 'openai', 'cindy_laxa_v1', NULL, NOW());

		SELECT * FROM project_cindy_platform_v1_to_legacy();

		INSERT INTO accounts
			(id, name, platform, type, credentials, deleted_at)
		VALUES
			(8, 'ordinary-member-added-after-rollback', 'openai', 'apikey',
			 '{"base_url":"https://api.openai.com","api_key":"fixture-added-ordinary"}', NULL),
			(9, 'independent-pure-cindy', 'openai', 'apikey',
			 '{"base_url":"https://api.laxarouter.ai","api_key":"fixture-independent-cindy"}', NULL);
		INSERT INTO groups
			(id, name, platform, fallback_group_id, deleted_at)
		VALUES
			(9, 'independent-pure-cindy', 'openai', NULL, NULL);
		INSERT INTO account_groups (account_id, group_id) VALUES
			(8, 2),
			(9, 9);
	`))

	// Migration 234 replays the canonical ledger before it discovers current
	// topology, reproducing the mixed-group promotion that 235 must repair.
	migration234, err := dbmigrations.FS.ReadFile("234_fix_cindy_platform_projection_round_trip.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration234)))
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-active", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "legacy-cindy-active", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "ordinary-member-added-after-rollback", "openai", "", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "independent-pure-cindy", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "independent-pure-cindy", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "cindy-soft-deleted", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-empty", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-soft-deleted", "cindy", "openai", "cindy_laxa_v1")

	migration235, err := dbmigrations.FS.ReadFile("235_preserve_mixed_openai_cindy_groups.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration235)))

	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-active", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "legacy-cindy-active", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "ordinary-member-added-after-rollback", "openai", "", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "independent-pure-cindy", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "independent-pure-cindy", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "cindy-soft-deleted", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-empty", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-soft-deleted", "cindy", "openai", "cindy_laxa_v1")

	var ledgerRows int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM cindy_platform_v1_projection
		WHERE (entity_type, entity_id) IN (('account', 2), ('group', 2), ('account', 9), ('group', 9))
	`).Scan(&ledgerRows))
	require.Equal(t, 4, ledgerRows)

	require.NoError(t, execRemoteSkillSQL(ctx, db, `SELECT * FROM project_cindy_platform_v1_to_legacy()`))
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-active", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "legacy-cindy-active", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "independent-pure-cindy", "openai", "", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "independent-pure-cindy", "openai", "", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "cindy-soft-deleted", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-empty", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-soft-deleted", "openai", "openai", "")

	require.NoError(t, execRemoteSkillSQL(ctx, db, `SELECT * FROM project_cindy_platform_v1_from_legacy()`))
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "legacy-cindy-active", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "legacy-cindy-active", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "independent-pure-cindy", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "independent-pure-cindy", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "cindy-soft-deleted", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-empty", "cindy", "openai", "cindy_laxa_v1")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "cindy-soft-deleted", "cindy", "openai", "cindy_laxa_v1")

	for range 2 {
		var promotedAccounts, promotedGroups int64
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT promoted_accounts, promoted_groups
			FROM project_cindy_platform_v1_from_legacy()
		`).Scan(&promotedAccounts, &promotedGroups))
		require.Zero(t, promotedAccounts)
		require.Zero(t, promotedGroups)
	}
}

func assertCindyProjectionForwardState(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct {
		table string
		name  string
	}{
		{table: "accounts", name: "legacy-cindy-active"},
		{table: "accounts", name: "cindy-soft-deleted"},
		{table: "accounts", name: "new-legacy-cindy"},
		{table: "accounts", name: "legacy-cindy-with-deleted-ordinary"},
		{table: "groups", name: "legacy-cindy-active"},
		{table: "groups", name: "cindy-empty"},
		{table: "groups", name: "cindy-soft-deleted"},
		{table: "groups", name: "new-legacy-cindy"},
		{table: "groups", name: "legacy-cindy-with-deleted-ordinary"},
	} {
		assertCindyPlatformIdentity(t, ctx, db, tc.table, tc.name, "cindy", "openai", "cindy_laxa_v1")
	}
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "ordinary-openai", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "accounts", "deleted-ordinary-companion", "openai", "openai", "")
	assertCindyPlatformIdentity(t, ctx, db, "groups", "ordinary-openai", "openai", "openai", "")

	var deletedCompanionProjectionRows int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM cindy_platform_v1_projection p
		JOIN accounts a ON a.id = p.entity_id
		WHERE p.entity_type = 'account'
		  AND a.name = 'deleted-ordinary-companion'
	`).Scan(&deletedCompanionProjectionRows))
	require.Zero(t, deletedCompanionProjectionRows)

	var pending int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM cindy_platform_v1_projection p
		LEFT JOIN accounts a ON p.entity_type = 'account' AND a.id = p.entity_id
		LEFT JOIN groups g ON p.entity_type = 'group' AND g.id = p.entity_id
		WHERE (p.entity_type = 'account' AND
		       (a.platform, a.wire_platform, a.provider_profile) IS NOT DISTINCT FROM
		       (p.original_platform, p.original_wire_platform, p.original_provider_profile))
		   OR (p.entity_type = 'group' AND
		       (g.platform, g.wire_platform, g.provider_profile) IS NOT DISTINCT FROM
		       (p.original_platform, p.original_wire_platform, p.original_provider_profile))
	`).Scan(&pending))
	require.Zero(t, pending)
}

func assertCindyPlatformIdentity(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	table string,
	name string,
	wantPlatform string,
	wantWirePlatform string,
	wantProviderProfile string,
) {
	t.Helper()
	require.Contains(t, []string{"accounts", "groups"}, table)
	var platform, wirePlatform, providerProfile string
	err := db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT platform, wire_platform, provider_profile
		FROM %s
		WHERE name = $1
	`, table), name).Scan(&platform, &wirePlatform, &providerProfile)
	require.NoError(t, err)
	require.Equal(t, wantPlatform, platform, "%s %s platform", table, name)
	require.Equal(t, wantWirePlatform, wirePlatform, "%s %s wire platform", table, name)
	require.Equal(t, wantProviderProfile, providerProfile, "%s %s provider profile", table, name)
}

const cindyPlatformPre229FixtureSQL = `
CREATE TABLE groups (
	id BIGINT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	platform VARCHAR(50) NOT NULL,
	fallback_group_id BIGINT,
	deleted_at TIMESTAMPTZ
);

CREATE TABLE accounts (
	id BIGINT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	platform VARCHAR(50) NOT NULL,
	type VARCHAR(50) NOT NULL,
	credentials JSONB NOT NULL DEFAULT '{}',
	deleted_at TIMESTAMPTZ
);

CREATE TABLE account_groups (
	account_id BIGINT NOT NULL REFERENCES accounts(id),
	group_id BIGINT NOT NULL REFERENCES groups(id),
	PRIMARY KEY (account_id, group_id)
);

INSERT INTO groups (id, name, platform, fallback_group_id, deleted_at) VALUES
	(1, 'ordinary-openai', 'openai', NULL, NULL),
	(2, 'legacy-cindy-active', 'openai', NULL, NULL),
	(6, 'legacy-cindy-with-deleted-ordinary', 'openai', NULL, NULL);

INSERT INTO accounts (id, name, platform, type, credentials, deleted_at) VALUES
	(1, 'ordinary-openai', 'openai', 'apikey',
	 '{"base_url":"https://api.openai.com","api_key":"fixture-ordinary"}', NULL),
	(2, 'legacy-cindy-active', 'openai', 'apikey',
	 '{"base_url":"https://api.laxarouter.ai","api_key":"fixture-cindy"}', NULL),
	(6, 'legacy-cindy-with-deleted-ordinary', 'openai', 'apikey',
	 '{"base_url":"https://api.laxarouter.ai","api_key":"fixture-cindy-with-deleted-companion"}', NULL),
	(7, 'deleted-ordinary-companion', 'openai', 'apikey',
	 '{"base_url":"https://api.openai.com","api_key":"fixture-deleted-ordinary"}', NOW());

INSERT INTO account_groups (account_id, group_id) VALUES
	(1, 1),
	(2, 2),
	(6, 6),
	(7, 6);
`
