package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
)

const (
	historicalCindy229      = "229_cindy_platform_wire_identity.sql"
	historicalCindy234      = "234_fix_cindy_platform_projection_round_trip.sql"
	historicalCindy235      = "235_preserve_mixed_openai_cindy_groups.sql"
	historicalCindy236      = "236_bind_strict_cindy_groups_to_catalog_channel.sql"
	historicalQuotaPurge238 = "238_purge_unlimited_user_platform_quotas.sql"
	historicalQuota241      = "241_minimax_cindy_platform_quota_compat.sql"
	cindyNullTotal252       = "252_cindy_identity_null_total.sql"
)

// HistoricalMigrationPreflightError is deliberately limited to classifications,
// counts and at most five numeric IDs. It must not expose credentials, URLs,
// arbitrary ledger contents or the server's SQL error detail.
type HistoricalMigrationPreflightError struct {
	Code      string
	Migration string
	Entity    string
	Reason    string
	Count     int64
	SampleIDs []int64
	cause     error
}

func (e *HistoricalMigrationPreflightError) Error() string {
	return fmt.Sprintf("historical migration preflight: code=%s migration=%s entity=%s reason=%s count=%d sample_ids=%v",
		e.Code, e.Migration, e.Entity, e.Reason, e.Count, e.SampleIDs)
}

// Preserve startup's existing transient-connection retry classification without
// including upstream SQL detail in Error(). Semantic refusals have no cause.
func (e *HistoricalMigrationPreflightError) Unwrap() error { return e.cause }

// This interface intentionally has no Exec or transaction-writing operation.
type historicalMigrationReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CheckHistoricalMigrationPreconditions performs the same read-only check used
// by startup before its first metadata/Atlas write. A successful check is a
// snapshot, not a lock against ordinary writers or a promise that the entire
// historical migration chain is atomic. Migration 252 rechecks under table locks.
func CheckHistoricalMigrationPreconditions(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", "", "database", "nil_database")
	}
	return checkHistoricalMigrationPreconditionsFS(ctx, db, migrations.FS)
}

func historicalPreflightFailure(code, migration, entity, reason string, cause ...error) *HistoricalMigrationPreflightError {
	failure := &HistoricalMigrationPreflightError{Code: code, Migration: migration, Entity: entity, Reason: reason}
	if len(cause) > 0 {
		failure.cause = cause[0]
	}
	return failure
}

func isHistoricalPreflightMigration(name string) bool {
	switch name {
	case historicalCindy229, historicalCindy234, historicalCindy235, historicalCindy236, historicalQuota241, cindyNullTotal252:
		return true
	default:
		return false
	}
}

type historicalPreflightTable struct {
	kind    string
	columns map[string]bool
}

type historicalPreflightSchema map[string]*historicalPreflightTable

func (s historicalPreflightSchema) has(table, column string) bool {
	t := s[table]
	return t != nil && t.kind != "" && (column == "" || t.columns[column])
}

func (s historicalPreflightSchema) require(migration, table string, columns ...string) error {
	t := s[table]
	if t == nil || t.kind == "" {
		return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", migration, table, "missing_table")
	}
	if t.kind != "r" && t.kind != "p" {
		return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", migration, table, "not_a_table")
	}
	for _, column := range columns {
		if !t.columns[column] {
			return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", migration, table, "missing_column_"+column)
		}
	}
	return nil
}

const historicalPreflightSchemaSQL = `
SELECT names.name, COALESCE(c.relkind::text, ''), COALESCE(a.attname, '')
FROM (VALUES ('schema_migrations'), ('accounts'), ('groups'), ('account_groups'),
             ('cindy_platform_v1_projection'), ('user_platform_quotas')) AS names(name)
LEFT JOIN pg_class c ON c.oid = to_regclass(names.name)
LEFT JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY names.name, a.attnum`

