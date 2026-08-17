package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type cindyBalanceProbeRepository struct {
	db *sql.DB
}

func NewCindyBalanceProbeRepository(db *sql.DB) service.CindyBalanceProbeRepository {
	return &cindyBalanceProbeRepository{db: db}
}

func (r *cindyBalanceProbeRepository) Preview(
	ctx context.Context,
	scope service.CindyBalanceProbeScope,
	rateRPS float64,
) (*service.CindyBalanceProbePreview, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	preview, err := loadCindyBalanceProbePreview(ctx, tx, scope, rateRPS)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return preview, nil
}

func (r *cindyBalanceProbeRepository) CreateJob(
	ctx context.Context,
	requestedBy *int64,
	scope service.CindyBalanceProbeScope,
	rateRPS float64,
	expectedCount int,
	expectedFingerprint string,
) (*service.CindyBalanceProbeJob, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	preview, err := loadCindyBalanceProbePreview(ctx, tx, scope, rateRPS)
	if err != nil {
		return nil, wrapCindyBalanceProbeCreateTransactionError(err)
	}
	if preview.CandidateCount != expectedCount ||
		preview.CandidateFingerprint != strings.TrimSpace(expectedFingerprint) {
		return nil, service.ErrCindyBalanceProbeChanged
	}
	if preview.CandidateCount == 0 {
		return nil, service.ErrCindyBalanceProbeNoCandidates
	}
	var jobID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_jobs
			(status, requested_by, scope, rate_rps, candidate_count, candidate_fingerprint)
		VALUES ('queued', $1, $2::jsonb, $3, $4, $5)
		RETURNING id
	`, nullableCindyBalanceProbeRequester(requestedBy), service.EncodeCindyBalanceProbeScope(preview.Scope), preview.RateRPS, preview.CandidateCount, preview.CandidateFingerprint).Scan(&jobID)
	if err != nil {
		return nil, wrapCindyBalanceProbeCreateTransactionError(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO cindy_balance_probe_items
			(job_id, account_id, ordinal, identity_fingerprint, account_updated_at, was_marked)
		VALUES ($1, $2, $3, $4, $5, $6)
	`)
	if err != nil {
		return nil, wrapCindyBalanceProbeCreateTransactionError(err)
	}
	defer func() { _ = statement.Close() }()
	for i, candidate := range preview.Candidates {
		if _, err = statement.ExecContext(ctx, jobID, candidate.AccountID, i+1, candidate.IdentityFingerprint, candidate.AccountUpdatedAt, candidate.WasMarked); err != nil {
			return nil, wrapCindyBalanceProbeCreateTransactionError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, wrapCindyBalanceProbeCreateTransactionError(err)
	}
	return r.GetJob(ctx, jobID)
}

func loadCindyBalanceProbePreview(
	ctx context.Context,
	tx *sql.Tx,
	scope service.CindyBalanceProbeScope,
	rateRPS float64,
) (*service.CindyBalanceProbePreview, error) {
	accounts, err := loadCindyBalanceProbeAccountSnapshot(ctx, tx)
	if err != nil {
		return nil, err
	}
	return service.BuildCindyBalanceProbePreviewFromSnapshot(scope, accounts, rateRPS, time.Now().UTC())
}

func loadCindyBalanceProbeAccountSnapshot(ctx context.Context, tx *sql.Tx) ([]service.Account, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT a.id, a.name, a.platform, a.type, a.credentials, a.extra,
		       a.proxy_id, a.management_folder_id, a.status, a.schedulable,
		       a.updated_at, a.cindy_balance_insufficient_at,
		       a.rate_limit_reset_at, a.temp_unschedulable_until,
		       ARRAY(SELECT ag.group_id FROM account_groups ag WHERE ag.account_id = a.id ORDER BY ag.group_id),
		       ARRAY(SELECT atb.tag_id FROM account_tag_bindings atb WHERE atb.account_id = a.id ORDER BY atb.tag_id)
		FROM accounts a
		WHERE a.deleted_at IS NULL
		ORDER BY a.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query Cindy balance probe account snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()

	accounts := make([]service.Account, 0)
	for rows.Next() {
		var account service.Account
		var credentialsJSON, extraJSON []byte
		var proxyID, folderID sql.NullInt64
		var markedAt, rateLimitResetAt, tempUnschedulableUntil sql.NullTime
		var groupIDs, tagIDs pq.Int64Array
		if err = rows.Scan(
			&account.ID,
			&account.Name,
			&account.Platform,
			&account.Type,
			&credentialsJSON,
			&extraJSON,
			&proxyID,
			&folderID,
			&account.Status,
			&account.Schedulable,
			&account.UpdatedAt,
			&markedAt,
			&rateLimitResetAt,
			&tempUnschedulableUntil,
			&groupIDs,
			&tagIDs,
		); err != nil {
			return nil, fmt.Errorf("scan Cindy balance probe account snapshot: %w", err)
		}
		account.Credentials, err = decodeCindyBalanceProbeJSONMap(credentialsJSON)
		if err != nil {
			return nil, fmt.Errorf("decode Cindy balance probe account %d credentials: %w", account.ID, err)
		}
		account.Extra, err = decodeCindyBalanceProbeJSONMap(extraJSON)
		if err != nil {
			return nil, fmt.Errorf("decode Cindy balance probe account %d extra: %w", account.ID, err)
		}
		if proxyID.Valid {
			value := proxyID.Int64
			account.ProxyID = &value
		}
		if folderID.Valid {
			value := folderID.Int64
			account.ManagementFolderID = &value
		}
		if markedAt.Valid {
			value := markedAt.Time
			account.CindyBalanceInsufficientAt = &value
		}
		if rateLimitResetAt.Valid {
			value := rateLimitResetAt.Time
			account.RateLimitResetAt = &value
		}
		if tempUnschedulableUntil.Valid {
			value := tempUnschedulableUntil.Time
			account.TempUnschedulableUntil = &value
		}
		account.GroupIDs = append([]int64(nil), groupIDs...)
		account.Tags = make([]service.AccountManagementTag, 0, len(tagIDs))
		for _, tagID := range tagIDs {
			account.Tags = append(account.Tags, service.AccountManagementTag{ID: tagID})
		}
		accounts = append(accounts, account)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Cindy balance probe account snapshot: %w", err)
	}
	return accounts, nil
}

