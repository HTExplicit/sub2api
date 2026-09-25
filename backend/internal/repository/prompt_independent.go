package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

// The runtime row serializes this one-time upgrade with config/account writes.
// All source rows are retained. Reading disk happens before any write, while
// the paired registry runtime is locked, so neither half can change generation.
func (r *businessSystemPromptRepository) InitializeIndependentPrompts(ctx context.Context, files service.RemoteSkillRegistryFiles) (map[string][]byte, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrBusinessSystemPromptUnavailable
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE`).Scan(&revision); err != nil {
		return nil, err
	}
	var complete bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM system_prompt_independent_state WHERE id = 1)`).Scan(&complete); err != nil {
		return nil, err
	}
	if complete {
		frozen, err := readFrozenPromptFiles(ctx, tx)
		if err != nil {
			return nil, err
		}
		return frozen, tx.Commit()
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT policy FROM system_prompt_rule_policies WHERE id = 1 FOR UPDATE`).Scan(&raw); err != nil {
		return nil, err
	}
	var policy nativeapi.PromptRulePolicy
	if err := json.Unmarshal(raw, &policy); err != nil || policy.Version != nativeapi.PromptRulePolicyVersion {
		return nil, service.ErrBusinessSystemPromptUnavailable
	}
	var registryRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE`).Scan(&registryRevision); err != nil {
		return nil, err
	}
	registry, err := loadRemoteSkillRegistrySnapshot(ctx, tx)
	if err != nil {
		return nil, err
	}
	frozen := make(map[string][]byte)
	var publication service.RemoteSkillPublication
	if registry.Active != nil {
		if registry.ActivePrompt == nil || files == nil {
			return nil, service.ErrBusinessSystemPromptBundleUnavailable
		}
		detail, err := getRemoteSkillVersionDetail(ctx, tx, registry.Active.ID, 0)
		if err != nil {
			return nil, err
		}
		if detail.Prompt.ID != registry.ActivePrompt.ID {
			return nil, service.ErrBusinessSystemPromptBundleInvalid
		}
		candidate, err := files.LoadCandidate(ctx, detail.RemoteSkillBundleVersion, detail.Prompt, detail.FileChanges)
		if err != nil {
			return nil, fmt.Errorf("%w: active skill files incomplete", service.ErrBusinessSystemPromptBundleUnavailable)
		}
		publication, err = service.ValidateFrozenSkillPublication(registryRevision, candidate)
		if err != nil {
			return nil, err
		}
		frozen = publication.Files
	}
	// Prepare every rule first. A missing disabled/custom-bound source is also
	// fatal: migration cannot silently replace its future behavior with a seed.
	type independentContent struct {
		ruleIndex   int
		body, mode  string
		oldTemplate int64
		echo        bool
	}
	contents := make([]independentContent, 0, len(policy.Rules))
	for i, rule := range policy.Rules {
		version, err := loadPromptConfigVersion(ctx, tx, rule.TemplateID, rule.VersionID)
		if err != nil {
			return nil, err
		}
		if err := validateStoredBusinessSystemPromptVersion(version.body, version.sha256, version.byteLength, version.compositionMode, version.bundleID, version.bundleManifestSHA256); err != nil {
			return nil, err
		}
		content := independentContent{ruleIndex: i, body: version.body, mode: version.compositionMode, oldTemplate: rule.TemplateID}
		if version.compositionMode == service.BusinessSystemPromptCompositionCodexSkillHybrid {
			if publication.EffectivePromptBody == "" || len(frozen) == 0 {
				return nil, service.ErrBusinessSystemPromptBundleUnavailable
			}
			content.body, content.mode, content.echo = publication.EffectivePromptBody, service.BusinessSystemPromptCompositionInline, true
		}
		if _, _, err := service.ValidateBusinessSystemPromptBody(content.body); err != nil {
			return nil, err
		}
		if content.mode == nativeapi.PromptContentAnthropicSystemBlocks {
			if err := service.ValidateStructuredPromptTextEdit(content.body, content.body); err != nil {
				return nil, err
			}
		}
		contents = append(contents, content)
	}
	for _, content := range contents {
		rule := &policy.Rules[content.ruleIndex]
		if err := tx.QueryRowContext(ctx, `INSERT INTO system_prompt_templates (slug, name) VALUES ($1, $2) RETURNING id`, "prompt-"+uuid.NewString(), rule.Name).Scan(&rule.TemplateID); err != nil {
			return nil, err
		}
		hash, length, _ := service.ValidateBusinessSystemPromptBody(content.body)
		if err := tx.QueryRowContext(ctx, `INSERT INTO system_prompt_template_versions
			(template_id, version, body, sha256, byte_length, composition_mode, note, published_at)
			VALUES ($1, 1, $2, $3, $4, $5, 'Independent content migration', NOW()) RETURNING id`, rule.TemplateID, content.body, hash, length, content.mode).Scan(&rule.VersionID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_independent_contents (template_id, rule_id, source_template_id, baseline_version_id) VALUES ($1, $2, $3, $4)`, rule.TemplateID, rule.ID, content.oldTemplate, rule.VersionID); err != nil {
			return nil, err
		}
		if content.echo {
			if _, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_version_compat (version_id, preserve_echo) VALUES ($1, TRUE)`, rule.VersionID); err != nil {
				return nil, err
			}
		}
	}
	paths := make([]string, 0, len(frozen))
	for path := range frozen {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		body := frozen[path]
		if _, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_frozen_public_files (path, body, sha256) VALUES ($1, $2, encode(sha256($2::bytea), 'hex'))`, path, body); err != nil {
			return nil, err
		}
	}
	raw, err = json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_rule_policies SET policy = $1::jsonb WHERE id = 1`, string(raw)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_runtime SET revision = revision + 1, updated_at = NOW() WHERE id = 1`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_independent_state (id) VALUES (1)`); err != nil {
		return nil, err
	}
	return frozen, tx.Commit()
}

