//go:build unit

package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func guardedCapabilityFixture() (*service.AccountCapabilityRun, []service.AccountCapabilityItem) {
	return &service.AccountCapabilityRun{CreatedBy: 5, Kind: "probe", IdempotencyKey: "key", RequestHash: strings.Repeat("a", 64),
			OnlyUntested: true, FolderIDs: []int64{7, 8}, AccountIDs: []int64{31}},
		[]service.AccountCapabilityItem{{Ordinal: 1, AccountID: 31, AccountName: "fixture", FolderID: 7,
			ConfigFingerprint: strings.Repeat("b", 64), UpstreamModel: "Real-Model", Protocol: "responses", Profile: "text"}}
}

func expectGuardedCapabilityStart(mock sqlmock.Sqlmock) {
	capabilityJobsBegin(mock)
	mock.ExpectQuery("SELECT .* FROM admin_capability_runs r WHERE r.created_by=\\$1 AND r.idempotency_key=\\$2").
		WithArgs(int64(5), "key").WillReturnError(sql.ErrNoRows)
}

func TestAccountCapabilityRepositoryGuardRejectsPriorAttemptBeforeInsert(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	run, items := guardedCapabilityFixture()
	expectGuardedCapabilityStart(mock)
	mock.ExpectQuery(regexp.QuoteMeta(capabilityUnattemptedConflictSQL)).
		WithArgs(int64(31), strings.Repeat("b", 64), "Real-Model", "responses").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()
	_, replayed, err := repo.Create(context.Background(), run, items)
	require.ErrorIs(t, err, service.ErrAccountCapabilityAlreadyAttempted)
	require.False(t, replayed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityRepositoryGuardCreatesOnlyUnattemptedWork(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	run, items := guardedCapabilityFixture()
	expectGuardedCapabilityStart(mock)
	mock.ExpectQuery(regexp.QuoteMeta(capabilityUnattemptedConflictSQL)).
		WithArgs(int64(31), strings.Repeat("b", 64), "Real-Model", "responses").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("INSERT INTO admin_capability_runs").
		WithArgs(int64(5), "probe", "key", strings.Repeat("a", 64), "[7,8]", "[31]", 1).
		WillReturnRows(capabilityJobsRunRows("pending"))
	mock.ExpectExec("INSERT INTO admin_capability_items").
		WithArgs(int64(1), 1, int64(31), "fixture", int64(7), strings.Repeat("b", 64), "Real-Model", "responses", "text", "[]").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	created, replayed, err := repo.Create(context.Background(), run, items)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, int64(1), created.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityRepositoryGuardRecoversSameIdempotentRun(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	run, items := guardedCapabilityFixture()
	capabilityJobsBegin(mock)
	mock.ExpectQuery("SELECT .* FROM admin_capability_runs r WHERE r.created_by=\\$1 AND r.idempotency_key=\\$2").
		WithArgs(int64(5), "key").WillReturnRows(capabilityJobsRunRows("pending"))
	mock.ExpectCommit()
	created, replayed, err := repo.Create(context.Background(), run, items)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, int64(1), created.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityRepositoryGuardTracksPossibleSendsAndBasicSuccess(t *testing.T) {
	for _, clause := range []string{"i.config_fingerprint=$2", "i.upstream_model=$3", "i.profile='text'", "i.protocol=$4",
		"i.status<>'canceled'", "i.dispatched_at IS NOT NULL", "i.request_count>0", "request_count_unknown",
		"i.status='succeeded'", "i.result->>'status'='alive'", "'chat_completions'", "'messages'"} {
		require.Contains(t, capabilityUnattemptedConflictSQL, clause)
	}
	require.NotContains(t, capabilityUnattemptedConflictSQL, "NOW()", "age must not authorize a repeat")
}
