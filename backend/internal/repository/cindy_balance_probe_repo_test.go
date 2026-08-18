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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestCindyBalanceProbeRepositoryListJobsReturnsNewestWithCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM cindy_balance_probe_jobs`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(7)))
	mock.ExpectQuery(`SELECT id, status, requested_by, scope,[\s\S]+FROM cindy_balance_probe_jobs ORDER BY created_at DESC, id DESC LIMIT \$1`).
		WithArgs(2).
		WillReturnRows(cindyBalanceProbeJobRows().
			AddRow(int64(9), "running", int64(3), []byte(`{"mode":"all"}`), 0.5, 4, strings.Repeat("a", 64), 2, 0, now, "lease", now.Add(time.Minute), now, nil, now, nil, nil, now, now).
			AddRow(int64(8), "completed", nil, []byte(`{"mode":"selected"}`), 0.3, 1, strings.Repeat("b", 64), 1, 0, now, nil, nil, now, nil, now, now, nil, now.Add(-time.Hour), now))
	mock.ExpectQuery(`SELECT[\s\S]+FROM cindy_balance_probe_items WHERE job_id = \$1`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"pending", "running", "healthy", "recovered", "exhausted", "inconclusive", "skipped"}).
			AddRow(2, 1, 1, 0, 0, 0, 0))
	mock.ExpectQuery(`SELECT[\s\S]+FROM cindy_balance_probe_items WHERE job_id = \$1`).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"pending", "running", "healthy", "recovered", "exhausted", "inconclusive", "skipped"}).
			AddRow(0, 0, 1, 0, 0, 0, 0))

	repo := &cindyBalanceProbeRepository{db: db}
	result, err := repo.ListJobs(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, int64(7), result.Total)
	require.Len(t, result.Items, 2)
	require.Equal(t, int64(9), result.Items[0].ID)
	require.Equal(t, 2, result.Items[0].Counts.Pending)
	require.Equal(t, 1, result.Items[1].Counts.Healthy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyBalanceProbeRepositoryListJobsBoundsLimit(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		expected  int
	}{
		{name: "default", requested: 0, expected: 10},
		{name: "maximum", requested: 500, expected: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM cindy_balance_probe_jobs`)).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
			mock.ExpectQuery(`SELECT id, status, requested_by, scope,[\s\S]+FROM cindy_balance_probe_jobs ORDER BY created_at DESC, id DESC LIMIT \$1`).
				WithArgs(tt.expected).
				WillReturnRows(cindyBalanceProbeJobRows())

			result, err := (&cindyBalanceProbeRepository{db: db}).ListJobs(context.Background(), tt.requested)
			require.NoError(t, err)
			require.Empty(t, result.Items)
			require.Zero(t, result.Total)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCindyBalanceProbeRepositoryLatestByAccountIDs(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.ValueConverterOption(pgxSQLMockValueConverter{}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT DISTINCT ON \(account_id\)[\s\S]+state AS outcome[\s\S]+WHERE account_id = ANY\(\$1\)[\s\S]+ORDER BY account_id, COALESCE\(finished_at, updated_at\) DESC`).
		WithArgs([]int64{11, 22}).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "job_id", "outcome", "checked_at"}).
			AddRow(int64(11), int64(101), "healthy", now).
			AddRow(int64(22), int64(102), "luna_running", now.Add(time.Minute)))

	latest, err := (&cindyBalanceProbeRepository{db: db}).LatestByAccountIDs(
		context.Background(), []int64{11, 0, 22, 11, -1},
	)
	require.NoError(t, err)
	require.Equal(t, map[int64]service.CindyBalanceProbeLatest{
		11: {AccountID: 11, JobID: 101, Outcome: "healthy", CheckedAt: now},
		22: {AccountID: 22, JobID: 102, Outcome: "luna_running", CheckedAt: now.Add(time.Minute)},
	}, latest)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyBalanceProbeRepositoryLatestByAccountIDsEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	latest, err := (&cindyBalanceProbeRepository{db: db}).LatestByAccountIDs(context.Background(), []int64{0, -1})
	require.NoError(t, err)
	require.Empty(t, latest)
	require.NotNil(t, latest)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyBalanceProbeRepositoryCreateJobRejectsTransactionalCandidateDrift(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	credentials := map[string]any{
		"api_key":  "sk-candidate-one",
		"base_url": "https://api.laxarouter.ai",
	}
	scope := service.CindyBalanceProbeScope{
		Mode:    "all",
		Filters: service.AccountConsoleFilters{CindyOnly: true},
	}
	previewedAccount := service.Account{
		ID: 17, Name: "candidate-one", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: credentials, Extra: map[string]any{}, UpdatedAt: now,
	}
	expected, err := service.BuildCindyBalanceProbePreviewFromSnapshot(
		scope,
		[]service.Account{previewedAccount},
		0.5,
		now,
	)
	require.NoError(t, err)

	tests := []struct {
		name string
		rows *sqlmock.Rows
	}{
		{
			name: "existing candidate generation changed",
			rows: cindyBalanceProbeAccountSnapshotRows().AddRow(
				int64(17), "candidate-one", service.PlatformOpenAI, service.AccountTypeAPIKey,
				mustMarshalCindyBalanceProbeTestJSON(t, credentials), []byte(`{}`), nil, nil,
				service.StatusActive, true, now.Add(time.Second), nil, nil, nil, "{}", "{}",
			),
		},
		{
			name: "new matching candidate appeared",
			rows: cindyBalanceProbeAccountSnapshotRows().
				AddRow(
					int64(17), "candidate-one", service.PlatformOpenAI, service.AccountTypeAPIKey,
					mustMarshalCindyBalanceProbeTestJSON(t, credentials), []byte(`{}`), nil, nil,
					service.StatusActive, true, now, nil, nil, nil, "{}", "{}",
				).
				AddRow(
					int64(18), "candidate-two", service.PlatformOpenAI, service.AccountTypeAPIKey,
					[]byte(`{"api_key":"sk-candidate-two","base_url":"https://api.laxarouter.ai"}`), []byte(`{}`), nil, nil,
					service.StatusActive, true, now, nil, nil, nil, "{}", "{}",
				),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT a\.id, a\.name, a\.platform, a\.type, a\.credentials, a\.extra,[\s\S]+FROM accounts a[\s\S]+ORDER BY a\.id`).
				WillReturnRows(tt.rows)
			mock.ExpectRollback()

			job, err := (&cindyBalanceProbeRepository{db: db}).CreateJob(
				context.Background(),
				nil,
				scope,
				0.5,
				expected.CandidateCount,
				expected.CandidateFingerprint,
			)

			require.Nil(t, job)
			require.ErrorIs(t, err, service.ErrCindyBalanceProbeChanged)
			require.NoError(t, mock.ExpectationsWereMet(), "drift must roll back before inserting jobs or items")
		})
	}
}