func decodeCindyBalanceProbeJSONMap(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	result := make(map[string]any)
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func wrapCindyBalanceProbeCreateTransactionError(err error) error {
	var postgresError *pq.Error
	if errors.As(err, &postgresError) && postgresError.Code == "40001" {
		return service.ErrCindyBalanceProbeChanged
	}
	return service.WrapCindyBalanceProbeCreateError(err)
}

func (r *cindyBalanceProbeRepository) GetJob(ctx context.Context, jobID int64) (*service.CindyBalanceProbeJob, error) {
	job, err := scanCindyBalanceProbeJob(r.db.QueryRowContext(ctx, cindyBalanceProbeJobSelect+` WHERE id = $1`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCindyBalanceProbeNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadCounts(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *cindyBalanceProbeRepository) ListJobs(ctx context.Context, limit int) (*service.CindyBalanceProbeJobList, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cindy_balance_probe_jobs`).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, cindyBalanceProbeJobSelect+` ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	jobs := make([]service.CindyBalanceProbeJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanCindyBalanceProbeJob(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		jobs = append(jobs, *job)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range jobs {
		if err = r.loadCounts(ctx, &jobs[i]); err != nil {
			return nil, err
		}
	}
	return &service.CindyBalanceProbeJobList{Items: jobs, Total: total}, nil
}

func (r *cindyBalanceProbeRepository) ListItems(ctx context.Context, jobID int64, state string, page, pageSize int) (*service.CindyBalanceProbePage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	state = service.NormalizeCindyBalanceProbeState(state)
	where := "job_id = $1"
	args := []any{jobID}
	if state != "" {
		where += " AND state = $2"
		args = append(args, state)
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cindy_balance_probe_items WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	queryArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, job_id, account_id, ordinal, identity_fingerprint, account_updated_at,
		       was_marked, state, luna_outcome, luna_at, terra_outcome, terra_at,
		       request_count, final_outcome, started_at, finished_at, created_at, updated_at
		FROM cindy_balance_probe_items
		WHERE %s ORDER BY ordinal ASC LIMIT $%d OFFSET $%d
	`, where, limitArg, offsetArg), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.CindyBalanceProbeItem, 0, pageSize)
	for rows.Next() {
		item, scanErr := scanCindyBalanceProbeItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return &service.CindyBalanceProbePage{Items: items, Total: total, Page: page, Size: pageSize}, rows.Err()
}

func (r *cindyBalanceProbeRepository) LatestByAccountIDs(ctx context.Context, accountIDs []int64) (map[int64]service.CindyBalanceProbeLatest, error) {
	uniqueIDs := uniquePositiveCindyBalanceProbeAccountIDs(accountIDs)
	latestByAccountID := make(map[int64]service.CindyBalanceProbeLatest, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return latestByAccountID, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (account_id)
		       account_id,
		       job_id,
		       state AS outcome,
		       COALESCE(finished_at, terra_at, luna_at, updated_at) AS checked_at
		FROM cindy_balance_probe_items
		WHERE account_id = ANY($1)
		ORDER BY account_id, COALESCE(finished_at, updated_at) DESC, updated_at DESC, id DESC
	`, pq.Array(uniqueIDs))
	if err != nil {
		return nil, fmt.Errorf("query latest Cindy balance probes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var latest service.CindyBalanceProbeLatest
		if err := rows.Scan(&latest.AccountID, &latest.JobID, &latest.Outcome, &latest.CheckedAt); err != nil {
			return nil, fmt.Errorf("scan latest Cindy balance probe: %w", err)
		}
		latestByAccountID[latest.AccountID] = latest
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest Cindy balance probes: %w", err)
	}
	return latestByAccountID, nil
}

func uniquePositiveCindyBalanceProbeAccountIDs(accountIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(accountIDs))
	uniqueIDs := make([]int64, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		uniqueIDs = append(uniqueIDs, accountID)
	}
	return uniqueIDs
}

func (r *cindyBalanceProbeRepository) ClaimJob(ctx context.Context, leaseToken string, leaseUntil time.Time) (*service.CindyBalanceProbeJob, error) {
	row := r.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM cindy_balance_probe_jobs
			WHERE status IN ('queued', 'running', 'cancel_requested')
			  AND (lease_until IS NULL OR lease_until < NOW() OR lease_token = $1)
			ORDER BY created_at ASC
			FOR UPDATE SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE cindy_balance_probe_jobs AS j
			SET status = CASE WHEN j.status = 'queued' THEN 'running' ELSE j.status END,
			    lease_token = $1, lease_until = $2, heartbeat_at = NOW(),
			    started_at = COALESCE(started_at, NOW()), updated_at = NOW()
			FROM candidate WHERE j.id = candidate.id
			RETURNING j.*
		)
		SELECT id, status, requested_by, scope, rate_rps::float8, candidate_count,
		       candidate_fingerprint, request_count, consecutive_upstream_failures,
		       last_request_started_at, lease_token, lease_until, heartbeat_at,
		       cancel_requested_at, started_at, finished_at, failure_reason, created_at, updated_at
		FROM claimed
	`, leaseToken, leaseUntil)
	job, err := scanCindyBalanceProbeJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return job, err
}

func (r *cindyBalanceProbeRepository) Heartbeat(ctx context.Context, jobID int64, leaseToken string, leaseUntil time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET lease_until = $3, heartbeat_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND lease_token = $2
		  AND status IN ('running', 'cancel_requested') AND lease_until >= NOW()
	`, jobID, leaseToken, leaseUntil)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r *cindyBalanceProbeRepository) RecoverInterruptedItems(ctx context.Context, jobID int64, leaseToken string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE cindy_balance_probe_items AS i
		SET state = 'unknown_after_crash', final_outcome = 'unknown_after_crash',
		    finished_at = NOW(), updated_at = NOW()
		FROM cindy_balance_probe_jobs AS j
		WHERE i.job_id = j.id AND j.id = $1 AND j.lease_token = $2
		  AND j.status IN ('running', 'cancel_requested')
		  AND i.state IN ('luna_running', 'terra_running')
	`, jobID, leaseToken)
	return err
}

func (r *cindyBalanceProbeRepository) ReserveNext(ctx context.Context, jobID int64, leaseToken string, now time.Time, confirmationCutoff time.Time) (*service.CindyBalanceProbeReservation, time.Duration, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var rateRPS float64
	var lastStarted sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT status, rate_rps::float8, last_request_started_at
		FROM cindy_balance_probe_jobs
		WHERE id = $1 AND lease_token = $2 AND lease_until >= NOW()
		FOR UPDATE
	`, jobID, leaseToken).Scan(&status, &rateRPS, &lastStarted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if status != "running" {
		return nil, 0, nil
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE cindy_balance_probe_items
		SET state = 'confirmation_expired', final_outcome = 'confirmation_expired',
		    finished_at = NOW(), updated_at = NOW()
		WHERE job_id = $1 AND state = 'luna_exact' AND luna_at < $2
	`, jobID, confirmationCutoff); err != nil {
		return nil, 0, err
	}
	interval := time.Duration(float64(time.Second) / rateRPS)
	if lastStarted.Valid {
		delay := interval - now.Sub(lastStarted.Time)
		if delay > 0 {
			if err = tx.Commit(); err != nil {
				return nil, 0, err
			}
			return nil, delay, nil
		}
	}
	row := tx.QueryRowContext(ctx, `
		SELECT id, account_id, identity_fingerprint, account_updated_at, was_marked,
		       state, luna_at, request_count
		FROM cindy_balance_probe_items
		WHERE job_id = $1 AND state IN ('pending', 'luna_exact')
		ORDER BY ordinal ASC
		FOR UPDATE SKIP LOCKED LIMIT 1
	`, jobID)
	reservation := &service.CindyBalanceProbeReservation{JobID: jobID, LeaseToken: leaseToken}
	var state string
	var lunaAt sql.NullTime
	var itemRequestCount int
	err = row.Scan(&reservation.ItemID, &reservation.AccountID, &reservation.IdentityFingerprint,
		&reservation.AccountUpdatedAt, &reservation.WasMarked, &state, &lunaAt, &itemRequestCount)
	if errors.Is(err, sql.ErrNoRows) {
		if err = tx.Commit(); err != nil {
			return nil, 0, err
		}
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	reservation.Stage = "luna"
	nextState := "luna_running"
	if state == "luna_exact" {
		reservation.Stage = "terra"
		nextState = "terra_running"
		if lunaAt.Valid {
			value := lunaAt.Time
			reservation.LunaAt = &value
		}
	}
	err = tx.QueryRowContext(ctx, `
		UPDATE cindy_balance_probe_items
		SET state = $2, request_count = request_count + 1,
		    started_at = COALESCE(started_at, $3), updated_at = $3
		WHERE id = $1 AND job_id = $4 AND state = $5 AND request_count = $6
		RETURNING request_count
	`, reservation.ItemID, nextState, now, jobID, state, itemRequestCount).Scan(&reservation.RequestCount)
	if err != nil {
		return nil, 0, err
	}
	err = tx.QueryRowContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET request_count = request_count + 1, last_request_started_at = $3, updated_at = $3
		WHERE id = $1 AND lease_token = $2 AND status = 'running'
		  AND cancel_requested_at IS NULL AND lease_until >= NOW()
		RETURNING request_count
	`, jobID, leaseToken, now).Scan(&reservation.JobRequestCount)
	if err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	return reservation, 0, nil
}

func (r *cindyBalanceProbeRepository) ValidateReservationForSend(
	ctx context.Context,
	reservation *service.CindyBalanceProbeReservation,
	account *service.Account,
	leaseToken string,
) (bool, error) {
	if reservation == nil || leaseToken == "" || reservation.LeaseToken != leaseToken {
		return false, nil
	}
	expectedState := reservation.Stage + "_running"
	if reservation.Stage != "luna" && reservation.Stage != "terra" {
		return false, nil
	}
	if account == nil || account.ID != reservation.AccountID || account.Status != service.StatusActive ||
		!account.Schedulable || !service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		!account.UpdatedAt.UTC().Truncate(time.Microsecond).Equal(reservation.AccountUpdatedAt.UTC().Truncate(time.Microsecond)) ||
		(account.CindyBalanceInsufficientAt != nil) != reservation.WasMarked {
		return false, nil
	}
	fingerprint, err := service.CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	if err != nil {
		return false, fmt.Errorf("fingerprint Cindy balance probe dispatch account: %w", err)
	}
	if fingerprint != reservation.IdentityFingerprint {
		return false, nil
	}
	// JSONB equality in the CAS binds the current database row to the exact
	// credential object whose Go fingerprint matched the reservation.
	credentialsJSON, err := json.Marshal(account.Credentials)
	if err != nil {
		return false, fmt.Errorf("marshal Cindy balance probe dispatch credentials: %w", err)
	}
	var lunaAt any
	if reservation.LunaAt != nil {
		lunaAt = reservation.LunaAt.UTC()
	}
	// The claim token plus the per-item state and request count are the dispatch
	// epoch. Return the job-wide count and Luna timestamp comparisons from the
	// same UPDATE snapshot for diagnostics, but do not treat those observational
	// echoes as authority over an otherwise current reservation.
	var jobRequestCountMatch, lunaAtMatch bool
	err = r.db.QueryRowContext(ctx, `
		UPDATE cindy_balance_probe_items AS i
		SET updated_at = i.updated_at
		FROM cindy_balance_probe_jobs AS j
		JOIN accounts AS a ON a.id = $6
		WHERE i.id = $1 AND i.job_id = j.id AND j.id = $2 AND i.account_id = a.id
		  AND j.lease_token = $3 AND j.lease_until >= NOW()
		  AND j.status = 'running' AND j.cancel_requested_at IS NULL
		  AND i.state = $5 AND i.account_id = $6
		  AND i.identity_fingerprint = $7 AND i.account_updated_at = $8
		  AND i.was_marked = $9 AND i.request_count = $10
		  AND a.platform = $12 AND a.type = $13
		  AND a.status = $14 AND a.schedulable = TRUE AND a.deleted_at IS NULL
		  AND a.updated_at = $8
		  AND ((a.cindy_balance_insufficient_at IS NOT NULL) = $9)
		  AND a.credentials = $15::jsonb
		  AND LOWER(BTRIM(a.credentials->>'base_url')) IN
		      ('https://api.laxarouter.ai', 'https://api.laxarouter.ai/')
		RETURNING j.request_count = $4,
		          i.luna_at IS NOT DISTINCT FROM $11::timestamptz
	`, reservation.ItemID, reservation.JobID, leaseToken, reservation.JobRequestCount,
		expectedState, reservation.AccountID, reservation.IdentityFingerprint,
		reservation.AccountUpdatedAt, reservation.WasMarked, reservation.RequestCount, lunaAt,
		service.PlatformOpenAI, service.AccountTypeAPIKey, service.StatusActive, string(credentialsJSON)).
		Scan(&jobRequestCountMatch, &lunaAtMatch)
	if errors.Is(err, sql.ErrNoRows) {
		slog.Warn("cindy_balance_probe_dispatch_authority_rejected", "stage", reservation.Stage)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !jobRequestCountMatch || !lunaAtMatch {
		slog.Warn("cindy_balance_probe_dispatch_observation_mismatch",
			"stage", reservation.Stage,
			"job_request_count_match", jobRequestCountMatch,
			"luna_at_match", lunaAtMatch,
		)
	}
	return true, nil
}

func (r *cindyBalanceProbeRepository) CompleteStage(
	ctx context.Context,
	reservation *service.CindyBalanceProbeReservation,
	accountSnapshot *service.Account,
	leaseToken, outcome, finalState string,
	networkFailure bool,
) (bool, bool, error) {
	if reservation == nil || leaseToken == "" || reservation.LeaseToken != leaseToken ||
		(reservation.Stage != "luna" && reservation.Stage != "terra") {
		return false, false, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	currentState := reservation.Stage + "_running"
	var itemAccountID int64
	var itemState, itemFingerprint string
	var itemUpdatedAt time.Time
	var itemWasMarked bool
	var itemRequestCount int
	err = tx.QueryRowContext(ctx, `
		SELECT i.account_id, i.state, i.identity_fingerprint,
		       i.account_updated_at, i.was_marked, i.request_count
		FROM cindy_balance_probe_jobs AS j
		JOIN cindy_balance_probe_items AS i ON i.job_id = j.id
		WHERE j.id = $1 AND i.id = $2 AND j.lease_token = $3
		  AND j.status = 'running' AND j.cancel_requested_at IS NULL
		  AND j.lease_until >= clock_timestamp()
		FOR UPDATE OF j, i
	`, reservation.JobID, reservation.ItemID, leaseToken).Scan(
		&itemAccountID,
		&itemState,
		&itemFingerprint,
		&itemUpdatedAt,
		&itemWasMarked,
		&itemRequestCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if itemAccountID != reservation.AccountID || itemState != currentState ||
		itemFingerprint != reservation.IdentityFingerprint ||
		!itemUpdatedAt.UTC().Truncate(time.Microsecond).Equal(
			reservation.AccountUpdatedAt.UTC().Truncate(time.Microsecond),
		) || itemWasMarked != reservation.WasMarked || itemRequestCount != reservation.RequestCount {
		return false, false, nil
	}

	stale := outcome == "stale" && finalState == "skipped_stale"
	var platform, accountType, status string
	var schedulable bool
	var credentialsJSON []byte
	var accountUpdatedAt time.Time
	var markedAt, deletedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT platform, type, status, schedulable, credentials, updated_at,
		       cindy_balance_insufficient_at, deleted_at
		FROM accounts WHERE id = $1 FOR UPDATE
	`, reservation.AccountID).Scan(
		&platform,
		&accountType,
		&status,
		&schedulable,
		&credentialsJSON,
		&accountUpdatedAt,
		&markedAt,
		&deletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		stale = true
	} else if err != nil {
		return false, false, err
	} else {
		credentials := make(map[string]any)
		if unmarshalErr := json.Unmarshal(credentialsJSON, &credentials); unmarshalErr != nil {
			stale = true
		} else {
			currentAccount := &service.Account{
				ID:                         reservation.AccountID,
				Platform:                   platform,
				Type:                       accountType,
				Status:                     status,
				Schedulable:                schedulable,
				Credentials:                credentials,
				UpdatedAt:                  accountUpdatedAt,
				CindyBalanceInsufficientAt: cindyBalanceProbeNullableTimePointer(markedAt),
			}
			currentMatches := !deletedAt.Valid &&
				service.CindyBalanceProbeReservationMatchesAccount(reservation, currentAccount)
			snapshotMatches := service.CindyBalanceProbeReservationMatchesAccount(reservation, accountSnapshot) &&
				cindyBalanceProbeCredentialsEqual(accountSnapshot.Credentials, credentials) &&
				cindyBalanceProbeOptionalTimeEqual(
					accountSnapshot.CindyBalanceInsufficientAt,
					currentAccount.CindyBalanceInsufficientAt,
				)
			if !currentMatches || (!(outcome == "stale" && finalState == "skipped_stale") && !snapshotMatches) {
				stale = true
			}
		}
	}

	stageOutcome := outcome
	state := finalState
	if stale {
		stageOutcome = "stale"
		state = "skipped_stale"
		networkFailure = false
	}
	finished := state != "luna_exact"
	var finalOutcome any
	if finished {
		finalOutcome = stageOutcome
		if stale {
			finalOutcome = "skipped_stale"
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE cindy_balance_probe_items
		SET state = $3,
		    luna_outcome = CASE WHEN $4 = 'luna' THEN $5 ELSE luna_outcome END,
		    luna_at = CASE WHEN $4 = 'luna' THEN NOW() ELSE luna_at END,
		    terra_outcome = CASE WHEN $4 = 'terra' THEN $5 ELSE terra_outcome END,
		    terra_at = CASE WHEN $4 = 'terra' THEN NOW() ELSE terra_at END,
		    final_outcome = $6,
		    finished_at = CASE WHEN $7 THEN NOW() ELSE NULL END,
		    updated_at = NOW()
		WHERE id = $1 AND job_id = $2 AND state = $8
	`, reservation.ItemID, reservation.JobID, state, reservation.Stage, stageOutcome, finalOutcome, finished, currentState)
	if err != nil {
		return false, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return false, false, err
	}
	var failures int
	err = tx.QueryRowContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET consecutive_upstream_failures = CASE WHEN $3 THEN consecutive_upstream_failures + 1 ELSE 0 END,
		    status = CASE WHEN $3 AND consecutive_upstream_failures + 1 >= 3 THEN 'paused_upstream' ELSE status END,
		    lease_token = CASE WHEN $3 AND consecutive_upstream_failures + 1 >= 3 THEN NULL ELSE lease_token END,
		    lease_until = CASE WHEN $3 AND consecutive_upstream_failures + 1 >= 3 THEN NULL ELSE lease_until END,
		    failure_reason = CASE WHEN $3 AND consecutive_upstream_failures + 1 >= 3 THEN 'consecutive_upstream_failures' ELSE failure_reason END,
		    updated_at = NOW()
		WHERE id = $1 AND lease_token = $2
		RETURNING consecutive_upstream_failures
	`, reservation.JobID, leaseToken, networkFailure).Scan(&failures)
	if err != nil {
		return false, false, err
	}
	if err = tx.Commit(); err != nil {
		return false, false, err
	}
	return failures < 3, true, nil
}

func cindyBalanceProbeCredentialsEqual(left, right map[string]any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func cindyBalanceProbeOptionalTimeEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Truncate(time.Microsecond).Equal(right.UTC().Truncate(time.Microsecond))
}

func cindyBalanceProbeNullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func (r *cindyBalanceProbeRepository) FinalizeExhausted(
	ctx context.Context,
	reservation *service.CindyBalanceProbeReservation,
	leaseToken string,
	observedAt time.Time,
	confirmationWindow time.Duration,
) (string, error) {
	return r.finalizeAccountMarker(ctx, reservation, nil, leaseToken, observedAt, confirmationWindow, true)
}

func (r *cindyBalanceProbeRepository) FinalizeRecovery(
	ctx context.Context,
	reservation *service.CindyBalanceProbeReservation,
	accountSnapshot *service.Account,
	leaseToken string,
	observedAt time.Time,
) (bool, error) {
	state, err := r.finalizeAccountMarker(ctx, reservation, accountSnapshot, leaseToken, observedAt, 0, false)
	return state == "recovered", err
}

func (r *cindyBalanceProbeRepository) finalizeAccountMarker(
	ctx context.Context,
	reservation *service.CindyBalanceProbeReservation,
	accountSnapshot *service.Account,
	leaseToken string,
	observedAt time.Time,
	confirmationWindow time.Duration,
	mark bool,
) (string, error) {
	if reservation == nil {
		return "", nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var jobStatus, itemState string
	var cancelAt, lunaAt sql.NullTime
	var databaseNow time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT j.status, j.cancel_requested_at, i.state, i.luna_at, clock_timestamp()
		FROM cindy_balance_probe_jobs AS j
		JOIN cindy_balance_probe_items AS i ON i.job_id = j.id
		WHERE j.id = $1 AND i.id = $2 AND j.lease_token = $3 AND j.lease_until >= NOW()
		FOR UPDATE OF j, i
	`, reservation.JobID, reservation.ItemID, leaseToken).
		Scan(&jobStatus, &cancelAt, &itemState, &lunaAt, &databaseNow)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if jobStatus != "running" || cancelAt.Valid {
		return "", nil
	}
	expectedState := "terra_running"
	if !mark {
		expectedState = "luna_running"
	}
	if itemState != expectedState {
		return "", nil
	}
	if mark && !cindyBalanceProbeConfirmationCurrent(lunaAt, databaseNow, confirmationWindow) {
		return r.finishConfirmationExpiredTx(ctx, tx, reservation)
	}
	resetResult, err := tx.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET consecutive_upstream_failures = 0, updated_at = NOW()
		WHERE id = $1 AND lease_token = $2 AND status = 'running'
		  AND cancel_requested_at IS NULL AND lease_until >= NOW()
	`, reservation.JobID, leaseToken)
	if err != nil {
		return "", err
	}
	resetCount, err := resetResult.RowsAffected()
	if err != nil {
		return "", err
	}
	if resetCount != 1 {
		return "", fmt.Errorf("reset Cindy balance probe failure streak: lost job authority")
	}
	var platform, accountType, status string
	var schedulable bool
	var credentialsJSON []byte
	var updatedAt time.Time
	var markedAt, deletedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT platform, type, status, schedulable, credentials, updated_at,
		       cindy_balance_insufficient_at, deleted_at
		FROM accounts WHERE id = $1 FOR UPDATE
	`, reservation.AccountID).Scan(&platform, &accountType, &status, &schedulable, &credentialsJSON, &updatedAt, &markedAt, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r.finishStaleTx(ctx, tx, reservation, mark)
	}
	if err != nil {
		return "", err
	}
	credentials := make(map[string]any)
	if err = json.Unmarshal(credentialsJSON, &credentials); err != nil {
		return "", err
	}
	fingerprint, fingerprintErr := service.CindyAccountIdentityFingerprint(platform, accountType, credentials)
	eligible := fingerprintErr == nil && fingerprint == reservation.IdentityFingerprint &&
		updatedAt.UTC().Equal(reservation.AccountUpdatedAt.UTC()) &&
		status == service.StatusActive && schedulable && !deletedAt.Valid &&
		service.IsCindyAPIKeyAccount(platform, accountType, credentials)
	if !mark {
		currentMarker := cindyBalanceProbeNullableTimePointer(markedAt)
		eligible = eligible &&
			service.CindyBalanceProbeReservationMatchesAccount(reservation, accountSnapshot) &&
			cindyBalanceProbeCredentialsEqual(accountSnapshot.Credentials, credentials) &&
			cindyBalanceProbeOptionalTimeEqual(accountSnapshot.CindyBalanceInsufficientAt, currentMarker)
	}
	if !eligible {
		return r.finishStaleTx(ctx, tx, reservation, mark)
	}
	finalState := "exhausted"
	if mark && markedAt.Valid {
		finalState = "already_marked"
	} else if !mark && !markedAt.Valid {
		return r.finishStaleTx(ctx, tx, reservation, mark)
	} else {
		if mark {
			_, err = tx.ExecContext(ctx, `UPDATE accounts SET cindy_balance_insufficient_at = $2, updated_at = NOW() WHERE id = $1`, reservation.AccountID, observedAt)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE accounts SET cindy_balance_insufficient_at = NULL, updated_at = NOW() WHERE id = $1`, reservation.AccountID)
			finalState = "recovered"
		}
		if err != nil {
			return "", err
		}
		if err = enqueueSchedulerOutbox(
			ctx,
			tx,
			service.SchedulerOutboxEventAccountChanged,
			&reservation.AccountID,
			nil,
			map[string]any{
				"source": "cindy_balance_probe",
				"job_id": reservation.JobID,
			},
		); err != nil {
			return "", err
		}
	}
	stageColumn := "terra_outcome"
	stageOutcome := "exact"
	if !mark {
		stageColumn = "luna_outcome"
		stageOutcome = "success"
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE cindy_balance_probe_items
		SET state = $2, %s = CASE WHEN %s IS NULL THEN $3 ELSE %s END,
		    final_outcome = $2, finished_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, stageColumn, stageColumn, stageColumn), reservation.ItemID, finalState, stageOutcome); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return finalState, nil
}

func cindyBalanceProbeConfirmationCurrent(lunaAt sql.NullTime, now time.Time, window time.Duration) bool {
	if !lunaAt.Valid || now.IsZero() || window <= 0 {
		return false
	}
	lunaTime := lunaAt.Time.UTC()
	now = now.UTC()
	return !lunaTime.After(now) && !lunaTime.Before(now.Add(-window))
}

func (r *cindyBalanceProbeRepository) finishConfirmationExpiredTx(
	ctx context.Context,
	tx *sql.Tx,
	reservation *service.CindyBalanceProbeReservation,
) (string, error) {
	const outcome = "inconclusive_confirmation_expired"
	if _, err := tx.ExecContext(ctx, `
		UPDATE cindy_balance_probe_items
		SET state = 'inconclusive', terra_outcome = 'exact', terra_at = NOW(),
		    final_outcome = $2, finished_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND state = 'terra_running'
	`, reservation.ItemID, outcome); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return outcome, nil
}

func (r *cindyBalanceProbeRepository) finishStaleTx(ctx context.Context, tx *sql.Tx, reservation *service.CindyBalanceProbeReservation, mark bool) (string, error) {
	column := "terra_outcome"
	if !mark {
		column = "luna_outcome"
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE cindy_balance_probe_items
		SET state = 'skipped_stale', %s = 'stale', final_outcome = 'skipped_stale',
		    finished_at = NOW(), updated_at = NOW() WHERE id = $1
	`, column), reservation.ItemID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return "skipped_stale", nil
}

func (r *cindyBalanceProbeRepository) FinishIfDone(ctx context.Context, jobID int64, leaseToken string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM cindy_balance_probe_jobs WHERE id = $1 AND lease_token = $2 FOR UPDATE`, jobID, leaseToken).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if status == "cancel_requested" {
		if _, err = tx.ExecContext(ctx, `
			UPDATE cindy_balance_probe_items SET state = 'canceled', final_outcome = 'canceled',
			finished_at = NOW(), updated_at = NOW()
			WHERE job_id = $1 AND state IN ('pending', 'luna_exact')
		`, jobID); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE cindy_balance_probe_jobs SET status = 'canceled', finished_at = NOW(),
			lease_token = NULL, lease_until = NULL, updated_at = NOW() WHERE id = $1
		`, jobID); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if status != "running" {
		return true, nil
	}
	var remaining int
	if err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cindy_balance_probe_items
		WHERE job_id = $1 AND state IN ('pending', 'luna_running', 'luna_exact', 'terra_running')
	`, jobID).Scan(&remaining); err != nil {
		return false, err
	}
	if remaining > 0 {
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	var issues int
	if err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cindy_balance_probe_items
		WHERE job_id = $1 AND state IN ('inconclusive', 'confirmation_expired', 'skipped_stale', 'unknown_after_crash')
	`, jobID).Scan(&issues); err != nil {
		return false, err
	}
	finalStatus := "completed"
	if issues > 0 {
		finalStatus = "completed_with_issues"
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs SET status = $2, finished_at = NOW(),
		lease_token = NULL, lease_until = NULL, heartbeat_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, jobID, finalStatus); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *cindyBalanceProbeRepository) SetRate(ctx context.Context, jobID int64, rateRPS float64) (*service.CindyBalanceProbeJob, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs SET rate_rps = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('queued', 'running', 'paused', 'paused_upstream')
	`, jobID, rateRPS)
	if err != nil {
		return nil, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return nil, service.ErrCindyBalanceProbeNotFound
	}
	return r.GetJob(ctx, jobID)
}

func (r *cindyBalanceProbeRepository) Pause(ctx context.Context, jobID int64) (*service.CindyBalanceProbeJob, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs SET status = 'paused', lease_token = NULL,
		lease_until = NULL, updated_at = NOW()
		WHERE id = $1 AND status IN ('queued', 'running')
	`, jobID)
	if err != nil {
		return nil, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return nil, service.ErrCindyBalanceProbeNotFound
	}
	return r.GetJob(ctx, jobID)
}

func (r *cindyBalanceProbeRepository) Resume(ctx context.Context, jobID int64) (*service.CindyBalanceProbeJob, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs SET status = 'queued', consecutive_upstream_failures = 0,
		failure_reason = NULL, updated_at = NOW()
		WHERE id = $1 AND status IN ('paused', 'paused_upstream')
	`, jobID)
	if err != nil {
		return nil, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return nil, service.ErrCindyBalanceProbeNotFound
	}
	return r.GetJob(ctx, jobID)
}

func (r *cindyBalanceProbeRepository) Cancel(ctx context.Context, jobID int64) (*service.CindyBalanceProbeJob, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs SET status = 'cancel_requested', cancel_requested_at = NOW(),
		lease_token = NULL, lease_until = NULL, updated_at = NOW()
		WHERE id = $1 AND status IN ('queued', 'running', 'paused', 'paused_upstream')
	`, jobID)
	if err != nil {
		return nil, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return nil, service.ErrCindyBalanceProbeNotFound
	}
	return r.GetJob(ctx, jobID)
}

func (r *cindyBalanceProbeRepository) PruneFinished(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM cindy_balance_probe_jobs
		WHERE finished_at < $1 AND status IN ('completed', 'completed_with_issues', 'canceled')
	`, before)
	return err
}

func (r *cindyBalanceProbeRepository) loadCounts(ctx context.Context, job *service.CindyBalanceProbeJob) error {
	if job == nil {
		return nil
	}
	return r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE state IN ('pending', 'luna_exact')),
			COUNT(*) FILTER (WHERE state IN ('luna_running', 'terra_running')),
			COUNT(*) FILTER (WHERE state = 'healthy'),
			COUNT(*) FILTER (WHERE state = 'recovered'),
			COUNT(*) FILTER (WHERE state IN ('exhausted', 'already_marked', 'still_exhausted')),
			COUNT(*) FILTER (WHERE state IN ('inconclusive', 'confirmation_expired', 'unknown_after_crash')),
			COUNT(*) FILTER (WHERE state IN ('skipped_stale', 'canceled'))
		FROM cindy_balance_probe_items WHERE job_id = $1
	`, job.ID).Scan(&job.Counts.Pending, &job.Counts.Running, &job.Counts.Healthy,
		&job.Counts.Recovered, &job.Counts.Exhausted, &job.Counts.Inconclusive, &job.Counts.Skipped)
}

