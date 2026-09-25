package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
)

const businessSystemPromptRequestProfileKey = "business_system_prompt_request_profile"

const businessSystemPromptStrictChatRoleKey = "business_system_prompt_strict_chat_role"
const anthropicPromptAttemptKey = "business_system_prompt_anthropic_attempt"

type compiledAnthropicPromptSource struct {
	blocks json.RawMessage
	hash   string
}

// Only the bounded server-owned sources are retained here. The user's request
// remains owned by the gateway's clean body and is never duplicated in context.
type anthropicPromptAttempt struct {
	accountID int64
	model     string
	binding   string
	sources   map[string]compiledAnthropicPromptSource
}

func setBusinessSystemPromptRequestProfile(c *gin.Context, account *Account, mimic bool) {
	profile := ""
	if account != nil && account.IsAnthropicOAuthOrSetupToken() {
		profile = "claude-code"
		if mimic {
			profile = "generic-mimic"
		}
	}
	businessSystemPromptRequestSet(c, businessSystemPromptRequestProfileKey, profile)
}

func promptSendTarget(c *gin.Context, account *Account, body []byte, protocol string, compact bool, upstreamModel string) BusinessSystemPromptTarget {
	target := enrichPromptTarget(c, body, businessSystemPromptTargetForAccount(account, protocol, compact))
	if protocol == BusinessSystemPromptProtocolChat {
		if strict, exists := businessSystemPromptRequestGet(c, businessSystemPromptStrictChatRoleKey); exists {
			target.ChatSystemRoleOnly, _ = strict.(bool)
		}
	}
	if profile, exists := businessSystemPromptRequestGet(c, businessSystemPromptRequestProfileKey); exists {
		target.RequestProfile, _ = profile.(string)
	}
	if upstreamModel = strings.TrimSpace(upstreamModel); upstreamModel != "" {
		target.UpstreamModel = upstreamModel
		if target.RequestedModel == "" {
			target.RequestedModel = upstreamModel
		}
	}
	return target
}

func (s *BusinessSystemPromptService) freshPromptBinding(c *gin.Context, account *Account, target BusinessSystemPromptTarget) (BusinessSystemPromptTarget, error) {
	if s.accountRepo != nil {
		fresh, err := s.accountRepo.GetByID(promptPolicyRequestContext(c), account.ID)
		if err != nil || fresh == nil {
			return target, fmt.Errorf("%w: account binding unavailable", ErrBusinessSystemPromptUnavailable)
		}
		target.BindingJSON = businessSystemPromptTargetForAccount(fresh, target.Protocol, target.Compact).BindingJSON
	}
	return target, nil
}

// HasAnthropicSystemBlocksForSend plans the same frozen selection used by the
// final send. A migrated structured source already contains its infrastructure
// block layout, so the mimicry adapter must not prepend a second default layout.
// This method never applies content or caches a partly prepared wire body.
func (s *BusinessSystemPromptService) HasAnthropicSystemBlocksForSend(c *gin.Context, account *Account, body []byte, model string) (bool, error) {
	if s == nil || account == nil {
		return false, nil
	}
	snapshot, err := s.snapshotForRequest(c)
	if err != nil {
		return false, err
	}
	target := promptSendTarget(c, account, body, "messages", false, model)
	if !promptSnapshotNeedsBinding(snapshot, target) {
		return false, nil
	}
	if !snapshot.Draft {
		target, err = s.freshPromptBinding(c, account, target)
		if err != nil {
			return false, err
		}
	}
	application, err := planBusinessSystemPrompt(promptPolicyRequestContext(c), snapshot, target)
	if err != nil {
		return false, err
	}
	attempt := anthropicPromptAttempt{accountID: account.ID, model: model, binding: target.BindingJSON, sources: map[string]compiledAnthropicPromptSource{}}
	if application.RulesPlan != nil {
		for _, placement := range application.RulesPlan.Placements {
			if placement.ContentFormat == extensionv1.PromptContentAnthropicSystemBlocks {
				source, err := compileAnthropicPromptSource(body, placement.Body)
				if err != nil {
					return false, err
				}
				attempt.sources[placement.RuleID] = source
			}
		}
	}
	businessSystemPromptRequestSet(c, anthropicPromptAttemptKey, attempt)
	return len(attempt.sources) != 0, nil
}

