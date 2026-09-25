package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	businessSystemPromptRequestApplicationKey = "openai_business_system_prompt_application"
	businessSystemPromptRequestSnapshotKey    = "openai_business_system_prompt_snapshot"
	businessSystemPromptRequestTurnKey        = "openai_business_system_prompt_turn"
	businessSystemPromptRequestTargetKey      = "openai_business_system_prompt_target"
	businessSystemPromptCacheIdentityKey      = "openai_business_system_prompt_cache_identity"
	businessSystemPromptRestoreMaxBytes       = 64 << 10
)

// Cache identities belong to one logical request/WS turn, not an account or
// adapter. Keep the source separate from the wire key so a retry or protocol
// fallback can recognize our own output without guessing from its syntax.
type businessSystemPromptCacheIdentity struct {
	source    string
	namespace string
	wire      string
}

type businessSystemPromptCacheIdentities struct {
	values []businessSystemPromptCacheIdentity
}

type businessSystemPromptRequestState struct {
	rulesUndo   []promptRulesCarrierUndo
	application BusinessSystemPromptApplication
	snapshot    BusinessSystemPromptSnapshot
	target      BusinessSystemPromptTarget
	inputHash   [32]byte
}

func businessSystemPromptTargetForAccount(account *Account, protocol string, compact bool) BusinessSystemPromptTarget {
	target := BusinessSystemPromptTarget{Protocol: protocol, Compact: compact}
	if account != nil {
		target.AccountID, target.Platform, target.AccountType = account.ID, account.Platform, account.Type
		target.ProviderPlatform, target.ProviderProfile = account.Platform, ""
		target.ChatSystemRoleOnly = protocol == BusinessSystemPromptProtocolChat && requiresSystemChatRole(account, account.GetOpenAIBaseURL())
		if binding := account.Extra[PromptAccountBindingExtraKey]; binding != nil {
			raw, err := json.Marshal(binding)
			if err == nil {
				target.BindingJSON = string(raw)
			} else {
				target.BindingJSON = "invalid"
			}
		}
	}
	return target
}

func rememberBusinessSystemPromptTarget(ctx *gin.Context, target BusinessSystemPromptTarget) {
	if ctx != nil {
		businessSystemPromptRequestSet(ctx, businessSystemPromptContextKey(ctx, businessSystemPromptRequestTargetKey, ""), target)
	}
}

// Only exact structured echoes and integrity checks use these insertion
// proofs. Sending and retrying always start from the caller's clean body.
func cacheBusinessSystemPromptState(input, output []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget, application BusinessSystemPromptApplication) businessSystemPromptRequestState {
	return businessSystemPromptRequestState{
		application: application, snapshot: snapshot, target: target,
		inputHash: sha256.Sum256(input),
		rulesUndo: promptRulesUndo(input, output, application),
	}
}

// Compatibility wrappers delegate to the protocol-neutral send service. All
// production callers use them only at their final wire boundary.
func (s *OpenAIGatewayService) applyBusinessSystemPrompt(body []byte, account *Account, protocol string, compact bool) ([]byte, BusinessSystemPromptApplication, error) {
	return s.applyBusinessSystemPromptForRequest(nil, body, account, protocol, compact)
}

func (s *OpenAIGatewayService) businessSystemPromptSnapshotForRequest(c *gin.Context, account *Account) (BusinessSystemPromptSnapshot, bool, error) {
	if s == nil || s.businessPromptService == nil || account == nil {
		return BusinessSystemPromptSnapshot{}, false, nil
	}
	snapshot, err := s.businessPromptService.snapshotForRequest(c)
	return snapshot, true, err
}

func (s *OpenAIGatewayService) applyBusinessSystemPromptForRequest(c *gin.Context, body []byte, account *Account, protocol string, compact bool) ([]byte, BusinessSystemPromptApplication, error) {
	if s == nil || s.businessPromptService == nil {
		return body, BusinessSystemPromptApplication{}, nil
	}
	return s.businessPromptService.ApplyForSend(c, account, body, protocol, compact)
}

func businessSystemPromptApplicationFromRequest(ctx *gin.Context, protocol string) (BusinessSystemPromptApplication, bool) {
	if ctx == nil {
		return BusinessSystemPromptApplication{}, false
	}
	value, exists := businessSystemPromptRequestGet(ctx, businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol))
	if !exists {
		return BusinessSystemPromptApplication{}, false
	}
	if state, ok := value.(businessSystemPromptRequestState); ok {
		if current, exists := businessSystemPromptRequestGet(ctx, businessSystemPromptContextKey(ctx, businessSystemPromptRequestTargetKey, "")); exists {
			if target, ok := current.(BusinessSystemPromptTarget); ok &&
				(target.AccountID != state.target.AccountID || target.ProviderPlatform != state.target.ProviderPlatform || target.Platform != state.target.Platform || target.AccountType != state.target.AccountType || target.Protocol != state.target.Protocol || target.Compact != state.target.Compact) {
				return BusinessSystemPromptApplication{}, false
			}
		}
		return state.application, true
	}
	return BusinessSystemPromptApplication{}, false
}

func (s *OpenAIGatewayService) rewriteBusinessSystemPromptJSONForRequest(c *gin.Context, body []byte, protocol string) []byte {
	application, ok := businessSystemPromptApplicationFromRequest(c, protocol)
	if !ok {
		return body
	}
	rewritten, err := RewriteBusinessSystemPromptResponseJSON(body, application, application.ExposeServerPrompt)
	if err != nil {
		return body
	}
	return rewritePromptRulesStructuredEcho(c, rewritten, protocol)
}

