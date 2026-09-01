package service

import (
	"crypto/sha256"
	"encoding/hex"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const (
	OpenAIAlphaSearchModeDirect             = "direct"
	OpenAIAlphaSearchModeResponsesWebSearch = "responses_web_search"
	OpenAIAlphaSearchModeDisabled           = "disabled"

	OpenAIPromptCacheKeyModePassthrough = "passthrough"
	OpenAIPromptCacheKeyModeSHA25664    = "sha256_64"
)

const cindyManagedCompatibilityContextKey = "cindy_managed_compatibility"

// SetCindyManagedCompatibility freezes the authenticated group decision for
// the request. Account identity is checked again at the final wire boundary,
// so neither a mixed group nor a non-Cindy account can inherit this policy.
func SetCindyManagedCompatibility(c *gin.Context, enabled bool) {
	if c != nil {
		c.Set(cindyManagedCompatibilityContextKey, enabled)
	}
}

func cindyManagedCompatibilityEnabled(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, exists := c.Get(cindyManagedCompatibilityContextKey)
	enabled, ok := value.(bool)
	return exists && ok && enabled
}

func observeCindyManagedPromptCacheNormalization(c *gin.Context, changed bool) {
	if !changed || c == nil || c.Request == nil {
		return
	}
	logger.FromContext(c.Request.Context()).Info(
		"openai.cindy_prompt_cache_key_normalized",
		zap.Bool("normalized", true),
	)
}

// normalizeCindyManagedPromptCacheKey is the single final-wire normalizer.
// It intentionally ignores the legacy account extras and global settings,
// which remain stored for one rollback window only.
func normalizeCindyManagedPromptCacheKey(body []byte, c *gin.Context, account *Account) ([]byte, bool, error) {
	if !cindyManagedCompatibilityEnabled(c) || account == nil ||
		!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
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
