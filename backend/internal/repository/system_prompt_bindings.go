package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type systemPromptBindingStats struct {
	db *sql.DB
}

// NewSystemPromptBindingStats counts accounts by their extra.system_prompt
// binding for the admin page. Request paths never call it.
func NewSystemPromptBindingStats(db *sql.DB) service.SystemPromptBindingStats {
	return &systemPromptBindingStats{db: db}
}

func (r *systemPromptBindingStats) CountSystemPromptBindings(ctx context.Context) ([]service.SystemPromptBindingCount, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("system prompt binding statistics are unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(extra->'system_prompt'->>'mode', ''),
		       COALESCE(extra->'system_prompt'->>'prompt_id', ''),
		       COUNT(*)
		FROM accounts
		WHERE deleted_at IS NULL
		GROUP BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var counts []service.SystemPromptBindingCount
	for rows.Next() {
		var count service.SystemPromptBindingCount
		if err := rows.Scan(&count.Mode, &count.PromptID, &count.Accounts); err != nil {
			return nil, err
		}
		counts = append(counts, count)
	}
	return counts, rows.Err()
}
