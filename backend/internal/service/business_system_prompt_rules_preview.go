package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type PromptRulesPreviewRequest struct {
	Model           string                            `json:"model,omitempty"`
	Protocol        string                            `json:"protocol"`
	Transport       string                            `json:"transport"`
	Compact         bool                              `json:"compact"`
	Body            json.RawMessage                   `json:"body"`
	Policy          *extensionv1.PromptRulePolicy     `json:"policy,omitempty"`
	Contents        map[string]PromptContentDraft     `json:"contents,omitempty"`
	Binding         *extensionv1.PromptAccountBinding `json:"binding,omitempty"`
	SimulateEnabled *bool                             `json:"simulate_enabled,omitempty"`
}

type PromptRulesPreviewResult struct {
	AccountID               int64                           `json:"account_id"`
	RequestedModel          string                          `json:"requested_model"`
	UpstreamModel           string                          `json:"upstream_model"`
	Protocol                string                          `json:"protocol"`
	Transport               string                          `json:"transport"`
	Body                    json.RawMessage                 `json:"body"`
	BeforeRules             json.RawMessage                 `json:"before_rules"`
	ClientControl           map[string]any                  `json:"client_control"`
	GatewayBaseInstructions string                          `json:"gateway_base_instructions"`
	Application             BusinessSystemPromptApplication `json:"application"`
	Continuation            bool                            `json:"continuation"`
	SequenceScope           string                          `json:"sequence_scope"`
	Simulated               bool                            `json:"simulated"`
	WireVerified            bool                            `json:"wire_verified"`
}

func (s *BusinessSystemPromptService) PreviewPromptRules(ctx context.Context, accountID int64, request PromptRulesPreviewRequest) (PromptRulesPreviewResult, error) {
	result := PromptRulesPreviewResult{AccountID: accountID, Transport: request.Transport, SequenceScope: "current_outbound_sequence"}
	if s.accountRepo == nil || !json.Valid(request.Body) || len(request.Body) > 1<<20 {
		return result, ErrBusinessSystemPromptInvalid
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return result, err
	}
	if !supportsPromptAccount(account) {
		return result, ErrPromptDeliveryUnsupported
	}
	snapshot, ok := s.CurrentSnapshot()
	if !ok || snapshot.RulePolicy == nil {
		return result, ErrBusinessSystemPromptUnavailable
	}
	if request.Policy != nil {
		snapshot.RulePolicy = request.Policy
		result.Simulated = true
	}
	if request.SimulateEnabled != nil {
		snapshot.Enabled = *request.SimulateEnabled
		result.Simulated = true
	}
	if request.Policy != nil || len(request.Contents) > 0 || (request.SimulateEnabled != nil && *request.SimulateEnabled) {
		if err := s.preparePromptRulesDraft(ctx, &snapshot, request.Contents); err != nil {
			return result, err
		}
		result.Simulated = true
	}
	if request.Binding != nil {
		cloned := *account
		cloned.Extra = maps.Clone(account.Extra)
		if cloned.Extra == nil {
			cloned.Extra = map[string]any{}
		}
		cloned.Extra[PromptAccountBindingExtraKey] = request.Binding
		account = &cloned
		result.Simulated = true
	}
	ginContext := &gin.Context{}
	ginContext.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/preview", nil)
	// Preview uses the selected account binding, including an unsaved override.
	// It must never refresh that selection from a live account during preparation.
	snapshot.Draft = true
	businessSystemPromptRequestSet(ginContext, businessSystemPromptContextKey(ginContext, businessSystemPromptRequestSnapshotKey, ""), snapshot)
	requested := strings.TrimSpace(gjson.GetBytes(request.Body, "model").String())
	if requested == "" {
		requested = strings.TrimSpace(request.Model)
	}
	if requested == "" {
		return result, fmt.Errorf("%w: preview model is required", ErrBusinessSystemPromptInvalid)
	}
	mapped := resolveOpenAIForwardModel(account, requested, "")
	if mapped == "" {
		mapped = requested
	}
	mapped = normalizeOpenAIModelForUpstream(account, mapped)
	businessSystemPromptRequestSet(ginContext, promptRequestedModelContextKey, requested)
	var before []byte
	var protocol, base, profile string
	if account.Platform == PlatformAnthropic || account.Platform == PlatformGemini || account.Platform == PlatformAntigravity || account.IsAnthropicProtocol() {
		ingress := request.Protocol
		if ingress == "" {
			ingress = BusinessSystemPromptProtocolResponses
		}
		mapped, err = ResolvePromptGatewayPreviewModel(account, ingress, request.Body, requested)
		if err != nil {
			return result, err
		}
		before, protocol, base, profile, err = PreparePromptGatewayPreview(ctx, ginContext, account, ingress, request.Body, mapped, s.previewSettings, s)
		if account.IsAnthropicOAuthOrSetupToken() || account.Platform == PlatformAntigravity {
			result.Simulated = true
		}
	} else {
		before, protocol, base, err = s.preparePromptRulesPreviewBody(account, request)
	}
	if err != nil {
		return result, err
	}
	result.Protocol, result.BeforeRules, result.GatewayBaseInstructions = protocol, before, base
	result.RequestedModel = requested
	result.UpstreamModel = gjson.GetBytes(before, "model").String()
	if protocol == BusinessSystemPromptProtocolGemini || result.UpstreamModel == "" {
		result.UpstreamModel = mapped
	}
	result.ClientControl = promptPreviewClientControl(request.Body)
	result.Continuation = gjson.GetBytes(before, "previous_response_id").String() != "" || gjson.GetBytes(before, "input.#(type==\"compaction\")").Exists()
	if result.Transport == "" {
		result.Transport = "http"
	}
	if result.Transport != "http" && result.Transport != "ws" {
		return result, ErrBusinessSystemPromptInvalid
	}
	if result.Transport == "ws" && protocol != BusinessSystemPromptProtocolResponses {
		return result, ErrPromptDeliveryUnsupported
	}
	target := businessSystemPromptTargetForAccount(account, protocol, request.Compact)
	target.RequestedModel, target.UpstreamModel = result.RequestedModel, result.UpstreamModel
	target.RequestProfile = profile
	snapshot, target, err = s.prepareAnthropicPromptSend(ginContext, account, before, snapshot, target)
	if err != nil {
		return result, err
	}
	updated, application, err := ApplyBusinessSystemPromptToJSONContext(ctx, before, snapshot, target)
	if err != nil {
		return result, err
	}
	if protocol == "messages" {
		billingUA := ""
		if profile == "generic-mimic" {
			billingUA = claude.DefaultUserAgent()
		}
		updated, application, err = FinalizePromptMessageApplication(updated, application, account, s.previewSettings, billingUA, ginContext)
		if err != nil {
			return result, err
		}
	}
	// This is the identical applier and final carrier validator used at live
	// HTTP/WS write boundaries. No credentials, ticket, network, or model IO.
	businessSystemPromptRequestSet(ginContext, businessSystemPromptContextKey(ginContext, businessSystemPromptRequestApplicationKey, protocol), cacheBusinessSystemPromptState(before, updated, snapshot, target, application))
	if err := validateBusinessSystemPromptFinal(ginContext, updated, protocol); err != nil {
		return result, err
	}
	if result.Transport == "ws" {
		updated, err = sjson.SetBytes(updated, "type", "response.create")
		if err != nil {
			return result, err
		}
	}
	result.Body, result.Application, result.WireVerified = updated, application, true
	return result, nil
}

