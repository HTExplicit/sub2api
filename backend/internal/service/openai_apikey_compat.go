package service

import (
	"crypto/sha256"
	"encoding/hex"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	OpenAIAlphaSearchModeDirect             = "direct"
	OpenAIAlphaSearchModeResponsesWebSearch = "responses_web_search"
	OpenAIAlphaSearchModeDisabled           = "disabled"

	OpenAIPromptCacheKeyModePassthrough = "passthrough"
	OpenAIPromptCacheKeyModeSHA25664    = "sha256_64"
)

func (a *Account) GetOpenAIAlphaSearchMode() string {
	if a == nil || !a.IsOpenAIApiKey() {
		return OpenAIAlphaSearchModeDirect
	}
	switch a.GetExtraString("openai_alpha_search_mode") {
	case OpenAIAlphaSearchModeResponsesWebSearch:
		return OpenAIAlphaSearchModeResponsesWebSearch
	case OpenAIAlphaSearchModeDisabled:
		return OpenAIAlphaSearchModeDisabled
	default:
		return OpenAIAlphaSearchModeDirect
	}
}

func (a *Account) GetOpenAIPromptCacheKeyMode() string {
	if a == nil || !a.IsOpenAIApiKey() {
		return OpenAIPromptCacheKeyModePassthrough
	}
	if a.GetExtraString("openai_prompt_cache_key_mode") == OpenAIPromptCacheKeyModeSHA25664 {
		return OpenAIPromptCacheKeyModeSHA25664
	}
	return OpenAIPromptCacheKeyModePassthrough
}

func normalizeOpenAIAPIKeyPromptCacheKey(body []byte, account *Account, globallyEnabled bool) ([]byte, bool, error) {
	if !globallyEnabled || account == nil || !account.IsOpenAIApiKey() ||
		account.GetOpenAIPromptCacheKeyMode() != OpenAIPromptCacheKeyModeSHA25664 {
		return body, false, nil
	}
	value := gjson.GetBytes(body, "prompt_cache_key")
	if value.Type != gjson.String || utf8.RuneCountInString(value.String()) <= 64 {
		return body, false, nil
	}
	digest := sha256.Sum256([]byte(value.String()))
	normalized, err := sjson.SetBytes(body, "prompt_cache_key", hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}
