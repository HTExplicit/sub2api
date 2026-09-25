package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Wei-Shaw/sub2api/internal/nativeapi"
	promptpolicy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SavePromptConfig serializes both content edits and rule selection against the
// same runtime row used by account bindings. A reader therefore sees either the
// entire previous configuration or the entire new configuration.
func (r *businessSystemPromptRepository) SavePromptConfig(ctx context.Context, input service.PromptConfigUpdate, actorID int64) error {
	if r == nil || r.db == nil {
		return service.ErrBusinessSystemPromptUnavailable
	}
	if input.Policy.Version != nativeapi.PromptRulePolicyVersion || len(input.Policy.Rules) > nativeapi.PromptRulesMaxCount {
		return service.ErrBusinessSystemPromptInvalid
	}
	policy := input.Policy
	policy.Rules = append([]nativeapi.PromptRule{}, input.Policy.Rules...)
	draftIDs := make([]string, 0, len(input.Contents))
	for id := range input.Contents {
		draftIDs = append(draftIDs, id)
	}
	var err error
	policy, err = promptpolicy.ValidateRulePolicyDraft(policy, draftIDs)
	if err != nil {
		return fmt.Errorf("%w: %v", service.ErrBusinessSystemPromptInvalid, err)
	}
	seen := make(map[string]bool, len(policy.Rules))
	for _, rule := range policy.Rules {
		if rule.ID == "" || seen[rule.ID] || rule.FollowActive || rule.Delivery != "" {
			return service.ErrBusinessSystemPromptInvalid
		}
		seen[rule.ID] = true
	}
	for id, draft := range input.Contents {
		if !seen[id] {
			return fmt.Errorf("%w: content refers to an unknown rule", service.ErrBusinessSystemPromptInvalid)
		}
		if _, _, err := service.ValidateBusinessSystemPromptBody(draft.Body); err != nil {
			return err
		}
	}
	for _, id := range policy.DefaultRuleIDs {
		if !seen[id] {
			return fmt.Errorf("%w: default refers to an unknown rule", service.ErrBusinessSystemPromptInvalid)
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, input.ExpectedRevision); err != nil {
		return err
	}
	var previousRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT policy FROM system_prompt_rule_policies WHERE id = 1`).Scan(&previousRaw); err != nil {
		return err
	}
	var previous nativeapi.PromptRulePolicy
	if err := json.Unmarshal(previousRaw, &previous); err != nil || previous.Version != nativeapi.PromptRulePolicyVersion {
		return service.ErrBusinessSystemPromptUnavailable
	}
	previousRules := make(map[string]nativeapi.PromptRule, len(previous.Rules))
	for _, rule := range previous.Rules {
		previousRules[rule.ID] = rule
	}
	if err := checkPromptConfigAccountReferences(ctx, tx, policy); err != nil {
		return err
	}

	for i := range policy.Rules {
		rule := &policy.Rules[i]
		draft, hasDraft := input.Contents[rule.ID]
		old, exists := previousRules[rule.ID]
		if exists && old.TemplateID != rule.TemplateID {
			return fmt.Errorf("%w: an existing rule cannot change its content source", service.ErrBusinessSystemPromptInvalid)
		}
		if !exists && (rule.TemplateID != 0 || rule.VersionID != 0 || !hasDraft) {
			return fmt.Errorf("%w: a new rule requires independent content", service.ErrBusinessSystemPromptInvalid)
		}
		var version promptConfigStoredVersion
		if rule.TemplateID == 0 && rule.VersionID == 0 && hasDraft {
			if err := tx.QueryRowContext(ctx, `INSERT INTO system_prompt_templates (slug, name, created_by, updated_by)
				VALUES ($1, $2, $3, $3) RETURNING id`, "prompt-"+uuid.NewString(), strings.TrimSpace(rule.Name), nullableActor(actorID)).Scan(&rule.TemplateID); err != nil {
				return translateBusinessSystemPromptWriteError(err)
			}
			version = promptConfigStoredVersion{compositionMode: service.BusinessSystemPromptCompositionInline}
		} else {
			if rule.TemplateID < 1 || rule.VersionID < 1 {
				return service.ErrBusinessSystemPromptVersionNotFound
			}
			version, err = loadPromptConfigVersion(ctx, tx, rule.TemplateID, rule.VersionID)
			if err != nil {
				return err
			}
			if err := validateStoredBusinessSystemPromptVersion(version.body, version.sha256, version.byteLength,
				version.compositionMode, version.bundleID, version.bundleManifestSHA256); err != nil {
				return err
			}
			if version.managedSource != "" || version.compositionMode == service.BusinessSystemPromptCompositionCodexSkillHybrid {
				return fmt.Errorf("%w: managed prompt content requires an independent copy", service.ErrBusinessSystemPromptSourceNotManaged)
			}
		}
		if hasDraft {
			preserveEcho := false
			if draft.RestoreVersionID > 0 {
				preserveEcho, err = validatePromptHistoryRestore(ctx, tx, rule.ID, rule.TemplateID, version.compositionMode, draft.RestoreVersionID, draft.Body)
				if err != nil {
					return err
				}
			} else if version.compositionMode == nativeapi.PromptContentAnthropicSystemBlocks {
				if err := service.ValidateStructuredPromptTextEdit(version.body, draft.Body); err != nil {
					return err
				}
			}
			version.body = draft.Body
			version.sha256, version.byteLength, err = service.ValidateBusinessSystemPromptBody(draft.Body)
			if err != nil {
				return err
			}
			var latest int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM system_prompt_template_versions WHERE template_id = $1`, rule.TemplateID).Scan(&latest); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `INSERT INTO system_prompt_template_versions
				(template_id, version, body, sha256, byte_length, composition_mode, bundle_id, bundle_manifest_sha256, note, created_by, published_at, published_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '', $9, NOW(), $9) RETURNING id`,
				rule.TemplateID, latest+1, version.body, version.sha256, version.byteLength, version.compositionMode,
				nullableString(version.bundleID), nullableString(version.bundleManifestSHA256), nullableActor(actorID)).Scan(&rule.VersionID); err != nil {
				return translateBusinessSystemPromptWriteError(err)
			}
			if preserveEcho {
				if _, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_version_compat (version_id, preserve_echo) VALUES ($1, TRUE)`, rule.VersionID); err != nil {
					return err
				}
			}
		}
		if !hasDraft {
			if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_template_versions
				SET published_at = COALESCE(published_at, NOW()), published_by = $3
				WHERE id = $1 AND template_id = $2`, rule.VersionID, rule.TemplateID, nullableActor(actorID)); err != nil {
				return err
			}
		}
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_rule_policies SET policy = $1::jsonb WHERE id = 1`, string(raw)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_runtime
		SET enabled = $1, expose_server_prompt = $2, compact_enabled = $3,
		    revision = revision + 1, updated_by = $4, updated_at = NOW() WHERE id = 1`,
		input.Enabled, input.ExposeServerPrompt, input.CompactEnabled, nullableActor(actorID)); err != nil {
		return err
	}
	return tx.Commit()
}

