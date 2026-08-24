//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageBillingApplyStoresFirstSuccessfulAccountOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	expectInsert := mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO usage_billing_dedup"))
	expectInsert.WithArgs("req-1", int64(7), sqlmock.AnyArg(), int64(42))
	expectInsert.WillReturnRows(sqlmock.NewRows([]string{"id", "account_id"}).AddRow(1, 42))
	expectArchive := mock.ExpectQuery(regexp.QuoteMeta("SELECT request_fingerprint"))
	expectArchive.WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}))
	expectBalance := mock.ExpectQuery(regexp.QuoteMeta("UPDATE users"))
	expectBalance.WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(99.0))
	mock.ExpectCommit()

	repo := &usageBillingRepository{db: db}
	result, err := repo.Apply(context.Background(), &service.UsageBillingCommand{
		RequestID: "req-1", APIKeyID: 7, UserID: 9, AccountID: 42,
		RequestFingerprint: "fingerprint", BalanceCost: 1,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, int64(42), result.OwnerAccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}
