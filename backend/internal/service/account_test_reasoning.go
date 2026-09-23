package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
)

const accountTestReasoningContextKey = "account_test_reasoning_effort"

// AccountTestReasoningOptions uses the same account mapping and capability
// sources as the model catalog. Unknown model capabilities stay unknown.
func AccountTestReasoningOptions(account *Account, model string) ([]string, string) {
	if account == nil || strings.TrimSpace(model) == "" {
		return nil, ""
	}
	model = account.GetMappedModel(strings.TrimSpace(model))
	if !accountTestSupportsReasoningWire(account, model) || isOpenAIImageModel(model) || isGrokVideoGenerationModel(model) {
		return nil, ""
	}
	if IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		if capability, ok := ResolveCindyCapability(model); ok {
			return capability.CodexReasoningEfforts(), capability.DefaultReasoningEffort
		}
		return nil, ""
	}
	if metadata, ok := account.GetUpstreamModelMetadata(model); ok && metadata.Reasoning != nil {
		if !*metadata.Reasoning {
			return nil, ""
		}
		levels := normalizeReasoningLevels(metadata.SupportedReasoningLevels)
		if isOpenAIGPT6SolOrLunaModel(model) {
			levels = intersectOrderedStrings(levels, openai.GPT6APIReasoningEfforts())
		}
		return levels, normalizeReasoningLevel(metadata.DefaultReasoningLevel)
	}
	if account.IsOpenAIApiKey() && isOpenAIGPT6SolOrLunaModel(model) {
		return openai.GPT6APIReasoningEfforts(), "medium"
	}
	var levels []configuredCodexReasoningLevel
	switch {
	case account.Platform == PlatformGrok:
		levels = configuredCodexGrokReasoningLevels(model)
	case account.IsOpenAI() && isOpenAICodexReasoningGPTModel(model):
		levels = configuredCodexGPTReasoningLevels(model)
	}
	out := make([]string, 0, len(levels))
	for _, level := range levels {
		// Ultra is client orchestration, never a native inference effort.
		if level.Effort == "ultra" && isOpenAIGPT6SolOrLunaModel(model) {
			continue
		}
		out = append(out, level.Effort)
	}
	return out, ""
}

func accountTestSupportsReasoningWire(account *Account, model string) bool {
	switch {
	case account.IsOpenCodeGo():
		protocol := openCodeGoNativeProtocol(account, model)
		return protocol == APIProtocolResponses || protocol == APIProtocolChatCompletions
	case account.IsCNProvider():
		protocol := account.GetAPIProtocol()
		// The adaptive test verifies all native endpoints, including Messages,
		// whose effort contract differs. Never silently omit a chosen effort on
		// one leg of that test.
		return protocol == APIProtocolResponses || protocol == APIProtocolChatCompletions
	case account.IsOpenAI(), account.Platform == PlatformGrok:
		return true
	default:
		return false
	}
}

func ValidateAccountTestReasoning(account *Account, model, mode, effort string) error {
	return ValidateAccountTestReasoningContext(context.Background(), account, model, mode, effort)
}

func ValidateAccountTestReasoningContext(ctx context.Context, account *Account, model, mode, effort string) error {
	if effort == "" {
		return nil
	}
	if account == nil {
		return errors.New("account is unavailable")
	}
	levels, _ := AccountTestReasoningOptions(account, model)
	return accountToolsOperationForAccount(ctx, account, "test.reasoning", extensionv1.ReasoningSelection{Mode: mode, Effort: effort, Levels: levels}, nil)
}

func applyAccountTestReasoning(c *gin.Context, payload map[string]any, chat bool) {
	if effort := c.GetString(accountTestReasoningContextKey); effort != "" {
		if chat {
			payload["reasoning_effort"] = effort
		} else {
			payload["reasoning"] = map[string]any{"effort": effort}
		}
		c.Set("account_test_effective_reasoning_effort", effort)
	}
}
