package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// managedMessagesBridgeDefaults is deliberately a service-level policy rather
// than a change to the shared apicompat converters. Private groups, Codex/GPT,
// OAuth, Cindy and native Anthropic forwarding retain their existing shape.
type managedMessagesBridgeDefaults struct {
	omitImplicitReasoning bool
	disableDeepseekV4     bool
}

func managedMessagesDefaultsForRequest(ctx context.Context, account *Account, requested *apicompat.AnthropicRequest, upstreamModel string) managedMessagesBridgeDefaults {
	unchanged := managedMessagesBridgeDefaults{}
	managed, ok := ManagedModelRequestFromContext(ctx)
	if !ok || managed.Endpoint != CompositeRouteEndpointMessages || account == nil || requested == nil ||
		account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey ||
		strings.TrimSpace(managed.Route.PublicModel) == "" || strings.TrimSpace(upstreamModel) == "" ||
		account.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1 ||
		IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		managedMessagesIsGPTFamily(managed.Route.PublicModel) || managedMessagesIsGPTFamily(upstreamModel) {
		return unchanged
	}
	// Explicit effort and explicit enabled/adaptive thinking keep the existing
	// conversion and group-policy semantics, including validation/rejection. Do
	// not silently disable a user's thinking request just to make tool_choice
	// accepted by a provider. A contradictory disabled+explicit-effort request
	// also retains existing precedence rather than inventing a new policy here.
	if requested.OutputConfig != nil && strings.TrimSpace(requested.OutputConfig.Effort) != "" {
		return unchanged
	}
	if requested.Thinking != nil && !strings.EqualFold(strings.TrimSpace(requested.Thinking.Type), "disabled") {
		return unchanged
	}
	profile := managedMessagesBridgeDefaults{omitImplicitReasoning: true}
	public := strings.ToLower(strings.TrimSpace(managed.Route.PublicModel))
	actual := strings.ToLower(lastOpenAIModelSegment(strings.TrimSpace(upstreamModel)))
	// This is an exact, verified V4 text-model contract, not a prefix rule for
	// arbitrary DeepSeek aliases, future models, or unrelated compatible hosts.
	profile.disableDeepseekV4 = (public == "deepseek-v4-flash" || public == "deepseek-v4-pro") &&
		(actual == "deepseek-v4-flash" || actual == "deepseek-v4-pro")
	return profile
}

func managedMessagesIsGPTFamily(model string) bool {
	model = strings.ToLower(lastOpenAIModelSegment(strings.TrimSpace(model)))
	if strings.HasPrefix(model, "gpt-") {
		return true
	}
	for _, family := range []string{"o1", "o3", "o4"} {
		if model == family || strings.HasPrefix(model, family+"-") {
			return true
		}
	}
	// The spelling helper is GPT-specific and may return empty for other model
	// families, so recognize o-series above before consulting it. Normalize
	// only this classification key, never the actual wire model ID.
	return strings.HasPrefix(strings.ToLower(canonicalizeOpenAIModelAliasSpelling(model)), "gpt-")
}

func (profile managedMessagesBridgeDefaults) applyResponses(request *apicompat.ResponsesRequest) {
	if !profile.omitImplicitReasoning || request == nil {
		return
	}
	// These fields were manufactured by AnthropicToResponses, not supplied by
	// a Messages client. Preserve input/opaque history, tool choice, tools,
	// parallel-tool policy, token limits, store and all other converted fields.
	request.Reasoning = nil
	request.Text = nil
	request.Include = nil
	if profile.disableDeepseekV4 {
		// DeepSeek V4 Responses defaults to thinking when effort is omitted.
		// "none" expresses Messages' non-thinking default on this specific wire.
		// Official contract: https://api-docs.deepseek.com/api/create-response/
		request.Reasoning = &apicompat.ResponsesReasoning{Effort: "none"}
	}
}

func (profile managedMessagesBridgeDefaults) applyChat(request *apicompat.ChatCompletionsRequest) {
	if profile.omitImplicitReasoning && request != nil {
		request.ReasoningEffort = ""
	}
}

func (profile managedMessagesBridgeDefaults) patchChatBody(body []byte) ([]byte, error) {
	if !profile.disableDeepseekV4 {
		return body, nil
	}
	// Chat has a different documented control; reasoning_effort:"none" is not
	// a valid substitute. Patch only the freshly converted body because the
	// shared Chat request type intentionally has no provider-specific field.
	// Official contract: https://api-docs.deepseek.com/api/create-chat-completion/
	out, err := sjson.SetBytes(body, "thinking", map[string]string{"type": "disabled"})
	if err != nil {
		return nil, err
	}
	// DeepSeek Chat's documented budget field is max_tokens. Move the exact
	// positive converted value without increasing it; an already-present
	// max_tokens keeps its existing precedence. Other routes keep their wire.
	maxCompletion := gjson.GetBytes(out, "max_completion_tokens")
	if maxCompletion.Type == gjson.Number && maxCompletion.Int() > 0 {
		if !gjson.GetBytes(out, "max_tokens").Exists() {
			out, err = sjson.SetRawBytes(out, "max_tokens", []byte(maxCompletion.Raw))
			if err != nil {
				return nil, err
			}
		}
		return sjson.DeleteBytes(out, "max_completion_tokens")
	}
	return out, nil
}
