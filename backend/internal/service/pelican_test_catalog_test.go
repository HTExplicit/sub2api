//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type pelicanTestCatalogReaderStub struct {
	accounts []*Account
	reads    int
	ids      []int64
}

func (r *pelicanTestCatalogReaderStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	r.reads++
	r.ids = append([]int64(nil), ids...)
	return r.accounts, nil
}

func TestPelicanTestCatalogReadsOnlyRequestedLocalAccountsAndRetainsStates(t *testing.T) {
	now := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	limitedUntil := now.Add(time.Hour)
	reader := &pelicanTestCatalogReaderStub{accounts: []*Account{
		{ID: 2, Name: "paused", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled, Schedulable: false},
		{ID: 1, Name: "limited", Platform: PlatformGemini, Type: AccountTypeServiceAccount, Status: StatusError, Schedulable: true, ErrorMessage: "full error body token=administrator-visible", RateLimitResetAt: &limitedUntil},
	}}
	catalog := &PelicanTestCatalog{accounts: reader, now: func() time.Time { return now }}
	defaults, err := catalog.Options(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, now, defaults.GeneratedAt)
	require.Equal(t, 10, defaults.MaxConcurrency)
	require.Equal(t, 600, defaults.DefaultGenerationTimeoutSeconds)
	require.Equal(t, 60, defaults.MinGenerationTimeoutSeconds)
	require.Equal(t, 1800, defaults.MaxGenerationTimeoutSeconds)
	require.Empty(t, defaults.Accounts)
	require.Zero(t, reader.reads, "opening the page cannot even enumerate accounts implicitly")

	options, err := catalog.Options(context.Background(), []int64{2, 1, 999, 2})
	require.NoError(t, err)
	require.Equal(t, 1, reader.reads)
	require.Equal(t, []int64{2, 1, 999}, reader.ids, "one local batch read, without scheduling filters")
	require.Len(t, options.Accounts, 3)
	require.Equal(t, StatusDisabled, options.Accounts[0].Status)
	require.False(t, options.Accounts[0].Schedulable)
	require.Equal(t, AccountTypeServiceAccount, options.Accounts[1].Type)
	require.Equal(t, reader.accounts[1].ErrorMessage, options.Accounts[1].ErrorMessage)
	require.Equal(t, &limitedUntil, options.Accounts[1].RateLimitedUntil)
	require.Equal(t, int64(999), options.Accounts[2].ID)
	require.Equal(t, "missing", options.Accounts[2].Status)
	require.Contains(t, options.Accounts[2].CapabilityReason, "not found")
	_, err = catalog.Options(context.Background(), []int64{0})
	require.ErrorIs(t, err, ErrPelicanTestOptionsInvalidRequest)
	require.Equal(t, 1, reader.reads)
}

