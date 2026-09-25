package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelContextCapacitySelectedGPTReferenceAndExactVariants(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://capacity.example/v1"}}
	for _, tc := range []struct {
		model     string
		window    int64
		canonical string
	}{
		{"gpt-6-astra", 272000, "gpt-6-astra"},
		{"team/region/GPT-6", 272000, "gpt-6-astra"},
		{"vendor/gpt-5.6", 272000, "gpt-5.6-sol"},
		{"vendor/gpt-5.6-terra-high", 272000, "gpt-5.6-terra"},
		{"gpt-5.6-luna", 272000, "gpt-5.6-luna"},
		{"gpt-daybreak-blue-latest", 272000, "gpt-daybreak-blue-latest"},
		{"gpt-daybreak-red-latest", 372000, "gpt-daybreak-red-latest"},
		{"vendor/gpt-5.4", 272000, "gpt-5.4"},
		{"gpt-5.4-2026-03-05", 272000, "gpt-5.4"},
		{"gpt-5.2-pro-2025-12-11", 400000, "gpt-5.2-pro"},
		{"gpt-5.5", 272000, "gpt-5.5"},
		{"vendor/GPT5.4mini", 272000, "gpt-5.4-mini"},
		{"gpt-5.2", 272000, "gpt-5.2"},
		{"vendor/gpt-5.3-codex", 400000, "gpt-5.3-codex"},
		{"vendor/claude-sonnet-4-6", 1000000, "claude-sonnet-4-6"},
		{"anthropic/claude-opus-4.6", 1000000, "claude-opus-4-6"},
		{"anthropic/claude-opus-4-6:free", 1000000, "claude-opus-4-6"},
		{"vendor/gpt-5.4-2099-01-01", 0, ""},
		{"vendor/gpt-5.6-sol-unknown", 0, ""},
		{"vendor/gpt-5.1", 0, ""},
		{"contains-gpt-5.4", 0, ""},
		{"vendor/unknown", 0, ""},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := ResolveAccountModelContextCapacity(account, tc.model)
			require.Equal(t, tc.window, got.ContextWindow)
			official := LookupOfficialModelContextCapacity(account, tc.model)
			if tc.canonical == "" {
				require.Nil(t, official)
				require.False(t, got.Known(), "an unknown model is not given a default window")
			} else {
				require.Equal(t, tc.canonical, official.ModelID)
				require.Equal(t, "official", got.Source)
			}
		})
	}
	ref := LookupOfficialModelContextCapacity(account, "gpt-6")
	require.Equal(t, int64(272000), ref.ContextWindow)
	require.Equal(t, int64(872000), ref.MaxContextWindow, "the API-key reference is the Codex subscription maximum")
	require.Equal(t, int64(272000), ref.Reference.ContextWindow)
	require.Equal(t, int64(872000), ref.Reference.MaxContextWindow)
	require.Equal(t, "codex_subscription", ref.Product)
	require.Contains(t, ref.Conditions, "Codex")
	ref.Reference.ContextWindow = 1
	require.Equal(t, int64(272000), LookupOfficialModelContextCapacity(account, "gpt-6").Reference.ContextWindow, "returned evidence must not mutate the release catalog")
	mini := LookupOfficialModelContextCapacity(account, "gpt-5.4-mini")
	require.Equal(t, "codex_subscription", mini.Product)
	require.Zero(t, mini.MaxOutputTokens, "the subscription reference does not publish an independent output hard limit")
}

func TestModelContextCapacityReferenceMatchingKeepsRawKeys(t *testing.T) {
	account := newGroupCapacityAccount(1, map[string]any{"public": "team/gpt-5.4"}, map[string]int64{"team/gpt-5.4": 258000, "gpt-5.4": 600000})
	account.Extra[UpstreamModelMetadataExtraKey] = observedCapacityExtra(map[string]UpstreamModelMetadata{
		"team/gpt-5.4": {ContextWindow: 256000}, "team/unknown": {ContextWindow: 258000},
	})
	before, err := json.Marshal(account)
	require.NoError(t, err)
	rows := BuildAccountModelContextCapacityRows(&account, []string{"team/gpt-5.4"})
	var matched *AccountModelContextCapacityRow
	for i := range rows {
		if rows[i].UpstreamModelID == "team/gpt-5.4" {
			matched = &rows[i]
		}
	}
	require.NotNil(t, matched)
	require.Equal(t, []string{"public"}, matched.Aliases)
	require.Equal(t, "gpt-5.4", matched.Official.ModelID)
	require.Equal(t, int64(258000), matched.EffectiveContextWindow)
	require.Equal(t, int64(256000), matched.AutomaticContextWindow, "the account's own declaration outranks the reference catalog")
	require.Equal(t, "custom", matched.EffectiveSource)
	upstream := ResolveAccountModelContextCapacity(&account, "team/unknown")
	require.Equal(t, int64(258000), upstream.ContextWindow)
	require.Equal(t, "upstream", upstream.Source)
	after, err := json.Marshal(account)
	require.NoError(t, err)
	require.Equal(t, before, after)
	overrides, ok := account.Extra[ModelContextOverridesExtraKey].(map[string]int64)
	require.True(t, ok)
	delete(overrides, "team/gpt-5.4")
	require.Equal(t, int64(256000), ResolveAccountModelContextCapacity(&account, "team/gpt-5.4").ContextWindow, "clearing raw override must not borrow the bare-ID override")
}

func TestModelContextCapacityReferenceCandidateMinimumAndWireIdentity(t *testing.T) {
	known := newGroupCapacityAccount(1, map[string]any{"gpt-6": "vendor/gpt-5.4"}, nil)
	unknown := newGroupCapacityAccount(2, map[string]any{"gpt-6": "opaque-model"}, nil)
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	repo := &groupCapacityAccountRepo{accounts: []Account{known, unknown}}
	catalog := loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, nil, &group.ID, group.Platform)
	body := []byte(`{"object":"list","data":[{"id":"gpt-6","display_name":"Keep label","sentinel":true}]}`)
	out, err := projectModelCapacityEnvelope(body, false, func(model string) ResolvedModelContextCapacity {
		return catalog.resolve(context.Background(), group.Platform, model)
	})
	require.NoError(t, err)
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &envelope))
	require.Equal(t, "gpt-6", envelope.Data[0]["id"])
	require.Equal(t, "Keep label", envelope.Data[0]["display_name"])
	require.Equal(t, true, envelope.Data[0]["sentinel"])
	require.Equal(t, float64(272000), envelope.Data[0]["context_window"])
	require.Equal(t, float64(1000000), envelope.Data[0]["max_context_window"], "a GPT-looking public alias must not identify the unknown actual target")
	require.NotContains(t, string(body), "context_window", "projection must not mutate the input cache body")
}
