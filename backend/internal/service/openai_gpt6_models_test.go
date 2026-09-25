package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT6NamedModelRoutingAndWireEffort(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		require.Equal(t, model, normalizeCodexModel("openai/"+model))
		require.Equal(t, model, normalizeKnownOpenAICodexModel(model+"-max"))
		require.False(t, isOpenAIGPT6AstraModel(model))
		for _, effort := range []string{"none", "max"} {
			request := map[string]any{"model": model + "-" + effort, "reasoning": map[string]any{"summary": "none"}}
			require.True(t, applyOpenAIModelReasoningAlias(request))
			require.Equal(t, model, request["model"])
			reasoning, ok := request["reasoning"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, effort, reasoning["effort"])
			request = map[string]any{"model": model + "-" + effort, "reasoning": map[string]any{"effort": "high"}}
			require.True(t, applyOpenAIModelReasoningAlias(request))
			reasoning, ok = request["reasoning"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "high", reasoning["effort"])
		}
		req := &apicompat.AnthropicRequest{OutputConfig: &apicompat.AnthropicOutputConfig{Effort: "max"}}
		require.Equal(t, "max", openAICompatAnthropicReasoningEffort(req, model, "xhigh"))
	}
	for _, unknown := range []string{"gpt-6-future", "gpt-6-solstice", "gpt-6-luna-2099-01-01"} {
		require.Equal(t, unknown, normalizeCodexModel(unknown))
		require.Empty(t, normalizeKnownOpenAICodexModel(unknown))
	}
	require.Equal(t, "gpt-6-astra", normalizeCodexModel("gpt-6"))
}

func TestGPT6CodexReferenceAndAPICatalogDefaults(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com"}}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		descriptor := newConfiguredCodexModelDescriptor(model)
		require.EqualValues(t, 272000, descriptor.ContextWindow)
		require.EqualValues(t, 872000, descriptor.MaxContextWindow)
		require.Nil(t, descriptor.AutoCompactTokenLimit)
		require.Nil(t, descriptor.MultiAgentReasoningEffort)
		require.Equal(t, "v2", descriptor.MultiAgentVersion)
		require.Equal(t, "0.155.0", descriptor.MinimalClientVersion)
		require.Equal(t, "medium", *descriptor.DefaultReasoningLevel)
		var efforts []string
		for _, level := range descriptor.SupportedReasoningLevels {
			efforts = append(efforts, level.Effort)
		}
		require.Equal(t, model == "gpt-6-sol", stringSliceContains(efforts, "ultra"))
		require.Contains(t, efforts, "max")
		require.NotContains(t, efforts, "none")
		require.Equal(t, []string{"text", "image"}, descriptor.InputModalities)
		require.Equal(t, "priority", descriptor.ServiceTiers[0].ID)

		body := convertOpenAIModelListToCodexManifestForAccount([]byte(`{"data":[{"id":"`+model+`"}]}`), account)
		require.EqualValues(t, 272000, gjson.GetBytes(body, "models.0.context_window").Int(), "API defaults carry capabilities, not a capacity")
		require.EqualValues(t, 872000, gjson.GetBytes(body, "models.0.max_context_window").Int())
		require.Equal(t, "medium", gjson.GetBytes(body, "models.0.default_reasoning_level").String())
		models := decodeCodexManifestModels(t, body)
		require.Equal(t, openai.GPT6APIReasoningEfforts(), effortsFromManifestModel(t, models[0]))
		require.False(t, gjson.GetBytes(body, "models.0.use_responses_lite").Bool())
	}
}