func TestPelicanTestCatalogUsesMappingAndSourceBoundLocalMetadataWithoutMutation(t *testing.T) {
	reasoning := true
	account := &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://catalog.example", "model_mapping": map[string]any{
			"sol-alias": "gpt-6.1-sol", "custom": "custom-upstream", "media": "gpt-image-2", "gpt-6-*": "gpt-6-astra",
		},
	}}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		SourceIdentity: UpstreamModelMetadataSourceIdentity(account), Models: map[string]UpstreamModelMetadata{
			"custom-upstream": {ID: "custom-upstream", Reasoning: &reasoning, SupportedReasoningLevels: []string{"low", "high"}, InputModalities: []string{"text"}},
			"not-mapped":      {ID: "not-mapped", InputModalities: []string{"text"}},
		},
	})
	beforeCredentials, err := json.Marshal(account.Credentials)
	require.NoError(t, err)
	beforeExtra, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	options := pelicanTestAccountOptions(account)
	byID := make(map[string]PelicanTestModelOption)
	for _, model := range options.Models {
		byID[model.ID] = model
	}
	require.Contains(t, byID, "gpt-6-astra")
	require.Contains(t, byID, "sol-alias")
	require.NotContains(t, byID, "gpt-6-*")
	require.NotContains(t, byID, "not-mapped")
	require.Equal(t, "gpt-6.1-sol", byID["sol-alias"].UpstreamModel)
	require.Equal(t, "high", byID["sol-alias"].DefaultEffort)
	require.Equal(t, []string{"low", "high"}, byID["custom"].ReasoningEfforts)
	require.Equal(t, "high", byID["custom"].DefaultEffort)
	require.False(t, byID["media"].TextSupported)
	require.Contains(t, byID["media"].CapabilityReason, "gpt-image-2")
	afterCredentials, err := json.Marshal(account.Credentials)
	require.NoError(t, err)
	afterExtra, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	require.JSONEq(t, string(beforeCredentials), string(afterCredentials))
	require.JSONEq(t, string(beforeExtra), string(afterExtra))

	account.Credentials["base_url"] = "https://different-source.example"
	changed := PelicanTestModelOptions(account, "custom")
	require.Empty(t, changed.ReasoningEfforts, "old endpoint metadata must not claim current reasoning capabilities")
	require.Empty(t, changed.DefaultEffort)
}

func TestPelicanTestCatalogDefaultsNativeCapabilitiesAndSpecificUnsupportedReasons(t *testing.T) {
	for _, test := range []struct {
		name     string
		platform string
		kind     string
		model    string
		high     bool
	}{
		{"astra api", PlatformOpenAI, AccountTypeAPIKey, "gpt-6-astra", true},
		{"sol oauth", PlatformOpenAI, AccountTypeOAuth, "gpt-6.1-sol", true},
		{"claude setup token", PlatformAnthropic, AccountTypeSetupToken, "claude-opus-5-5", true},
		{"vertex", PlatformAnthropic, AccountTypeServiceAccount, "claude-opus-5-5", true},
		{"gemini", PlatformGemini, AccountTypeAPIKey, "gemini-2.5-flash", false},
		{"antigravity", PlatformAntigravity, AccountTypeOAuth, "gemini-3-flash", false},
		{"kimi", PlatformKimi, AccountTypeAPIKey, "kimi-k2.5", false},
		{"zhipu", PlatformZhipu, AccountTypeAPIKey, "glm-5", false},
		{"deepseek", PlatformDeepseek, AccountTypeAPIKey, "deepseek-v4-pro", false},
		{"minimax", PlatformMiniMax, AccountTypeAPIKey, "MiniMax-M3", false},
		{"opencode", PlatformOpenCodeGo, AccountTypeAPIKey, DefaultOpenCodeGoTestModel, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := &Account{Platform: test.platform, Type: test.kind}
			option := PelicanTestModelOptions(account, test.model)
			require.True(t, option.TextSupported, option.CapabilityReason)
			if test.high {
				require.Contains(t, option.ReasoningEfforts, "high")
				require.Equal(t, "high", option.DefaultEffort)
			} else {
				require.Empty(t, option.DefaultEffort, "models without high keep their normal default")
			}
		})
	}
	unsupported := PelicanTestModelOptions(&Account{Platform: PlatformTypeSafe, Type: AccountTypeAPIKey}, "jev-latest")
	require.False(t, unsupported.TextSupported)
	require.Contains(t, unsupported.CapabilityReason, "System One")
	unknown := PelicanTestModelOptions(&Account{Platform: "legacy-unknown", Type: AccountTypeAPIKey}, "model")
	require.False(t, unknown.TextSupported)
	require.Contains(t, unknown.CapabilityReason, "legacy-unknown")
	manual := PelicanTestModelOptions(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "vendor/concrete-model")
	require.True(t, manual.TextSupported)
	require.Empty(t, manual.DefaultEffort)
}

