//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newCapabilityJobsRepoTest(t *testing.T) (*accountCapabilityRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &accountCapabilityRepository{db: db}, mock
}

func capabilityJobsItemColumns() []string {
	return []string{"id", "run_id", "ordinal", "account_id", "account_name", "folder_id", "config_fingerprint", "upstream_model", "protocol", "profile", "aliases", "status", "result", "request_count", "claimed_at", "dispatched_at", "finished_at", "created_at", "kind", "publication_superseded"}
}
func capabilityJobsRunColumns() []string {
	return []string{"id", "created_by", "kind", "idempotency_key", "request_hash", "folder_ids", "account_ids", "status", "target_count", "started_at", "finished_at", "created_at", "updated_at", "processed_count", "succeeded_count", "failed_count", "request_count", "possibly_sent_count"}
}

func capabilityJobsRunRows(status string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows(capabilityJobsRunColumns()).AddRow(1, 5, "probe", "key", strings.Repeat("a", 64), []byte(`[7,8]`), []byte(`[31]`), status, 2, nil, nil, now, now, 0, 0, 0, 0, 0)
}

func capabilityJobsBegin(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestAccountCapabilityJobsRepositoryCreateAtomicAndSecretFree(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO admin_capability_runs").WithArgs(int64(5), "probe", "key", strings.Repeat("a", 64), "[7,8]", "[31]", 1).WillReturnRows(capabilityJobsRunRows("pending"))
	mock.ExpectExec("INSERT INTO admin_capability_items").WithArgs(int64(1), 1, int64(31), "fixture", int64(7), strings.Repeat("b", 64), "Real-Model", "responses", "text", "[\"public\"]").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	run, replayed, err := repo.Create(context.Background(), &service.AccountCapabilityRun{CreatedBy: 5, Kind: "probe", IdempotencyKey: "key", RequestHash: strings.Repeat("a", 64), FolderIDs: []int64{7, 8}, AccountIDs: []int64{31}}, []service.AccountCapabilityItem{{Ordinal: 1, AccountID: 31, AccountName: "fixture", FolderID: 7, ConfigFingerprint: strings.Repeat("b", 64), UpstreamModel: "Real-Model", Protocol: "responses", Profile: "text", Aliases: []string{"public"}}})
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, int64(1), run.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityJobsRepositoryClaimGlobalAndPerAccountLimits(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	now := time.Now()
	capabilityJobsBegin(mock)
	mock.ExpectQuery(`WITH candidate AS[\s\S]+COUNT\(\*\)[\s\S]+< 2[\s\S]+active.account_id=i.account_id[\s\S]+FOR UPDATE OF i SKIP LOCKED`).
		WillReturnRows(sqlmock.NewRows(capabilityJobsItemColumns()).AddRow(9, 1, 1, 31, "fixture", 7, strings.Repeat("b", 64), "Real-Model", "responses", "text", []byte(`[]`), "running", []byte(`{}`), 0, now, nil, nil, now, "probe", false))
	mock.ExpectExec("UPDATE admin_capability_runs SET status='running'").WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	item, err := repo.Claim(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(9), item.ID)
	require.Nil(t, item.DispatchedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityJobsRepositoryPausePreventsUnsentDispatch(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	capabilityJobsBegin(mock)
	mock.ExpectExec(`UPDATE admin_capability_items i SET dispatched_at=NOW\(\)[\s\S]+i.dispatched_at IS NULL AND r.status IN \('pending','running'\)`).WithArgs(int64(9)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	dispatched, err := repo.MarkDispatched(context.Background(), 9)
	require.NoError(t, err)
	require.False(t, dispatched)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityJobsRepositoryTerminalEvidenceCannotBeOverwritten(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	capabilityJobsBegin(mock)
	mock.ExpectQuery(`UPDATE admin_capability_items[\s\S]+WHERE id=\$1 AND status='running' RETURNING run_id`).WithArgs(int64(9), "succeeded", `{"status":"alive"}`, 1).WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()
	require.NoError(t, repo.Complete(context.Background(), 9, "succeeded", json.RawMessage(`{"status":"alive"}`), 1))
	require.ErrorIs(t, repo.Complete(context.Background(), 9, "succeeded", json.RawMessage(`{"api_key":"forbidden"}`), 1), service.ErrAccountCapabilityInvalid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityJobsRepositoryRecoveryNeverReplaysPossibleSends(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	capabilityJobsBegin(mock)
	mock.ExpectExec(`UPDATE admin_capability_items SET status='indeterminate'[\s\S]+WHERE status='running' AND dispatched_at IS NOT NULL`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE admin_capability_items SET status='pending',claimed_at=NULL WHERE status='running' AND dispatched_at IS NULL`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE admin_capability_items i SET status='canceled'[\s\S]+r.status='canceling'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE admin_capability_runs SET status='pausing' WHERE status='running'`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT id FROM admin_capability_runs WHERE status IN \('pausing','canceling'\)`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec(`WITH counts AS[\s\S]+WHEN r.status='pausing' AND c.running=0 THEN 'paused'`).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.RecoverInterrupted(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityJobsRepositoryLatestFailureCannotHideBehindOlderSuccess(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	distinct := `SELECT DISTINCT ON \(ci.account_id,ci.upstream_model,ci.protocol,ci.profile\)[\s\S]+ORDER BY ci.account_id,ci.upstream_model,ci.protocol,ci.profile,ci.finished_at DESC NULLS LAST,ci.id DESC\) i[\s\S]+WHERE TRUE AND i.status=\$1`
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM \(` + distinct).WithArgs("succeeded").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT `+regexp.QuoteMeta(capabilityItemColumns)+` FROM \(`+distinct).WithArgs("succeeded", 50, 0).WillReturnRows(sqlmock.NewRows(capabilityJobsItemColumns()))
	page, err := repo.LatestItems(context.Background(), service.AccountCapabilityFilter{Status: "succeeded"})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityJobsRepositorySupersededProjectionSurvivesCatalogRecovery(t *testing.T) {
	// A's old alive observation must stay superseded after B's credential
	// failure, even when a newer catalog success replaces B in a latest view.
	// PostgreSQL evaluates the history predicate in the integration scenario;
	// this unit verifies that every public reader uses it and preserves its flag.
	require.Contains(t, capabilityPublicationSupersededSQL, "FROM admin_capability_items n")
	require.Contains(t, capabilityPublicationSupersededSQL, "JOIN admin_capability_runs nr ON nr.id=n.run_id")
	require.NotContains(t, capabilityPublicationSupersededSQL, "n.kind")
	require.Contains(t, capabilityPublicationSupersededSQL, "OR (n.status='failed' AND n.result->>'account_failure'='true')")
	require.Contains(t, capabilityPublicationSupersededSQL, "nr.kind='discover' AND n.result->>'source'='upstream'")
	for _, method := range []string{"latest", "run", "ids"} {
		t.Run(method, func(t *testing.T) {
			repo, mock := newCapabilityJobsRepoTest(t)
			now := time.Now().UTC()
			rows := sqlmock.NewRows(capabilityJobsItemColumns()).AddRow(9, 1, 1, 31, "fixture", 7,
				strings.Repeat("b", 64), "Model-A", "responses", "text", []byte(`[]`), "succeeded",
				[]byte(`{"status":"alive","request_count":1}`), 1, now, now, now, now, "probe", true)
			if method != "ids" {
				mock.ExpectQuery(`SELECT COUNT\(\*\) FROM`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			}
			// capabilityItemColumns embeds the exact shared expression, not a
			// latest-only approximation that would forget the auth failure.
			mock.ExpectQuery(`SELECT ` + regexp.QuoteMeta(capabilityItemColumns) + ` FROM`).WillReturnRows(rows)
			var items []service.AccountCapabilityItem
			var err error
			switch method {
			case "latest":
				var page *service.AccountCapabilityItemPage
				page, err = repo.LatestItems(context.Background(), service.AccountCapabilityFilter{AccountID: 31})
				if page != nil {
					items = page.Items
				}
			case "run":
				var page *service.AccountCapabilityItemPage
				page, err = repo.ListItems(context.Background(), 1, service.AccountCapabilityFilter{})
				if page != nil {
					items = page.Items
				}
			case "ids":
				items, err = repo.GetItemsByIDs(context.Background(), []int64{9})
			}
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, "succeeded", items[0].Status, "historical outcomes remain immutable")
			require.True(t, items[0].PublicationSuperseded)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
