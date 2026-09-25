package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// OpenAIPromptCacheKeyModeExtraKey selects how an API-key account forwards the
// client prompt_cache_key. Some OpenAI-compatible relays reject keys longer
// than 64 characters; sha256_64 replaces those keys with their SHA-256 hex.
const OpenAIPromptCacheKeyModeExtraKey = "openai_prompt_cache_key_mode"

const (
	OpenAIPromptCacheKeyModePassthrough = "passthrough"
	OpenAIPromptCacheKeyModeSHA25664    = "sha256_64"
)

const openAIPromptCacheKeyMaxRunes = 64

// OpenAIPromptCacheKeyMode returns the account's prompt_cache_key forwarding
// mode. Anything other than sha256_64 forwards the key unchanged.
func (a *Account) OpenAIPromptCacheKeyMode() string {
	if a == nil || a.Type != AccountTypeAPIKey || a.Extra == nil {
		return OpenAIPromptCacheKeyModePassthrough
	}
	mode, _ := a.Extra[OpenAIPromptCacheKeyModeExtraKey].(string)
	if strings.TrimSpace(mode) == OpenAIPromptCacheKeyModeSHA25664 {
		return OpenAIPromptCacheKeyModeSHA25664
	}
	return OpenAIPromptCacheKeyModePassthrough
}

func observeOpenAIPromptCacheKeyNormalization(c *gin.Context, changed bool) {
	if !changed || c == nil || c.Request == nil {
		return
	}
	logger.FromContext(c.Request.Context()).Info(
		"openai.prompt_cache_key_normalized",
		zap.Bool("normalized", true),
	)
}

// applyOpenAIAPIKeyPromptCacheKeyMode applies the account option to a final
// Responses wire body. The gateway calls it at every send point, after any
// other body rewrite, so the upstream never sees an over-long key.
func applyOpenAIAPIKeyPromptCacheKeyMode(c *gin.Context, account *Account, body []byte) ([]byte, error) {
	normalized, changed, err := normalizeOpenAIAPIKeyPromptCacheKey(body, c, account)
	if err != nil {
		return nil, err
	}
	observeOpenAIPromptCacheKeyNormalization(c, changed)
	return normalized, nil
}

// normalizeOpenAIAPIKeyPromptCacheKey is the single final-wire normalizer for
// the account option openai_prompt_cache_key_mode=sha256_64.
func normalizeOpenAIAPIKeyPromptCacheKey(body []byte, _ *gin.Context, account *Account) ([]byte, bool, error) {
	if account.OpenAIPromptCacheKeyMode() != OpenAIPromptCacheKeyModeSHA25664 {
		return body, false, nil
	}
	value := gjson.GetBytes(body, "prompt_cache_key")
	if value.Type != gjson.String || utf8.RuneCountInString(value.String()) <= openAIPromptCacheKeyMaxRunes {
		return body, false, nil
	}
	digest := sha256.Sum256([]byte(value.String()))
	normalized, err := sjson.SetBytes(body, "prompt_cache_key", hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}
