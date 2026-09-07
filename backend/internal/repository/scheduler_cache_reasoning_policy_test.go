//go:build unit

package repository

import (
	"maps"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCacheReasoningPolicyProjectionPreservesMissingAndBooleanSemantics(t *testing.T) {
	keys := []string{service.OpenAIChatReasoningReplayEnabledExtraKey, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey}
	for _, source := range []map[string]any{nil, {}, {"unrelated": true}, {"quota_limit": nil}} {
		filtered := filterSchedulerExtra(source)
		for _, key := range keys {
			require.NotContains(t, filtered, key, "absence must keep the enabled policy default")
		}
	}
	for _, key := range keys {
		for _, value := range []any{true, false, nil, "true", "false", 0, 1, []bool{true}, map[string]any{}} {
			source := map[string]any{key: value, "mixed_scheduling": true, "quota_limit": nil, "unrelated": true}
			original := maps.Clone(source)
			filtered := filterSchedulerExtra(source)
			expected, _ := value.(bool)
			require.Contains(t, filtered, key)
			require.Equal(t, expected, filtered[key])
			require.Equal(t, true, filtered["mixed_scheduling"])
			require.NotContains(t, filtered, "quota_limit", "unrelated nil filtering must not change")
			require.NotContains(t, filtered, "unrelated", "the existing allowlist stays restricted")
			require.Equal(t, original, source, "projection must not rewrite persistent account values")
			for _, other := range keys {
				if other != key {
					require.NotContains(t, filtered, other)
				}
			}
		}
	}
}

func TestSchedulerCacheReasoningPolicyAccountRoundTripMatchesSource(t *testing.T) {
	parentID := int64(10)
	accounts := []service.Account{
		{Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
		{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
		{Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken},
		{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, ParentAccountID: &parentID, QuotaDimension: service.QuotaDimensionSpark},
		{Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI, Type: service.AccountTypeAPIKey},
		{Platform: service.PlatformGrok, Type: service.AccountTypeOAuth},
	}
	for _, account := range accounts {
		for _, extra := range []map[string]any{
			nil,
			{service.OpenAIChatReasoningReplayEnabledExtraKey: false, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey: true},
			{service.OpenAIChatReasoningReplayEnabledExtraKey: true, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey: false},
			{service.OpenAIChatReasoningReplayEnabledExtraKey: nil, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey: "true"},
			{service.OpenAIChatReasoningReplayEnabledExtraKey: 1},
		} {
			account.Extra = extra
			_, metadata, err := marshalSchedulerCacheAccount(account)
			require.NoError(t, err)
			restored, err := decodeCachedAccount(metadata)
			require.NoError(t, err)
			require.Equal(t, account.IsOpenAIChatReasoningReplayEnabled(), restored.IsOpenAIChatReasoningReplayEnabled())
			require.Equal(t, account.IsOpenAIReasoningSignatureRecoveryEnabled(), restored.IsOpenAIReasoningSignatureRecoveryEnabled())
		}
	}
}

func TestSchedulerCacheReasoningPolicyUpdatesUseExistingInvalidation(t *testing.T) {
	for _, key := range []string{service.OpenAIChatReasoningReplayEnabledExtraKey, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
		for _, enabled := range []bool{true, false} {
			require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{key: enabled}))
		}
	}
}
