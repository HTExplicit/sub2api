package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIAPIKeyPromptCacheKeyRequiresBothSwitches(t *testing.T) {
	longKey := strings.Repeat("cache-key-", 9)
	body := []byte(`{"prompt_cache_key":"` + longKey + `","input":[]}`)
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{"openai_prompt_cache_key_mode": OpenAIPromptCacheKeyModeSHA25664},
	}

	unchanged, changed, err := normalizeOpenAIAPIKeyPromptCacheKey(body, account, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, unchanged)

	account.Extra["openai_prompt_cache_key_mode"] = OpenAIPromptCacheKeyModePassthrough
	unchanged, changed, err = normalizeOpenAIAPIKeyPromptCacheKey(body, account, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, unchanged)
}

func TestNormalizeOpenAIAPIKeyPromptCacheKeyHashesOnlyLongExplicitAPIKeyValues(t *testing.T) {
	longKey := strings.Repeat("x", 65)
	body := []byte(`{"prompt_cache_key":"` + longKey + `","input":[]}`)
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{"openai_prompt_cache_key_mode": OpenAIPromptCacheKeyModeSHA25664},
	}

	normalized, changed, err := normalizeOpenAIAPIKeyPromptCacheKey(body, account, true)
	require.NoError(t, err)
	require.True(t, changed)
	digest := sha256.Sum256([]byte(longKey))
	require.Equal(t, hex.EncodeToString(digest[:]), gjson.GetBytes(normalized, "prompt_cache_key").String())
	require.Len(t, gjson.GetBytes(normalized, "prompt_cache_key").String(), 64)

	shortBody := []byte(`{"prompt_cache_key":"` + strings.Repeat("y", 64) + `"}`)
	shortResult, shortChanged, err := normalizeOpenAIAPIKeyPromptCacheKey(shortBody, account, true)
	require.NoError(t, err)
	require.False(t, shortChanged)
	require.Equal(t, shortBody, shortResult)

	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: account.Extra}
	oauthResult, oauthChanged, err := normalizeOpenAIAPIKeyPromptCacheKey(body, oauth, true)
	require.NoError(t, err)
	require.False(t, oauthChanged)
	require.Equal(t, body, oauthResult)
}

func TestOpenAIAlphaSearchModeDisabledExcludesOnlyExplicitAPIKeyAccount(t *testing.T) {
	disabled := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{"openai_alpha_search_mode": OpenAIAlphaSearchModeDisabled},
	}
	direct := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: disabled.Extra}

	require.False(t, disabled.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))
	require.True(t, direct.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))
	require.True(t, oauth.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))
}
