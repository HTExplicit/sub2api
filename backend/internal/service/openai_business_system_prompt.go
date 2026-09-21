package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	businessSystemPromptRequestCompiledKey    = "openai_business_system_prompt_compiled_snapshot"
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
	application      BusinessSystemPromptApplication
	snapshot         BusinessSystemPromptSnapshot
	target           BusinessSystemPromptTarget
	inputHash        [32]byte
	output           []byte
	undo             businessSystemPromptUndo
	otherUndo        businessSystemPromptUndo
	historyUncertain bool
}

// One output exists per protocol/turn. At most one proof for each of the two
// native carriers is retained; only the instructions proof holds a bounded
// original value. Neither proof stores customer history or another full body.
type businessSystemPromptUndo struct {
	present      bool
	valid        bool
	restorable   bool
	carrier      string
	server       string
	beforeExists bool
	beforeHash   [32]byte
	afterHash    [32]byte
	instructions []byte
	messageIndex int
}

func businessSystemPromptTargetForAccount(account *Account, protocol string, compact bool) BusinessSystemPromptTarget {
	target := BusinessSystemPromptTarget{Protocol: protocol, Compact: compact}
	if account != nil {
		target.AccountID, target.Platform, target.AccountType = account.ID, account.EffectiveWirePlatform(), account.Type
	}
	return target
}

func rememberBusinessSystemPromptTarget(ctx *gin.Context, target BusinessSystemPromptTarget) {
	if ctx != nil {
		ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestTargetKey, ""), target)
	}
}

// Ignore only JSON framing whitespace. Hash slices directly so a large message
// history is neither copied nor retained in a second request-cache snapshot.
func businessSystemPromptCarrierHash(raw []byte) [32]byte {
	digest := sha256.New()
	quoted, escaped, start := false, false, 0
	for index, character := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				quoted = false
			}
			continue
		}
		if character == '"' {
			quoted = true
			continue
		}
		if character == ' ' || character == '\t' || character == '\r' || character == '\n' {
			_, _ = digest.Write(raw[start:index])
			start = index + 1
		}
	}
	_, _ = digest.Write(raw[start:])
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func businessSystemPromptCarrier(body []byte, carrier string) (gjson.Result, []byte, bool) {
	field := "instructions"
	if carrier == BusinessSystemPromptCarrierSystemMessage {
		field = "messages"
	}
	// The existing read-only view does not copy a large messages array. Reject
	// duplicate carriers: encoding/json and gjson select different duplicates,
	// so an insertion index would not be an unambiguous ownership proof.
	view := parseRawJSONView(body)
	if !view.IsObject() {
		return gjson.Result{}, nil, false
	}
	var value gjson.Result
	matches := 0
	view.ForEach(func(key, candidate gjson.Result) bool {
		if key.String() == field {
			matches++
			value = candidate
		}
		return matches < 2
	})
	if matches == 0 {
		return value, nil, true
	}
	if matches != 1 || value.Index <= 0 || value.Index+len(value.Raw) > len(body) {
		return value, nil, false
	}
	return value, body[value.Index : value.Index+len(value.Raw)], true
}

func cacheBusinessSystemPromptState(input, output []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget, application BusinessSystemPromptApplication) businessSystemPromptRequestState {
	state := businessSystemPromptRequestState{application: application, snapshot: snapshot, target: target, inputHash: sha256.Sum256(input), output: append([]byte(nil), output...)}
	if !application.Applied {
		return state
	}
	state.undo = businessSystemPromptUndo{present: true, carrier: application.Carrier, server: strings.TrimSpace(application.ServerInstructions)}
	before, beforeRaw, beforeUnique := businessSystemPromptCarrier(input, application.Carrier)
	after, afterRaw, afterUnique := businessSystemPromptCarrier(output, application.Carrier)
	if !beforeUnique || !afterUnique {
		return state
	}
	undo := state.undo
	undo.beforeExists, undo.beforeHash, undo.afterHash = before.Exists(), businessSystemPromptCarrierHash(beforeRaw), businessSystemPromptCarrierHash(afterRaw)
	switch application.Carrier {
	case BusinessSystemPromptCarrierInstructions:
		undo.valid = after.Type == gjson.String
		undo.restorable = !before.Exists() || len(beforeRaw) <= businessSystemPromptRestoreMaxBytes
		if before.Exists() && undo.restorable {
			undo.instructions = append([]byte(nil), beforeRaw...)
		}
	case BusinessSystemPromptCarrierSystemMessage:
		undo.valid, undo.restorable = before.IsArray() && after.IsArray(), true
		before.ForEach(func(_, message gjson.Result) bool {
			role := strings.ToLower(strings.TrimSpace(message.Get("role").String()))
			if role != "system" && role != "developer" {
				return false
			}
			undo.messageIndex++
			return true
		})
	}
	state.undo = undo
	return state
}

