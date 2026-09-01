package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func managedCindyPromptCacheContext() *gin.Context {
	c, _ := gin.CreateTestContext(nil)
	SetCindyManagedCompatibility(c, true)
	return c
}

func managedCindyPromptCacheAccount() *Account {
	return &Account{
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Credentials:     cindyCredentials(),
	}
}

func TestCindyManagedPromptCacheKeyRequiresGroupAndExactAccountIdentity(t *testing.T) {
	longKey := strings.Repeat("x", 65)
	body := []byte(`{"prompt_cache_key":"` + longKey + `","input":[]}`)
	cindy := managedCindyPromptCacheAccount()

	unmanaged, changed, err := normalizeCindyManagedPromptCacheKey(body, nil, cindy)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, unmanaged)

	ordinary := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com"},
		Extra: map[string]any{
			"openai_prompt_cache_key_mode": OpenAIPromptCacheKeyModeSHA25664,
		},
	}
	ordinaryResult, changed, err := normalizeCindyManagedPromptCacheKey(body, managedCindyPromptCacheContext(), ordinary)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, ordinaryResult)

	legacy := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: cindyCredentials()}
	legacyResult, changed, err := normalizeCindyManagedPromptCacheKey(body, managedCindyPromptCacheContext(), legacy)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, legacyResult)
}

func TestCindyManagedPromptCacheKeyNormalizesFinalUnicodeWireValue(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantHash bool
	}{
		{name: "64 ASCII", value: strings.Repeat("a", 64)},
		{name: "65 ASCII", value: strings.Repeat("b", 65), wantHash: true},
		{name: "363 ASCII", value: strings.Repeat("c", 363), wantHash: true},
		{name: "64 Unicode characters", value: strings.Repeat("界", 64)},
		{name: "65 Unicode characters", value: strings.Repeat("界", 65), wantHash: true},
		{name: "empty", value: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"prompt_cache_key":"` + test.value + `","input":[]}`)
			normalized, changed, err := normalizeCindyManagedPromptCacheKey(
				body, managedCindyPromptCacheContext(), managedCindyPromptCacheAccount(),
			)
			require.NoError(t, err)
			require.Equal(t, test.wantHash, changed)
			if !test.wantHash {
				require.Equal(t, body, normalized)
				return
			}
			digest := sha256.Sum256([]byte(test.value))
			require.Equal(t, hex.EncodeToString(digest[:]), gjson.GetBytes(normalized, "prompt_cache_key").String())
			require.Len(t, gjson.GetBytes(normalized, "prompt_cache_key").String(), 64)
		})
	}

	nonString := []byte(`{"prompt_cache_key":123,"input":[]}`)
	result, changed, err := normalizeCindyManagedPromptCacheKey(
		nonString, managedCindyPromptCacheContext(), managedCindyPromptCacheAccount(),
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, nonString, result)
}

func TestLegacyCompatibilityModesAreStoredButIgnoredByManagedRuntime(t *testing.T) {
	longKey := strings.Repeat("x", 363)
	body := []byte(`{"prompt_cache_key":"` + longKey + `","input":[]}`)
	cindy := managedCindyPromptCacheAccount()
	cindy.Extra = map[string]any{
		"openai_alpha_search_mode":     OpenAIAlphaSearchModeDisabled,
		"openai_prompt_cache_key_mode": OpenAIPromptCacheKeyModePassthrough,
	}

	normalized, changed, err := normalizeCindyManagedPromptCacheKey(
		body, managedCindyPromptCacheContext(), cindy,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotEqual(t, longKey, gjson.GetBytes(normalized, "prompt_cache_key").String())
	require.True(t, cindy.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))

	ordinaryDisabled := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{"openai_alpha_search_mode": OpenAIAlphaSearchModeDisabled},
	}
	require.True(t, ordinaryDisabled.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))
}

func TestCindyManagedPromptCacheObservationRecordsOnlyBoolean(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()
	c := managedCindyPromptCacheContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	observeCindyManagedPromptCacheNormalization(c, true)

	require.True(t, logSink.ContainsMessage("openai.cindy_prompt_cache_key_normalized"))
	require.True(t, logSink.ContainsFieldValue("normalized", "true"))
	require.False(t, logSink.ContainsField("prompt_cache_key"))
	require.False(t, logSink.ContainsField("original_value"))
}
