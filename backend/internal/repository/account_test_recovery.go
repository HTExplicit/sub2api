package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Lock and inspect the current account in the same statement as the recovery.
// schedulable is never assigned; disabled/expired accounts are left intact.
func (r *accountRepository) RecoverAfterSuccessfulTest(ctx context.Context, id int64) (*service.SuccessfulTestRecoveryResult, error) {
	result := &service.SuccessfulTestRecoveryResult{}
	rows, err := r.sql.QueryContext(ctx, `WITH current AS MATERIALIZED (
		SELECT id, status, schedulable, status='error' AS had_error,
		(rate_limited_at IS NOT NULL OR rate_limit_reset_at IS NOT NULL OR overload_until IS NOT NULL
		 OR temp_unschedulable_until IS NOT NULL
		 OR COALESCE(extra->'model_rate_limits','{}'::jsonb) NOT IN ('{}'::jsonb,'null'::jsonb)
		 OR COALESCE(extra->'antigravity_quota_scopes','{}'::jsonb) NOT IN ('{}'::jsonb,'null'::jsonb)) AS had_limits
		FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE
	), updated AS (
		UPDATE accounts a SET status=CASE WHEN current.had_error THEN 'active' ELSE a.status END,
		error_message=CASE WHEN current.had_error THEN '' ELSE a.error_message END,
		rate_limited_at=NULL, rate_limit_reset_at=NULL, overload_until=NULL,
		temp_unschedulable_until=NULL, temp_unschedulable_reason=NULL,
		extra=COALESCE(a.extra,'{}'::jsonb)-'model_rate_limits'-'antigravity_quota_scopes', updated_at=NOW()
		FROM current WHERE a.id=current.id AND current.status IN ('active','error')
		AND (current.had_error OR current.had_limits) RETURNING a.id
	), emitted AS (
		INSERT INTO scheduler_outbox (event_type,account_id,group_id,payload)
		SELECT $2,id,NULL,NULL FROM updated
	)
	SELECT current.had_error AND EXISTS(SELECT 1 FROM updated),
	 current.had_limits AND EXISTS(SELECT 1 FROM updated),
	 current.status NOT IN ('active','error') OR NOT current.schedulable FROM current`,
		id, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrAccountNotFound
	}
	if err := rows.Scan(&result.ClearedError, &result.ClearedRateLimit, &result.ManualStatePreserved); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if result.ClearedError || result.ClearedRateLimit {
		r.syncSchedulerAccountSnapshotDetached(ctx, id)
	}
	return result, nil
}

var _ service.SuccessfulTestRecoveryRepository = (*accountRepository)(nil)
