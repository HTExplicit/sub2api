package repository

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// UpdateExtraIfRevision reuses the normal protected-field, outbox and cache
// path, while fencing an asynchronous metadata result against the input row.
func (r *accountRepository) UpdateExtraIfRevision(ctx context.Context, id int64, expected time.Time, updates map[string]any) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Client().QueryContext(ctx, `SELECT updated_at FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`, id)
	if err != nil {
		return false, err
	}
	if !rows.Next() {
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return false, rowErr
		}
		return false, service.ErrAccountNotFound
	}
	var current time.Time
	err = rows.Scan(&current)
	closeErr := rows.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if !current.Equal(expected) {
		return false, nil
	}
	if err := r.UpdateExtra(dbent.NewTxContext(ctx, tx), id, updates); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return true, nil
}
