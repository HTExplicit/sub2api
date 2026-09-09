package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountCapabilityRepository struct{ db *sql.DB }

func NewAccountCapabilityRepository(db *sql.DB) service.AccountCapabilityRepository {
	return &accountCapabilityRepository{db: db}
}

const capabilityRunColumns = `r.id,r.created_by,r.kind,r.idempotency_key,r.request_hash,r.folder_ids,r.account_ids,r.status,r.target_count,r.started_at,r.finished_at,r.created_at,r.updated_at`
const capabilityRunCounts = `,
 (SELECT COUNT(*) FROM admin_capability_items ci WHERE ci.run_id=r.id AND ci.status NOT IN ('pending','running')),
 (SELECT COUNT(*) FROM admin_capability_items ci WHERE ci.run_id=r.id AND ci.status='succeeded'),
 (SELECT COUNT(*) FROM admin_capability_items ci WHERE ci.run_id=r.id AND ci.status IN ('failed','indeterminate','stale')),
 (SELECT COALESCE(SUM(ci.request_count),0) FROM admin_capability_items ci WHERE ci.run_id=r.id),
 (SELECT COUNT(*) FROM admin_capability_items ci WHERE ci.run_id=r.id AND ci.result->>'request_count_unknown'='true')`
const capabilityItemColumns = `i.id,i.run_id,i.ordinal,i.account_id,i.account_name,i.folder_id,i.config_fingerprint,i.upstream_model,i.protocol,i.profile,i.aliases,i.status,i.result,i.request_count,i.claimed_at,i.dispatched_at,i.finished_at,i.created_at,r.kind,` + capabilityPublicationSupersededSQL + ` AS publication_superseded`

type capabilityScanner interface{ Scan(...any) error }

