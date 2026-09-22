package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestHistoricalMigrationPreflightUnrelatedFSHasNoDatabaseCalls(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, fsys := range []fstest.MapFS{{}, {"001_unit.sql": {Data: []byte("SELECT 1;")}}} {
		require.NoError(t, checkHistoricalMigrationPreconditionsFS(context.Background(), db, fsys))
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoricalMigrationPreflightRunnerRejectsChecksumBeforeAnyWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	fsys := fstest.MapFS{
		"001_prefix.sql":  {Data: []byte("CREATE TABLE must_not_be_created (id BIGINT);")},
		cindyNullTotal252: {Data: []byte("SELECT 252;")},
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).WithArgs(migrationsAdvisoryLockID).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(historicalPreflightSchemaSQL)).WillReturnRows(historicalPreflightLedgerSchemaRows())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT filename, checksum FROM schema_migrations ORDER BY filename")).
		WillReturnRows(sqlmock.NewRows([]string{"filename", "checksum"}).AddRow(cindyNullTotal252, "must-not-leak-ledger-value"))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err = applyMigrationsFS(context.Background(), db, fsys)
	var preflightErr *HistoricalMigrationPreflightError
	require.ErrorAs(t, err, &preflightErr)
	require.Equal(t, "HISTORICAL_MIGRATION_CHECKSUM_MISMATCH", preflightErr.Code)
	require.Equal(t, cindyNullTotal252, preflightErr.Migration)
	require.NotContains(t, err.Error(), "must-not-leak-ledger-value")
	require.NoError(t, mock.ExpectationsWereMet(), "no metadata, Atlas, prefix or ledger-writing expectation is allowed")
}

func TestHistoricalMigrationPreflightAppliedAndExistingCompatibilityRemainAccepted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	legacy, err := migrations.FS.ReadFile("054_drop_legacy_cache_columns.sql")
	require.NoError(t, err)
	latest := []byte("SELECT 252;")
	fsys := fstest.MapFS{
		"054_drop_legacy_cache_columns.sql": {Data: legacy},
		cindyNullTotal252:                   {Data: latest},
	}
	mock.ExpectQuery(regexp.QuoteMeta(historicalPreflightSchemaSQL)).WillReturnRows(historicalPreflightLedgerSchemaRows())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT filename, checksum FROM schema_migrations ORDER BY filename")).
		WillReturnRows(sqlmock.NewRows([]string{"filename", "checksum"}).
			AddRow("054_drop_legacy_cache_columns.sql", "182c193f3359946cf094090cd9e57d5c3fd9abaffbc1e8fc378646b8a6fa12b4").
			AddRow(cindyNullTotal252, historicalPreflightTestChecksum(latest)))
	require.NoError(t, checkHistoricalMigrationPreconditionsFS(context.Background(), db, fsys))
	require.NoError(t, mock.ExpectationsWereMet(), "already applied relevant files need no data probes")
}

func TestHistoricalMigrationPreflightQueryErrorsAreSanitized(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	upstream := &pq.Error{Code: "57P03", Message: "upstream detail with private credentials"}
	mock.ExpectQuery(regexp.QuoteMeta(historicalPreflightSchemaSQL)).
		WillReturnError(upstream)
	err = checkHistoricalMigrationPreconditionsFS(context.Background(), db,
		fstest.MapFS{cindyNullTotal252: {Data: []byte("SELECT 252;")}})
	var preflightErr *HistoricalMigrationPreflightError
	require.ErrorAs(t, err, &preflightErr)
	require.Equal(t, "HISTORICAL_MIGRATION_PREFLIGHT_UNAVAILABLE", preflightErr.Code)
	require.NotContains(t, err.Error(), "private credentials")
	require.ErrorIs(t, err, upstream)
	require.True(t, isTransientDatabaseInitializationError(err), "retain the existing 57P03 retry policy")
	require.False(t, isTransientDatabaseInitializationError(historicalPreflightFailure(
		"HISTORICAL_CINDY_PROJECTION_PRECONDITION", historicalCindy229, "groups", "historical_fallback_guard")))
	require.NoError(t, mock.ExpectationsWereMet())
}

func historicalPreflightLedgerSchemaRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "kind", "column"}).
		AddRow("schema_migrations", "r", "filename").AddRow("schema_migrations", "r", "checksum")
}

func historicalPreflightTestChecksum(data []byte) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(data))))
	return hex.EncodeToString(sum[:])
}
