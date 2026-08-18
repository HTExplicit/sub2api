package repository

import (
	"database/sql"
	"testing"
)

const postgresDriverTestDSN = "host=127.0.0.1 port=5432 user=postgres dbname=sub2api sslmode=disable TimeZone=UTC"

func TestPGXMajorVersionDriverIsRegistered(t *testing.T) {
	found := false
	for _, name := range sql.Drivers() {
		if name == "pgx/v5" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registered SQL drivers %v do not contain pgx/v5", sql.Drivers())
	}

	db, err := sql.Open("pgx/v5", postgresDriverTestDSN)
	if err != nil {
		t.Fatalf("sql.Open(pgx/v5): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func TestOpenPostgresDBPreservesPGXDriverWithServerTiming(t *testing.T) {
	for _, enableServerTiming := range []bool{false, true} {
		db, err := openPostgresDB(postgresDriverTestDSN, enableServerTiming)
		if err != nil {
			t.Fatalf("openPostgresDB(server timing=%v): %v", enableServerTiming, err)
		}
		if !isPostgresDriver(db) {
			_ = db.Close()
			t.Fatalf("openPostgresDB(server timing=%v) did not retain pgx driver", enableServerTiming)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close database pool: %v", err)
		}
	}
}
