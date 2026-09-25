package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	promptpolicy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"
)

// PromptContentDraft exists only in an edit/preview request. Published content
// remains an immutable version referenced by its rule.
type PromptContentDraft struct {
	Body string `json:"body"`
}

type PromptConfigUpdate struct {
	ExpectedRevision   int64                         `json:"expected_revision"`
	Enabled            bool                          `json:"enabled"`
	ExposeServerPrompt bool                          `json:"expose_server_prompt"`
	CompactEnabled     bool                          `json:"compact_enabled"`
	Policy             extensionv1.PromptRulePolicy  `json:"policy"`
	Contents           map[string]PromptContentDraft `json:"contents"`
}

type PromptConfigContent struct {
	Body            string `json:"body"`
	CompositionMode string `json:"composition_mode"`
	Managed         bool   `json:"managed"`
	TemplateID      int64  `json:"template_id"`
	VersionID       int64  `json:"version_id"`
	Available       bool   `json:"available"`
}

type PromptConfigState struct {
	Revision           int64                                  `json:"revision"`
	Enabled            bool                                   `json:"enabled"`
	ExposeServerPrompt bool                                   `json:"expose_server_prompt"`
	CompactEnabled     bool                                   `json:"compact_enabled"`
	Policy             extensionv1.PromptRulePolicy           `json:"policy"`
	Contents           map[string]PromptConfigContent         `json:"contents"`
	Capabilities       []extensionv1.PromptProtocolCapability `json:"capabilities"`
}

type PromptConfigStore interface {
	SavePromptConfig(context.Context, PromptConfigUpdate, int64) error
}

func (s *BusinessSystemPromptService) PublishVersionToRules(ctx context.Context, templateID, versionID, expectedRevision int64, ruleIDs []string, action string, actorID int64) (PromptConfigState, error) {
	if len(ruleIDs) == 0 || (action != "publish" && action != "rollback") {
		return PromptConfigState{}, fmt.Errorf("%w: select the rules to update", ErrBusinessSystemPromptInvalid)
	}
	state, err := s.PromptConfig(ctx)
	if err != nil {
		return PromptConfigState{}, err
	}
	if state.Revision != expectedRevision {
		return PromptConfigState{}, ErrBusinessSystemPromptRevisionConflict
	}
	state.Policy.Rules = slices.Clone(state.Policy.Rules)
	seen := make(map[string]bool, len(ruleIDs))
	for _, id := range ruleIDs {
		index := slices.IndexFunc(state.Policy.Rules, func(rule extensionv1.PromptRule) bool { return rule.ID == id })
		if index < 0 || seen[id] || state.Policy.Rules[index].TemplateID != templateID {
			return PromptConfigState{}, ErrBusinessSystemPromptInvalid
		}
		seen[id] = true
		state.Policy.Rules[index].VersionID = versionID
	}
	return s.SavePromptConfig(ctx, PromptConfigUpdate{
		ExpectedRevision: expectedRevision, Enabled: state.Enabled, ExposeServerPrompt: state.ExposeServerPrompt,
		CompactEnabled: state.CompactEnabled, Policy: state.Policy,
	}, actorID)
}

func supportsPromptAccount(account *Account) bool {
	return account != nil && slices.Contains(promptpolicy.PromptPlatforms(), account.Platform)
}

func (s *BusinessSystemPromptService) PromptConfig(ctx context.Context) (PromptConfigState, error) {
	snapshot, ok := s.CurrentSnapshot()
	if !ok || snapshot.RulePolicy == nil {
		return PromptConfigState{}, ErrBusinessSystemPromptUnavailable
	}
	state := PromptConfigState{
		Revision: snapshot.Revision, Enabled: snapshot.Enabled,
		ExposeServerPrompt: snapshot.ExposeServerPrompt, CompactEnabled: snapshot.CompactEnabled,
		Policy: *snapshot.RulePolicy, Contents: make(map[string]PromptConfigContent),
		Capabilities: promptpolicy.PromptProtocolCapabilities(),
	}
	details := make(map[int64]BusinessSystemPromptTemplateDetail)
	var publication *RemoteSkillPublication
	if s.registry != nil {
		publication = s.registry.publication.Load()
	}
	for _, rule := range state.Policy.Rules {
		detail, exists := details[rule.TemplateID]
		if !exists {
			var err error
			detail, err = s.store.GetBusinessSystemPromptTemplate(ctx, rule.TemplateID)
			if err != nil {
				return PromptConfigState{}, err
			}
			details[rule.TemplateID] = detail
		}
		index := slices.IndexFunc(detail.Versions, func(version BusinessSystemPromptVersion) bool { return version.ID == rule.VersionID })
		if index < 0 {
			return PromptConfigState{}, ErrBusinessSystemPromptVersionNotFound
		}
		version := detail.Versions[index]
		body := version.Body
		available := true
		if version.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
			body, available = "", false
			if publication != nil {
				body, available = publication.EffectivePromptBody, true
			}
		}
		state.Contents[rule.ID] = PromptConfigContent{
			Body: body, CompositionMode: version.CompositionMode,
			Managed:    detail.Template.ManagedSource != "" || version.CompositionMode != BusinessSystemPromptCompositionInline,
			TemplateID: rule.TemplateID, VersionID: rule.VersionID, Available: available,
		}
	}
	return state, nil
}

