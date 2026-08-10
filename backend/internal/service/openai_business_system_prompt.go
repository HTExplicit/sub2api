package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	openaiutil "github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	businessSystemPromptRequestApplicationKey = "openai_business_system_prompt_application"
	businessSystemPromptRequestSnapshotKey    = "openai_business_system_prompt_snapshot"
	businessSystemPromptRequestCompiledKey    = "openai_business_system_prompt_compiled_snapshot"
	businessSystemPromptRequestTurnKey        = "openai_business_system_prompt_turn"
	businessSystemPromptResponseRouteTTL      = time.Hour
)

type businessSystemPromptRequestState struct {
	application BusinessSystemPromptApplication
	snapshot    BusinessSystemPromptSnapshot
	inputHash   [32]byte
	output      []byte
}

func (s *OpenAIGatewayService) applyBusinessSystemPrompt(
	body []byte,
	account *Account,
	protocol string,
	compact bool,
) ([]byte, BusinessSystemPromptApplication, error) {
	if s == nil || s.businessPromptService == nil || account == nil || account.Platform != PlatformOpenAI {
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
		snapshot, err = s.businessPromptService.compileBusinessSystemPromptSnapshot(
			snapshot, businessSystemPromptRequestTextFromJSON(body, protocol), false, nil, true,
		)
		if err != nil {
			return nil, BusinessSystemPromptApplication{}, err
		}
	}
	return ApplyBusinessSystemPromptToJSON(body, snapshot, BusinessSystemPromptTarget{
		Platform: account.Platform,
		Protocol: protocol,
		Compact:  compact,
	})
}