func readHistoricalPreflightSchema(ctx context.Context, db historicalMigrationReader) (historicalPreflightSchema, error) {
	rows, err := db.QueryContext(ctx, historicalPreflightSchemaSQL)
	if err != nil {
		return nil, historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", "", "schema", "catalog_query_failed", err)
	}
	defer func() { _ = rows.Close() }()
	schema := historicalPreflightSchema{}
	for rows.Next() {
		var name, kind, column string
		if err := rows.Scan(&name, &kind, &column); err != nil {
			return nil, historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", "", "schema", "catalog_row_unavailable")
		}
		if schema[name] == nil {
			schema[name] = &historicalPreflightTable{kind: kind, columns: map[string]bool{}}
		}
		if column != "" {
			schema[name].columns[column] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", "", "schema", "catalog_read_failed", err)
	}
	return schema, nil
}

func checkHistoricalMigrationPreconditionsFS(ctx context.Context, db historicalMigrationReader, fsys fs.FS) error {
	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", "", "filesystem", "list_failed")
	}
	active := false
	for _, name := range files {
		active = active || isHistoricalPreflightMigration(name)
	}
	if !active {
		return nil // Do not add database interactions to unrelated/test migration FSs.
	}
	sort.Strings(files)
	checksums := map[string]string{}
	for _, name := range files {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", name, "filesystem", "read_failed")
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			sum := sha256.Sum256([]byte(content))
			checksums[name] = hex.EncodeToString(sum[:])
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	schema, err := readHistoricalPreflightSchema(ctx, db)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	ledgerCount := 0
	if schema.has("schema_migrations", "") {
		if err := schema.require("", "schema_migrations", "filename", "checksum"); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, "SELECT filename, checksum FROM schema_migrations ORDER BY filename")
		if err != nil {
			return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", "", "schema_migrations", "ledger_query_failed", err)
		}
		for rows.Next() {
			var name, stored string
			if err := rows.Scan(&name, &stored); err != nil {
				_ = rows.Close()
				return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", "", "schema_migrations", "ledger_row_unavailable")
			}
			ledgerCount++
			checksum, exists := checksums[name]
			if !exists {
				continue
			}
			if stored != checksum && !isMigrationChecksumCompatible(name, stored, checksum) {
				_ = rows.Close()
				failure := historicalPreflightFailure("HISTORICAL_MIGRATION_CHECKSUM_MISMATCH", name, "schema_migrations", "applied_checksum_unrecognized")
				failure.Count = 1
				return failure
			}
			if applied[name] {
				_ = rows.Close()
				return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", name, "schema_migrations", "duplicate_filename")
			}
			applied[name] = true
		}
		readErr := rows.Err()
		_ = rows.Close()
		if readErr != nil {
			return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", "", "schema_migrations", "ledger_read_failed", readErr)
		}
	}
	pending := func(name string) bool { return checksums[name] != "" && !applied[name] }
	needsData := false
	for name := range checksums {
		needsData = needsData || (isHistoricalPreflightMigration(name) && pending(name))
	}
	if !needsData {
		return nil
	}
	if !schema.has("accounts", "") && !schema.has("groups", "") && !schema.has("account_groups", "") &&
		!schema.has("user_platform_quotas", "") && !schema.has("cindy_platform_v1_projection", "") {
		var empty bool
		err := db.QueryRowContext(ctx, `SELECT NOT EXISTS (
            SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
              AND c.relname NOT IN ('schema_migrations', 'atlas_schema_revisions')
        )`).Scan(&empty)
		if err != nil {
			return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", "", "schema", "fresh_schema_query_failed", err)
		}
		if empty && ledgerCount == 0 {
			return nil // Truly fresh: no business relations or claimed applied history.
		}
		return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", "", "schema", "missing_business_tables_in_initialized_schema")
	}

	if pending(historicalCindy229) || pending(historicalCindy234) || pending(historicalCindy235) || pending(cindyNullTotal252) {
		for _, item := range []struct {
			table   string
			columns []string
		}{
			{"accounts", []string{"id", "platform", "type", "credentials", "deleted_at"}},
			{"groups", []string{"id", "platform", "fallback_group_id", "deleted_at"}},
			{"account_groups", []string{"account_id", "group_id"}},
		} {
			if err := schema.require("cindy_preconditions", item.table, item.columns...); err != nil {
				return err
			}
		}
	}
	if pending(historicalCindy229) {
		if err := checkHistoricalBlockedIDs(ctx, db, historicalCindy229, "groups", "historical_fallback_guard", "HISTORICAL_CINDY_PROJECTION_PRECONDITION", historicalCindy229GuardSQL); err != nil {
			return err
		}
	}
	if pending(historicalCindy234) || pending(historicalCindy235) || pending(cindyNullTotal252) {
		if err := requireHistoricalProjectionSchema(schema, pending); err != nil {
			return err
		}
		preview := historicalProjectionPreviewSQL(schema, pending)
		if pending(historicalCindy234) {
			query := preview + historicalActiveFallbackGuardSQL("historical_accounts_234", "historical_groups_234")
			if err := checkHistoricalBlockedIDs(ctx, db, historicalCindy234, "groups", "historical_fallback_guard", "HISTORICAL_CINDY_PROJECTION_PRECONDITION", query); err != nil {
				return err
			}
		}
		if pending(historicalCindy235) {
			query := preview + historicalActiveFallbackGuardSQL("historical_accounts_235", "historical_groups_235")
			if err := checkHistoricalBlockedIDs(ctx, db, historicalCindy235, "groups", "historical_fallback_guard_after_rollback", "HISTORICAL_CINDY_PROJECTION_PRECONDITION", query); err != nil {
				return err
			}
		}
		if pending(cindyNullTotal252) {
			if err := schema.require(cindyNullTotal252, "groups", "fallback_group_id_on_invalid_request"); err != nil {
				return err
			}
			if err := checkHistoricalBlockedIDs(ctx, db, cindyNullTotal252, "accounts", "null_total_identity", "CINDY_IDENTITY_NULL_TOTAL_VIOLATIONS", preview+historicalNullTotalAccountsSQL); err != nil {
				return err
			}
			if err := checkHistoricalBlockedIDs(ctx, db, cindyNullTotal252, "groups", "null_total_topology", "CINDY_GROUP_NULL_TOTAL_VIOLATIONS", preview+historicalNullTotalGroupsSQL); err != nil {
				return err
			}
		}
	}
	if pending(historicalQuota241) {
		if err := schema.require(historicalQuota241, "user_platform_quotas", "id", "platform"); err != nil {
			return err
		}
		retained := ""
		if pending(historicalQuotaPurge238) {
			if err := schema.require(historicalQuota241, "user_platform_quotas", "daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd"); err != nil {
				return err
			}
			retained = " AND (daily_limit_usd IS NOT NULL OR weekly_limit_usd IS NOT NULL OR monthly_limit_usd IS NOT NULL)"
		}
		query := `SELECT id, COUNT(*) OVER () FROM user_platform_quotas
            WHERE platform NOT IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                                   'kimi', 'zhipu', 'deepseek', 'minimax', 'cindy')` + retained + ` ORDER BY id LIMIT 5`
		if err := checkHistoricalBlockedIDs(ctx, db, historicalQuota241, "user_platform_quotas", "retained_platform_outside_241_check", "HISTORICAL_QUOTA_241_UNSUPPORTED_PLATFORM", query); err != nil {
			return err
		}
	}
	return nil
}