func (s *BusinessSystemPromptService) SavePromptConfig(ctx context.Context, input PromptConfigUpdate, actorID int64) (PromptConfigState, error) {
	store, ok := s.store.(PromptConfigStore)
	if !ok {
		return PromptConfigState{}, ErrBusinessSystemPromptUnavailable
	}
	current, ok := s.CurrentSnapshot()
	if !ok || current.RulePolicy == nil {
		return PromptConfigState{}, ErrBusinessSystemPromptUnavailable
	}
	if input.ExpectedRevision < 1 || current.Revision != input.ExpectedRevision {
		return PromptConfigState{}, ErrBusinessSystemPromptRevisionConflict
	}
	current.Enabled, current.ExposeServerPrompt, current.CompactEnabled = input.Enabled, input.ExposeServerPrompt, input.CompactEnabled
	current.RulePolicy = &input.Policy
	if err := s.preparePromptRulesDraft(ctx, &current, input.Contents); err != nil {
		return PromptConfigState{}, err
	}
	input.Policy = *current.RulePolicy
	if err := store.SavePromptConfig(ctx, input, actorID); err != nil {
		return PromptConfigState{}, err
	}
	if err := s.Reload(ctx); err != nil {
		return PromptConfigState{}, err
	}
	state, err := s.PromptConfig(ctx)
	if err == nil && s.bus != nil {
		if publishErr := s.bus.Publish(ctx, state.Revision); publishErr != nil {
			_ = s.retainLastGood(publishErr)
		}
	}
	return state, err
}

// Draft compilation is also used by preview, so unsaved text follows exactly
// the same content validation and source resolution as a published rule.
func (s *BusinessSystemPromptService) preparePromptRulesDraft(ctx context.Context, snapshot *BusinessSystemPromptSnapshot, drafts map[string]PromptContentDraft) error {
	return s.compilePromptRules(ctx, snapshot, drafts, true)
}