func (s *BusinessSystemPromptService) preparePromptRulesPreviewBody(account *Account, request PromptRulesPreviewRequest) ([]byte, string, string, error) {
	ingress := request.Protocol
	if ingress == "" {
		ingress = BusinessSystemPromptProtocolResponses
	}
	if ingress != BusinessSystemPromptProtocolResponses && ingress != BusinessSystemPromptProtocolChat && ingress != "messages" {
		return nil, "", "", ErrBusinessSystemPromptInvalid
	}
	protocol := BusinessSystemPromptProtocolResponses
	if !account.UsesOpenAICodexProtocol() && shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		protocol = BusinessSystemPromptProtocolChat
	}
	body := []byte(request.Body)
	requested := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if requested == "" {
		return nil, "", "", fmt.Errorf("%w: preview model is required", ErrBusinessSystemPromptInvalid)
	}
	mapped := resolveOpenAIForwardModel(account, requested, "")
	if mapped == "" {
		mapped = requested
	}
	mapped = normalizeOpenAIModelForUpstream(account, mapped)
	var err error
	if ingress != protocol {
		var responses *apicompat.ResponsesRequest
		switch ingress {
		case BusinessSystemPromptProtocolChat:
			var chat apicompat.ChatCompletionsRequest
			if json.Unmarshal(body, &chat) != nil {
				return nil, "", "", ErrBusinessSystemPromptInvalid
			}
			responses, err = apicompat.ChatCompletionsToResponses(&chat)
		case "messages":
			var messages apicompat.AnthropicRequest
			if json.Unmarshal(body, &messages) != nil {
				return nil, "", "", ErrBusinessSystemPromptInvalid
			}
			responses, err = apicompat.AnthropicToResponses(&messages)
		default:
			responses = &apicompat.ResponsesRequest{}
			err = json.Unmarshal(body, responses)
		}
		if err != nil {
			return nil, "", "", err
		}
		if protocol == BusinessSystemPromptProtocolChat {
			chat, err := apicompat.ResponsesToChatCompletionsRequest(responses)
			if err != nil {
				return nil, "", "", err
			}
			body, err = json.Marshal(chat)
			if err != nil {
				return nil, "", "", err
			}
		} else {
			body, err = json.Marshal(responses)
			if err != nil {
				return nil, "", "", err
			}
		}
	}
	body, err = sjson.SetBytes(body, "model", mapped)
	if err != nil {
		return nil, "", "", err
	}
	base := ""
	if account.UsesOpenAICodexProtocol() {
		var object map[string]any
		if json.Unmarshal(body, &object) != nil {
			return nil, "", "", ErrBusinessSystemPromptInvalid
		}
		previous, previousExists := object["previous_response_id"]
		defaultMode := OpenAIWSIngressModeCtxPool
		if s.previewConfig != nil && s.previewConfig.Gateway.OpenAIWS.IngressModeDefault != "" {
			defaultMode = s.previewConfig.Gateway.OpenAIWS.IngressModeDefault
		}
		passthrough := (request.Transport == "ws" && account.ResolveOpenAIResponsesWebSocketV2Mode(defaultMode) == OpenAIWSIngressModePassthrough) || (request.Transport != "ws" && ingress == BusinessSystemPromptProtocolResponses && account.IsOpenAIPassthroughEnabled())
		if passthrough {
			if request.Transport != "ws" && detectOpenAIPassthroughInstructionsRejectReason(mapped, body) != "" {
				return nil, "", "", fmt.Errorf("%w: Codex passthrough requires nonempty client instructions", ErrBusinessSystemPromptInvalid)
			}
			if request.Transport != "ws" && isOpenAICodexModel(mapped) && !gjson.GetBytes(body, "instructions").Exists() {
				base = defaultCodexSynthInstructions(mapped)
				body, err = sjson.SetBytes(body, "instructions", base)
			}
			return body, protocol, base, err
		}
		// Native Responses adds its model base before promoting client system
		// text. Chat and Messages adapters deliberately leave defaults disabled.
		skipDefault := ingress != BusinessSystemPromptProtocolResponses
		if !skipDefault && isInstructionsEmpty(object) {
			base = defaultCodexSynthInstructions(mapped)
			object["instructions"] = base
		}
		omitSystem := ingress != "messages" && !strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String()), "json_object")
		transformed := applyCodexOAuthTransformWithOptions(object, codexOAuthTransformOptions{IsCodexCLI: true, IsCompact: request.Compact, SkipDefaultInstructions: skipDefault, PreserveToolCallIDs: ingress != BusinessSystemPromptProtocolResponses, OmitPromotedSystemMessagesFromInput: omitSystem})
		if transformed.Error != nil {
			return nil, "", "", transformed.Error
		}
		if previousExists {
			object["previous_response_id"] = previous
		}
		if ingress == "messages" {
			forced := ""
			if s.previewConfig != nil {
				forced = s.previewConfig.Gateway.ForcedCodexInstructionsTemplate
			}
			existing, _ := object["instructions"].(string)
			if strings.TrimSpace(existing) == "" {
				existing = extractPromptLikeInstructionsFromInput(object)
			}
			if _, err := applyForcedCodexInstructionsTemplate(object, forced, forcedCodexInstructionsTemplateData{ExistingInstructions: strings.TrimSpace(existing), OriginalModel: requested, NormalizedModel: normalizeCodexModel(requested), BillingModel: mapped, UpstreamModel: transformed.NormalizedModel}); err != nil {
				return nil, "", "", err
			}
			if forced != "" {
				base, _ = object["instructions"].(string)
			}
			if shouldAutoInjectPromptCacheKeyForCompat(mapped) {
				appendOpenAICompatClaudeCodeTodoGuardToRequestBody(object)
			}
		}
		if skipDefault {
			ensureCodexOAuthInstructionsField(object)
		}
		body, err = json.Marshal(object)
		if err != nil {
			return nil, "", "", err
		}
	}
	return body, protocol, base, nil
}

func promptPreviewClientControl(body []byte) map[string]any {
	control := map[string]any{}
	for _, field := range []string{"instructions", "system", "systemInstruction"} {
		value := gjson.GetBytes(body, field)
		if value.Exists() {
			control[field] = value.Value()
		}
	}
	for _, field := range []string{"messages", "input"} {
		items := []any{}
		gjson.GetBytes(body, field).ForEach(func(_, item gjson.Result) bool {
			role := item.Get("role").String()
			if role == "system" || role == "developer" {
				items = append(items, item.Value())
			}
			return true
		})
		if len(items) > 0 {
			control[field] = items
		}
	}
	return control
}
