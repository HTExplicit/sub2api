//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const publicationTestMappingSQL = `UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}',(((CASE WHEN jsonb_typeof(credentials->'model_mapping')='object' THEN credentials->'model_mapping' ELSE '{}'::jsonb END)-COALESCE($2::text[],'{}'::text[]))||$3::jsonb),true),updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`
const publicationTestScheduleSQL = `UPDATE accounts SET schedulable=$2,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`

func newPublicationRepoTest(t *testing.T) (*accountCapabilityPublicationRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return &accountCapabilityPublicationRepository{db: db}, mock
}

func publicationTestBegin(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL lock_timeout = '5s'`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL statement_timeout = '30s'`)).WillReturnResult(sqlmock.NewResult(0, 0))
}

func publicationTestChangeSetRows(t *testing.T, set *service.CapabilityChangeSet) *sqlmock.Rows {
	t.Helper()
	request, err := json.Marshal(set.Request)
	require.NoError(t, err)
	plan, err := json.Marshal(set.Plan)
	require.NoError(t, err)
	var applied any
	if set.AppliedAt != nil {
		applied = *set.AppliedAt
	}
	return sqlmock.NewRows([]string{"id", "status", "request", "plan", "before_fingerprint", "created_at", "applied_at"}).
		AddRow(set.ID, set.Status, request, plan, set.BeforeFingerprint, set.CreatedAt, applied)
}

func publicationTestExpectRead(mock sqlmock.Sqlmock, t *testing.T, set *service.CapabilityChangeSet, byID bool) {
	t.Helper()
	query := `SELECT id,status,request,plan,before_fingerprint,created_at,applied_at FROM admin_capability_changesets WHERE `
	if byID {
		mock.ExpectQuery(regexp.QuoteMeta(query + `id = $1 FOR UPDATE`)).WithArgs(set.ID).
			WillReturnRows(publicationTestChangeSetRows(t, set))
		return
	}
	mock.ExpectQuery(regexp.QuoteMeta(query + `idempotency_key = $1`)).WithArgs(set.Request.IdempotencyKey).
		WillReturnRows(publicationTestChangeSetRows(t, set))
}