func (s *OpenAIGatewayService) businessSystemPromptSnapshotForRequest(
	ctx *gin.Context,
	account *Account,
) (BusinessSystemPromptSnapshot, bool, error) {
	if s == nil || s.businessPromptService == nil || account == nil || account.Platform != PlatformOpenAI {
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

// applyBusinessSystemPromptForRequest stores application metadata on the
// request context so retries/fallbacks can reuse the same normalized decision
// without appending the server prompt a second time.
func (s *OpenAIGatewayService) applyBusinessSystemPromptForRequest(
	ctx *gin.Context,
	body []byte,
	account *Account,
	protocol string,
	compact bool,
) ([]byte, BusinessSystemPromptApplication, error) {
	// Eligibility is checked before consulting request-scoped state. This keeps
	// an OpenAI attempt's frozen application from crossing into a Grok-specific
	// transform if a caller ever reuses the Gin context across platforms.
	if s == nil || s.businessPromptService == nil || account == nil || account.Platform != PlatformOpenAI {
		return body, BusinessSystemPromptApplication{}, nil
	}
	if ctx != nil {
		applicationKey := businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol)
		if value, exists := ctx.Get(applicationKey); exists {
			if state, ok := value.(businessSystemPromptRequestState); ok {
				if state.inputHash == sha256.Sum256(body) {
					return append([]byte(nil), state.output...), state.application, nil
				}
				if bytes.Equal(body, state.output) {
					return body, state.application, nil
				}
				if !state.application.Applied || businessSystemPromptAlreadyApplied(body, state.application, protocol) {
					return body, state.application, nil
				}
				frozen := state.snapshot
				if frozen.Revision < 1 {
					// Compatibility for request contexts populated by older code.
					frozen = BusinessSystemPromptSnapshot{
						Enabled: true, ExposeServerPrompt: state.application.ExposeServerPrompt,
						CompactEnabled: state.application.CompactEnabled,
						TemplateID:     state.application.TemplateID, VersionID: state.application.VersionID,
						TemplateVersion: state.application.TemplateVersion, Revision: state.application.Revision,
						Body: state.application.ServerInstructions, SHA256: state.application.SHA256,
						CompositionMode: state.application.CompositionMode,
						BundleID:        state.application.BundleID, BundleManifestSHA256: state.application.BundleManifestSHA256,
						RegistryRevision:       state.application.BundleRevision,
						RegistryManifestSHA256: state.application.BundleManifestSHA256,
						RegistryArchiveSHA256:  state.application.BundleArchiveSHA256,
						RegistrySourceCommit:   state.application.BundleSourceCommit,
						RegistrySourceID:       state.application.BundleSourceID,
						RegistryRemoteRoot:     state.application.BundleRemoteRoot,
						baseSHA256:             state.application.BaseSHA256, effectiveSHA256: state.application.EffectiveSHA256,
						effectiveByteLength: state.application.EffectiveByteLength,
						routeIDs:            append([]string(nil), state.application.RouteIDs...),
						documentIDs:         append([]string(nil), state.application.DocumentIDs...),
						referenceIDs:        append([]string(nil), state.application.ReferenceIDs...),
						omittedDocumentIDs:  append([]string(nil), state.application.OmittedDocumentIDs...),
					}
				}
				updated, application, err := ApplyBusinessSystemPromptToJSON(body, frozen, BusinessSystemPromptTarget{
					Platform: PlatformOpenAI, Protocol: protocol, Compact: compact,
				})
				if err != nil {
					return nil, BusinessSystemPromptApplication{}, err
				}
				ctx.Set(applicationKey, businessSystemPromptRequestState{
					application: application, snapshot: frozen,
					inputHash: sha256.Sum256(body), output: append([]byte(nil), updated...),
				})
				return updated, application, nil
			} else if application, ok := value.(BusinessSystemPromptApplication); ok {
				// Keep compatibility with contexts created by older callers while
				// the request is being retried.
				return body, application, nil
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
		if snapshot.effectiveSHA256 == "" &&
			(snapshot.CompositionMode == BusinessSystemPromptCompositionOfflineBundle || snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid) {
			requestText := businessSystemPromptRequestTextFromJSON(body, protocol)
			previousResponseID := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
			var previousMetadata *BusinessSystemPromptBundleMetadata
			if previousResponseID != "" && strings.TrimSpace(requestText) == "" {
				loadCtx := context.Background()
				if ctx != nil && ctx.Request != nil {
					loadCtx = ctx.Request.Context()
				}
				previousMetadata, _ = s.businessPromptService.LoadResponseRouteMetadata(loadCtx, previousResponseID)
			}
			compiled, compileErr := s.businessPromptService.compileBusinessSystemPromptSnapshot(
				snapshot, requestText, previousResponseID != "" && strings.TrimSpace(requestText) == "", previousMetadata,
				!isOfficialCodexBusinessSystemPromptRequest(ctx),
			)
			if compileErr != nil {
				return nil, BusinessSystemPromptApplication{}, compileErr
			}
			snapshot = compiled
			if ctx != nil {
				ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestCompiledKey, ""), snapshot)
			}
		}
	}
	updated, application, err := ApplyBusinessSystemPromptToJSON(body, snapshot, BusinessSystemPromptTarget{
		Platform: account.Platform,
		Protocol: protocol,
		Compact:  compact,
	})
	if err != nil {
		return nil, application, err
	}
	if ctx != nil {
		ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol), businessSystemPromptRequestState{
			application: application,
			snapshot:    snapshot,
			inputHash:   sha256.Sum256(body),
			output:      append([]byte(nil), updated...),
		})
	}
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
		return state.application, true
	}
	application, ok := value.(BusinessSystemPromptApplication)
	return application, ok
}

