//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var remoteSkillMigrationDSN string
var remoteSkillMigrationDB *sql.DB
var remoteSkillMigrationSchemaSequence uint64

func TestMain(m *testing.M) {
	ctx := context.Background()
	remoteSkillMigrationDSN = strings.TrimSpace(os.Getenv("SUB2API_MIGRATION_TEST_DSN"))
	var container *tcpostgres.PostgresContainer
	var err error
	if remoteSkillMigrationDSN == "" {
		if runtime.GOOS == "windows" {
			fmt.Fprintln(os.Stderr, "remote-skill migration integration tests require SUB2API_MIGRATION_TEST_DSN on Windows")
			os.Exit(0)
		}
		container, err = tcpostgres.Run(
			ctx,
			"postgres:18.1-alpine3.23",
			tcpostgres.WithDatabase("sub2api_migration_test"),
			tcpostgres.WithUsername("postgres"),
			tcpostgres.WithPassword("postgres"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err == nil {
			remoteSkillMigrationDSN, err = container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
		}
	}
	if err == nil {
		remoteSkillMigrationDB, err = sql.Open("pgx/v5", remoteSkillMigrationDSN)
	}
	if err == nil {
		err = remoteSkillMigrationDB.PingContext(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "remote-skill migration PostgreSQL setup:", err)
		if container != nil {
			_ = container.Terminate(ctx)
		}
		os.Exit(1)
	}

	code := m.Run()
	_ = remoteSkillMigrationDB.Close()
	if container != nil {
		_ = container.Terminate(ctx)
	}
	os.Exit(code)
}

func remoteSkillMigrationTestDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	require.NotNil(t, remoteSkillMigrationDB)
	schema := fmt.Sprintf("remote_skill_paired_%d", atomic.AddUint64(&remoteSkillMigrationSchemaSequence, 1))
	_, err := remoteSkillMigrationDB.ExecContext(context.Background(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)

	db, err := sql.Open("pgx/v5", remoteSkillMigrationDSN)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	require.NoError(t, db.PingContext(context.Background()))
	_, err = db.ExecContext(context.Background(), "SET search_path TO "+schema+", public")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = remoteSkillMigrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})
	return db, schema
}

func execRemoteSkillSQL(ctx context.Context, db *sql.DB, query string) error {
	_, err := db.ExecContext(ctx, query)
	return err
}