func publicationTestAccountOutbox(mock sqlmock.Sqlmock) *sqlmock.ExpectedExec {
	return mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`)).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(31), nil, nil, sqlmock.AnyArg())
}

func TestCapabilityPublicationApplyAccountUsesOnlyScopedPatches(t *testing.T) {
	repo, mock := newPublicationRepoTest(t)
	mock.ExpectBegin()
	// Exact SQL expectations reject replacing the credentials object, touching
	// extra/priority, or deleting all of an account's private memberships.
	mock.ExpectExec(regexp.QuoteMeta(publicationTestMappingSQL)).
		WithArgs(int64(31), pq.Array([]string{"s2pub-retired"}), `{"s2pub-approved":"Vendor/Model-SSVIP"}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`)).
		WithArgs(int64(31), int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	// ON CONFLICT DO NOTHING preserves an already-bound group's priority.
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,50) ON CONFLICT(account_id,group_id) DO NOTHING`)).
		WithArgs(int64(31), int64(5)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(publicationTestScheduleSQL)).WithArgs(int64(31), true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	publicationTestAccountOutbox(mock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload) VALUES ($1, $2, $3, $4)`)).
		WithArgs(service.SchedulerOutboxEventAccountGroupsChanged, int64(31), nil, []byte(`{"group_ids":[5,3]}`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx, err := repo.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	enabled := true
	err = publicationApplyAccount(context.Background(), tx, service.CapabilityPublicationAccountPatch{
		AccountID: 31, ModelMapping: map[string]string{"s2pub-approved": "Vendor/Model-SSVIP"},
		RemoveSelectors: []string{"s2pub-retired"}, AddGroupIDs: []int64{5}, RemoveGroupIDs: []int64{3}, Schedulable: &enabled,
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func TestCapabilityPublicationApplyAccountRemoveOnlyKeepsJSONObject(t *testing.T) {
	repo, mock := newPublicationRepoTest(t)
	mock.ExpectBegin()
	// Concatenating JSON null converts a JSON object into an array in PostgreSQL.
	// A removal-only patch must merge {}, preserving all remaining private keys.
	mock.ExpectExec(regexp.QuoteMeta(publicationTestMappingSQL)).
		WithArgs(int64(31), pq.Array([]string{"s2pub-retired"}), `{}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	publicationTestAccountOutbox(mock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx, err := repo.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = publicationApplyAccount(context.Background(), tx, service.CapabilityPublicationAccountPatch{
		AccountID: 31, RemoveSelectors: []string{"s2pub-retired"},
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func TestCapabilityPublicationApplyAlreadyAppliedDoesNotWriteBusinessState(t *testing.T) {
	repo, mock := newPublicationRepoTest(t)
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	set := &service.CapabilityChangeSet{ID: 71, Status: "applied", CreatedAt: now, AppliedAt: &now}
	publicationTestBegin(mock)
	publicationTestExpectRead(mock, t, set, true)
	mock.ExpectRollback() // Read-only idempotent return releases the row lock.

	got, err := repo.Apply(context.Background(), set.ID, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
		t.Fatal("an applied changeset must not snapshot or rebuild the publication")
		return nil, nil
	})
	require.NoError(t, err)
	require.Equal(t, set.ID, got.ID)
	require.Equal(t, "applied", got.Status)
	require.Equal(t, set.AppliedAt, got.AppliedAt)
}

func TestCapabilityPublicationPreviewIdempotencyDoesNotReserveAnotherGroupID(t *testing.T) {
	for _, changedRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_request", true: "same_key_different_request"}[changedRequest], func(t *testing.T) {
			repo, mock := newPublicationRepoTest(t)
			request := service.CapabilityPublicationRequest{
				IdempotencyKey: "fixture-publication-key",
				Scope:          service.CapabilityPublicationScope{FolderIDs: []int64{7}, AccountIDs: []int64{31}},
				Groups:         []service.CapabilityPublicationGroup{{Name: "Qwen", Platform: service.PlatformOpenAI, RateMultiplier: 0.3}},
			}
			storedRequest := request
			storedRequest.Groups = append([]service.CapabilityPublicationGroup(nil), request.Groups...)
			storedRequest.Groups[0].ID = 417 // Reserved by the first preview.
			set := &service.CapabilityChangeSet{ID: 71, Status: "preview", Request: storedRequest, CreatedAt: time.Unix(1, 0)}
			if changedRequest {
				request.Groups[0].RateMultiplier = 0.4
			}
			publicationTestBegin(mock)
			mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtextextended($1, 481627))`)).
				WithArgs(request.IdempotencyKey).WillReturnResult(sqlmock.NewResult(0, 0))
			publicationTestExpectRead(mock, t, set, false)
			// No nextval, group insert, changeset insert, or business mutation is allowed.
			mock.ExpectRollback()

			got, err := repo.Preview(context.Background(), request, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
				t.Fatal("an existing idempotency key must not rebuild or reserve IDs")
				return nil, nil
			})
			if changedRequest {
				require.ErrorIs(t, err, service.ErrCapabilityPublicationConflict)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(417), got.Request.Groups[0].ID)
				require.Equal(t, int64(0), request.Groups[0].ID)
			}
		})
	}
}

// A scheduling-only publication is sufficient to exercise the complete snapshot
// CAS and transaction boundary without mocking unrelated channel price writes.
func publicationTestSnapshot(mock sqlmock.Sqlmock, schedulable bool, privatePriority int) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT to_jsonb(g)-'updated_at' FROM groups g WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`)).
		WithArgs("{}").WillReturnRows(sqlmock.NewRows([]string{"group"}))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id,group_id,priority FROM account_groups WHERE group_id=ANY($1) ORDER BY group_id,account_id FOR UPDATE`)).
		WithArgs("{}").WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority"}))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,name,platform,wire_platform,provider_profile,type,credentials,extra,proxy_id,management_folder_id,parent_account_id,schedulable,status FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`)).
		WithArgs("{31}").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "wire_platform", "provider_profile", "type", "credentials", "extra", "proxy_id", "management_folder_id", "parent_account_id", "schedulable", "status"}).
		AddRow(31, "fixture-account", "openai", "openai", "", "apikey", []byte(`{"api_key":"fixture-only","base_url":"https://example.invalid","model_mapping":{"private-alias":"Private/Model"}}`), []byte(`{"openai_responses_mode":"force_responses"}`), nil, 7, nil, schedulable, "active"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id,group_id,priority FROM account_groups WHERE account_id=ANY($1) ORDER BY account_id,group_id FOR UPDATE`)).
		WithArgs("{31}").WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority"}).AddRow(31, 93, privatePriority))
	// The locked publication view must use exactly the same complete-ledger
	// evidence predicate as the inventory, including the run-kind join.
	mock.ExpectQuery(`SELECT i\.id,i\.account_id,i\.folder_id,[\s\S]+` + regexp.QuoteMeta(capabilityPublicationSupersededSQL) + `[\s\S]+FOR SHARE OF i,r`).
		WithArgs("{9}").WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "folder_id", "config_fingerprint", "upstream_model", "protocol", "profile", "status", "result", "finished_at", "kind", "folder_ids", "account_ids", "superseded"}).
		AddRow(9, 31, 7, "fixture-fingerprint", "Vendor/Model", "responses", "text", "succeeded", []byte(`{"status":"alive","account_failure":false}`), time.Unix(100, 0).UTC(), "probe", []byte(`[7]`), []byte(`[31]`), false))
}

func publicationTestCreateSchedulingPreview(t *testing.T, repo *accountCapabilityPublicationRepository, mock sqlmock.Sqlmock) *service.CapabilityChangeSet {
	t.Helper()
	request := service.CapabilityPublicationRequest{
		IdempotencyKey:        "fixture-scheduling-key",
		Scope:                 service.CapabilityPublicationScope{FolderIDs: []int64{7}, AccountIDs: []int64{31}},
		SchedulingEvidenceIDs: []int64{9},
	}
	enabled := true
	plan := &service.CapabilityPublicationPlan{Accounts: []service.CapabilityPublicationAccountPatch{{AccountID: 31, Schedulable: &enabled}}}
	publicationTestBegin(mock)
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtextextended($1, 481627))`)).
		WithArgs(request.IdempotencyKey).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,status,request,plan,before_fingerprint,created_at,applied_at FROM admin_capability_changesets WHERE idempotency_key = $1`)).
		WithArgs(request.IdempotencyKey).WillReturnError(sql.ErrNoRows)
	publicationTestSnapshot(mock, false, 7)
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO admin_capability_changesets(idempotency_key,request,plan,before_fingerprint,status) VALUES($1,$2::jsonb,$3::jsonb,$4,'preview') RETURNING id,created_at`)).
		WithArgs(request.IdempotencyKey, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(71, time.Unix(101, 0).UTC()))
	mock.ExpectCommit()
	set, err := repo.Preview(context.Background(), request, func(snap *service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
		require.False(t, snap.Accounts[31].Account.Schedulable)
		require.Equal(t, 7, snap.Accounts[31].Bindings[93])
		return plan, nil
	})
	require.NoError(t, err)
	require.Len(t, set.BeforeFingerprint, 64)
	return set
}

