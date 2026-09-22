package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountEditNativeStateDigestUsesTheSameRawRow(t *testing.T) {
	account := &service.Account{ID: 29, Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, CindyCredentialGeneration: 13,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "must-not-leak", "model_mapping": map[string]any{"saved": "target"}},
		Extra:       map[string]any{"openai_responses_mode": nil, "openai_apikey_responses_websockets_v2_mode": "shared"}}
	full := AccountFromServiceShallow(account)
	require.Equal(t, service.AccountEditStateDigest(account), full.AccountEditStateSHA256)
	require.Len(t, full.AccountEditStateSHA256, 64)
	list := AccountListItemFromAccount(full)
	require.Equal(t, full.AccountEditStateSHA256, list.AccountEditStateSHA256)
	raw, err := json.Marshal(list)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"account_edit_state_sha256"`)
	require.NotContains(t, string(raw), "must-not-leak")
	account.Credentials["model_mapping"] = nil
	newer := AccountFromServiceShallow(account)
	require.NotEqual(t, full.AccountEditStateSHA256, newer.AccountEditStateSHA256)
	account.CindyCredentialGeneration++
	require.NotEqual(t, newer.AccountEditStateSHA256, AccountFromServiceShallow(account).AccountEditStateSHA256)
	for _, noncanonical := range []*service.Account{
		{ID: 29, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: account.Credentials, Extra: map[string]any{"is_cindy": true}},
		{ID: 29, Platform: service.PlatformCindy, WirePlatform: "other-wire", Type: service.AccountTypeAPIKey, Credentials: account.Credentials},
		{ID: 29, Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey}, // an incomplete projection has no authoritative profile
	} {
		mapped := AccountFromServiceShallow(noncanonical)
		require.Empty(t, mapped.AccountEditStateSHA256)
		raw, err := json.Marshal(AccountListItemFromAccount(mapped))
		require.NoError(t, err)
		require.NotContains(t, string(raw), `"account_edit_state_sha256"`)
	}
}
