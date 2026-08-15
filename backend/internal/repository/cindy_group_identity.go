package repository

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// classifyStrictCindyGroupWithSQL evaluates complete active membership with a
// single aggregate query. COUNT/SUM(CASE) is supported by both PostgreSQL and
// the SQLite repository tests, while retaining the exact structured identity
// boundary used by service.IsCindyAPIKeyAccount.
func classifyStrictCindyGroupWithSQL(ctx context.Context, executor sqlExecutor, groupID int64) (bool, error) {
	if executor == nil {
		return false, errors.New("account repository SQL executor is unavailable")
	}
	rows, err := executor.QueryContext(ctx, `
		SELECT
			COUNT(*) AS active_account_count,
			COALESCE(SUM(CASE WHEN
				a.platform = $2
				AND a.type = $3
				AND LOWER(TRIM(a.credentials ->> 'base_url')) IN ($4, $5)
			THEN 1 ELSE 0 END), 0) AS cindy_account_count
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = $1
			AND a.deleted_at IS NULL
			AND a.status = $6
	`,
		groupID,
		service.PlatformOpenAI,
		service.AccountTypeAPIKey,
		"https://api.laxarouter.ai",
		"https://api.laxarouter.ai/",
		service.StatusActive,
	)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, errors.New("cindy group identity query returned no row")
	}
	var activeAccountCount, cindyAccountCount int64
	if err := rows.Scan(&activeAccountCount, &cindyAccountCount); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return activeAccountCount > 0 && activeAccountCount == cindyAccountCount, nil
}