func compileAnthropicPromptSource(body []byte, source string) (compiledAnthropicPromptSource, error) {
	blocks, err := ExpandClaudeOAuthSystemPromptBlocks(body, source)
	if err != nil {
		return compiledAnthropicPromptSource{}, fmt.Errorf("%w: structured prompt expansion", ErrBusinessSystemPromptInvalid)
	}
	hash, _, err := validateBusinessSystemPromptBodyWithLimit(string(blocks), extensionv1.PromptRulesMaxBytes)
	if err != nil {
		return compiledAnthropicPromptSource{}, err
	}
	return compiledAnthropicPromptSource{blocks: blocks, hash: hash}, nil
}

// prepareAnthropicPromptSend consumes the pre-mimic source expansion and binding
// together. Their selection cannot diverge after the adapter omitted its base
// layout, even if an account is edited while this attempt is being prepared.
func (s *BusinessSystemPromptService) prepareAnthropicPromptSend(c *gin.Context, account *Account, body []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) (BusinessSystemPromptSnapshot, BusinessSystemPromptTarget, error) {
	if !promptSnapshotNeedsBinding(snapshot, target) {
		return snapshot, target, nil
	}
	var attempt anthropicPromptAttempt
	matchedAttempt := false
	if target.Protocol == "messages" && target.RequestProfile == "generic-mimic" {
		if value, ok := businessSystemPromptRequestGet(c, anthropicPromptAttemptKey); ok {
			attempt, matchedAttempt = value.(anthropicPromptAttempt)
			matchedAttempt = matchedAttempt && attempt.accountID == account.ID && attempt.model == target.UpstreamModel
		}
	}
	if matchedAttempt {
		target.BindingJSON = attempt.binding
	} else if !snapshot.Draft {
		var err error
		target, err = s.freshPromptBinding(c, account, target)
		if err != nil {
			return snapshot, target, err
		}
	}
	if target.Protocol != "messages" {
		return snapshot, target, nil
	}
	application, err := planBusinessSystemPrompt(promptPolicyRequestContext(c), snapshot, target)
	if err != nil {
		return snapshot, target, err
	}
	selected := map[string]bool{}
	if application.RulesPlan != nil {
		for _, placement := range application.RulesPlan.Placements {
			if placement.ContentFormat == extensionv1.PromptContentAnthropicSystemBlocks {
				selected[placement.RuleID] = true
			}
		}
	}
	if len(selected) == 0 {
		return snapshot, target, nil
	}
	snapshot.ResolvedRules = append([]extensionv1.ResolvedPromptRule(nil), snapshot.ResolvedRules...)
	for index := range snapshot.ResolvedRules {
		rule := &snapshot.ResolvedRules[index]
		if !selected[rule.Rule.ID] {
			continue
		}
		source, ok := attempt.sources[rule.Rule.ID]
		if !matchedAttempt || !ok {
			source, err = compileAnthropicPromptSource(body, rule.Body)
			if err != nil {
				return snapshot, target, err
			}
		}
		rule.Body, rule.StructuredContent, rule.SHA256 = string(source.blocks), source.blocks, source.hash
	}
	return snapshot, target, nil
}

func promptSnapshotNeedsBinding(snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) bool {
	if !snapshot.Enabled || target.Compact && !snapshot.CompactEnabled || snapshot.RulePolicy == nil {
		return false
	}
	for _, rule := range snapshot.RulePolicy.Rules {
		if rule.Enabled {
			return true
		}
	}
	return false
}

