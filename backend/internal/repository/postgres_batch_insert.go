package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const postgresMaxBindParameters = 65535

// postgresBatchInsert inserts a bounded batch in one atomic PostgreSQL statement.
func postgresBatchInsert(
	ctx context.Context,
	db *sql.DB,
	table string,
	columns []string,
	rows [][]any,
) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("nil database")
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if table == "" || len(columns) == 0 {
		return 0, fmt.Errorf("invalid batch insert target")
	}
	if len(rows) > postgresMaxBindParameters/len(columns) {
		return 0, fmt.Errorf("batch insert exceeds PostgreSQL bind parameter limit")
	}

	var query strings.Builder
	_, _ = query.WriteString("INSERT INTO ")
	_, _ = query.WriteString(pgx.Identifier{table}.Sanitize())
	_, _ = query.WriteString(" (")
	for index, column := range columns {
		if column == "" {
			return 0, fmt.Errorf("invalid empty batch insert column")
		}
		if index > 0 {
			_ = query.WriteByte(',')
		}
		_, _ = query.WriteString(pgx.Identifier{column}.Sanitize())
	}
	_, _ = query.WriteString(") VALUES ")

	args := make([]any, 0, len(rows)*len(columns))
	parameter := 1
	for rowIndex, row := range rows {
		if len(row) != len(columns) {
			return 0, fmt.Errorf("batch insert row %d has %d values, want %d", rowIndex, len(row), len(columns))
		}
		if rowIndex > 0 {
			_ = query.WriteByte(',')
		}
		_ = query.WriteByte('(')
		for columnIndex, value := range row {
			if columnIndex > 0 {
				_ = query.WriteByte(',')
			}
			_ = query.WriteByte('$')
			_, _ = query.WriteString(strconv.Itoa(parameter))
			parameter++
			args = append(args, value)
		}
		_ = query.WriteByte(')')
	}

	result, err := db.ExecContext(ctx, query.String(), args...)
	if err != nil {
		return 0, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if inserted != int64(len(rows)) {
		return inserted, fmt.Errorf("batch insert affected %d rows, want %d", inserted, len(rows))
	}
	return inserted, nil
}
