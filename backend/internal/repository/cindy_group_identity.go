package repository

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// classifyStrictCindyGroupWithSQL evaluates complete non-deleted membership with a
// single aggregate query. COUNT/SUM(CASE) is supported by both PostgreSQL and
// the SQLite repository tests, while retaining the exact structured identity
// boundary used by service.IsCindyAPIKeyAccount.
func classifyStrictCindyGroupWithSQL(ctx context.Context, executor sqlExecutor, groupID int64) (bool, error) {
	if executor == nil {
		return false, errors.New("account repository SQL executor is unavailable")
	}
	rows, err := executor.QueryContext(ctx, `
		SELECT
			COUNT(*) AS account_count,
			COALESCE(SUM(CASE WHEN
				a.platform = $3
				AND a.type = $4
				AND LOWER(TRIM(a.credentials ->> 'base_url')) IN ($5, $6)
			THEN 1 ELSE 0 END), 0) AS cindy_account_count
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		JOIN groups g ON g.id = ag.group_id
		WHERE ag.group_id = $1
			AND g.platform = $2
			AND g.deleted_at IS NULL
			AND a.deleted_at IS NULL
	`,
		groupID,
		service.PlatformOpenAI,
		service.PlatformOpenAI,
		service.AccountTypeAPIKey,
		"https://api.laxarouter.ai",
		"https://api.laxarouter.ai/",
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
	var accountCount, cindyAccountCount int64
	if err := rows.Scan(&accountCount, &cindyAccountCount); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return accountCount > 0 && accountCount == cindyAccountCount, nil
}