func checkHistoricalBlockedIDs(ctx context.Context, db historicalMigrationReader, migration, entity, reason, code, query string) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", migration, entity, "precondition_query_failed", err)
	}
	defer func() { _ = rows.Close() }()
	failure := historicalPreflightFailure(code, migration, entity, reason)
	for rows.Next() {
		var id, count int64
		if err := rows.Scan(&id, &count); err != nil {
			return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", migration, entity, "precondition_row_unavailable")
		}
		failure.Count = count
		failure.SampleIDs = append(failure.SampleIDs, id)
	}
	if err := rows.Err(); err != nil {
		return historicalPreflightFailure("HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", migration, entity, "precondition_read_failed", err)
	}
	if failure.Count > 0 {
		return failure
	}
	return nil
}

const historicalCindy229GuardSQL = `SELECT g.id, COUNT(*) OVER () FROM groups g
WHERE g.deleted_at IS NULL AND g.platform = 'openai' AND g.fallback_group_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.group_id = g.id)
  AND NOT EXISTS (
      SELECT 1 FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
      WHERE ag.group_id = g.id AND (a.deleted_at IS NOT NULL OR a.platform <> 'openai'
        OR a.type <> 'apikey' OR jsonb_typeof(a.credentials->'base_url') <> 'string'
        OR LOWER(BTRIM(a.credentials->>'base_url')) NOT IN ('https://api.laxarouter.ai', 'https://api.laxarouter.ai/'))
  ) ORDER BY g.id LIMIT 5`