type promptConfigStoredVersion struct {
	managedSource        string
	body                 string
	sha256               string
	byteLength           int
	compositionMode      string
	bundleID             string
	bundleManifestSHA256 string
}

func loadPromptConfigVersion(ctx context.Context, tx *sql.Tx, templateID, versionID int64) (promptConfigStoredVersion, error) {
	var version promptConfigStoredVersion
	var managedSource, bundleID, bundleManifestSHA256 sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT t.managed_source, v.body, v.sha256, v.byte_length,
		v.composition_mode, v.bundle_id, v.bundle_manifest_sha256
		FROM system_prompt_templates t JOIN system_prompt_template_versions v ON v.template_id = t.id
		WHERE t.id = $1 AND v.id = $2 AND t.deleted_at IS NULL FOR UPDATE OF t`, templateID, versionID).Scan(
		&managedSource, &version.body, &version.sha256, &version.byteLength,
		&version.compositionMode, &bundleID, &bundleManifestSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return version, service.ErrBusinessSystemPromptVersionNotFound
	}
	version.managedSource = nullableStringValue(managedSource)
	version.bundleID = nullableStringValue(bundleID)
	version.bundleManifestSHA256 = nullableStringValue(bundleManifestSHA256)
	return version, err
}

func checkPromptConfigAccountReferences(ctx context.Context, tx *sql.Tx, policy nativeapi.PromptRulePolicy) error {
	ids := make([]string, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		ids = append(ids, rule.ID)
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return err
	}
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
	return nil
}