func TestPelicanTestCatalogPassthroughAndDeclaredNoTextUseEffectiveModel(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"model_mapping": map[string]any{"gpt-6.1-sol": "gpt-image-2"},
	}, Extra: map[string]any{"openai_passthrough": true}}
	require.True(t, account.IsOpenAIPassthroughEnabled())
	option := PelicanTestModelOptions(account, "gpt-6.1-sol")
	require.Equal(t, "gpt-6.1-sol", option.UpstreamModel)
	require.True(t, option.TextSupported)
	require.Equal(t, "high", option.DefaultEffort)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		SourceIdentity: UpstreamModelMetadataSourceIdentity(account), Models: map[string]UpstreamModelMetadata{
			"gpt-6.1-sol": {ID: "gpt-6.1-sol", InputModalities: []string{"image"}},
		},
	})
	option = PelicanTestModelOptions(account, "gpt-6.1-sol")
	require.False(t, option.TextSupported)
	require.Contains(t, option.CapabilityReason, "no text input capability")
}

func TestPelicanTestCatalogNativeUpstreamAndAdaptiveUseBusinessEffortCapabilities(t *testing.T) {
	upstream := &Account{Platform: PlatformAntigravity, Type: AccountTypeUpstream, Credentials: map[string]any{
		"model_mapping": map[string]any{"claude-opus-5-5": "gemini-3-flash"},
	}}
	option := PelicanTestModelOptions(upstream, "claude-opus-5-5")
	require.True(t, option.TextSupported, option.CapabilityReason)
	require.Equal(t, "claude-opus-5-5", option.UpstreamModel, "native upstream forwarding preserves the request model")
	require.Contains(t, option.ReasoningEfforts, "high")
	require.Equal(t, "high", option.DefaultEffort)

	adaptive := &Account{Platform: PlatformKimi, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	reasoning := true
	adaptive.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		SourceIdentity: UpstreamModelMetadataSourceIdentity(adaptive), Models: map[string]UpstreamModelMetadata{
			"kimi-k2.5": {ID: "kimi-k2.5", Reasoning: &reasoning, SupportedReasoningLevels: []string{"medium", "high"}},
		},
	})
	option = PelicanTestModelOptions(adaptive, "kimi-k2.5")
	require.Equal(t, []string{"medium", "high"}, option.ReasoningEfforts, "single native request can use the declared effort on adaptive accounts")
	require.Equal(t, "high", option.DefaultEffort)
	adaptive.Credentials["api_protocol"] = APIProtocolAnthropic
	option = PelicanTestModelOptions(adaptive, "claude-opus-5-5")
	require.True(t, option.TextSupported)
	require.Contains(t, option.ReasoningEfforts, "high", "native Anthropic adapter retains Claude output_config.effort")

	invalidType := PelicanTestModelOptions(&Account{Platform: PlatformOpenAI, Type: AccountTypeBedrock}, "gpt-6-astra")
	require.False(t, invalidType.TextSupported)
	require.Contains(t, invalidType.CapabilityReason, AccountTypeBedrock)
}

func TestPelicanTestCatalogOpenAIModelCapabilityUsesNativeResolvedAlias(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://local-catalog.example", "model_mapping": map[string]any{"public-model": "gpt-6.1-sol-high"},
	}}
	reasoning := false
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		SourceIdentity: UpstreamModelMetadataSourceIdentity(account), Models: map[string]UpstreamModelMetadata{
			"gpt-6.1-sol": {ID: "gpt-6.1-sol", Reasoning: &reasoning, InputModalities: []string{"text"}},
		},
	})
	option := PelicanTestModelOptions(account, "public-model")
	require.True(t, option.TextSupported, option.CapabilityReason)
	require.Equal(t, "gpt-6.1-sol", option.UpstreamModel, "native OpenAI forwarding removes the finite effort suffix")
	require.Empty(t, option.ReasoningEfforts, "capabilities belong to the actual wire model, not its mapped spelling")
	require.Empty(t, option.DefaultEffort, "the saved non-reasoning declaration must not be bypassed by local defaults")
}