func inheritBusinessSystemPromptProvenance(next, previous businessSystemPromptRequestState) businessSystemPromptRequestState {
	next.historyUncertain = previous.historyUncertain
	if !next.application.Applied {
		next.undo, next.otherUndo = previous.undo, previous.otherUndo
		return next
	}
	for _, old := range [...]businessSystemPromptUndo{previous.undo, previous.otherUndo} {
		if !old.present {
			continue
		}
		if old.carrier == next.undo.carrier {
			// A compatible planner need not always return identical text. Keep
			// no unbounded text history: unknown bodies must now fail closed.
			if old.server != next.undo.server {
				next.historyUncertain = true
			}
			continue
		}
		next.otherUndo = old
	}
	return next
}

func restoreBusinessSystemPromptBody(body []byte, state businessSystemPromptRequestState) ([]byte, error) {
	if (!state.application.Applied && !state.undo.present && !state.otherUndo.present) || sha256.Sum256(body) == state.inputHash {
		return body, nil
	}
	if state.application.Applied && !state.undo.present {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	// The one cached output was built from an already-clean input. Only its
	// latest insertion needs removal; older carrier data is known original.
	if bytes.Equal(body, state.output) {
		return restoreBusinessSystemPromptCarrier(body, state.undo)
	}
	if state.historyUncertain {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	clean, err := restoreBusinessSystemPromptCarrier(body, state.undo)
	if err != nil {
		return nil, err
	}
	if state.otherUndo.present && !businessSystemPromptPriorCarrierUnchanged(body, state) {
		return restoreBusinessSystemPromptCarrier(clean, state.otherUndo)
	}
	return clean, nil
}

func businessSystemPromptPriorCarrierUnchanged(body []byte, state businessSystemPromptRequestState) bool {
	if !state.otherUndo.present || len(state.output) == 0 {
		return false
	}
	value, raw, unique := businessSystemPromptCarrier(body, state.otherUndo.carrier)
	cached, cachedRaw, cachedUnique := businessSystemPromptCarrier(state.output, state.otherUndo.carrier)
	return unique && cachedUnique && value.Exists() == cached.Exists() && businessSystemPromptCarrierHash(raw) == businessSystemPromptCarrierHash(cachedRaw)
}

func restoreBusinessSystemPromptCarrier(body []byte, undo businessSystemPromptUndo) ([]byte, error) {
	if !undo.present {
		return body, nil
	}
	value, raw, unique := businessSystemPromptCarrier(body, undo.carrier)
	if !unique {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	// Absence/before is clean only for this carrier, not for every previously
	// used carrier. The caller must continue checking the other bounded proof.
	if !value.Exists() {
		return body, nil
	}
	if !undo.valid {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	hash := businessSystemPromptCarrierHash(raw)
	if value.Exists() == undo.beforeExists && hash == undo.beforeHash {
		return body, nil
	}
	if !value.Exists() || hash != undo.afterHash || !undo.restorable {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	if undo.carrier == BusinessSystemPromptCarrierInstructions {
		if undo.beforeExists {
			return sjson.SetRawBytes(body, "instructions", undo.instructions)
		}
		return sjson.DeleteBytes(body, "instructions")
	}
	return sjson.DeleteBytes(body, "messages."+strconv.Itoa(undo.messageIndex))
}

// Restore in the source protocol before a converter can relocate our carrier
// into customer control messages. Never search/delete a system message by text.
func restoreBusinessSystemPromptBeforeConversion(ctx *gin.Context, body []byte, protocol string) ([]byte, error) {
	if ctx == nil {
		return body, nil
	}
	value, exists := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol))
	if !exists {
		return body, nil
	}
	state, ok := value.(businessSystemPromptRequestState)
	if !ok {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	return restoreBusinessSystemPromptBody(body, state)
}

// Another platform may have rebuilt a clean native carrier. Preserve it only
// when bounded carrier inspection proves our actual insertion text is absent;
// user messages are not searched and matching control text is never deleted.
func businessSystemPromptCarrierExcludesInsertion(body []byte, undo businessSystemPromptUndo) bool {
	value, raw, unique := businessSystemPromptCarrier(body, undo.carrier)
	if !unique {
		return false
	}
	if !value.Exists() {
		return true
	}
	if undo.server == "" || len(raw) > businessSystemPromptRestoreMaxBytes {
		return false
	}
	if undo.carrier == BusinessSystemPromptCarrierInstructions {
		return value.Type == gjson.String && !strings.Contains(value.String(), undo.server)
	}
	if undo.carrier != BusinessSystemPromptCarrierSystemMessage || !value.IsArray() {
		return false
	}
	clean := true
	value.ForEach(func(_, message gjson.Result) bool {
		if !message.IsObject() || hasDuplicateJSONObjectKeys(message) {
			clean = false
			return false
		}
		role := strings.ToLower(strings.TrimSpace(message.Get("role").String()))
		if role != "system" && role != "developer" {
			return true
		}
		content := message.Get("content")
		clean = content.Type == gjson.String && !strings.Contains(content.String(), undo.server)
		return clean
	})
	return clean
}

func businessSystemPromptExcludesAllInsertions(body []byte, state businessSystemPromptRequestState) bool {
	if state.historyUncertain || (state.application.Applied && !state.undo.present && !state.otherUndo.present) {
		return false
	}
	if state.undo.present && !businessSystemPromptCarrierExcludesInsertion(body, state.undo) {
		return false
	}
	return !state.otherUndo.present || businessSystemPromptPriorCarrierUnchanged(body, state) || businessSystemPromptCarrierExcludesInsertion(body, state.otherUndo)
}

func restoreBusinessSystemPromptForExcludedTarget(ctx *gin.Context, body []byte, protocol string) ([]byte, error) {
	if ctx == nil {
		return body, nil
	}
	value, exists := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol))
	if !exists {
		return body, nil
	}
	state, ok := value.(businessSystemPromptRequestState)
	if !ok {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	clean, err := restoreBusinessSystemPromptBody(body, state)
	if err == nil {
		return clean, nil
	}
	if businessSystemPromptExcludesAllInsertions(body, state) {
		return body, nil
	}
	return nil, ErrBusinessSystemPromptUnavailable
}

func (s *OpenAIGatewayService) applyBusinessSystemPrompt(
	body []byte,
	account *Account,
	protocol string,
	compact bool,
) ([]byte, BusinessSystemPromptApplication, error) {
	if s == nil || s.businessPromptService == nil || account == nil || !account.IsOpenAI() {
		return body, BusinessSystemPromptApplication{}, nil
	}
	snapshot, ok := s.businessPromptService.CurrentSnapshot()
	if !ok {
		return nil, BusinessSystemPromptApplication{}, ErrBusinessSystemPromptUnavailable
	}
	if snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
		if err := s.businessPromptService.prepareBusinessSystemPromptSnapshot(&snapshot); err != nil {
			return nil, BusinessSystemPromptApplication{}, err
		}
	}
	if snapshot.Enabled && (!compact || snapshot.CompactEnabled) {
		var err error
		snapshot, err = s.businessPromptService.compileBusinessSystemPromptSnapshot(snapshot)
		if err != nil {
			return nil, BusinessSystemPromptApplication{}, err
		}
	}
	return ApplyBusinessSystemPromptToJSON(body, snapshot, BusinessSystemPromptTarget{
		AccountID:   account.ID,
		Platform:    account.EffectiveWirePlatform(),
		AccountType: account.Type,
		Protocol:    protocol,
		Compact:     compact,
	})
}

func (s *OpenAIGatewayService) businessSystemPromptSnapshotForRequest(
	ctx *gin.Context,
	account *Account,
) (BusinessSystemPromptSnapshot, bool, error) {
	if s == nil || s.businessPromptService == nil || account == nil || !account.IsOpenAI() {
		return BusinessSystemPromptSnapshot{}, false, nil
	}
	if ctx != nil {
		if value, exists := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestSnapshotKey, "")); exists {
			if snapshot, ok := value.(BusinessSystemPromptSnapshot); ok {
				return snapshot, true, nil
			}
		}
	}
	snapshot, ok := s.businessPromptService.CurrentSnapshot()
	if !ok {
		return BusinessSystemPromptSnapshot{}, true, ErrBusinessSystemPromptUnavailable
	}
	if snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
		if err := s.businessPromptService.prepareBusinessSystemPromptSnapshot(&snapshot); err != nil {
			return BusinessSystemPromptSnapshot{}, true, err
		}
	}
	if ctx != nil {
		ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestSnapshotKey, ""), snapshot)
	}
	return snapshot, true, nil
}

