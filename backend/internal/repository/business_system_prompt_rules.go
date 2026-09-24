package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *businessSystemPromptRepository) LoadBusinessSystemPromptRules(ctx context.Context) (service.BusinessSystemPromptSnapshot, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	snapshot, err := loadBusinessSystemPromptSnapshot(ctx, tx)
	if err != nil {
		return snapshot, err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT policy FROM system_prompt_rule_policies WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, tx.Commit()
	}
	if err != nil {
		return snapshot, err
	}
	var policy extensionv1.PromptRulePolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return snapshot, err
	}
	snapshot.RulePolicy = &policy
	return snapshot, tx.Commit()
}

func (r *businessSystemPromptRepository) UpdateBusinessSystemPromptRules(ctx context.Context, policy extensionv1.PromptRulePolicy, expectedRevision, actorID int64) error {
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE`).Scan(&revision); err != nil {
		return err
	}
	if revision != expectedRevision {
		return service.ErrBusinessSystemPromptRevisionConflict
	}
	for _, rule := range policy.Rules {
		if rule.FollowActive {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM system_prompt_template_versions v
            JOIN system_prompt_templates t ON t.id = v.template_id
            WHERE v.id = $1 AND t.id = $2 AND t.deleted_at IS NULL)`, rule.VersionID, rule.TemplateID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return service.ErrBusinessSystemPromptVersionNotFound
		}
	}
	// A rule cannot disappear while an account references it. Off/disabled rules
	// remain editable and visibly disabled without silently changing account intent.
	ids := make([]string, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		ids = append(ids, rule.ID)
	}
	idsJSON, _ := json.Marshal(ids)
	var dangling bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
        SELECT 1 FROM accounts a, jsonb_array_elements_text(
            CASE WHEN jsonb_typeof(a.extra->'prompt_skills'->'rule_ids') = 'array'
                 THEN a.extra->'prompt_skills'->'rule_ids' ELSE '[]'::jsonb END) ref
        WHERE a.deleted_at IS NULL AND a.extra->'prompt_skills'->>'mode' = 'custom'
          AND NOT ($1::jsonb ? ref.value))`, string(idsJSON)).Scan(&dangling); err != nil {
		return err
	}
	if dangling {
		return service.ErrPromptRuleReferenced
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_rule_policies (id, policy) VALUES (1, $1::jsonb)
        ON CONFLICT (id) DO UPDATE SET policy = EXCLUDED.policy`, string(raw)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_runtime SET revision = revision + 1, updated_by = $1, updated_at = NOW() WHERE id = 1`, actorID); err != nil {
		return err
	}
	return tx.Commit()
}
