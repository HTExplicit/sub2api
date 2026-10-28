package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
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
	for _, unknown := range []string{"gpt-6-future", "gpt-6-solstice"} {
		require.Equal(t, unknown, normalizeCodexModel(unknown))
		require.Empty(t, normalizeKnownOpenAICodexModel(unknown))
	}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol", "gpt-6.1-terra"} {
		require.Equal(t, model, normalizeCodexModel(model+"-2099-01-01"))
	}
	require.Equal(t, "gpt-6-astra", normalizeCodexModel("gpt-6"))
}

func TestGPT6NativeReasoning(t *testing.T) {
	api := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		body := []byte(`{"models":[{"slug":"openai/` + model + `","use_responses_lite":true,"context_window":333000,"max_context_window":777000,"auto_compact_token_limit":222000}]}`)
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
