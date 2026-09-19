package service

import (
	"errors"
	"slices"
	"strings"

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
		return normalizeReasoningLevels(metadata.SupportedReasoningLevels), normalizeReasoningLevel(metadata.DefaultReasoningLevel)
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
	if effort == "" {
		return nil
	}
	if effort != strings.TrimSpace(effort) || len(effort) > 32 || (mode != "" && mode != AccountTestModeDefault && mode != AccountTestModeGrokText) {
		return errors.New("reasoning effort is unsupported for this test mode")
	}
	levels, _ := AccountTestReasoningOptions(account, model)
	if !slices.Contains(levels, effort) {
		return errors.New("reasoning effort is not supported by the selected account model")
	}
	return nil
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