func (s *OpenAIGatewayService) rewriteBusinessSystemPromptJSONForRequest(c *gin.Context, body []byte, protocol string) []byte {
	application, ok := businessSystemPromptApplicationFromRequest(c, protocol)
	if !ok {
		return body
	}
	s.storeBusinessSystemPromptResponseRouteMetadata(c, body, application)
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
	s.storeBusinessSystemPromptSSERouteMetadata(c, body, application)
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

func isOfficialCodexBusinessSystemPromptRequest(ctx *gin.Context) bool {
	if ctx == nil || ctx.Request == nil {
		return false
	}
	return openaiutil.IsCodexOfficialClientByHeaders(
		ctx.Request.Header.Get("User-Agent"),
		ctx.Request.Header.Get("originator"),
	)
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
}

func (s *OpenAIGatewayService) storeBusinessSystemPromptResponseRouteMetadata(c *gin.Context, body []byte, application BusinessSystemPromptApplication) {
	if s == nil || s.businessPromptService == nil || !application.Applied ||
		(application.CompositionMode != BusinessSystemPromptCompositionOfflineBundle && application.CompositionMode != BusinessSystemPromptCompositionCodexSkillHybrid) {
		return
	}
	responseID := ""
	for _, path := range []string{"response.id", "id", "response_id"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			responseID = value
			break
		}
	}
	if responseID == "" {
		return
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	_ = s.businessPromptService.StoreResponseRouteMetadata(ctx, responseID, application, businessSystemPromptResponseRouteTTL)
}

func (s *OpenAIGatewayService) storeBusinessSystemPromptSSERouteMetadata(c *gin.Context, body []byte, application BusinessSystemPromptApplication) {
	for _, rawLine := range bytes.Split(body, []byte("\n")) {
		line := bytes.TrimSuffix(rawLine, []byte("\r"))
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || !json.Valid(payload) {
			continue
		}
		s.storeBusinessSystemPromptResponseRouteMetadata(c, payload, application)
	}
}

func appendBusinessSystemPromptApplicationToCacheKey(key string, application BusinessSystemPromptApplication) string {
	key = strings.TrimSpace(key)
	if key == "" || !application.Applied || application.Revision < 1 || strings.TrimSpace(application.SHA256) == "" {
		return key
	}
	suffix := ":business-system-prompt:" + strconv.FormatInt(application.Revision, 10) + ":" + strings.TrimSpace(application.SHA256)
	if (application.CompositionMode == BusinessSystemPromptCompositionOfflineBundle || application.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid) && application.BundleManifestSHA256 != "" && application.EffectiveSHA256 != "" {
		metadata := BusinessSystemPromptBundleMetadata{
			BundleID: application.BundleID, ManifestSHA256: application.BundleManifestSHA256,
			BaseSHA256: application.BaseSHA256, EffectiveSHA256: application.EffectiveSHA256,
			ByteLength:     application.EffectiveByteLength,
			RouteIDs:       append([]string(nil), application.RouteIDs...),
			DocumentPaths:  append([]string(nil), application.DocumentIDs...),
			ReferencePaths: append([]string(nil), application.ReferenceIDs...),
			Degraded:       application.Degraded,
		}
		suffix = ":" + metadata.CacheKey(application.Revision)
		if application.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
			suffix += ":bundle-revision:" + strconv.FormatInt(application.BundleRevision, 10) +
				":" + strings.ToLower(strings.TrimSpace(application.BundleArchiveSHA256))
		}
	}
	if strings.HasSuffix(key, suffix) {
		return key
	}
	return key + suffix
}

func businessSystemPromptRequestTextFromJSON(body []byte, protocol string) string {
	if !gjson.ValidBytes(body) {
		return ""
	}
	var items gjson.Result
	switch protocol {
	case BusinessSystemPromptProtocolResponses:
		input := gjson.GetBytes(body, "input")
		if input.Type == gjson.String {
			return strings.TrimSpace(input.String())
		}
		items = input
	case BusinessSystemPromptProtocolChat:
		items = gjson.GetBytes(body, "messages")
	default:
		return ""
	}
	if !items.IsArray() {
		return ""
	}
	values := items.Array()
	for i := len(values) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(values[i].Get("role").String()))
		if role != "user" {
			continue
		}
		if text := strings.TrimSpace(businessSystemPromptContentText(values[i].Get("content"))); text != "" {
			return text
		}
	}
	return ""
}

// BusinessSystemPromptRequestText extracts only the latest user-authored text
// used by deterministic offline routing. It never includes system/developer or
// assistant content.
func BusinessSystemPromptRequestText(body []byte, protocol string) string {
	return businessSystemPromptRequestTextFromJSON(body, protocol)
}

func businessSystemPromptContentText(content gjson.Result) string {
	if content.Type == gjson.String {
		return content.String()
	}
	if !content.IsArray() {
		return ""
	}
	parts := make([]string, 0, len(content.Array()))
	for _, part := range content.Array() {
		partType := strings.ToLower(strings.TrimSpace(part.Get("type").String()))
		if partType != "" && partType != "text" && partType != "input_text" {
			continue
		}
		text := strings.TrimSpace(part.Get("text").String())
		if text == "" {
			text = strings.TrimSpace(part.Get("content").String())
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func rewriteBusinessSystemPromptCacheKey(body []byte, application BusinessSystemPromptApplication) ([]byte, error) {
	value := gjson.GetBytes(body, "prompt_cache_key")
	if !value.Exists() || value.Type != gjson.String {
		return body, nil
	}
	effective := appendBusinessSystemPromptApplicationToCacheKey(value.String(), application)
	if effective == "" || effective == value.String() {
		return body, nil
	}
	updated, err := sjson.SetBytes(body, "prompt_cache_key", effective)
	if err != nil {
		return nil, fmt.Errorf("rewrite business system prompt cache key: %w", err)
	}
	return updated, nil
}
