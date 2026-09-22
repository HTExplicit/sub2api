package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) QuotaCostPoint(ctx context.Context, id int64) (service.QuotaCostPoint, error) {
	var point service.QuotaCostPoint
	err := scanSingleRow(ctx, r.sql, `SELECT COALESCE(MAX(id),0),COUNT(*),COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0)
		FROM usage_logs WHERE account_id=$1`, []any{id}, &point.Cursor, &point.Requests, &point.Cost)
	return point, err
}

func (r *accountRepository) ObserveQuotaEstimate(ctx context.Context, point service.QuotaEstimateObservation) error {
	_, err := r.sql.ExecContext(ctx, `INSERT INTO account_quota_estimates(account_id,identity_hash,period_key,resets_at,baseline_at,baseline_utilization,baseline_cost,observed_at,utilization,account_cost)
		VALUES($1,$2,$3,$4,$5,$6,$7,$5,$6,$7)
		ON CONFLICT(account_id,identity_hash,period_key) DO UPDATE SET
		baseline_at=CASE WHEN EXCLUDED.account_cost<account_quota_estimates.account_cost OR EXCLUDED.utilization<account_quota_estimates.utilization OR (EXCLUDED.utilization>account_quota_estimates.utilization AND EXCLUDED.account_cost<=account_quota_estimates.account_cost) THEN EXCLUDED.observed_at ELSE account_quota_estimates.baseline_at END,
		baseline_utilization=CASE WHEN EXCLUDED.account_cost<account_quota_estimates.account_cost OR EXCLUDED.utilization<account_quota_estimates.utilization OR (EXCLUDED.utilization>account_quota_estimates.utilization AND EXCLUDED.account_cost<=account_quota_estimates.account_cost) THEN EXCLUDED.utilization ELSE account_quota_estimates.baseline_utilization END,
		baseline_cost=CASE WHEN EXCLUDED.account_cost<account_quota_estimates.account_cost OR EXCLUDED.utilization<account_quota_estimates.utilization OR (EXCLUDED.utilization>account_quota_estimates.utilization AND EXCLUDED.account_cost<=account_quota_estimates.account_cost) THEN EXCLUDED.account_cost ELSE account_quota_estimates.baseline_cost END,
		observed_at=EXCLUDED.observed_at,utilization=EXCLUDED.utilization,account_cost=EXCLUDED.account_cost
		WHERE account_quota_estimates.observed_at<EXCLUDED.observed_at`, point.AccountID, point.Identity, point.Period, point.ResetsAt, point.ObservedAt, point.Utilization, point.Cost)
	return err
}

func (r *accountRepository) ReadQuotaEstimate(ctx context.Context, id int64, identity, period string) (*service.QuotaEstimateObservation, *service.QuotaEstimateObservation, error) {
	base := &service.QuotaEstimateObservation{AccountID: id, Identity: identity, Period: period}
	latest := &service.QuotaEstimateObservation{AccountID: id, Identity: identity, Period: period}
	err := scanSingleRow(ctx, r.sql, `SELECT resets_at,baseline_at,baseline_utilization,baseline_cost,observed_at,utilization,account_cost
		FROM account_quota_estimates WHERE account_id=$1 AND identity_hash=$2 AND period_key=$3`, []any{id, identity, period},
		&base.ResetsAt, &base.ObservedAt, &base.Utilization, &base.Cost, &latest.ObservedAt, &latest.Utilization, &latest.Cost)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	latest.ResetsAt = base.ResetsAt
	return base, latest, err
}
