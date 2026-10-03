package service

import (
	"slices"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
)

// PickConnectionTestModel chooses the model of a connection test that does not
// name one: the first of the platform's default test models the account serves,
// otherwise the first text-chat model of the account's own model list
// (candidates, in display order) or, without a list, of its model mapping.
// "" leaves the choice to the platform tester's own default.
func PickConnectionTestModel(account *Account, candidates ...string) string {
	if account == nil {
		return ""
	}
	preferred := connectionTestDefaultModels(account)
	if len(candidates) == 0 {
		for _, model := range preferred {
			if account.IsModelSupported(model) {
				return model
			}
		}
		for model := range account.GetModelMapping() {
			candidates = append(candidates, model)
		}
		sort.Strings(candidates)
	} else {
		for _, model := range preferred {
			if slices.Contains(candidates, model) {
				return model
			}
		}
	}
	for _, model := range candidates {
		if AccountTestSupportsTextConversation(account, model) {
			return model
		}
	}
	return ""
}

// connectionTestDefaultModels lists, most preferred first, the empty-model
// defaults of the platform tester that TestAccountConnection dispatches to.
func connectionTestDefaultModels(account *Account) []string {
	switch {
	case account.IsOpenCodeGo():
		return []string{DefaultOpenCodeGoTestModel}
	case account.IsCNProvider():
		if account.GetAPIProtocol() == APIProtocolAnthropic {
			return claudeConnectionTestModels
		}
		return []string{openai.DefaultTestModel}
	case account.IsOpenAI():
		return []string{openai.DefaultTestModel}
	case account.IsGemini():
		return []string{geminicli.DefaultTestModel}
	case account.Platform == PlatformGrok:
		return []string{grokDefaultResponsesModel}
	case account.Platform == PlatformAntigravity:
		return []string{defaultAntigravityTestModel}
	case account.IsTypeSafe():
		// The System One probe always sends this model.
		return []string{typesafe.JevLatestModel}
	default:
		return claudeConnectionTestModels
	}
}

// claudeConnectionTestModels prefers Anthropic's replacement for the deprecated
// Sonnet 4.5 test model, then the current Opus.
var claudeConnectionTestModels = []string{claude.DefaultTestModel, claude.DefaultTestModelFallback}

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