// Freeze prompt content and retain local rewrite provenance, while rechecking
// execution admission on every attempt. Admitted retries can reuse their bytes
// without appending the server prompt a second time.
func (s *OpenAIGatewayService) applyBusinessSystemPromptForRequest(
	ctx *gin.Context,
	body []byte,
	account *Account,
	protocol string,
	compact bool,
) ([]byte, BusinessSystemPromptApplication, error) {
	target := businessSystemPromptTargetForAccount(account, protocol, compact)
	// Platform eligibility precedes policy invocation or application reuse.
	// Local provenance can still undo an earlier OpenAI insertion before an
	// unrelated platform's transform; clean customer carriers stay untouched.
	if s == nil || s.businessPromptService == nil || account == nil || !account.IsOpenAI() {
		clean, err := restoreBusinessSystemPromptForExcludedTarget(ctx, body, protocol)
		if err != nil {
			return nil, BusinessSystemPromptApplication{}, err
		}
		rememberBusinessSystemPromptTarget(ctx, target)
		return clean, BusinessSystemPromptApplication{}, nil
	}
	if ctx != nil {
		applicationKey := businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol)
		if value, exists := ctx.Get(applicationKey); exists {
			if state, ok := value.(businessSystemPromptRequestState); ok {
				frozen := state.snapshot
				if frozen.Revision < 1 && state.application.Applied {
					return nil, BusinessSystemPromptApplication{}, ErrBusinessSystemPromptUnavailable
				}
				application, err := planBusinessSystemPromptWithInvoker(promptPolicyRequestContext(ctx), body, frozen, target, invokeProcessExtension)
				if err != nil {
					return nil, BusinessSystemPromptApplication{}, err
				}
				previousPlan := state.application
				previousPlan.ClientInstructions = ""
				if state.target == target && previousPlan == application {
					rememberBusinessSystemPromptTarget(ctx, target)
					if state.inputHash == sha256.Sum256(body) {
						return append([]byte(nil), state.output...), state.application, nil
					}
					if bytes.Equal(body, state.output) || (!state.historyUncertain && ((!state.application.Applied && !state.undo.present && !state.otherUndo.present) || businessSystemPromptAlreadyApplied(body, state.application, protocol))) {
						return body, state.application, nil
					}
				}
				clean, restoreErr := restoreBusinessSystemPromptBody(body, state)
				if restoreErr != nil {
					// An admitted same-target retry may have rebuilt a new client
					// carrier. No removal is attempted in that case. Revocation or
					// cross-target reuse always requires provenance for any undo.
					if state.target != target || !application.Applied || !businessSystemPromptExcludesAllInsertions(body, state) {
						return nil, BusinessSystemPromptApplication{}, ErrBusinessSystemPromptUnavailable
					}
					clean = body
				}
				updated, application, err := applyBusinessSystemPromptApplication(clean, application)
				if err != nil {
					return nil, BusinessSystemPromptApplication{}, err
				}
				next := cacheBusinessSystemPromptState(clean, updated, frozen, target, application)
				next = inheritBusinessSystemPromptProvenance(next, state)
				ctx.Set(applicationKey, next)
				rememberBusinessSystemPromptTarget(ctx, target)
				return updated, application, nil
			} else {
				return nil, BusinessSystemPromptApplication{}, ErrBusinessSystemPromptUnavailable
			}
		}
	}
	snapshot, eligible, err := s.businessSystemPromptSnapshotForRequest(ctx, account)
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, err
	}
	if !eligible {
		return body, BusinessSystemPromptApplication{}, nil
	}
	if snapshot.Enabled && (!compact || snapshot.CompactEnabled) {
		if ctx != nil {
			if value, exists := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestCompiledKey, "")); exists {
				if compiled, ok := value.(BusinessSystemPromptSnapshot); ok && compiled.Revision == snapshot.Revision {
					snapshot = compiled
				}
			}
		}
		if snapshot.EffectiveSHA256 == "" &&
			snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
			compiled, compileErr := s.businessPromptService.compileBusinessSystemPromptSnapshot(snapshot)
			if compileErr != nil {
				return nil, BusinessSystemPromptApplication{}, compileErr
			}
			snapshot = compiled
			if ctx != nil {
				ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestCompiledKey, ""), snapshot)
			}
		}
	}
	updated, application, err := ApplyBusinessSystemPromptToJSONContext(promptPolicyRequestContext(ctx), body, snapshot, target)
	if err != nil {
		return nil, application, err
	}
	if ctx != nil {
		ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol), cacheBusinessSystemPromptState(body, updated, snapshot, target, application))
	}
	rememberBusinessSystemPromptTarget(ctx, target)
	return updated, application, nil
}

