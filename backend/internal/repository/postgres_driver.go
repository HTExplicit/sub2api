package repository

import (
	"database/sql"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgresDB opens a database/sql pool backed by pgx's PostgreSQL driver.
func OpenPostgresDB(dsn string) (*sql.DB, error) {
	return openPostgresDB(dsn, false)
}

func openPostgresDB(dsn string, enableServerTiming bool) (*sql.DB, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	connector := stdlib.GetConnector(*config)
	if enableServerTiming {
		connector = newServerTimingConnector(connector)
	}
	return sql.OpenDB(connector), nil
}
