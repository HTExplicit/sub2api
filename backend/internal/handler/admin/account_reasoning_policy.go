package admin

import "github.com/Wei-Shaw/sub2api/internal/service"

func mergeAccountUpdateExtra(existing, incoming map[string]any) map[string]any {
	merged := make(map[string]any, len(existing)+len(incoming))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range incoming {
		merged[key] = value
	}
	for _, key := range [...]string{service.OpenAIChatReasoningReplayEnabledExtraKey, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
		if _, provided := incoming[key]; !provided {
			// UpdateAccount owns preservation of omitted policies. Do not present a
			// legacy stored value as newly submitted admin input during re-import.
			delete(merged, key)
		}
	}
	return merged
}