func scanCapabilityRun(row capabilityScanner) (*service.AccountCapabilityRun, error) {
	run := &service.AccountCapabilityRun{}
	var folders, accounts []byte
	var started, finished sql.NullTime
	err := row.Scan(&run.ID, &run.CreatedBy, &run.Kind, &run.IdempotencyKey, &run.RequestHash, &folders, &accounts, &run.Status, &run.TargetCount, &started, &finished, &run.CreatedAt, &run.UpdatedAt, &run.ProcessedCount, &run.SucceededCount, &run.FailedCount, &run.RequestCount, &run.PossiblySentCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountCapabilityNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(folders, &run.FolderIDs); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(accounts, &run.AccountIDs); err != nil {
		return nil, err
	}
	if started.Valid {
		run.StartedAt = &started.Time
	}
	if finished.Valid {
		run.FinishedAt = &finished.Time
	}
	return run, nil
}

func scanCapabilityItem(row capabilityScanner) (*service.AccountCapabilityItem, error) {
	item := &service.AccountCapabilityItem{}
	var aliases, result []byte
	var claimed, dispatched, finished sql.NullTime
	err := row.Scan(&item.ID, &item.RunID, &item.Ordinal, &item.AccountID, &item.AccountName, &item.FolderID, &item.ConfigFingerprint, &item.UpstreamModel, &item.Protocol, &item.Profile, &aliases, &item.Status, &result, &item.RequestCount, &claimed, &dispatched, &finished, &item.CreatedAt, &item.Kind, &item.PublicationSuperseded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountCapabilityNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(aliases, &item.Aliases); err != nil {
		return nil, err
	}
	item.Result = append(json.RawMessage(nil), result...)
	var observation struct {
		RequestCountUnknown bool `json:"request_count_unknown"`
	}
	if json.Unmarshal(result, &observation) == nil {
		item.RequestCountUnknown = observation.RequestCountUnknown
	}
	if claimed.Valid {
		item.ClaimedAt = &claimed.Time
	}
	if dispatched.Valid {
		item.DispatchedAt = &dispatched.Time
	}
	if finished.Valid {
		item.FinishedAt = &finished.Time
	}
	return item, nil
}

func (r *accountCapabilityRepository) FindIdempotent(ctx context.Context, actor int64, key string) (*service.AccountCapabilityRun, error) {
	return scanCapabilityRun(r.db.QueryRowContext(ctx, `SELECT `+capabilityRunColumns+capabilityRunCounts+` FROM admin_capability_runs r WHERE r.created_by=$1 AND r.idempotency_key=$2`, actor, key))
}

func (r *accountCapabilityRepository) GetRun(ctx context.Context, id int64) (*service.AccountCapabilityRun, error) {
	return scanCapabilityRun(r.db.QueryRowContext(ctx, `SELECT `+capabilityRunColumns+capabilityRunCounts+` FROM admin_capability_runs r WHERE r.id=$1`, id))
}

func (r *accountCapabilityRepository) Create(ctx context.Context, run *service.AccountCapabilityRun, items []service.AccountCapabilityItem) (*service.AccountCapabilityRun, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if run.OnlyUntested {
		if run.Kind != service.AccountCapabilityKindProbe || len(items) == 0 {
			return nil, false, service.ErrAccountCapabilityInvalid
		}
		// Serialize automatic creation with queue claims and other automatic
		// batches. A new idempotency key must not authorize the same paid probe.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('admin_capability_queue'))`); err != nil {
			return nil, false, err
		}
		existing, findErr := scanCapabilityRun(tx.QueryRowContext(ctx, `SELECT `+capabilityRunColumns+capabilityRunCounts+` FROM admin_capability_runs r WHERE r.created_by=$1 AND r.idempotency_key=$2`, run.CreatedBy, run.IdempotencyKey))
		if findErr == nil {
			if existing.RequestHash != run.RequestHash {
				return nil, false, service.ErrAccountCapabilityIdempotencyConflict
			}
			if err = tx.Commit(); err != nil {
				return nil, false, err
			}
			return existing, true, nil
		}
		if !errors.Is(findErr, service.ErrAccountCapabilityNotFound) {
			return nil, false, findErr
		}
		for _, item := range items {
			var attempted bool
			err = tx.QueryRowContext(ctx, capabilityUnattemptedConflictSQL,
				item.AccountID, item.ConfigFingerprint, item.UpstreamModel, item.Protocol).Scan(&attempted)
			if err != nil {
				return nil, false, err
			}
			if attempted {
				return nil, false, service.ErrAccountCapabilityAlreadyAttempted
			}
		}
	}
	folders, err := json.Marshal(run.FolderIDs)
	if err != nil {
		return nil, false, err
	}
	accounts, err := json.Marshal(run.AccountIDs)
	if err != nil {
		return nil, false, err
	}
	columns := strings.ReplaceAll(capabilityRunColumns, "r.", "")
	created, err := scanCapabilityRun(tx.QueryRowContext(ctx, `INSERT INTO admin_capability_runs
 (created_by,kind,idempotency_key,request_hash,folder_ids,account_ids,target_count)
 VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7)
 ON CONFLICT(created_by,idempotency_key) DO NOTHING RETURNING `+columns+`,0,0,0,0,0`, run.CreatedBy, run.Kind, run.IdempotencyKey, run.RequestHash, string(folders), string(accounts), len(items)))
	if errors.Is(err, service.ErrAccountCapabilityNotFound) {
		_ = tx.Rollback()
		existing, findErr := r.FindIdempotent(ctx, run.CreatedBy, run.IdempotencyKey)
		if findErr != nil {
			return nil, false, findErr
		}
		if existing.RequestHash != run.RequestHash {
			return nil, false, service.ErrAccountCapabilityIdempotencyConflict
		}
		return existing, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	for _, item := range items {
		aliases := item.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		raw, marshalErr := json.Marshal(aliases)
		if marshalErr != nil {
			return nil, false, marshalErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO admin_capability_items
 (run_id,ordinal,account_id,account_name,folder_id,config_fingerprint,upstream_model,protocol,profile,aliases)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)`, created.ID, item.Ordinal, item.AccountID, item.AccountName, item.FolderID, item.ConfigFingerprint, item.UpstreamModel, item.Protocol, item.Profile, string(raw))
		if err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return created, false, nil
}

// Canceled work is reusable only if it was certainly never dispatched.
// Any prior basic success for this exact configuration/model prevents an
// automatic extra-interface probe, even when its current readiness changed.
const capabilityUnattemptedConflictSQL = `SELECT EXISTS (
 SELECT 1 FROM admin_capability_items i
 JOIN admin_capability_runs r ON r.id=i.run_id
 WHERE r.kind='probe' AND i.profile='text'
 AND i.account_id=$1 AND i.config_fingerprint=$2 AND i.upstream_model=$3
 AND (
   (i.protocol=$4 AND (i.status<>'canceled' OR i.dispatched_at IS NOT NULL
     OR i.request_count>0 OR i.result->>'request_count_unknown'='true'))
   OR (i.status='succeeded' AND i.result->>'status'='alive'
     AND i.protocol IN ('responses','chat_completions','messages','responses_websocket'))
 ))`

func capabilityPage(filter service.AccountCapabilityFilter) service.AccountCapabilityFilter {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 50
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	return filter
}

func capabilityWhere(filter service.AccountCapabilityFilter, item bool, args *[]any) string {
	clauses := []string{"TRUE"}
	add := func(expression string, value any) {
		*args = append(*args, value)
		clauses = append(clauses, fmt.Sprintf(expression, len(*args)))
	}
	if filter.Kind != "" {
		add("r.kind=$%d", filter.Kind)
	}
	if filter.Status != "" {
		if item {
			add("i.status=$%d", filter.Status)
		} else {
			add("r.status=$%d", filter.Status)
		}
	}
	if item {
		if filter.AccountID > 0 {
			add("i.account_id=$%d", filter.AccountID)
		}
		if len(filter.AccountIDs) > 0 {
			parts := make([]string, 0, len(filter.AccountIDs))
			for _, id := range filter.AccountIDs {
				*args = append(*args, id)
				parts = append(parts, "$"+strconv.Itoa(len(*args)))
			}
			clauses = append(clauses, "i.account_id IN ("+strings.Join(parts, ",")+")")
		}
		if filter.Model != "" {
			add("i.upstream_model ILIKE '%%' || $%d || '%%'", filter.Model)
		}
		if len(filter.FolderIDs) > 0 {
			parts := make([]string, 0, len(filter.FolderIDs))
			for _, id := range filter.FolderIDs {
				*args = append(*args, id)
				parts = append(parts, "$"+strconv.Itoa(len(*args)))
			}
			clauses = append(clauses, "i.folder_id IN ("+strings.Join(parts, ",")+")")
		}
	} else {
		accountIDs := append([]int64(nil), filter.AccountIDs...)
		if filter.AccountID > 0 {
			accountIDs = append(accountIDs, filter.AccountID)
		}
		if len(accountIDs) > 0 {
			parts := make([]string, 0, len(accountIDs))
			for _, id := range accountIDs {
				*args = append(*args, strconv.FormatInt(id, 10))
				parts = append(parts, "r.account_ids @> ('[' || $"+strconv.Itoa(len(*args))+"::text || ']')::jsonb")
			}
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
		if len(filter.FolderIDs) > 0 {
			parts := make([]string, 0, len(filter.FolderIDs))
			for _, id := range filter.FolderIDs {
				*args = append(*args, strconv.FormatInt(id, 10))
				parts = append(parts, "r.folder_ids @> ('[' || $"+strconv.Itoa(len(*args))+"::text || ']')::jsonb")
			}
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}
	return strings.Join(clauses, " AND ")
}

func (r *accountCapabilityRepository) ListRuns(ctx context.Context, filter service.AccountCapabilityFilter) (*service.AccountCapabilityRunPage, error) {
	filter = capabilityPage(filter)
	args := []any{}
	where := capabilityWhere(filter, false, &args)
	page := &service.AccountCapabilityRunPage{Items: []service.AccountCapabilityRun{}, Page: filter.Page, PageSize: filter.PageSize}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_capability_runs r WHERE `+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+capabilityRunColumns+capabilityRunCounts+` FROM admin_capability_runs r WHERE `+where+` ORDER BY r.id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		run, scanErr := scanCapabilityRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		page.Items = append(page.Items, *run)
	}
	return page, rows.Err()
}

func (r *accountCapabilityRepository) ListItems(ctx context.Context, runID int64, filter service.AccountCapabilityFilter) (*service.AccountCapabilityItemPage, error) {
	return r.listItems(ctx, runID, filter, false)
}

func (r *accountCapabilityRepository) LatestItems(ctx context.Context, filter service.AccountCapabilityFilter) (*service.AccountCapabilityItemPage, error) {
	return r.listItems(ctx, 0, filter, true)
}

func (r *accountCapabilityRepository) listItems(ctx context.Context, runID int64, filter service.AccountCapabilityFilter, latest bool) (*service.AccountCapabilityItemPage, error) {
	filter = capabilityPage(filter)
	args := []any{}
	where := capabilityWhere(filter, true, &args)
	from := `admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id`
	if runID > 0 {
		args = append(args, runID)
		where += ` AND i.run_id=$` + strconv.Itoa(len(args))
	}
	if latest {
		// Select the latest terminal observation BEFORE status filters. A newer
		// failure must not disappear behind an older successful observation.
		from = `(SELECT DISTINCT ON (ci.account_id,ci.upstream_model,ci.protocol,ci.profile)
 ci.* FROM admin_capability_items ci WHERE ci.status NOT IN ('pending','running')
 ORDER BY ci.account_id,ci.upstream_model,ci.protocol,ci.profile,ci.finished_at DESC NULLS LAST,ci.id DESC) i
 JOIN admin_capability_runs r ON r.id=i.run_id`
	}
	page := &service.AccountCapabilityItemPage{Items: []service.AccountCapabilityItem{}, Page: filter.Page, PageSize: filter.PageSize}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+from+` WHERE `+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+capabilityItemColumns+` FROM `+from+` WHERE `+where+` ORDER BY i.id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		item, scanErr := scanCapabilityItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		page.Items = append(page.Items, *item)
	}
	return page, rows.Err()
}

func (r *accountCapabilityRepository) GetItemsByIDs(ctx context.Context, ids []int64) ([]service.AccountCapabilityItem, error) {
	if len(ids) == 0 {
		return []service.AccountCapabilityItem{}, nil
	}
	args := make([]any, 0, len(ids))
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+capabilityItemColumns+` FROM admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id WHERE i.id IN (`+strings.Join(placeholders, ",")+`) ORDER BY i.id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []service.AccountCapabilityItem{}
	for rows.Next() {
		item, scanErr := scanCapabilityItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *accountCapabilityRepository) ScopeItems(ctx context.Context, runID int64) ([]service.AccountCapabilityItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT ON (i.account_id) `+capabilityItemColumns+` FROM admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id WHERE i.run_id=$1 AND i.status='pending' ORDER BY i.account_id,i.id`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []service.AccountCapabilityItem{}
	for rows.Next() {
		item, scanErr := scanCapabilityItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *accountCapabilityRepository) beginControl(ctx context.Context) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('admin_capability_queue'))`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (r *accountCapabilityRepository) Claim(ctx context.Context) (*service.AccountCapabilityItem, error) {
	tx, err := r.beginControl(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanCapabilityItem(tx.QueryRowContext(ctx, `WITH candidate AS (
 SELECT i.id FROM admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id
 WHERE i.status='pending' AND r.status IN ('pending','running')
 AND (SELECT COUNT(*) FROM admin_capability_items active WHERE active.status='running') < 2
 AND NOT EXISTS (SELECT 1 FROM admin_capability_items active WHERE active.account_id=i.account_id AND active.status='running')
 ORDER BY r.id,i.ordinal FOR UPDATE OF i SKIP LOCKED LIMIT 1
 ), claimed AS (
 UPDATE admin_capability_items SET status='running',claimed_at=NOW() WHERE id IN (SELECT id FROM candidate) RETURNING *
 ) SELECT `+capabilityItemColumns+` FROM claimed i JOIN admin_capability_runs r ON r.id=i.run_id`))
	if errors.Is(err, service.ErrAccountCapabilityNotFound) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE admin_capability_runs SET status='running',started_at=COALESCE(started_at,NOW()),updated_at=NOW() WHERE id=$1`, item.RunID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *accountCapabilityRepository) MarkDispatched(ctx context.Context, itemID int64) (bool, error) {
	tx, err := r.beginControl(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE admin_capability_items i SET dispatched_at=NOW()
 FROM admin_capability_runs r WHERE i.id=$1 AND i.run_id=r.id AND i.status='running'
 AND i.dispatched_at IS NULL AND r.status IN ('pending','running')`, itemID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return count == 1, nil
}

func (r *accountCapabilityRepository) Release(ctx context.Context, itemID int64) error {
	tx, err := r.beginControl(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var runID int64
	err = tx.QueryRowContext(ctx, `UPDATE admin_capability_items i SET status=CASE WHEN r.status IN ('canceling','canceled') THEN 'canceled' ELSE 'pending' END,
 claimed_at=NULL,finished_at=CASE WHEN r.status IN ('canceling','canceled') THEN NOW() ELSE NULL END
 FROM admin_capability_runs r WHERE i.id=$1 AND i.run_id=r.id AND i.status='running' AND i.dispatched_at IS NULL RETURNING i.run_id`, itemID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if err = reconcileCapabilityRun(ctx, tx, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *accountCapabilityRepository) Complete(ctx context.Context, itemID int64, status string, result json.RawMessage, count int) error {
	switch status {
	case "succeeded", "failed", "indeterminate", "stale", "canceled":
	default:
		return service.ErrAccountCapabilityInvalid
	}
	if count < 0 || !json.Valid(result) {
		return service.ErrAccountCapabilityInvalid
	}
	if err := service.ValidateAccountJobMetadata(result); err != nil {
		return service.ErrAccountCapabilityInvalid
	}
	tx, err := r.beginControl(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var runID int64
	err = tx.QueryRowContext(ctx, `UPDATE admin_capability_items SET status=$2,result=$3::jsonb,request_count=$4,finished_at=NOW() WHERE id=$1 AND status='running' RETURNING run_id`, itemID, status, string(result), count).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if err = reconcileCapabilityRun(ctx, tx, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func reconcileCapabilityRun(ctx context.Context, tx *sql.Tx, runID int64) error {
	_, err := tx.ExecContext(ctx, `WITH counts AS (
 SELECT COUNT(*) FILTER(WHERE status='pending') AS pending,COUNT(*) FILTER(WHERE status='running') AS running
 FROM admin_capability_items WHERE run_id=$1
 ), next AS (
 SELECT CASE WHEN r.status IN ('canceling','canceled') AND c.running=0 THEN 'canceled'
 WHEN c.pending=0 AND c.running=0 THEN 'completed'
 WHEN r.status='pausing' AND c.running=0 THEN 'paused' ELSE r.status END AS status
 FROM admin_capability_runs r CROSS JOIN counts c WHERE r.id=$1
 ) UPDATE admin_capability_runs r SET status=n.status,updated_at=NOW(),
 finished_at=CASE WHEN n.status IN ('completed','canceled') THEN COALESCE(r.finished_at,NOW()) ELSE NULL END FROM next n WHERE r.id=$1`, runID)
	return err
}

func (r *accountCapabilityRepository) Control(ctx context.Context, runID int64, action string) (*service.AccountCapabilityRun, error) {
	tx, err := r.beginControl(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM admin_capability_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountCapabilityNotFound
	}
	if err != nil {
		return nil, err
	}
	next := status
	switch action {
	case "pause":
		switch status {
		case "pending", "running":
			next = "pausing"
		case "paused", "pausing":
		default:
			return nil, service.ErrAccountCapabilityConflict
		}
	case "resume":
		switch status {
		case "paused":
			next = "pending"
		case "pending", "running":
		default:
			return nil, service.ErrAccountCapabilityConflict
		}
	case "cancel":
		if status != "completed" && status != "canceled" {
			next = "canceling"
		}
	default:
		return nil, service.ErrAccountCapabilityInvalid
	}
	if _, err = tx.ExecContext(ctx, `UPDATE admin_capability_runs SET status=$2,updated_at=NOW() WHERE id=$1`, runID, next); err != nil {
		return nil, err
	}
	if next == "canceling" {
		if _, err = tx.ExecContext(ctx, `UPDATE admin_capability_items SET status='canceled',finished_at=NOW() WHERE run_id=$1 AND status='pending'`, runID); err != nil {
			return nil, err
		}
	}
	if err = reconcileCapabilityRun(ctx, tx, runID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetRun(ctx, runID)
}

func (r *accountCapabilityRepository) RecoverInterrupted(ctx context.Context) error {
	tx, err := r.beginControl(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// A dispatch marker is durable before any upstream request. Ambiguous sent
	// items are never returned to pending; only never-dispatched reservations are.
	if _, err = tx.ExecContext(ctx, `UPDATE admin_capability_items SET status='indeterminate',finished_at=NOW(),
 result='{"status":"uncertain","classification":"interrupted_after_dispatch","request_count_unknown":true,"reason":"The request may have reached the upstream; it will not be retried automatically. The exact request count is unknown."}'::jsonb
 WHERE status='running' AND dispatched_at IS NOT NULL`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE admin_capability_items SET status='pending',claimed_at=NULL WHERE status='running' AND dispatched_at IS NULL`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE admin_capability_items i SET status='canceled',finished_at=NOW() FROM admin_capability_runs r WHERE i.run_id=r.id AND i.status='pending' AND r.status='canceling'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE admin_capability_runs SET status='pausing' WHERE status='running'`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM admin_capability_runs WHERE status IN ('pausing','canceling')`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return rowErr
	}
	for _, id := range ids {
		if err = reconcileCapabilityRun(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *accountCapabilityRepository) AcquireRuntimeLease(ctx context.Context) (func(), bool, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('admin_capability_runtime'))`).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, unlockErr := conn.ExecContext(releaseCtx, `SELECT pg_advisory_unlock(hashtext('admin_capability_runtime'))`); unlockErr != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			_ = conn.Close()
		})
	}
	return release, true, nil
}
