package service

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateAccountKeepsOmittedOpenAIAPIKeyProtocolModes(t *testing.T) {
	stored := map[string]any{
		"openai_responses_mode":                         "force_chat_completions",
		"openai_compact_mode":                           nil,
		"openai_apikey_responses_websockets_v2_mode":    "dedicated",
		"openai_apikey_responses_websockets_v2_enabled": false,
		"openai_ws_enabled":                             true,
		OpenAIPromptCacheKeyModeExtraKey:                OpenAIPromptCacheKeyModeSHA25664,
		"unrelated_setting":                             "old",
	}
	update := func(accountType string, before, extra map[string]any) map[string]any {
		t.Helper()
		repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {
			ID: 1, Name: "modes", Platform: PlatformOpenAI, Type: accountType, Status: StatusActive,
			Credentials: map[string]any{"api_key": "test"}, Extra: maps.Clone(before),
		}}}
		_, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: extra})
		require.NoError(t, err)
		return repo.accounts[1].Extra
	}
	// protocolModes keeps presence apart from a stored null.
	protocolModes := func(extra map[string]any) map[string]any {
		modes := map[string]any{}
		for _, key := range []string{
			"openai_responses_mode", "openai_compact_mode",
			"openai_apikey_responses_websockets_v2_mode", "openai_apikey_responses_websockets_v2_enabled",
			"responses_websockets_v2_enabled", "openai_ws_enabled",
			OpenAIPromptCacheKeyModeExtraKey,
		} {
			if value, present := extra[key]; present {
				modes[key] = value
			}
		}
		return modes
	}

	kept := update(AccountTypeAPIKey, stored, map[string]any{"other_setting": "new"})
	require.Equal(t, protocolModes(stored), protocolModes(kept))
	require.NotContains(t, kept, "unrelated_setting")
	require.Equal(t, "new", kept["other_setting"])

	changed := update(AccountTypeAPIKey, stored, map[string]any{
		"openai_responses_mode": "auto", "openai_ws_enabled": nil, "responses_websockets_v2_enabled": true,
	})
	want := protocolModes(stored)
	want["openai_responses_mode"] = "auto"
	want["openai_ws_enabled"] = nil
	want["responses_websockets_v2_enabled"] = true
	require.Equal(t, want, protocolModes(changed))

	require.Empty(t, protocolModes(update(AccountTypeAPIKey, map[string]any{"unrelated_setting": "old"}, map[string]any{"other_setting": "new"})))

	// Any other account replaces Extra as a whole.
	require.Empty(t, protocolModes(update(AccountTypeOAuth, stored, map[string]any{"other_setting": "new"})))
}