func (s *BusinessSystemPromptService) compilePromptRules(ctx context.Context, snapshot *BusinessSystemPromptSnapshot, drafts map[string]PromptContentDraft, strictSources bool) error {
	if snapshot.RulePolicy == nil {
		return ErrBusinessSystemPromptUnavailable
	}
	draftIDs := make([]string, 0, len(drafts))
	for id := range drafts {
		if !slices.ContainsFunc(snapshot.RulePolicy.Rules, func(rule extensionv1.PromptRule) bool { return rule.ID == id }) {
			return fmt.Errorf("%w: content references an unknown rule", ErrBusinessSystemPromptInvalid)
		}
		draftIDs = append(draftIDs, id)
	}
	policy, err := promptpolicy.ValidateRulePolicyDraft(*snapshot.RulePolicy, draftIDs)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBusinessSystemPromptInvalid, err)
	}
	snapshot.RulePolicy = &policy
	snapshot.Draft = len(drafts) > 0
	snapshot.ResolvedRules = nil
	snapshot.RegistryRevision = 0
	// The publication is immutable. Capture it once before resolving any rule
	// so a concurrent source publish cannot mix generations in one request.
	var publication *RemoteSkillPublication
	registryDegraded := false
	if s.registry != nil {
		publication = s.registry.publication.Load()
		registryDegraded = s.registry.CurrentSnapshot().Degraded
	}
	for _, rule := range policy.Rules {
		var content BusinessSystemPromptSnapshot
		if draft, edited := drafts[rule.ID]; edited {
			if rule.TemplateID > 0 {
				detail, err := s.store.GetBusinessSystemPromptTemplate(ctx, rule.TemplateID)
				if err != nil {
					return err
				}
				index := slices.IndexFunc(detail.Versions, func(version BusinessSystemPromptVersion) bool { return version.ID == rule.VersionID })
				if index < 0 {
					return ErrBusinessSystemPromptVersionNotFound
				}
				if detail.Template.ManagedSource != "" || detail.Versions[index].CompositionMode != BusinessSystemPromptCompositionInline {
					return ErrBusinessSystemPromptSourceNotManaged
				}
			}
			hash, size, err := ValidateBusinessSystemPromptBody(draft.Body)
			if err != nil {
				return err
			}
			content = BusinessSystemPromptSnapshot{Enabled: true, Body: draft.Body, SHA256: hash, ByteLength: size, CompositionMode: BusinessSystemPromptCompositionInline}
		} else {
			detail, err := s.store.GetBusinessSystemPromptTemplate(ctx, rule.TemplateID)
			if err != nil {
				return err
			}
			index := slices.IndexFunc(detail.Versions, func(version BusinessSystemPromptVersion) bool { return version.ID == rule.VersionID })
			if index < 0 {
				return ErrBusinessSystemPromptVersionNotFound
			}
			version := detail.Versions[index]
			content = BusinessSystemPromptSnapshot{Enabled: true, Revision: snapshot.Revision, TemplateID: rule.TemplateID, VersionID: rule.VersionID, Body: version.Body, SHA256: version.SHA256, ByteLength: version.ByteLength, CompositionMode: version.CompositionMode, BundleID: version.BundleID, BundleManifestSHA256: version.BundleManifestSHA256}
			if content.CompositionMode == extensionv1.PromptContentAnthropicSystemBlocks && !validStructuredPromptScope(rule) {
				return fmt.Errorf("%w: structured Claude OAuth sources require their native control carrier and request profile", ErrPromptDeliveryUnsupported)
			}
			if !rule.Enabled {
				continue
			}
			if content.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid && !snapshot.Enabled {
				// Turning off prompt serving must work even while its optional
				// source is unavailable. Enabling or simulating compiles it again.
				continue
			}
			if content.CompositionMode == extensionv1.PromptContentAnthropicSystemBlocks {
				if _, err := parseClaudeOAuthSystemPromptBlocksConfig(content.Body); err != nil {
					return fmt.Errorf("%w: invalid system blocks", ErrBusinessSystemPromptInvalid)
				}
			} else if content.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
				content, err = compilePromptPublication(content, publication)
				if err != nil && !strictSources {
					snapshot.Degraded = true
					snapshot.ResolvedRules = append(snapshot.ResolvedRules, extensionv1.ResolvedPromptRule{Rule: rule, Unavailable: true})
					continue
				}
				if err != nil {
					return err
				}
				snapshot.RegistryRevision = publication.Revision
				snapshot.BundleDegraded = registryDegraded
				snapshot.Degraded = snapshot.Degraded || registryDegraded
			} else {
				err = s.prepareBusinessSystemPromptSnapshot(&content)
				if err == nil {
					content, err = s.compileBusinessSystemPromptSnapshot(content)
				}
				if err != nil {
					return err
				}
			}
		}
		hash, _, err := validateBusinessSystemPromptBodyWithLimit(content.Body, extensionv1.PromptRulesMaxBytes)
		if err != nil {
			return err
		}
		if !rule.Enabled {
			continue
		}
		resolved := extensionv1.ResolvedPromptRule{Rule: rule, Body: content.Body, SHA256: hash, PreserveEcho: content.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid}
		if content.CompositionMode == extensionv1.PromptContentAnthropicSystemBlocks {
			resolved.ContentFormat = content.CompositionMode
			resolved.StructuredContent = json.RawMessage(content.Body)
		}
		snapshot.ResolvedRules = append(snapshot.ResolvedRules, resolved)
	}
	return nil
}

func validStructuredPromptScope(rule extensionv1.PromptRule) bool {
	if (rule.Role != "auto" && rule.Role != "system") || (rule.Position != "control_prepend" && rule.Position != "control_append") ||
		len(rule.Platforms) != 1 || rule.Platforms[0] != PlatformAnthropic || len(rule.AccountTypes) == 0 ||
		len(rule.RequestProfiles) != 1 || rule.RequestProfiles[0] != "generic-mimic" {
		return false
	}
	for _, accountType := range rule.AccountTypes {
		if accountType != AccountTypeOAuth && accountType != AccountTypeSetupToken {
			return false
		}
	}
	return true
}