func TestCapabilityPublicationApplyRejectsSnapshotOrPlanDriftBeforeMutation(t *testing.T) {
	for _, changed := range []string{"scheduling", "private_binding_priority", "rebuilt_plan"} {
		t.Run(changed, func(t *testing.T) {
			repo, mock := newPublicationRepoTest(t)
			set := publicationTestCreateSchedulingPreview(t, repo, mock)
			publicationTestBegin(mock)
			publicationTestExpectRead(mock, t, set, true)
			priority := 7
			if changed == "private_binding_priority" {
				priority = 19
			}
			publicationTestSnapshot(mock, changed == "scheduling", priority)
			mock.ExpectRollback()
			buildCalled := false
			got, err := repo.Apply(context.Background(), set.ID, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
				buildCalled = true
				// Only this case reaches the builder; any changed approved action
				// must fail even when the locked database fingerprint is identical.
				return &service.CapabilityPublicationPlan{}, nil
			})
			require.ErrorIs(t, err, service.ErrCapabilityPublicationConflict)
			require.Nil(t, got)
			require.Equal(t, changed == "rebuilt_plan", buildCalled)
		})
	}
}

func TestCapabilityPublicationApplyCommitsAuditAndOutboxTogether(t *testing.T) {
	for _, outcome := range []string{"commit", "outbox_failure", "audit_failure"} {
		t.Run(outcome, func(t *testing.T) {
			repo, mock := newPublicationRepoTest(t)
			set := publicationTestCreateSchedulingPreview(t, repo, mock)
			publicationTestBegin(mock)
			publicationTestExpectRead(mock, t, set, true)
			publicationTestSnapshot(mock, false, 7)
			mock.ExpectExec(regexp.QuoteMeta(publicationTestScheduleSQL)).WithArgs(int64(31), true).
				WillReturnResult(sqlmock.NewResult(0, 1))
			outboxFailure := errors.New("fixture outbox unavailable")
			outbox := publicationTestAccountOutbox(mock)
			if outcome == "outbox_failure" {
				outbox.WillReturnError(outboxFailure)
				mock.ExpectRollback()
			} else {
				outbox.WillReturnResult(sqlmock.NewResult(0, 1))
				audit := mock.ExpectQuery(regexp.QuoteMeta(`UPDATE admin_capability_changesets SET status='applied',applied_at=NOW() WHERE id=$1 AND status='preview' RETURNING applied_at`)).WithArgs(set.ID)
				if outcome == "audit_failure" {
					audit.WillReturnError(&pq.Error{Code: "40001", Message: "fixture serialization conflict"})
					mock.ExpectRollback()
				} else {
					audit.WillReturnRows(sqlmock.NewRows([]string{"applied_at"}).AddRow(time.Unix(102, 0).UTC()))
					mock.ExpectCommit()
				}
			}

			got, err := repo.Apply(context.Background(), set.ID, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
				return &set.Plan, nil
			})
			switch outcome {
			case "outbox_failure":
				require.ErrorIs(t, err, outboxFailure)
				require.Nil(t, got)
			case "audit_failure":
				require.ErrorIs(t, err, service.ErrCapabilityPublicationConflict)
				require.Nil(t, got)
			default:
				require.NoError(t, err)
				require.Equal(t, "applied", got.Status)
				require.NotNil(t, got.AppliedAt)
			}
		})
	}
}
