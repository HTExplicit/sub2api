package service

import "context"

type managedModelProtocolOverlayKey struct{}

type managedModelProtocolOverlay struct {
	account             *Account
	originalExtra       map[string]any
	originalCredentials map[string]any
}

// ManagedModelConfiguredProtocol is the actual fixed transport, not the
// provider's brand. Adaptive CN accounts have no single protocol until an
// ingress endpoint is selected, so callers must not infer equivalence from it.
func ManagedModelConfiguredProtocol(account *Account) string {
	if account == nil {
		return ""
	}
	if account.Platform == PlatformAnthropic {
		return CompositeRouteEndpointMessages
	}
	if account.IsCNProvider() {
		switch account.GetAPIProtocol() {
		case APIProtocolAnthropic:
			return CompositeRouteEndpointMessages
		case APIProtocolResponses:
			return CompositeRouteEndpointResponses
		case APIProtocolChatCompletions:
			return CompositeRouteEndpointChatCompletions
		default:
			return ""
		}
	}
	if account.Platform == PlatformOpenAI {
		if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
			return CompositeRouteEndpointChatCompletions
		}
		return CompositeRouteEndpointResponses
	}
	return ""
}

func managedModelEffectiveProtocol(account *Account, branch ManagedModelRouteBranch, endpoint string) string {
	if branch.UpstreamProtocol != "" {
		return branch.UpstreamProtocol
	}
	if protocol := ManagedModelConfiguredProtocol(account); protocol != "" {
		return protocol
	}
	if account != nil && account.IsCNProvider() && account.IsAdaptiveAPIProtocol() {
		switch endpoint {
		case CompositeRouteEndpointMessages, CompositeRouteEndpointChatCompletions:
			return endpoint
		case CompositeRouteEndpointResponses:
			if account.SupportsNativeCNResponses() {
				return endpoint
			}
			return CompositeRouteEndpointChatCompletions
		}
	}
	return ""
}

// WithManagedModelAccountProtocol selects the wire proven for this branch.
// Overrides live only on this call's clone; cached accounts, private groups and
// the stored capability fingerprint remain untouched.
func WithManagedModelAccountProtocol(ctx context.Context, account *Account, branch ManagedModelRouteBranch) (context.Context, *Account, error) {
	ctx = WithManagedModelBranch(ctx, branch)
	if account == nil || ValidateManagedModelAccount(ctx, account, branch.Selector) != nil {
		return ctx, nil, ErrManagedModelRouteUnavailable
	}
	if branch.UpstreamProtocol == "" {
		return ctx, account, nil
	}
	if account.Type != AccountTypeAPIKey || !ManagedModelBranchProtocolSupported(account.Platform, branch.UpstreamProtocol) {
		return ctx, nil, ErrManagedModelRouteUnavailable
	}
	if account.Platform == PlatformAnthropic {
		return ctx, account, nil
	}
	clone := *account
	clone.Credentials = make(map[string]any, len(account.Credentials))
	for key, value := range account.Credentials {
		clone.Credentials[key] = value
	}
	mapping := make(map[string]any, len(account.GetModelMapping()))
	for key, value := range account.GetModelMapping() {
		mapping[key] = value
	}
	clone.Credentials["model_mapping"] = mapping
	clone.modelMappingCacheReady = false
	clone.Extra = make(map[string]any, len(account.Extra)+2)
	for key, value := range account.Extra {
		clone.Extra[key] = value
	}
	if account.IsCNProvider() {
		protocol := branch.UpstreamProtocol
		baseURL := ""
		switch {
		case protocol == CompositeRouteEndpointMessages:
			protocol = APIProtocolAnthropic
			baseURL = account.GetAnthropicProtocolBaseURL()
		case account.IsAnthropicProtocol():
			// Match the evidence probe: never turn a configured Anthropic
			// relay into a different provider's default OpenAI endpoint.
			return ctx, nil, ErrManagedModelRouteUnavailable
		case account.IsAdaptiveAPIProtocol():
			baseURL = account.GetCNProtocolBaseURL(protocol)
		default:
			baseURL = account.GetOpenAIBaseURL()
		}
		if baseURL == "" {
			return ctx, nil, ErrManagedModelRouteUnavailable
		}
		clone.Credentials["api_protocol"] = protocol
		clone.Credentials["base_url"] = baseURL
	}
	if branch.UpstreamProtocol == CompositeRouteEndpointResponses {
		clone.Extra["openai_responses_mode"] = "force_responses"
		clone.Extra["openai_responses_supported"] = true
	} else {
		clone.Extra["openai_responses_mode"] = "force_chat_completions"
		clone.Extra["openai_responses_supported"] = false
	}
	return context.WithValue(ctx, managedModelProtocolOverlayKey{}, managedModelProtocolOverlay{account: &clone, originalExtra: account.Extra, originalCredentials: account.Credentials}), &clone, nil
}

func managedModelFingerprintForRequest(ctx context.Context, account *Account) string {
	if ctx == nil {
		return managedModelSchedulingFingerprint(account)
	}
	if overlay, ok := ctx.Value(managedModelProtocolOverlayKey{}).(managedModelProtocolOverlay); ok && overlay.account == account {
		original := *account
		original.Extra = make(map[string]any, len(account.Extra))
		for key, value := range account.Extra {
			original.Extra[key] = value
		}
		for _, key := range []string{"openai_responses_mode", "openai_responses_supported"} {
			if value, exists := overlay.originalExtra[key]; exists {
				original.Extra[key] = value
			} else {
				delete(original.Extra, key)
			}
		}
		if account.IsCNProvider() {
			original.Credentials = make(map[string]any, len(account.Credentials))
			for key, value := range account.Credentials {
				original.Credentials[key] = value
			}
			for _, key := range []string{"api_protocol", "base_url"} {
				if value, exists := overlay.originalCredentials[key]; exists {
					original.Credentials[key] = value
				} else {
					delete(original.Credentials, key)
				}
			}
		}
		return managedModelSchedulingFingerprint(&original)
	}
	return managedModelSchedulingFingerprint(account)
}