// snapshotForRequest freezes the compiled content for one HTTP request or WS
// turn. Account selection and its binding are deliberately not frozen here.
func (s *BusinessSystemPromptService) snapshotForRequest(c *gin.Context) (BusinessSystemPromptSnapshot, error) {
	key := businessSystemPromptContextKey(c, businessSystemPromptRequestSnapshotKey, "")
	if value, exists := businessSystemPromptRequestGet(c, key); exists {
		if snapshot, ok := value.(BusinessSystemPromptSnapshot); ok {
			return snapshot, nil
		}
		return BusinessSystemPromptSnapshot{}, ErrBusinessSystemPromptUnavailable
	}
	snapshot, ok := s.CurrentSnapshot()
	if !ok {
		return BusinessSystemPromptSnapshot{}, ErrBusinessSystemPromptUnavailable
	}
	// Reload and atomic saves already compile immutable rule versions. Freeze
	// that publication directly; a request must not resolve a later registry or
	// storage state while preparing its outbound body.
	if snapshot.Enabled && snapshot.RulePolicy == nil {
		return BusinessSystemPromptSnapshot{}, ErrBusinessSystemPromptUnavailable
	}
	businessSystemPromptRequestSet(c, key, snapshot)
	return snapshot, nil
}

// ApplyForSend applies the frozen rules to a clean, final-protocol request.
// Callers retain their clean body for retries, fallback and WS accumulation;
// the returned bytes belong only to this send. Apply before cache keys and
// signatures, after protocol conversion and required provider instructions.
func (s *BusinessSystemPromptService) ApplyForSend(c *gin.Context, account *Account, body []byte, protocol string, compact bool) ([]byte, BusinessSystemPromptApplication, error) {
	return s.ApplyForSendModel(c, account, body, protocol, compact, "")
}

// ApplyForSendModel is for protocols whose model is carried by the URL or an
// outer envelope. It never adds a synthetic model field to the wire body.
func (s *BusinessSystemPromptService) ApplyForSendModel(c *gin.Context, account *Account, body []byte, protocol string, compact bool, upstreamModel string) (out []byte, application BusinessSystemPromptApplication, returnErr error) {
	defer func() { writePromptDeliveryError(c, returnErr) }()
	target := promptSendTarget(c, account, body, protocol, compact, upstreamModel)
	rememberBusinessSystemPromptTarget(c, target)
	if s == nil || account == nil {
		return body, BusinessSystemPromptApplication{}, nil
	}
	if value, exists := businessSystemPromptRequestGet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, protocol)); exists {
		state, ok := value.(businessSystemPromptRequestState)
		if ok && state.application.Applied && state.snapshot.Revision > 0 && sha256.Sum256(body) != state.inputHash && promptRulesCarrierMatches(body, state) {
			return nil, BusinessSystemPromptApplication{}, fmt.Errorf("%w: a send body cannot be reused as the clean request", ErrBusinessSystemPromptUnavailable)
		}
	}
	snapshot, err := s.snapshotForRequest(c)
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, err
	}
	snapshot, target, err = s.prepareAnthropicPromptSend(c, account, body, snapshot, target)
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, err
	}
	// The engine consumes only clean provider bytes. It is pure and therefore
	// cannot return a cached wire body from another account or protocol.
	updated, application, err := ApplyBusinessSystemPromptToJSONContext(promptPolicyRequestContext(c), body, snapshot, target)
	if err != nil {
		return nil, application, err
	}
	if protocol == "messages" {
		billingUserAgent := ""
		if value, exists := businessSystemPromptRequestGet(c, businessSystemPromptBillingUserAgentKey); exists {
			billingUserAgent, _ = value.(string)
		} else if target.RequestProfile == "generic-mimic" {
			billingUserAgent = claude.DefaultUserAgent()
		}
		updated, application, err = FinalizePromptMessageApplication(updated, application, account, s.previewSettings, billingUserAgent, c)
		if err != nil {
			return nil, application, err
		}
	}
	businessSystemPromptRequestSet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, protocol), cacheBusinessSystemPromptState(body, updated, snapshot, target, application))
	rememberBusinessSystemPromptTarget(c, target)
	observePromptRulesFinal(c, account, protocol, application)
	return updated, application, nil
}