func historicalActiveFallbackGuardSQL(accounts, groups string) string {
	// These identifiers are private constants, never caller-supplied SQL.
	return fmt.Sprintf(`SELECT g.id, COUNT(*) OVER () FROM %s g
WHERE g.deleted_at IS NULL AND g.platform = 'openai' AND g.fallback_group_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM account_groups ag JOIN %s a ON a.id = ag.account_id
              WHERE ag.group_id = g.id AND a.deleted_at IS NULL)
  AND NOT EXISTS (
      SELECT 1 FROM account_groups ag JOIN %s a ON a.id = ag.account_id
      WHERE ag.group_id = g.id AND a.deleted_at IS NULL AND (a.platform <> 'openai'
        OR a.type <> 'apikey' OR jsonb_typeof(a.credentials->'base_url') <> 'string'
        OR LOWER(BTRIM(a.credentials->>'base_url')) NOT IN ('https://api.laxarouter.ai', 'https://api.laxarouter.ai/'))
  ) ORDER BY g.id LIMIT 5`, groups, accounts, accounts)
}

func requireHistoricalProjectionSchema(schema historicalPreflightSchema, pending func(string) bool) error {
	for _, table := range []string{"accounts", "groups"} {
		for _, column := range []string{"wire_platform", "provider_profile"} {
			if !schema.has(table, column) && pending(historicalCindy229) {
				continue
			}
			if err := schema.require("cindy_preconditions", table, column); err != nil {
				return err
			}
		}
	}
	if schema.has("cindy_platform_v1_projection", "") {
		return schema.require("cindy_preconditions", "cindy_platform_v1_projection", "entity_type", "entity_id", "original_platform", "original_wire_platform", "original_provider_profile")
	}
	if !pending(historicalCindy229) && (pending(historicalCindy234) || pending(historicalCindy235)) {
		return historicalPreflightFailure("HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", "cindy_preconditions", "cindy_platform_v1_projection", "missing_table")
	}
	return nil
}