func TestGPT6CapacityUsesSubscriptionReferenceBelowObservations(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			api := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}
			reference := LookupOfficialModelContextCapacity(api, "openai/"+model)
			require.NotNil(t, reference)
			require.EqualValues(t, 272000, reference.ContextWindow)
			require.EqualValues(t, 872000, reference.MaxContextWindow)
			require.EqualValues(t, 128000, reference.MaxOutputTokens)
			require.Zero(t, reference.MaxInputTokens)
			require.Equal(t, openai.GPT6CodexReferenceSource, reference.Reference.SourceURL)
			require.Nil(t, LookupOfficialModelContextCapacity(api, model+"-2099-01-01"))
			declared := UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
				model: {ID: model, ContextWindow: 200000, MaxContextWindow: 400000, MaxOutputTokens: 64000},
			}}
			for _, baseURL := range []string{"https://api.openai.com", "https://relay.example.test/v1"} {
				api.Credentials["base_url"] = baseURL
				api.Extra = map[string]any{UpstreamModelMetadataExtraKey: declared}
				observed := ResolveAccountModelContextCapacity(api, model)
				require.EqualValues(t, 200000, observed.ContextWindow, "the account's own declaration wins on every host")
				require.EqualValues(t, 64000, observed.MaxOutputTokens)
				require.Equal(t, "upstream", observed.Source)
			}
			api.Extra[ModelContextOverridesExtraKey] = map[string]int64{model: 512000}
			require.EqualValues(t, 512000, ResolveAccountModelContextCapacity(api, model).ContextWindow)

			oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			fallback := ResolveAccountModelContextCapacity(oauth, model)
			require.Equal(t, "official", fallback.Source, "an OAuth account participates through the reference catalog")
			require.EqualValues(t, 272000, fallback.ContextWindow)
			require.EqualValues(t, 872000, fallback.MaxContextWindow)
			rows := BuildAccountModelContextCapacityRows(oauth, []string{model})
			require.Len(t, rows, 1)
			require.True(t, rows[0].Editable)
			require.NotNil(t, rows[0].Official.Reference)
			oauth.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
				model: {ID: model, ContextWindow: 300000, MaxContextWindow: 900000, MaxOutputTokens: 48000},
			}})
			observed := ResolveAccountModelContextCapacity(oauth, model)
			require.EqualValues(t, 300000, observed.ContextWindow)
			require.EqualValues(t, 900000, observed.MaxContextWindow)
			require.EqualValues(t, 48000, observed.MaxOutputTokens)
			require.Equal(t, "upstream", observed.Source)
		})
	}
}

func TestGPT6APIKeyLiteAndNativeReasoning(t *testing.T) {
	api := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		body := []byte(`{"models":[{"slug":"openai/` + model + `","use_responses_lite":true,"context_window":333000,"max_context_window":777000,"auto_compact_token_limit":222000}]}`)
		adjusted, err := adjustAPIKeyCodexModelsManifest(body, api)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(adjusted, "models.0.use_responses_lite").Bool())
		manifest := &OpenAIModelsResponse{Body: body}
		require.NoError(t, (&OpenAIGatewayService{}).CompleteAPIKeyCodexModelsManifestForClient(manifest, oauth))
		require.Equal(t, body, manifest.Body, "OAuth raw manifest must not receive API-key adjustments")
		levels, defaultLevel := AccountTestReasoningOptions(api, model)
		require.Equal(t, openai.GPT6APIReasoningEfforts(), levels)
		require.Equal(t, "medium", defaultLevel)
		levels, _ = AccountTestReasoningOptions(oauth, model)
		require.Contains(t, levels, "max")
		require.NotContains(t, levels, "ultra", "delegation is not an account-test wire effort")
	}
}

func TestGPT6ProviderMetadataOverridesAPIDefaults(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example.test"}}
	reasoning := true
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
		"gpt-6-sol": {ID: "gpt-6-sol", Reasoning: &reasoning, DefaultReasoningLevel: "max", SupportedReasoningLevels: []string{"high", "max"}, InputModalities: []string{"text", "image"}, ContextWindow: 100000},
	}})
	body := []byte(`{"data":[{"id":"gpt-6-sol","reasoning":false,"input_modalities":["text"],"context_window":64000,"max_output_tokens":8000,"auto_compact_token_limit":50000,"supports_search_tool":false}]}`)
	converted := convertOpenAIModelListToCodexManifestForAccount(body, account)
	manifest := &OpenAIModelsResponse{Body: converted, upstreamSourceBody: body, convertedFromOpenAIModelList: true}
	require.NoError(t, (&OpenAIGatewayService{}).CompleteAPIKeyCodexModelsManifestForClient(manifest, account))
	converted = manifest.Body
	model := decodeCodexManifestModels(t, converted)[0]
	levels := effortsFromManifestModel(t, model)
	for _, effort := range []string{"max", "ultra"} {
		require.NotContains(t, levels, effort)
	}
	require.Equal(t, []any{"text"}, model["input_modalities"])
	require.EqualValues(t, 64000, model["context_window"])
	require.Equal(t, false, model["supports_search_tool"])
}

func TestGPT6ConfiguredOAuthCatalogUsesObservedCapacity(t *testing.T) {
	const groupID int64 = 764
	account := Account{ID: 25, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-sol": "gpt-6-sol"}}}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
		"gpt-6-sol": {ID: "gpt-6-sol", ContextWindow: 320000, MaxContextWindow: 940000, CapacitySource: ModelContextSourceUpstream},
	}})
	svc := &OpenAIGatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{groupID: {account}}}}
	manifest, configured, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: groupID, Platform: PlatformOpenAI}, "")
	require.NoError(t, err)
	require.True(t, configured)
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, "gpt-6-sol", model["slug"])
	require.EqualValues(t, 320000, model["context_window"])
	require.EqualValues(t, 940000, model["max_context_window"])
	require.Nil(t, model["auto_compact_token_limit"])
	require.Equal(t, true, model["use_responses_lite"])
}