func TestCindyBalanceProbeRepositoryCreateJobMapsSnapshotSerializationFailureToCandidateDrift(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT a\.id, a\.name, a\.platform, a\.type, a\.credentials, a\.extra,[\s\S]+FROM accounts a[\s\S]+ORDER BY a\.id`).
		WillReturnError(&pgconn.PgError{Code: "40001"})
	mock.ExpectRollback()

	job, err := (&cindyBalanceProbeRepository{db: db}).CreateJob(
		context.Background(),
		nil,
		service.CindyBalanceProbeScope{Mode: "all", Filters: service.AccountConsoleFilters{CindyOnly: true}},
		0.5,
		1,
		strings.Repeat("a", 64),
	)

	require.Nil(t, job)
	require.ErrorIs(t, err, service.ErrCindyBalanceProbeChanged)
	require.NoError(t, mock.ExpectationsWereMet(), "serialization failure must roll back without creating a job")
}

func TestCindyBalanceProbeRepositoryReserveNextCapturesDispatchEpoch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	accountUpdatedAt := now.Add(-time.Hour)
	fingerprint := strings.Repeat("a", 64)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status, rate_rps::float8, last_request_started_at[\s\S]+FOR UPDATE`).
		WithArgs(int64(7), "lease-epoch-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "rate_rps", "last_request_started_at"}).
			AddRow("running", 1.0, nil))
	mock.ExpectExec(`UPDATE cindy_balance_probe_items[\s\S]+state = 'luna_exact' AND luna_at < \$2`).
		WithArgs(int64(7), now.Add(-5*time.Minute)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, account_id, identity_fingerprint, account_updated_at, was_marked,[\s\S]+request_count[\s\S]+FOR UPDATE SKIP LOCKED LIMIT 1`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "account_id", "identity_fingerprint", "account_updated_at", "was_marked",
			"state", "luna_at", "request_count",
		}).AddRow(int64(11), int64(13), fingerprint, accountUpdatedAt, false, "pending", nil, 0))
	mock.ExpectQuery(`UPDATE cindy_balance_probe_items[\s\S]+RETURNING request_count`).
		WithArgs(int64(11), "luna_running", now, int64(7), "pending", 0).
		WillReturnRows(sqlmock.NewRows([]string{"request_count"}).AddRow(1))
	mock.ExpectQuery(`UPDATE cindy_balance_probe_jobs[\s\S]+RETURNING request_count`).
		WithArgs(int64(7), "lease-epoch-1", now).
		WillReturnRows(sqlmock.NewRows([]string{"request_count"}).AddRow(4))
	mock.ExpectCommit()

	reservation, delay, err := (&cindyBalanceProbeRepository{db: db}).ReserveNext(
		context.Background(), 7, "lease-epoch-1", now, now.Add(-5*time.Minute),
	)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.NotNil(t, reservation)
	require.Equal(t, "lease-epoch-1", reservation.LeaseToken)
	require.Equal(t, 4, reservation.JobRequestCount)
	require.Equal(t, 1, reservation.RequestCount)
	require.Equal(t, "luna", reservation.Stage)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyBalanceProbeRepositoryValidateReservationUsesClaimAndItemEpoch(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	lunaAt := now.Add(-time.Minute)
	credentials := map[string]any{
		"api_key":  "sk-current-generation",
		"base_url": "https://api.laxarouter.ai",
	}
	fingerprint, err := service.CindyAccountIdentityFingerprint(
		service.PlatformOpenAI,
		service.AccountTypeAPIKey,
		credentials,
	)
	require.NoError(t, err)
	credentialsJSON, err := json.Marshal(credentials)
	require.NoError(t, err)
	reservation := &service.CindyBalanceProbeReservation{
		JobID: 7, ItemID: 11, AccountID: 13, Stage: "terra",
		// These observation echoes are deliberately stale. Authorization is bound
		// to the lease token plus the per-item state and request count below.
		LeaseToken: "lease-epoch-1", JobRequestCount: -999, RequestCount: 2,
		IdentityFingerprint: fingerprint, AccountUpdatedAt: now.Add(-time.Hour),
		LunaAt: func() *time.Time {
			drifted := lunaAt.Add(time.Second)
			return &drifted
		}(),
	}
	account := &service.Account{
		ID: reservation.AccountID, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, UpdatedAt: reservation.AccountUpdatedAt,
		Credentials: credentials,
	}
	tests := []struct {
		name          string
		authorized    bool
		jobEchoMatch  bool
		lunaEchoMatch bool
		expected      bool
	}{
		{
			name:       "current authority may send despite stale observation echoes",
			authorized: true, jobEchoMatch: false, lunaEchoMatch: false, expected: true,
		},
		{name: "changed account or reservation may not send", authorized: false, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			expectation := mock.ExpectQuery(`UPDATE cindy_balance_probe_items AS i[\s\S]+FROM cindy_balance_probe_jobs AS j[\s\S]+JOIN accounts AS a ON a\.id = \$6[\s\S]+i\.account_id = a\.id[\s\S]+j\.lease_token = \$3 AND j\.lease_until >= NOW\(\)[\s\S]+j\.status = 'running' AND j\.cancel_requested_at IS NULL[\s\S]+i\.state = \$5[\s\S]+i\.identity_fingerprint = \$7[\s\S]+i\.request_count = \$10[\s\S]+a\.platform = \$12 AND a\.type = \$13[\s\S]+a\.status = \$14 AND a\.schedulable = TRUE AND a\.deleted_at IS NULL[\s\S]+a\.updated_at = \$8[\s\S]+a\.cindy_balance_insufficient_at IS NOT NULL[\s\S]+a\.credentials = \$15::jsonb[\s\S]+a\.credentials->>'base_url'[\s\S]+RETURNING j\.request_count = \$4[\s\S]+i\.luna_at IS NOT DISTINCT FROM \$11::timestamptz`).
				WithArgs(reservation.ItemID, reservation.JobID, reservation.LeaseToken,
					reservation.JobRequestCount, "terra_running", reservation.AccountID,
					reservation.IdentityFingerprint, reservation.AccountUpdatedAt,
					reservation.WasMarked, reservation.RequestCount, reservation.LunaAt.UTC(),
					service.PlatformOpenAI, service.AccountTypeAPIKey, service.StatusActive, string(credentialsJSON))
			if tt.authorized {
				expectation.WillReturnRows(sqlmock.NewRows([]string{"job_request_count_match", "luna_at_match"}).
					AddRow(tt.jobEchoMatch, tt.lunaEchoMatch))
			} else {
				expectation.WillReturnRows(sqlmock.NewRows([]string{"job_request_count_match", "luna_at_match"}))
			}

			ready, err := (&cindyBalanceProbeRepository{db: db}).ValidateReservationForSend(
				context.Background(), reservation, account, reservation.LeaseToken,
			)
			require.NoError(t, err)
			require.Equal(t, tt.expected, ready)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCindyBalanceProbeRepositoryValidateReservationRejectsOldClaimWithoutWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ready, err := (&cindyBalanceProbeRepository{db: db}).ValidateReservationForSend(
		context.Background(),
		&service.CindyBalanceProbeReservation{LeaseToken: "old-epoch", Stage: "luna"},
		nil,
		"new-epoch",
	)
	require.NoError(t, err)
	require.False(t, ready)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyBalanceProbeRepositoryCompleteStageDistinguishesPauseFromLostAuthority(t *testing.T) {
	updatedAt := time.Date(2026, 8, 17, 5, 0, 0, 0, time.UTC)
	credentials := map[string]any{
		"api_key":  "sk-cindy-complete-unit",
		"base_url": "https://api.laxarouter.ai",
	}
	fingerprint, err := service.CindyAccountIdentityFingerprint(
		service.PlatformOpenAI,
		service.AccountTypeAPIKey,
		credentials,
	)
	require.NoError(t, err)
	credentialsJSON, err := json.Marshal(credentials)
	require.NoError(t, err)
	reservation := &service.CindyBalanceProbeReservation{
		JobID: 7, ItemID: 11, AccountID: 13, Stage: "terra",
		LeaseToken: "lease-epoch-1", RequestCount: 1,
		IdentityFingerprint: fingerprint, AccountUpdatedAt: updatedAt,
	}
	account := &service.Account{
		ID: 13, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: credentials, UpdatedAt: updatedAt,
	}
	tests := []struct {
		name            string
		itemRows        int64
		failureCount    int
		wantKeepRunning bool
		wantApplied     bool
	}{
		{name: "lost authority is not applied"},
		{
			name:         "third upstream failure is applied before pausing",
			itemRows:     1,
			failureCount: 3,
			wantApplied:  true,
		},
		{
			name:            "ordinary completion remains running",
			itemRows:        1,
			failureCount:    1,
			wantKeepRunning: true,
			wantApplied:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			itemLock := mock.ExpectQuery(`SELECT i\.account_id, i\.state, i\.identity_fingerprint,[\s\S]+FOR UPDATE OF j, i`).
				WithArgs(reservation.JobID, reservation.ItemID, reservation.LeaseToken)
			if tt.itemRows == 0 {
				itemLock.WillReturnRows(sqlmock.NewRows([]string{
					"account_id", "state", "identity_fingerprint", "account_updated_at", "was_marked", "request_count",
				}))
				mock.ExpectRollback()
			} else {
				itemLock.WillReturnRows(sqlmock.NewRows([]string{
					"account_id", "state", "identity_fingerprint", "account_updated_at", "was_marked", "request_count",
				}).AddRow(account.ID, "terra_running", fingerprint, updatedAt, false, 1))
				mock.ExpectQuery(`SELECT platform, type, status, schedulable, credentials, updated_at,[\s\S]+FROM accounts WHERE id = \$1 FOR UPDATE`).
					WithArgs(account.ID).
					WillReturnRows(sqlmock.NewRows([]string{
						"platform", "type", "status", "schedulable", "credentials", "updated_at",
						"cindy_balance_insufficient_at", "deleted_at",
					}).AddRow(account.Platform, account.Type, account.Status, true, credentialsJSON, updatedAt, nil, nil))
				mock.ExpectExec(`UPDATE cindy_balance_probe_items[\s\S]+WHERE id = \$1 AND job_id = \$2 AND state = \$8`).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`UPDATE cindy_balance_probe_jobs[\s\S]+RETURNING consecutive_upstream_failures`).
					WillReturnRows(sqlmock.NewRows([]string{"consecutive_upstream_failures"}).AddRow(tt.failureCount))
				mock.ExpectCommit()
			}

			keepRunning, applied, err := (&cindyBalanceProbeRepository{db: db}).CompleteStage(
				context.Background(), reservation, account, "lease-epoch-1", "server_error", "inconclusive", true,
			)
			require.NoError(t, err)
			require.Equal(t, tt.wantKeepRunning, keepRunning)
			require.Equal(t, tt.wantApplied, applied)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCindyBalanceProbeRepositoryFinalizeExhaustedRejectsExpiredConfirmation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	reservation := &service.CindyBalanceProbeReservation{JobID: 7, ItemID: 11, AccountID: 13}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT j\.status, j\.cancel_requested_at, i\.state, i\.luna_at, clock_timestamp\(\)[\s\S]+FOR UPDATE OF j, i`).
		WithArgs(reservation.JobID, reservation.ItemID, "lease-epoch-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "cancel_requested_at", "state", "luna_at", "now"}).
			AddRow("running", nil, "terra_running", now.Add(-5*time.Minute-time.Nanosecond), now))
	mock.ExpectExec(`UPDATE cindy_balance_probe_items[\s\S]+final_outcome = \$2[\s\S]+WHERE id = \$1 AND state = 'terra_running'`).
		WithArgs(reservation.ItemID, "inconclusive_confirmation_expired").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	state, err := (&cindyBalanceProbeRepository{db: db}).FinalizeExhausted(
		context.Background(), reservation, "lease-epoch-1", now, 5*time.Minute,
	)
	require.NoError(t, err)
	require.Equal(t, "inconclusive_confirmation_expired", state)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyBalanceProbeRepositoryFinalizeExhaustedEnqueuesTypedAccountChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC)
	updatedAt := now.Add(-time.Hour)
	lunaAt := now.Add(-time.Minute)
	credentials := map[string]any{
		"api_key":  "sk-cindy-finalize-fixture",
		"base_url": "https://api.laxarouter.ai",
	}
	fingerprint, err := service.CindyAccountIdentityFingerprint(
		service.PlatformOpenAI,
		service.AccountTypeAPIKey,
		credentials,
	)
	require.NoError(t, err)
	reservation := &service.CindyBalanceProbeReservation{
		JobID:               7,
		ItemID:              11,
		AccountID:           13,
		Stage:               "terra",
		IdentityFingerprint: fingerprint,
		AccountUpdatedAt:    updatedAt,
	}
	payload := map[string]any{"source": "cindy_balance_probe", "job_id": reservation.JobID}
	payloadJSON, err := json.Marshal(payload)
	require.NoError(t, err)
	dedupKey := schedulerOutboxDedupKey(
		service.SchedulerOutboxEventAccountChanged,
		&reservation.AccountID,
		nil,
		payloadJSON,
	)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT j\.status, j\.cancel_requested_at, i\.state, i\.luna_at, clock_timestamp\(\)[\s\S]+FOR UPDATE OF j, i`).
		WithArgs(reservation.JobID, reservation.ItemID, "lease-epoch-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "cancel_requested_at", "state", "luna_at", "now"}).
			AddRow("running", nil, "terra_running", lunaAt, now))
	mock.ExpectExec(`UPDATE cindy_balance_probe_jobs[\s\S]+consecutive_upstream_failures = 0[\s\S]+WHERE id = \$1 AND lease_token = \$2`).
		WithArgs(reservation.JobID, "lease-epoch-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT platform, type, status, schedulable, credentials, updated_at,[\s\S]+FROM accounts WHERE id = \$1 FOR UPDATE`).
		WithArgs(reservation.AccountID).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform", "type", "status", "schedulable", "credentials", "updated_at",
			"cindy_balance_insufficient_at", "deleted_at",
		}).AddRow(
			service.PlatformOpenAI,
			service.AccountTypeAPIKey,
			service.StatusActive,
			true,
			mustMarshalCindyBalanceProbeTestJSON(t, credentials),
			updatedAt,
			nil,
			nil,
		))
	mock.ExpectExec(`UPDATE accounts SET cindy_balance_insufficient_at = \$2, updated_at = NOW\(\) WHERE id = \$1`).
		WithArgs(reservation.AccountID, now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO scheduler_outbox \(event_type, account_id, group_id, payload, dedup_key\)[\s\S]+ON CONFLICT \(dedup_key\) WHERE dedup_key IS NOT NULL DO NOTHING`).
		WithArgs(
			service.SchedulerOutboxEventAccountChanged,
			reservation.AccountID,
			nil,
			payloadJSON,
			dedupKey,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE cindy_balance_probe_items[\s\S]+terra_outcome = CASE WHEN terra_outcome IS NULL THEN \$3 ELSE terra_outcome END[\s\S]+WHERE id = \$1`).
		WithArgs(reservation.ItemID, "exhausted", "exact").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	state, err := (&cindyBalanceProbeRepository{db: db}).FinalizeExhausted(
		context.Background(), reservation, "lease-epoch-1", now, 5*time.Minute,
	)

	require.NoError(t, err)
	require.Equal(t, "exhausted", state)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyBalanceProbeConfirmationCurrent(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		lunaAt sql.NullTime
		want   bool
	}{
		{name: "exact boundary is current", lunaAt: sql.NullTime{Time: now.Add(-5 * time.Minute), Valid: true}, want: true},
		{name: "past boundary is expired", lunaAt: sql.NullTime{Time: now.Add(-5*time.Minute - time.Nanosecond), Valid: true}},
		{name: "future timestamp is invalid", lunaAt: sql.NullTime{Time: now.Add(time.Nanosecond), Valid: true}},
		{name: "missing Luna timestamp is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, cindyBalanceProbeConfirmationCurrent(tt.lunaAt, now, 5*time.Minute))
		})
	}
}

func cindyBalanceProbeJobRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "status", "requested_by", "scope", "rate_rps", "candidate_count",
		"candidate_fingerprint", "request_count", "consecutive_upstream_failures",
		"last_request_started_at", "lease_token", "lease_until", "heartbeat_at",
		"cancel_requested_at", "started_at", "finished_at", "failure_reason", "created_at", "updated_at",
	})
}

func cindyBalanceProbeAccountSnapshotRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "name", "platform", "type", "credentials", "extra",
		"proxy_id", "management_folder_id", "status", "schedulable", "updated_at",
		"cindy_balance_insufficient_at", "rate_limit_reset_at", "temp_unschedulable_until",
		"group_ids", "tag_ids",
	})
}

func mustMarshalCindyBalanceProbeTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
