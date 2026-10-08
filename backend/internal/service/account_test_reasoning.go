package service

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const accountTestReasoningContextKey = "account_test_reasoning_effort"

// AccountTestReasoningOptions uses the same account mapping and capability
// sources as the model catalog. Unknown model capabilities stay unknown.
func AccountTestReasoningOptions(account *Account, model string) (efforts []string, defaultEffort string) {
	// Ultra coordinates client-side work; it is never a single inference effort.
	// Filter every capability source, including aliases and upstream metadata.
	defer func() {
		efforts = slices.DeleteFunc(slices.Clone(efforts), func(effort string) bool {
			return normalizeReasoningLevel(effort) == "ultra"
		})
		if normalizeReasoningLevel(defaultEffort) == "ultra" {
			defaultEffort = ""
		}
	}()
	if account == nil || strings.TrimSpace(model) == "" {
		return nil, ""
	}
	model, ok := accountTestUpstreamModel(account, strings.TrimSpace(model))
	if !ok || !accountTestSupportsReasoningWire(account, model) || isOpenAIImageModel(model) || isGrokVideoGenerationModel(model) {
		return nil, ""
	}
	metadata, known := account.GetUpstreamModelMetadata(model)
	if account.Platform == PlatformAnthropic {
		// Claude effort levels are a property of the model; the account's
		// upstream metadata can only rule the model out.
		if known && metadata.Reasoning != nil && !*metadata.Reasoning {
			return nil, ""
		}
		return claude.EffortLevelsForModel(model), claude.DefaultEffortForModel(model)
	}
	if known && metadata.Reasoning != nil {
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
		out = append(out, level.Effort)
	}
	return out, ""
}

// accountTestUpstreamModel resolves the model ID the account test sends
// upstream; ok is false when the test cannot send the model at all.
func accountTestUpstreamModel(account *Account, model string) (string, bool) {
	if account.Platform == PlatformAnthropic {
		return accountTestClaudeUpstreamModel(account, model)
	}
	return account.GetMappedModel(model), true
}

// accountTestGrokUpstreamModel resolves Grok aliases the way forwardGrokResponses
// does. Effort rules apply to this ID, not to the mapped alias the test sends.
func accountTestGrokUpstreamModel(model string) string {
	return xai.ResolveGrokTextResponsesModelID(model, grokDefaultResponsesModel)
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
	case account.IsOpenAI():
		return true
	case account.Platform == PlatformGrok:
		// Responses forwarding removes the effort for every other model.
		return grokSupportsReasoningEffort(accountTestGrokUpstreamModel(model))
	case account.Platform == PlatformAnthropic:
		return !account.IsBedrock() || bedrockKeepsOutputConfigEffort(model)
	default:
		return false
	}
}

// ValidateAccountTestReasoning accepts a reasoning effort only in the text
// test modes and only when the tested account model offers it.
func ValidateAccountTestReasoning(account *Account, model, mode, effort string) error {
	if effort == "" {
		return nil
	}
	if normalizeReasoningLevel(effort) == "ultra" {
		return errors.New("reasoning effort is not supported by the selected account model")
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

// accountTestChatBody marshals a Chat Completions test payload. Chat
// forwarding moves a glm-* model's effort onto z.ai's high/max scale, so the
// effective effort is read from the final body.
func accountTestChatBody(c *gin.Context, payload map[string]any, model string) []byte {
	applyAccountTestReasoning(c, payload, true)
	body, _ := json.Marshal(payload)
	body, _ = NormalizeGLMOpenAIReasoningEffort(body, model)
	markAccountTestEffectiveReasoning(c, body, "reasoning_effort")
	return body
}

// markAccountTestEffectiveReasoning reports the effort the final wire body
// carries at path; forwarding rules may have rewritten or removed the
// selected one.
func markAccountTestEffectiveReasoning(c *gin.Context, body []byte, path string) {
	if c.GetString(accountTestReasoningContextKey) == "" {
		return
	}
	if effort := gjson.GetBytes(body, path).String(); effort != "" {
		c.Set("account_test_effective_reasoning_effort", effort)
	}
}

// accountTestClaudeEffortMaxTokens leaves room for a visible answer after
// adaptive thinking, whose tokens count toward max_tokens. 64000 is the
// documented guidance for xhigh/max effort and the smallest output cap among
// the effort families (Opus 4.5).
const accountTestClaudeEffortMaxTokens = 64000

// accountTestClaudeBody marshals a Claude Messages test payload. A selected
// effort is written the way clients send it through production forwarding:
// output_config.effort, adaptive thinking where the model pairs effort with
// it, and no temperature (1 is the API default and newer families reject the
// field). Without a selected effort the payload is sent as built.
func accountTestClaudeBody(c *gin.Context, payload map[string]any, model string) ([]byte, error) {
	effort := c.GetString(accountTestReasoningContextKey)
	if effort == "" {
		body, _ := json.Marshal(payload)
		return body, nil
	}
	payload["output_config"] = map[string]any{"effort": effort}
	if claude.EffortUsesAdaptiveThinking(model) {
		payload["thinking"] = map[string]any{"type": "adaptive"}
	}
	delete(payload, "temperature")
	payload["max_tokens"] = accountTestClaudeEffortMaxTokens
	body, _ := json.Marshal(payload)
	// Gateway forwarding rejects the same settings before any upstream request.
	return body, validateClaude55Request(body, model)
}
