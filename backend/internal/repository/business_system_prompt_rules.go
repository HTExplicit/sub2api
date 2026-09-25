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
	var snapshot service.BusinessSystemPromptSnapshot
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT r.enabled, r.expose_server_prompt, r.compact_enabled,
        r.revision, r.updated_at, p.policy
        FROM system_prompt_runtime r JOIN system_prompt_rule_policies p ON p.id = r.id
        WHERE r.id = 1`).Scan(&snapshot.Enabled, &snapshot.ExposeServerPrompt, &snapshot.CompactEnabled,
		&snapshot.Revision, &snapshot.UpdatedAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		snapshot, err = loadBusinessSystemPromptSnapshot(ctx, tx)
		if err != nil {
			return snapshot, err
		}
		return snapshot, tx.Commit()
	}
	if err != nil {
		return snapshot, err
	}
	var policy extensionv1.PromptRulePolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return snapshot, err
	}
	if policy.Version < extensionv1.PromptRulePolicyVersion {
		// Only the legacy import path reads the former global selection. V2
		// content is resolved exclusively from each rule's immutable version.
		snapshot, err = loadBusinessSystemPromptSnapshot(ctx, tx)
		if err != nil {
			return snapshot, err
		}
	}
	snapshot.RulePolicy = &policy
	return snapshot, tx.Commit()
}

func (r *businessSystemPromptRepository) UpdateBusinessSystemPromptRules(ctx context.Context, policy extensionv1.PromptRulePolicy, expectedRevision, actorID int64) error {
	if policy.Version != extensionv1.PromptRulePolicyVersion {
		return service.ErrBusinessSystemPromptInvalid
	}
	current, err := r.LoadBusinessSystemPromptRules(ctx)
	if err != nil {
		return err
	}
	return r.SavePromptConfig(ctx, service.PromptConfigUpdate{
		ExpectedRevision: expectedRevision, Enabled: current.Enabled,
		ExposeServerPrompt: current.ExposeServerPrompt, CompactEnabled: current.CompactEnabled,
		Policy: policy,
	}, actorID)
}