func readFrozenPromptFiles(ctx context.Context, tx *sql.Tx) (map[string][]byte, error) {
	rows, err := tx.QueryContext(ctx, `SELECT path, body, sha256 = encode(sha256(body), 'hex') FROM system_prompt_frozen_public_files ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	files := make(map[string][]byte)
	for rows.Next() {
		var path string
		var body []byte
		var valid bool
		if err := rows.Scan(&path, &body, &valid); err != nil {
			return nil, err
		}
		if !valid {
			return nil, service.ErrBusinessSystemPromptBundleInvalid
		}
		files[path] = body
	}
	return files, rows.Err()
}

func (r *businessSystemPromptRepository) PromptVersionPreserveEcho(ctx context.Context, versionID int64) (bool, error) {
	var preserve bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM system_prompt_version_compat WHERE version_id = $1 AND preserve_echo)`, versionID).Scan(&preserve)
	return preserve, err
}

func (r *businessSystemPromptRepository) PromptRuleHistory(ctx context.Context, ruleID string) ([]service.PromptHistoryVersion, error) {
	rows, err := r.db.QueryContext(ctx, `WITH current_rule AS (
		SELECT (rule->>'template_id')::bigint template_id, (rule->>'version_id')::bigint version_id
		FROM system_prompt_rule_policies, jsonb_array_elements(policy->'rules') rule WHERE id = 1 AND rule->>'id' = $1
	) SELECT v.id, v.body, v.composition_mode, v.created_at,
		(v.composition_mode IN ('inline', 'anthropic_system_blocks') AND v.composition_mode = current_version.composition_mode) restorable
	FROM current_rule r JOIN system_prompt_template_versions current_version ON current_version.id = r.version_id
	LEFT JOIN system_prompt_independent_contents origin ON origin.template_id = r.template_id AND origin.rule_id = $1
	JOIN system_prompt_template_versions v ON v.template_id = r.template_id OR v.template_id = origin.source_template_id
	ORDER BY v.created_at DESC, v.id DESC`, ruleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	versions := make([]service.PromptHistoryVersion, 0)
	for rows.Next() {
		var version service.PromptHistoryVersion
		if err := rows.Scan(&version.ID, &version.Body, &version.CompositionMode, &version.CreatedAt, &version.Restorable); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// Restoring is content-only and authorized by stable rule identity. Historical
// hybrid archives have no frozen-equivalent tree and cannot be restored.
func validatePromptHistoryRestore(ctx context.Context, tx *sql.Tx, ruleID string, templateID int64, mode string, versionID int64, body string) (bool, error) {
	var historicalBody string
	var historicalEcho bool
	err := tx.QueryRowContext(ctx, `SELECT v.body, COALESCE(compat.preserve_echo, FALSE) FROM system_prompt_template_versions v
		LEFT JOIN system_prompt_version_compat compat ON compat.version_id = v.id
		WHERE v.id = $1 AND v.composition_mode = $2
		AND v.composition_mode IN ('inline', 'anthropic_system_blocks')
		AND (v.template_id = $3 OR EXISTS (
			SELECT 1 FROM system_prompt_independent_contents origin
			WHERE origin.rule_id = $4 AND origin.template_id = $3 AND origin.source_template_id = v.template_id))`, versionID, mode, templateID, ruleID).Scan(&historicalBody, &historicalEcho)
	if err == sql.ErrNoRows {
		return false, service.ErrBusinessSystemPromptVersionNotFound
	}
	if err != nil {
		return false, err
	}
	if mode == nativeapi.PromptContentAnthropicSystemBlocks {
		if err := service.ValidateStructuredPromptTextEdit(historicalBody, body); err != nil {
			return false, err
		}
	}
	return service.PreserveRestoredPromptEcho(historicalBody, body, historicalEcho), nil
}
