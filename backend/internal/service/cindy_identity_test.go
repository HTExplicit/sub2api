package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func cindyCredentials() map[string]any {
	return map[string]any{
		"base_url": "https://api.laxarouter.ai",
		"api_key":  "test-key",
	}
}

func TestIsCindyAPIKeyAccount(t *testing.T) {
	require.True(t, IsCindyAPIKeyAccount(PlatformCindy, AccountTypeAPIKey, cindyCredentials()))
	require.True(t, IsCindyAPIKeyAccount(PlatformCindy, AccountTypeAPIKey, map[string]any{"base_url": "https://API.LAXAROUTER.AI/"}))
	require.False(t, IsCindyAPIKeyAccount(PlatformOpenAI, AccountTypeAPIKey, cindyCredentials()), "runtime legacy recognition must stay disabled")
	require.False(t, IsCindyAPIKeyAccount(PlatformCindy, AccountTypeOAuth, cindyCredentials()))
	require.False(t, IsCindyAPIKeyAccount(PlatformCindy, AccountTypeAPIKey, map[string]any{"base_url": "https://api.laxarouter.ai/v1"}))
	require.False(t, IsCindyAPIKeyAccount(PlatformCindy, AccountTypeAPIKey, map[string]any{"base_url": "https://api.laxarouter.ai.evil.test"}))
}

func TestNormalizeCindyDeviceIdentityExtraPreservesInput(t *testing.T) {
	deviceID := "7a986ef4-0fde-48df-a73f-f7c0de1a9cae"
	requested := map[string]any{
		CindyDeviceIDExtraKey:       deviceID,
		CindyDeviceIDSourceExtraKey: "registration-record",
		"ordinary":                  "kept",
	}

	got, err := NormalizeCindyDeviceIdentityExtra(PlatformCindy, AccountTypeAPIKey, cindyCredentials(), requested, nil)
	require.NoError(t, err)
	require.Equal(t, deviceID, got[CindyDeviceIDExtraKey])
	require.Equal(t, "registration-record", got[CindyDeviceIDSourceExtraKey])
	require.Equal(t, "kept", got["ordinary"])
	require.Equal(t, "force_responses", got[CindyResponsesModeExtraKey])
	require.Equal(t, OpenAIAlphaSearchModeResponsesWebSearch, got[CindyAlphaSearchExtraKey])
	require.Equal(t, OpenAIPromptCacheKeyModeSHA25664, got[CindyPromptCacheExtraKey])
	require.Equal(t, deviceID, requested[CindyDeviceIDExtraKey], "input map must not be mutated")
}

func TestNormalizeCindyDeviceIdentityExtraPreservesExplicitCompatibilityModes(t *testing.T) {
	requested := map[string]any{
		CindyResponsesModeExtraKey: "force_chat_completions",
		CindyAlphaSearchExtraKey:   OpenAIAlphaSearchModeResponsesWebSearch,
		CindyPromptCacheExtraKey:   OpenAIPromptCacheKeyModePassthrough,
	}
	got, err := NormalizeCindyDeviceIdentityExtra(
		PlatformCindy, AccountTypeAPIKey, cindyCredentials(), requested, nil,
	)
	require.NoError(t, err)
	require.Equal(t, requested[CindyResponsesModeExtraKey], got[CindyResponsesModeExtraKey])
	require.Equal(t, requested[CindyAlphaSearchExtraKey], got[CindyAlphaSearchExtraKey])
	require.Equal(t, requested[CindyPromptCacheExtraKey], got[CindyPromptCacheExtraKey])
}

func TestNormalizeCindyDeviceIdentityExtraGeneratesUniqueStoreOnlyIdentity(t *testing.T) {
	first, err := NormalizeCindyDeviceIdentityExtra(PlatformCindy, AccountTypeAPIKey, cindyCredentials(), nil, nil)
	require.NoError(t, err)
	second, err := NormalizeCindyDeviceIdentityExtra(PlatformCindy, AccountTypeAPIKey, cindyCredentials(), nil, nil)
	require.NoError(t, err)

	firstID, firstOK := first[CindyDeviceIDExtraKey].(string)
	secondID, secondOK := second[CindyDeviceIDExtraKey].(string)
	require.True(t, firstOK)
	require.True(t, secondOK)
	require.Len(t, firstID, 64)
	require.True(t, ValidCindyDeviceID(firstID))
	require.NotEqual(t, firstID, secondID)
	require.Equal(t, "generated-production-v1", first[CindyDeviceIDSourceExtraKey])
}

func TestNormalizeCindyDeviceIdentityExtraPreservesStoredIdentityAndAcceptsMask(t *testing.T) {
	deviceID := strings.Repeat("a", 64)
	current := map[string]any{
		CindyDeviceIDExtraKey:       deviceID,
		CindyDeviceIDSourceExtraKey: "generated-production-v1",
	}

	got, err := NormalizeCindyDeviceIdentityExtra(PlatformCindy, AccountTypeAPIKey, cindyCredentials(), map[string]any{
		CindyDeviceIDExtraKey: MaskCindyDeviceID(deviceID),
		"ordinary":            true,
	}, current)
	require.NoError(t, err)
	require.Equal(t, deviceID, got[CindyDeviceIDExtraKey])
	require.Equal(t, "generated-production-v1", got[CindyDeviceIDSourceExtraKey])
	require.Equal(t, true, got["ordinary"])
}

func TestNormalizeCindyDeviceIdentityExtraRejectsInvalidValues(t *testing.T) {
	tests := []map[string]any{
		{CindyDeviceIDExtraKey: strings.Repeat("A", 64)},
		{CindyDeviceIDExtraKey: "not-a-device-id"},
		{CindyDeviceIDExtraKey: strings.Repeat("a", 64), CindyDeviceIDSourceExtraKey: "unknown"},
	}
	for _, requested := range tests {
		_, err := NormalizeCindyDeviceIdentityExtra(PlatformCindy, AccountTypeAPIKey, cindyCredentials(), requested, nil)
		require.Error(t, err)
	}
}

func TestNormalizeCindyDeviceIdentityExtraIgnoresNonCindyAccount(t *testing.T) {
	requested := map[string]any{"ordinary": "unchanged"}
	got, err := NormalizeCindyDeviceIdentityExtra(PlatformOpenAI, AccountTypeAPIKey, map[string]any{"base_url": "https://api.openai.com"}, requested, nil)
	require.NoError(t, err)
	require.Equal(t, requested, got)
}

func TestMaskCindyDeviceID(t *testing.T) {
	require.Equal(t, "aaaaaaaa...aaaaaaaa", MaskCindyDeviceID(strings.Repeat("a", 64)))
	require.Equal(t, "7a986ef4...9cae", MaskCindyDeviceID("7a986ef4-0fde-48df-a73f-f7c0de1a9cae"))
	require.Empty(t, MaskCindyDeviceID("invalid"))
}