func (s *OpenAIGatewayService) rewriteBusinessSystemPromptJSONForAnyRequest(c *gin.Context, body []byte) []byte {
	for _, protocol := range []string{BusinessSystemPromptProtocolResponses, BusinessSystemPromptProtocolChat, BusinessSystemPromptProtocolMessages, BusinessSystemPromptProtocolGemini} {
		body = s.rewriteBusinessSystemPromptJSONForRequest(c, body, protocol)
	}
	return body
}

func (s *OpenAIGatewayService) rewriteBusinessSystemPromptSSEForRequest(c *gin.Context, body []byte, protocol string) []byte {
	application, ok := businessSystemPromptApplicationFromRequest(c, protocol)
	if !ok {
		return body
	}
	rewritten, err := RewriteBusinessSystemPromptSSE(body, application, application.ExposeServerPrompt)
	if err != nil {
		return body
	}
	if application.RulesPlan != nil {
		return rewritePromptRulesStructuredSSE(c, rewritten, protocol)
	}
	return rewritten
}

func businessSystemPromptContextKey(ctx *gin.Context, base, protocol string) string {
	key := base
	if protocol != "" {
		key += ":" + protocol
	}
	if ctx == nil {
		return key
	}
	if value, exists := ctx.Get(businessSystemPromptRequestTurnKey); exists {
		if turn, ok := value.(int64); ok && turn > 0 {
			return key + ":turn:" + strconv.FormatInt(turn, 10)
		}
	}
	return key
}

func beginBusinessSystemPromptRequestTurn(ctx *gin.Context) {
	if ctx == nil {
		return
	}
	var turn int64
	if value, exists := ctx.Get(businessSystemPromptRequestTurnKey); exists {
		turn, _ = value.(int64)
	}
	ctx.Set(businessSystemPromptRequestTurnKey, turn+1)
	ctx.Set(businessSystemPromptTurnCacheKey, &businessSystemPromptTurnCache{})
	ctx.Set(businessSystemPromptCacheIdentityKey, &businessSystemPromptCacheIdentities{})
}

// This encoding is an internal namespace, never an upstream field. Preserve
// its historical bytes so Cindy's SHA256(old expanded key) remains unchanged.
func businessSystemPromptCacheNamespace(application BusinessSystemPromptApplication) string {
	if application.RulesPlan != nil && application.Applied {
		return ":prompt-rules:v2:" + strconv.FormatInt(application.Revision, 10) + ":" + application.RulesPlan.SHA256
	}
	if !application.Applied || application.Revision < 1 || strings.TrimSpace(application.SHA256) == "" {
		return ""
	}
	suffix := ":business-system-prompt:" + strconv.FormatInt(application.Revision, 10) + ":" + strings.TrimSpace(application.SHA256)
	if application.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid && application.BundleEffectiveTreeSHA256 != "" && application.EffectiveSHA256 != "" {
		suffix = ":business-system-prompt:" + strconv.FormatInt(application.Revision, 10) +
			":" + strings.TrimSpace(application.BundleID) +
			":" + strings.ToLower(strings.TrimSpace(application.BundleEffectiveTreeSHA256)) +
			":" + strings.ToLower(strings.TrimSpace(application.BaseSHA256)) +
			":" + strings.ToLower(strings.TrimSpace(application.EffectiveSHA256)) +
			":bundle-revision:" + strconv.FormatInt(application.BundleRevision, 10) +
			":" + strings.ToLower(strings.TrimSpace(application.BundlePromptEffectiveSHA256))
	}
	return suffix
}

func deriveBusinessSystemPromptCacheKey(c *gin.Context, key string, application BusinessSystemPromptApplication) string {
	namespace := businessSystemPromptCacheNamespace(application)
	if namespace == "" || strings.TrimSpace(key) == "" {
		return key
	}
	source := strings.TrimSpace(key)
	var identities *businessSystemPromptCacheIdentities
	if c != nil {
		value, _ := c.Get(businessSystemPromptCacheIdentityKey)
		identities, _ = value.(*businessSystemPromptCacheIdentities)
		if identities == nil {
			identities = &businessSystemPromptCacheIdentities{}
			c.Set(businessSystemPromptCacheIdentityKey, identities)
		}
		for _, identity := range identities.values {
			if identity.namespace == namespace && (source == identity.source || source == identity.wire) {
				return identity.wire
			}
		}
	}
	digest := sha256.Sum256([]byte(source + namespace))
	wire := hex.EncodeToString(digest[:])
	if identities != nil {
		identities.values = append(identities.values, businessSystemPromptCacheIdentity{
			source: source, namespace: namespace, wire: wire,
		})
	}
	return wire
}

func rewriteBusinessSystemPromptCacheKey(c *gin.Context, body []byte, application BusinessSystemPromptApplication) ([]byte, error) {
	value := gjson.GetBytes(body, "prompt_cache_key")
	if !value.Exists() || value.Type != gjson.String {
		return body, nil
	}
	effective := deriveBusinessSystemPromptCacheKey(c, value.String(), application)
	if effective == "" || effective == value.String() {
		return body, nil
	}
	updated, err := sjson.SetBytes(body, "prompt_cache_key", effective)
	if err != nil {
		return nil, fmt.Errorf("rewrite business system prompt cache key: %w", err)
	}
	return updated, nil
}

// Read the final body after all wire transforms (including Cindy's separate
// policy). Bridges that intentionally omit the body field retain their seed
// for header fallback, without injecting a new field or mutating local state.
func businessSystemPromptUpstreamCacheKey(c *gin.Context, body []byte, seed string, application BusinessSystemPromptApplication) string {
	value := gjson.GetBytes(body, "prompt_cache_key")
	if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
		return strings.TrimSpace(value.String())
	}
	return deriveBusinessSystemPromptCacheKey(c, seed, application)
}
