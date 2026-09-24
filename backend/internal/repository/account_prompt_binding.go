package repository

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) UpdatePromptBindingIfRevision(ctx context.Context, id int64, expected time.Time, policyRevision int64, binding extensionv1.PromptAccountBinding) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Client().QueryContext(ctx, `SELECT r.revision, p.policy FROM system_prompt_runtime r JOIN system_prompt_rule_policies p ON p.id = r.id WHERE r.id = 1 FOR SHARE OF r`)
	if err != nil {
		return false, err
	}
	var revision int64
	var raw []byte
	if !rows.Next() {
		_ = rows.Close()
		return false, service.ErrBusinessSystemPromptUnavailable
	}
	err = rows.Scan(&revision, &raw)
	closeErr := rows.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if revision != policyRevision {
		return false, service.ErrBusinessSystemPromptRevisionConflict
	}
	var policy extensionv1.PromptRulePolicy
	if json.Unmarshal(raw, &policy) != nil {
		return false, service.ErrBusinessSystemPromptUnavailable
	}
	for _, ref := range binding.RuleIDs {
		if !slices.ContainsFunc(policy.Rules, func(rule extensionv1.PromptRule) bool { return rule.ID == ref }) {
			return false, service.ErrBusinessSystemPromptRevisionConflict
		}
	}
	rows, err = tx.Client().QueryContext(ctx, `SELECT updated_at FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`, id)
	if err != nil {
		return false, err
	}
	if !rows.Next() {
		_ = rows.Close()
		return false, service.ErrAccountNotFound
	}
	var updated time.Time
	err = rows.Scan(&updated)
	closeErr = rows.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if !updated.Equal(expected) {
		return false, nil
	}
	if err := r.UpdateExtra(dbent.NewTxContext(ctx, tx), id, map[string]any{service.PromptAccountBindingExtraKey: binding}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return true, nil
}
