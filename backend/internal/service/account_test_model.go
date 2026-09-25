package service

import (
	"slices"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// PickConnectionTestModel chooses the model of a connection test that does not
// name one: the platform's upstream default test model when the account serves
// it, otherwise the first text-chat model of the account's own model list
// (candidates, in display order) or, without a list, of its model mapping.
// "" leaves the choice to the platform tester's own default.
func PickConnectionTestModel(account *Account, candidates ...string) string {
	if account == nil {
		return ""
	}
	preferred := connectionTestDefaultModel(account)
	if len(candidates) == 0 {
		if account.IsModelSupported(preferred) {
			return preferred
		}
		for model := range account.GetModelMapping() {
			candidates = append(candidates, model)
		}
		sort.Strings(candidates)
	} else if slices.Contains(candidates, preferred) {
		return preferred
	}
	for _, model := range candidates {
		if AccountTestSupportsTextConversation(account, model) {
			return model
		}
	}
	return ""
}

// connectionTestDefaultModel mirrors the empty-model default of the platform
// tester that TestAccountConnection dispatches to.
func connectionTestDefaultModel(account *Account) string {
	switch {
	case account.IsOpenCodeGo():
		return DefaultOpenCodeGoTestModel
	case account.IsCNProvider():
		if account.GetAPIProtocol() == APIProtocolAnthropic {
			return claude.DefaultTestModel
		}
		return openai.DefaultTestModel
	case account.IsOpenAI():
		return openai.DefaultTestModel
	case account.IsGemini():
		return geminicli.DefaultTestModel
	case account.Platform == PlatformGrok:
		return grokDefaultResponsesModel
	case account.Platform == PlatformAntigravity:
		return defaultAntigravityTestModel
	default:
		return claude.DefaultTestModel
	}
}

// AccountTestSupportsTextConversation reports whether a model of the account
// can answer a text connection test: image, audio, embedding, realtime, speech
// and review-only models are excluded, before and after the account mapping.
// Only local mappings are consulted, never another provider snapshot.
func AccountTestSupportsTextConversation(account *Account, model string) bool {
	if account == nil || strings.TrimSpace(model) == "" || strings.Contains(model, "*") {
		return false
	}
	mapped := model
	mapping := account.GetModelMapping()
	if target, matched := resolveRequestedModelInMapping(mapping, model); matched {
		mapped = target
	} else if target, matched := resolveRequestedModelInMapping(mapping, normalizeRequestedModelForLookup(account.Platform, model)); matched {
		mapped = target
	}
	for _, id := range []string{model, mapped} {
		if isOpenAIImageModel(id) || isGrokImageGenerationModel(id) || isGrokVideoGenerationModel(id) || isImageGenerationModel(id) {
			return false
		}
		lower := strings.ToLower(id)
		if lower == "codex-auto-review" {
			return false
		}
		for _, marker := range []string{"image", "audio", "embedding", "realtime", "tts", "transcribe", "whisper", "moderation", "dall-e", "sora", "voice"} {
			if strings.Contains(lower, marker) {
				return false
			}
		}
	}
	return true
}
