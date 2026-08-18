package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPostgresBatchInsertBuildsAtomicMultiRowStatement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`INSERT INTO "events" \("name","count"\) VALUES \(\$1,\$2\),\(\$3,\$4\)`).
		WithArgs("first", 1, "second", 2).
		WillReturnResult(sqlmock.NewResult(0, 2))

	inserted, err := postgresBatchInsert(
		context.Background(),
		db,
		"events",
		[]string{"name", "count"},
		[][]any{{"first", 1}, {"second", 2}},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresBatchInsertRejectsInvalidRowsBeforeExecution(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	inserted, err := postgresBatchInsert(
		context.Background(),
		db,
		"events",
		[]string{"name", "count"},
		[][]any{{"missing-count"}},
	)
	require.ErrorContains(t, err, "has 1 values, want 2")
	require.Zero(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresBatchInsertRejectsUnexpectedAffectedCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`INSERT INTO "events" \("name"\) VALUES \(\$1\)`).
		WithArgs("first").
		WillReturnResult(sqlmock.NewResult(0, 0))

	inserted, err := postgresBatchInsert(
		context.Background(),
		db,
		"events",
		[]string{"name"},
		[][]any{{"first"}},
	)
	require.ErrorContains(t, err, "affected 0 rows, want 1")
	require.Zero(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}
