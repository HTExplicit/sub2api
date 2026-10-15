package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.PelicanTaskDeleter = (*pelicanTestRepository)(nil)

func (r *pelicanTestRepository) DeleteTask(ctx context.Context, id string) error {
	return r.deletePelican(ctx, id, "")
}

func (r *pelicanTestRepository) DeleteTaskResult(ctx context.Context, taskID, resultID string) error {
	return r.deletePelican(ctx, taskID, resultID)
}

func (r *pelicanTestRepository) deletePelican(ctx context.Context, taskID, resultID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var deleted sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT status,deleted_at FROM codex_gateway_borrow_test_tasks
 WHERE scope='manual_pelican' AND id=$1 AND expires_at>CURRENT_TIMESTAMP FOR UPDATE`, taskID).Scan(&status, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrPelicanTestNotFound
	}
	if err != nil {
		return err
	}
	if resultID != "" {
		var elsewhere bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM codex_gateway_borrow_test_results WHERE id=$2 AND task_id<>$1)`, taskID, resultID).Scan(&elsewhere); err != nil {
			return err
		}
		if elsewhere {
			return service.ErrPelicanTestNotFound
		}
	}
	if deleted.Valid {
		return nil
	}
	if status == "pending" || status == "running" {
		return service.ErrPelicanTestRunning
	}
	if resultID != "" {
		var resultStatus string
		err = tx.QueryRowContext(ctx, `DELETE FROM codex_gateway_borrow_test_results
 WHERE task_id=$1 AND id=$2 RETURNING status`, taskID, resultID).Scan(&resultStatus)
		if errors.Is(err, sql.ErrNoRows) {
			// Repeated deletes of an absent, correctly scoped result are harmless.
			return nil
		}
		if err != nil {
			return err
		}
		var total, completed int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE status NOT IN ('pending','running'))
 FROM codex_gateway_borrow_test_results WHERE task_id=$1`, taskID).Scan(&total, &completed); err != nil {
			return err
		}
		if total > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_tasks
 SET total=$2,completed=$3,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, taskID, total, completed); err != nil {
				return err
			}
			return tx.Commit()
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM codex_gateway_borrow_test_results WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_tasks
 SET deleted_at=CURRENT_TIMESTAMP,error=$2,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, taskID, []byte{}); err != nil {
		return err
	}
	return tx.Commit()
}