func businessSystemPromptAlreadyApplied(body []byte, application BusinessSystemPromptApplication, protocol string) bool {
	if !application.Applied {
		return false
	}
	switch protocol {
	case BusinessSystemPromptProtocolResponses:
		instructions := gjson.GetBytes(body, "instructions")
		return instructions.Exists() && instructions.Type == gjson.String &&
			instructions.String() == MergeBusinessSystemPromptInstructions(application.ClientInstructions, application.ServerInstructions)
	case BusinessSystemPromptProtocolChat:
		messages := gjson.GetBytes(body, "messages")
		if !messages.IsArray() {
			return false
		}
		for _, message := range messages.Array() {
			if strings.EqualFold(strings.TrimSpace(message.Get("role").String()), "system") &&
				strings.TrimSpace(message.Get("content").String()) == application.ServerInstructions {
				return true
			}
		}
	}
	return false
}

func businessSystemPromptApplicationFromRequest(ctx *gin.Context, protocol string) (BusinessSystemPromptApplication, bool) {
	if ctx == nil {
		return BusinessSystemPromptApplication{}, false
	}
	value, exists := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol))
	if !exists {
		return BusinessSystemPromptApplication{}, false
	}
	if state, ok := value.(businessSystemPromptRequestState); ok {
		if current, exists := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestTargetKey, "")); exists {
			if target, ok := current.(BusinessSystemPromptTarget); ok &&
				(target.AccountID != state.target.AccountID || target.Platform != state.target.Platform || target.AccountType != state.target.AccountType || target.Compact != state.target.Compact) {
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
	return rewritten
}

func (s *OpenAIGatewayService) rewriteBusinessSystemPromptJSONForAnyRequest(c *gin.Context, body []byte) []byte {
	body = s.rewriteBusinessSystemPromptJSONForRequest(c, body, BusinessSystemPromptProtocolResponses)
	return s.rewriteBusinessSystemPromptJSONForRequest(c, body, BusinessSystemPromptProtocolChat)
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
	ctx.Set(businessSystemPromptCacheIdentityKey, &businessSystemPromptCacheIdentities{})
}

// This encoding is an internal namespace, never an upstream field. Preserve
// its historical bytes so Cindy's SHA256(old expanded key) remains unchanged.
func businessSystemPromptCacheNamespace(application BusinessSystemPromptApplication) string {
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