func historicalProjectionPreviewSQL(schema historicalPreflightSchema, pending func(string) bool) string {
	// Read-only identity transitions relevant to the known guards. Discovery only
	// promotes exact valid accounts/closed groups and cannot promote a fallback
	// group or its members. Do not run discovery, create its temp tables, or claim
	// this preview models unrelated historical DML or the entire migration chain.
	parts := make([]string, 0, 6)
	for _, table := range []string{"accounts", "groups"} {
		alias, entity := "a", "account"
		extra := "a.type, a.credentials, a.deleted_at"
		if table == "groups" {
			alias, entity = "g", "group"
			fallback := "NULL::bigint"
			if schema.has(table, "fallback_group_id_on_invalid_request") {
				fallback = "g.fallback_group_id_on_invalid_request"
			}
			extra = "g.deleted_at, g.fallback_group_id, " + fallback + " AS fallback_group_id_on_invalid_request"
		}
		wire, profile := "''::text", "''::text"
		if schema.has(table, "wire_platform") {
			wire = alias + ".wire_platform"
		}
		if schema.has(table, "provider_profile") {
			profile = alias + ".provider_profile"
		}
		if pending(historicalCindy229) {
			profile = fmt.Sprintf("CASE WHEN %s = '' THEN '' ELSE %s END", wire, profile)
			wire = fmt.Sprintf("CASE WHEN %s = '' THEN LOWER(BTRIM(%s.platform)) ELSE %s END", wire, alias, wire)
		}
		base := "historical_" + table
		parts = append(parts, fmt.Sprintf("%s_229 AS (SELECT %s.id, %s.platform, %s AS wire_platform, %s AS provider_profile, %s FROM %s %s)", base, alias, alias, wire, profile, extra, table, alias))
		join := ""
		hasProjection := schema.has("cindy_platform_v1_projection", "")
		if hasProjection {
			join = fmt.Sprintf(" LEFT JOIN cindy_platform_v1_projection p ON p.entity_type = '%s' AND p.entity_id = %s.id", entity, alias)
		}
		identity := alias + ".platform, " + alias + ".wire_platform, " + alias + ".provider_profile"
		if pending(historicalCindy234) && hasProjection {
			match := fmt.Sprintf("p.entity_id IS NOT NULL AND (%s.platform, %s.wire_platform, %s.provider_profile) IS NOT DISTINCT FROM (p.original_platform, p.original_wire_platform, p.original_provider_profile)", alias, alias, alias)
			identity = fmt.Sprintf("CASE WHEN %s THEN 'cindy' ELSE %s.platform END AS platform, CASE WHEN %s THEN 'openai' ELSE %s.wire_platform END AS wire_platform, CASE WHEN %s THEN 'cindy_laxa_v1' ELSE %s.provider_profile END AS provider_profile", match, alias, match, alias, match, alias)
		}
		parts = append(parts, fmt.Sprintf("%s_234 AS (SELECT %s.id, %s, %s FROM %s_229 %s%s)", base, alias, identity, extra, base, alias, join))
		identity = alias + ".platform, " + alias + ".wire_platform, " + alias + ".provider_profile"
		if pending(historicalCindy235) {
			originalPlatform, originalWire, originalProfile := "'openai'", "'openai'", "''"
			if hasProjection {
				originalPlatform = "CASE WHEN p.entity_id IS NULL THEN 'openai' ELSE p.original_platform END"
				originalWire = "CASE WHEN p.entity_id IS NULL THEN 'openai' ELSE p.original_wire_platform END"
				originalProfile = "CASE WHEN p.entity_id IS NULL THEN '' ELSE p.original_provider_profile END"
			}
			identity = fmt.Sprintf("CASE WHEN %s.platform = 'cindy' THEN %s ELSE %s.platform END AS platform, CASE WHEN %s.platform = 'cindy' THEN %s ELSE %s.wire_platform END AS wire_platform, CASE WHEN %s.platform = 'cindy' THEN %s ELSE %s.provider_profile END AS provider_profile", alias, originalPlatform, alias, alias, originalWire, alias, alias, originalProfile, alias)
		}
		parts = append(parts, fmt.Sprintf("%s_235 AS (SELECT %s.id, %s, %s FROM %s_234 %s%s)", base, alias, identity, extra, base, alias, join))
	}
	return "WITH " + strings.Join(parts, ",\n") + "\n"
}

const historicalNullTotalAccountsSQL = `SELECT a.id, COUNT(*) OVER () FROM historical_accounts_235 a
WHERE (a.platform <> 'cindy' OR (
    a.wire_platform = 'openai' AND a.provider_profile = 'cindy_laxa_v1' AND a.type = 'apikey'
    AND jsonb_typeof(a.credentials->'base_url') = 'string'
    AND LOWER(BTRIM(a.credentials->>'base_url')) IN ('https://api.laxarouter.ai', 'https://api.laxarouter.ai/')
)) IS NOT TRUE ORDER BY a.id LIMIT 5`

const historicalNullTotalGroupsSQL = `SELECT g.id, COUNT(*) OVER () FROM historical_groups_235 g
WHERE g.deleted_at IS NULL AND g.platform = 'cindy' AND (
    g.wire_platform IS DISTINCT FROM 'openai' OR g.provider_profile IS DISTINCT FROM 'cindy_laxa_v1'
    OR g.fallback_group_id IS NOT NULL OR g.fallback_group_id_on_invalid_request IS NOT NULL
    OR EXISTS (
        SELECT 1 FROM account_groups ag JOIN historical_accounts_235 a ON a.id = ag.account_id
        WHERE ag.group_id = g.id AND a.deleted_at IS NULL AND (
            a.platform = 'cindy' AND a.wire_platform = 'openai' AND a.provider_profile = 'cindy_laxa_v1'
            AND a.type = 'apikey' AND jsonb_typeof(a.credentials->'base_url') = 'string'
            AND LOWER(BTRIM(a.credentials->>'base_url')) IN ('https://api.laxarouter.ai', 'https://api.laxarouter.ai/')
        ) IS NOT TRUE
    )
) ORDER BY g.id LIMIT 5`
