package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ResolvePromptGatewayPreviewModel shares each provider's live model selection
// before body conversion, so upstream-scoped rules see the real destination.
func ResolvePromptGatewayPreviewModel(account *Account, ingress string, body []byte, requested string) (string, error) {
	if account == nil {
		return "", ErrBusinessSystemPromptInvalid
	}
	if account.IsBedrock() {
		mapped, ok := ResolveBedrockModelID(account, requested)
		if !ok {
			return "", ErrPromptDeliveryUnsupported
		}
		return mapped, nil
	}
	if account.Platform == PlatformAntigravity {
		if account.Type == AccountTypeUpstream {
			return requested, nil
		}
		adapter := &AntigravityGatewayService{}
		if ingress == "gemini" {
			mapped := adapter.getMappedModelForThinkingLevel(account, requested, geminiThinkingLevelFromBody(body))
			if mapped == "" {
				return "", ErrPromptDeliveryUnsupported
			}
			return mapped, nil
		}
		anthropicBody, err := promptPreviewAnthropicInput(ingress, body, requested)
		if err != nil {
			return "", err
		}
		var request antigravity.ClaudeRequest
		if err := json.Unmarshal(anthropicBody, &request); err != nil {
			return "", err
		}
		mapped := adapter.getMappedModelForThinkingLevel(account, requested, geminiThinkingLevelFromClaudeThinking(request.Thinking))
		if mapped == "" {
			return "", ErrPromptDeliveryUnsupported
		}
		thinking := request.Thinking != nil && (request.Thinking.Type == "enabled" || request.Thinking.Type == "adaptive")
		return applyThinkingModelSuffix(mapped, thinking), nil
	}
	if account.Type == AccountTypeAPIKey {
		return account.GetMappedModel(requested), nil
	}
	if account.Type == AccountTypeServiceAccount {
		if mapped, matched := account.ResolveMappedModel(requested); matched {
			return mapped, nil
		}
		if account.Platform == PlatformAnthropic {
			return normalizeVertexAnthropicModelID(claude.NormalizeModelID(requested)), nil
		}
	}
	if account.Platform == PlatformAnthropic {
		return claude.NormalizeModelID(requested), nil
	}
	return requested, nil
}

// PreparePromptGatewayPreview uses the same body converters and required
// provider preparation as the live non-OpenAI gateways. It performs no token,
// credential, fingerprint, or upstream access. OAuth identity and envelope
// values are consequently simulated. The caller installs its draft snapshot
// on c before calling and applies the unified prompt engine to the result.
func PreparePromptGatewayPreview(ctx context.Context, c *gin.Context, account *Account, ingress string, body []byte, model string, settings *SettingService, prompts *BusinessSystemPromptService) (clean []byte, protocol, base, profile string, err error) {
	if account == nil {
		return nil, "", "", "", ErrBusinessSystemPromptInvalid
	}
	rememberPromptRequestedModel(c, body)
	if ingress != "gemini" {
		body, err = promptPreviewAnthropicInput(ingress, body, model)
		if err != nil {
			return nil, "", "", "", err
		}
	}
	if account.Platform == PlatformGemini {
		if ingress != "gemini" {
			body, err = convertClaudeMessagesToGeminiGenerateContent(body)
			if err != nil {
				return nil, "", "", "", err
			}
		} else if filtered, filterErr := filterEmptyPartsFromGeminiRequest(body); filterErr == nil {
			body = filtered
		}
		body = ensureGeminiFunctionCallThoughtSignatures(body)
		if ingress != "gemini" && (account.Type != AccountTypeOAuth || account.GetCredential("project_id") == "") {
			body = normalizeGeminiRequestForAIStudio(body)
		}
		return body, "gemini", "", "", nil
	}
	if account.Platform == PlatformAntigravity && account.Type != AccountTypeUpstream {
		adapter := &AntigravityGatewayService{settingService: settings}
		if ingress == "gemini" {
			body, err = injectIdentityPatchToGeminiRequest(body)
			if err != nil {
				return nil, "", "", "", err
			}
			if cleaned, cleanErr := cleanGeminiRequest(body); cleanErr == nil {
				body = cleaned
			}
			if reconciled, reconcileErr := enableMixedGeminiToolInvocations(body); reconcileErr == nil {
				body = reconciled
			}
			return body, "gemini", antigravity.GetDefaultIdentityPatch(), "", nil
		}
		var request antigravity.ClaudeRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, "", "", "", err
		}
		opts := adapter.getClaudeTransformOptions(ctx)
		opts.EnableIdentityPatch = true
		wrapped, err := antigravity.TransformClaudeToGeminiWithOptions(&request, "preview", model, opts)
		if err != nil {
			return nil, "", "", "", err
		}
		identity := opts.IdentityPatch
		if identity == "" {
			identity = antigravity.GetDefaultIdentityPatch()
		}
		return []byte(gjson.GetBytes(wrapped, "request").Raw), "gemini", identity, "", nil
	}
	if ingress == "gemini" {
		return nil, "", "", "", ErrPromptDeliveryUnsupported
	}
	adapter := &GatewayService{settingService: settings, businessPromptService: prompts}
	mimic := account.IsAnthropicOAuthOrSetupToken()
	if mimic && c != nil && c.Request != nil {
		metadata := gjson.GetBytes(body, "metadata.user_id").String()
		mimic = !IsClaudeCodeClient(ctx) && !isClaudeCodeClient(c.GetHeader("User-Agent"), metadata) && (metadata == "" || !systemHasBillingAttributionBlock(body))
	}
	setBusinessSystemPromptRequestProfile(c, account, mimic)
	if mimic {
		body, err = adapter.prepareClaudeCodeOAuthMimicryToBody(ctx, c, account, body, json.RawMessage(gjson.GetBytes(body, "system").Raw), model)
		if err != nil {
			return nil, "", "", "", err
		}
		profile = "generic-mimic"
		base = gjson.GetBytes(body, "system").Raw
	} else if account.IsAnthropicOAuthOrSetupToken() {
		profile = "claude-code"
	}
	if next, changed := adapter.normalizeClientDatelineIfEnabled(ctx, account, body); changed {
		body = next
	}
	body = enforceCacheControlLimit(body)
	body = FilterThinkingBlocks(FilterWebSearchHistoryBlocks(StripEmptyTextBlocks(body), model), model)
	if ResolveThinkingProtocol(model) == ThinkingProtocolPassbackRequired {
		if normalized, changed := NormalizeChineseLLMThinking(body, model); changed {
			body = normalized
		}
	}
	return body, "messages", base, profile, nil
}

func promptPreviewAnthropicInput(ingress string, body []byte, model string) ([]byte, error) {
	if ingress == "messages" {
		return ReplaceModelInBody(body, model), nil
	}
	var request *apicompat.ResponsesRequest
	switch ingress {
	case "chat":
		var chat apicompat.ChatCompletionsRequest
		if err := json.Unmarshal(body, &chat); err != nil {
			return nil, err
		}
		converted, err := apicompat.ChatCompletionsToResponses(&chat)
		if err != nil {
			return nil, err
		}
		request = converted
	case "responses":
		adapted, _, err := adaptResponsesClientToolsForAnthropic(body)
		if err != nil {
			return nil, err
		}
		request = &apicompat.ResponsesRequest{}
		if err := json.Unmarshal(adapted, request); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: unknown ingress protocol", ErrBusinessSystemPromptInvalid)
	}
	request.Model = model
	converted, err := apicompat.ResponsesToAnthropicRequest(request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}
