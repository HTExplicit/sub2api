//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func newAccountJobRepoTest(t *testing.T) (*accountJobRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &accountJobRepository{db: db}, mock
}

func accountJobTestColumns() []string {
	return []string{
		"id", "created_by", "kind", "idempotency_key", "request_hash", "status", "metadata",
		"target_count", "processed_count", "succeeded_count", "failed_count", "canceled_count",
		"cancel_requested_at", "error_code", "error_message", "retry_of_job_id", "attempt",
		"started_at", "finished_at", "created_at", "updated_at",
	}
}

func accountJobItemTestColumns() []string {
	return []string{
		"id", "job_id", "ordinal", "action", "target_account_id", "status", "metadata",
		"error_code", "error_message", "started_at", "finished_at", "created_at", "updated_at",
	}
}

func accountJobRows(now time.Time, id int64, status string) *sqlmock.Rows {
	return sqlmock.NewRows(accountJobTestColumns()).AddRow(
		id, int64(7), service.AccountJobKindBatchDelete, "key", regexp.MustCompile("^[0-9a-f]{64}$").FindString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		status, []byte(`{}`), 1, 0, 0, 0, 0, nil, nil, nil, nil, 1, nil, nil, now, now,
	)
}

func TestAccountJobRepositoryCreatePersistsJobAndItemsAtomically(t *testing.T) {
	repo, mock := newAccountJobRepoTest(t)
	now := time.Now().UTC()
	requestHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	targetID := int64(11)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO admin_account_jobs").
		WithArgs(int64(7), service.AccountJobKindBatchDelete, "key", requestHash, "cipher", sqlmock.AnyArg(), "{}", 1, nil, 1).
		WillReturnRows(accountJobRows(now, 41, service.AccountJobStatusPending))
	mock.ExpectExec("INSERT INTO admin_account_job_items").
		WithArgs(int64(41), 1, "delete", targetID, "{}").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	job, replayed, err := repo.Create(context.Background(), service.CreateAccountJobParams{
		CreatedBy: 7, Kind: service.AccountJobKindBatchDelete, IdempotencyKey: "key", RequestHash: requestHash,
		PayloadCipher: "cipher", PayloadExpires: now.Add(time.Hour), Metadata: json.RawMessage(`{}`),
		Items: []service.AccountJobItemSeed{{Ordinal: 1, Action: "delete", TargetAccountID: &targetID, Metadata: json.RawMessage(`{}`)}}, Attempt: 1,
	})
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, int64(41), job.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountJobRepositoryMarkInterruptedFailsRunningItemsAndJobs(t *testing.T) {
	repo, mock := newAccountJobRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE admin_account_job_items[\\s\\S]+error_code='interrupted'").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE admin_account_jobs[\\s\\S]+status='failed'[\\s\\S]+error_code='interrupted'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.MarkInterrupted(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountJobRepositoryClaimEnforcesGlobalAndAdminKindLimits(t *testing.T) {
	repo, mock := newAccountJobRepoTest(t)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("WITH candidate AS[\\s\\S]+COUNT\\(\\*\\).*status = 'running'.*< 2[\\s\\S]+active.created_by = pending.created_by[\\s\\S]+active.kind = pending.kind[\\s\\S]+FOR UPDATE SKIP LOCKED").
		WillReturnRows(accountJobRows(now, 51, service.AccountJobStatusRunning))
	mock.ExpectCommit()

	job, err := repo.Claim(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(51), job.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountJobRepositoryReserveCapsBatchAtOneHundred(t *testing.T) {
	repo, mock := newAccountJobRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectQuery("WITH picked AS[\\s\\S]+LIMIT \\$2[\\s\\S]+UPDATE admin_account_job_items").
		WithArgs(int64(51), service.AccountJobBatchSize).
		WillReturnRows(sqlmock.NewRows(accountJobItemTestColumns()))
	mock.ExpectCommit()

	items, err := repo.ReservePendingItems(context.Background(), 51, 500)
	require.NoError(t, err)
	require.Empty(t, items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountJobRepositoryPruneOnlyFinishedBeforeCutoff(t *testing.T) {
	repo, mock := newAccountJobRepoTest(t)
	cutoff := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	// The strict comparison retains jobs completed exactly at the cutoff;
	// unfinished jobs have no finished_at and cannot match the predicate.
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM admin_account_jobs WHERE finished_at IS NOT NULL AND finished_at < $1")).
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 2))

	require.NoError(t, repo.Prune(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
	// Results belong only to their parent job and are removed by the existing FK.
	migration, err := migrations.FS.ReadFile("232_admin_account_jobs.sql")
	require.NoError(t, err)
	require.Contains(t, string(migration), "job_id BIGINT NOT NULL REFERENCES admin_account_jobs(id) ON DELETE CASCADE")
}
