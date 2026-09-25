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

func promptCacheKeyModeAccount(mode any) *Account {
	return &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://relay.example.test"},
		Extra:       map[string]any{OpenAIPromptCacheKeyModeExtraKey: mode},
	}
}

func TestOpenAIPromptCacheKeyModeAppliesOnlyToOptedInAPIKeys(t *testing.T) {
	longKey := strings.Repeat("x", 65)
	body := []byte(`{"prompt_cache_key":"` + longKey + `","input":[]}`)

	for _, account := range []*Account{
		nil,
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		promptCacheKeyModeAccount(OpenAIPromptCacheKeyModePassthrough),
		promptCacheKeyModeAccount("SHA256_64"),
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{OpenAIPromptCacheKeyModeExtraKey: OpenAIPromptCacheKeyModeSHA25664}},
	} {
		unchanged, changed, err := normalizeOpenAIAPIKeyPromptCacheKey(body, nil, account)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, unchanged)
	}

	hashed, changed, err := normalizeOpenAIAPIKeyPromptCacheKey(body, nil, promptCacheKeyModeAccount(OpenAIPromptCacheKeyModeSHA25664))
	require.NoError(t, err)
	require.True(t, changed)
	digest := sha256.Sum256([]byte(longKey))
	require.Equal(t, hex.EncodeToString(digest[:]), gjson.GetBytes(hashed, "prompt_cache_key").String())
}

func TestOpenAIPromptCacheKeyModeNormalizesFinalUnicodeWireValue(t *testing.T) {
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
	account := promptCacheKeyModeAccount(OpenAIPromptCacheKeyModeSHA25664)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"prompt_cache_key":"` + test.value + `","input":[]}`)
			normalized, changed, err := normalizeOpenAIAPIKeyPromptCacheKey(body, nil, account)
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
	result, changed, err := normalizeOpenAIAPIKeyPromptCacheKey(nonString, nil, account)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, nonString, result)
}

func TestValidateOpenAIPromptCacheKeyModeExtra(t *testing.T) {
	require.NoError(t, ValidateOpenAIPromptCacheKeyModeExtra(nil))
	require.NoError(t, ValidateOpenAIPromptCacheKeyModeExtra(map[string]any{OpenAIPromptCacheKeyModeExtraKey: nil}))
	require.NoError(t, ValidateOpenAIPromptCacheKeyModeExtra(map[string]any{OpenAIPromptCacheKeyModeExtraKey: OpenAIPromptCacheKeyModePassthrough}))
	require.NoError(t, ValidateOpenAIPromptCacheKeyModeExtra(map[string]any{OpenAIPromptCacheKeyModeExtraKey: OpenAIPromptCacheKeyModeSHA25664}))
	for _, invalid := range []any{"", "sha256", "SHA256_64", true, 64} {
		require.Error(t, ValidateOpenAIPromptCacheKeyModeExtra(map[string]any{OpenAIPromptCacheKeyModeExtraKey: invalid}), "%v", invalid)
	}
}

func TestOpenAIPromptCacheKeyObservationRecordsOnlyBoolean(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	observeOpenAIPromptCacheKeyNormalization(c, true)

	require.True(t, logSink.ContainsMessage("openai.prompt_cache_key_normalized"))
	require.True(t, logSink.ContainsFieldValue("normalized", "true"))
	require.False(t, logSink.ContainsField("prompt_cache_key"))
	require.False(t, logSink.ContainsField("original_value"))
}