const cindyBalanceProbeJobSelect = `
	SELECT id, status, requested_by, scope, rate_rps::float8, candidate_count,
	       candidate_fingerprint, request_count, consecutive_upstream_failures,
	       last_request_started_at, lease_token, lease_until, heartbeat_at,
	       cancel_requested_at, started_at, finished_at, failure_reason, created_at, updated_at
	FROM cindy_balance_probe_jobs`

type cindyBalanceProbeRowScanner interface {
	Scan(dest ...any) error
}

func scanCindyBalanceProbeJob(row cindyBalanceProbeRowScanner) (*service.CindyBalanceProbeJob, error) {
	job := &service.CindyBalanceProbeJob{}
	var requestedBy sql.NullInt64
	var scopeJSON []byte
	var lastStarted, leaseUntil, heartbeat, canceled, started, finished sql.NullTime
	var leaseToken, failureReason sql.NullString
	err := row.Scan(&job.ID, &job.Status, &requestedBy, &scopeJSON, &job.RateRPS,
		&job.CandidateCount, &job.CandidateFingerprint, &job.RequestCount, &job.ConsecutiveFailures,
		&lastStarted, &leaseToken, &leaseUntil, &heartbeat, &canceled, &started, &finished,
		&failureReason, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if requestedBy.Valid {
		value := requestedBy.Int64
		job.RequestedBy = &value
	}
	job.Scope = service.DecodeCindyBalanceProbeScope(scopeJSON)
	job.LeaseToken = leaseToken.String
	job.FailureReason = failureReason.String
	setNullTime := func(source sql.NullTime, target **time.Time) {
		if source.Valid {
			value := source.Time
			*target = &value
		}
	}
	setNullTime(lastStarted, &job.LastRequestStartedAt)
	setNullTime(leaseUntil, &job.LeaseUntil)
	setNullTime(heartbeat, &job.HeartbeatAt)
	setNullTime(canceled, &job.CancelRequestedAt)
	setNullTime(started, &job.StartedAt)
	setNullTime(finished, &job.FinishedAt)
	return job, nil
}

func scanCindyBalanceProbeItem(row cindyBalanceProbeRowScanner) (*service.CindyBalanceProbeItem, error) {
	item := &service.CindyBalanceProbeItem{}
	var lunaOutcome, terraOutcome, finalOutcome sql.NullString
	var lunaAt, terraAt, startedAt, finishedAt sql.NullTime
	err := row.Scan(&item.ID, &item.JobID, &item.AccountID, &item.Ordinal,
		&item.IdentityFingerprint, &item.AccountUpdatedAt, &item.WasMarked, &item.State,
		&lunaOutcome, &lunaAt, &terraOutcome, &terraAt, &item.RequestCount, &finalOutcome,
		&startedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.LunaOutcome = lunaOutcome.String
	item.TerraOutcome = terraOutcome.String
	item.FinalOutcome = finalOutcome.String
	if lunaAt.Valid {
		value := lunaAt.Time
		item.LunaAt = &value
	}
	if terraAt.Valid {
		value := terraAt.Time
		item.TerraAt = &value
	}
	if startedAt.Valid {
		value := startedAt.Time
		item.StartedAt = &value
	}
	if finishedAt.Valid {
		value := finishedAt.Time
		item.FinishedAt = &value
	}
	return item, nil
}

var _ service.CindyBalanceProbeRepository = (*cindyBalanceProbeRepository)(nil)

func nullableCindyBalanceProbeRequester(requestedBy *int64) any {
	if requestedBy == nil {
		return nil
	}
	return *requestedBy
}
