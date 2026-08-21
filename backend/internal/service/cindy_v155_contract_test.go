package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCindyV155CatalogContract(t *testing.T) {
	require.Equal(t, "makecindy/cindy@v0.1.55+8932b34ca684f3a0794e023c93797b0e23603f49", CindyModelMetadataSourceRevision)

	public := []string{
		"claude-opus-4-8",
		"claude-opus-5",
		"claude-sonnet-5",
		"gemini-3-pro-image",
		"gemini-3.6-flash",
		"glm-5.2",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-image-2",
		"grok-4.5",
		"grok-4.6",
	}
	sort.Strings(public)
	require.Equal(t, public, CindyPublicModelIDs())
	projectedPublic := make([]string, 0, len(public))
	for _, capability := range CindyVerifiedModelCapabilities() {
		projectedPublic = append(projectedPublic, capability.ID)
	}
	require.Equal(t, public, projectedPublic)

	hidden := []string{
		"cindy/auto-review",
		"cindy/web-search",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"gemini-3.5-flash",
		"gemini-3.7-flash",
		"glm-5.3",
		"hy3",
		"kimi-k3",
		"qwen/qwen3.8-max-preview",
		"seed-2.1-pro",
	}
	sort.Strings(hidden)
	models := CindyCatalogModels()
	gotHidden := make([]string, 0, len(hidden))
	for _, model := range models {
		if !model.PublicModel {
			gotHidden = append(gotHidden, model.ID)
		}
	}
	require.Equal(t, hidden, gotHidden)

	byID := make(map[string]CindyCatalogModel, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	require.Len(t, byID, 23)

	expectedCapacity := map[string]struct {
		context int
		source  CindyCapacitySource
	}{
		"claude-opus-4-8":          {1000000, CindyCapacityPinnedRegistry},
		"claude-opus-5":            {1000000, CindyCapacityPinnedRegistry},
		"claude-sonnet-5":          {1000000, CindyCapacityPinnedRegistry},
		"seed-2.1-pro":             {256000, CindyCapacityPinnedRegistry},
		"cindy/auto-review":        {0, CindyCapacityUnknown},
		"cindy/web-search":         {0, CindyCapacityUnknown},
		"deepseek-v4-flash":        {1048576, CindyCapacityPinnedRegistry},
		"deepseek-v4-pro":          {1048576, CindyCapacityPinnedRegistry},
		"gemini-3-pro-image":       {0, CindyCapacityUnknown},
		"gemini-3.5-flash":         {1000000, CindyCapacityPinnedRegistry},
		"gemini-3.6-flash":         {1000000, CindyCapacityApprovedManual},
		"gemini-3.7-flash":         {1000000, CindyCapacityApprovedManual},
		"kimi-k3":                  {1000000, CindyCapacityPinnedRegistry},
		"gpt-5.6-luna":             {1050000, CindyCapacityPinnedRegistry},
		"gpt-5.6-sol":              {1050000, CindyCapacityPinnedRegistry},
		"gpt-5.6-terra":            {1050000, CindyCapacityPinnedRegistry},
		"gpt-image-2":              {0, CindyCapacityUnknown},
		"qwen/qwen3.8-max-preview": {983616, CindyCapacityPinnedRegistry},
		"hy3":                      {0, CindyCapacityUnknown},
		"grok-4.5":                 {500000, CindyCapacityPinnedRegistry},
		"grok-4.6":                 {500000, CindyCapacityApprovedManual},
		"glm-5.2":                  {1000000, CindyCapacityPinnedRegistry},
		"glm-5.3":                  {1000000, CindyCapacityApprovedManual},
	}
	require.Len(t, expectedCapacity, len(byID))
	for id, expected := range expectedCapacity {
		model, ok := byID[id]
		require.True(t, ok, id)
		require.Equal(t, expected.context, model.BaseContextWindow, id)
		require.Equal(t, expected.context, model.CodexContextWindow, id)
		require.Equal(t, expected.source, model.CapacitySource, id)
	}

	qwen, ok := resolveKnownCindyCapability("qwen/qwen3.8-max-preview")
	require.True(t, ok)
	require.Equal(t, "qwen/qwen3.8-max-preview", qwen.RegistryID)
	require.Equal(t, "qwen/qwen3.8-max", qwen.LiveUpstreamID)
	_, oldQwenID := resolveKnownCindyCapability("qwen3.8-max")
	require.False(t, oldQwenID)
}

func TestCindyCodexV0147Contract(t *testing.T) {
	for _, version := range []string{"", "0.147.0", "v0.147.0", "0.148.1"} {
		require.NoError(t, ValidateCindyCodexClientVersion(version), version)
	}
	for _, version := range []string{"0.146.0", "0.147", "latest"} {
		require.Error(t, ValidateCindyCodexClientVersion(version), version)
	}

	manifest, err := BuildCindyCodexModelsManifest("")
	require.NoError(t, err)
	var envelope struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(manifest.Body, &envelope))
	for _, model := range envelope.Models {
		var slug string
		require.NoError(t, json.Unmarshal(model["slug"], &slug))
		if slug != "gpt-5.6-luna" && slug != "gpt-5.6-sol" && slug != "gpt-5.6-terra" {
			continue
		}
		var contextWindow, maxContextWindow, autoCompact int
		require.NoError(t, json.Unmarshal(model["context_window"], &contextWindow))
		require.NoError(t, json.Unmarshal(model["max_context_window"], &maxContextWindow))
		require.NoError(t, json.Unmarshal(model["auto_compact_token_limit"], &autoCompact))
		require.Equal(t, 1050000, contextWindow, slug)
		require.Equal(t, 1050000, maxContextWindow, slug)
		require.Equal(t, 900000, autoCompact, slug)
		for _, field := range []string{"include_plugin_usage_instructions", "include_apps_usage_instructions", "model_specialty"} {
			_, exists := model[field]
			require.True(t, exists, "%s missing %s", slug, field)
		}
	}
}

func TestCindyResponsesImageBridgeIsIndependentAndGPTImage2Only(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCindyResponsesImageBridgeHelper$")
	cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
		CindyCapabilityCatalogEnabledEnv,
		ImageStudioEnabledEnv,
		CindyImageStudioEnabledEnv,
		CindyResponsesImageBridgeEnabledEnv,
		"SUB2API_CINDY_RESPONSES_IMAGE_HELPER",
	),
		CindyCapabilityCatalogEnabledEnv+"=false",
		ImageStudioEnabledEnv+"=false",
		CindyResponsesImageBridgeEnabledEnv+"=true",
		"SUB2API_CINDY_RESPONSES_IMAGE_HELPER=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated Responses image check failed: %v\n%s", err, output)
	}
}

func TestCindyResponsesImageBridgeHelper(t *testing.T) {
	if os.Getenv("SUB2API_CINDY_RESPONSES_IMAGE_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	require.True(t, CindyResponsesImageBridgeFeatureEnabled())
	require.False(t, ImageStudioFeatureEnabled())
	require.True(t, CindyModelSupportsResponsesImageBridge("gpt-image-2"))
	require.True(t, CindyModelSupportsResponsesImageBridge("openai/gpt-image-2"))
	require.False(t, CindyModelSupportsResponsesImageBridge("gemini-3-pro-image"))
	_, gptEnabled := ResolveCindyCapability("gpt-image-2")
	require.True(t, gptEnabled)
	_, geminiEnabled := ResolveCindyCapability("gemini-3-pro-image")
	require.False(t, geminiEnabled)
	require.Empty(t, CindyImageModelCapabilities())
}

func TestCindyImageStudioDoesNotDependOnCatalog(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCindyImageStudioIndependentHelper$")
	cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
		CindyCapabilityCatalogEnabledEnv,
		ImageStudioEnabledEnv,
		CindyImageStudioEnabledEnv,
		CindyResponsesImageBridgeEnabledEnv,
		"SUB2API_CINDY_STUDIO_INDEPENDENT_HELPER",
	),
		CindyCapabilityCatalogEnabledEnv+"=false",
		ImageStudioEnabledEnv+"=true",
		CindyResponsesImageBridgeEnabledEnv+"=false",
		"SUB2API_CINDY_STUDIO_INDEPENDENT_HELPER=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated Image Studio check failed: %v\n%s", err, output)
	}
}

func TestCindyImageStudioIndependentHelper(t *testing.T) {
	if os.Getenv("SUB2API_CINDY_STUDIO_INDEPENDENT_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	require.False(t, CindyCapabilityCatalogFeatureEnabled())
	require.True(t, ImageStudioFeatureEnabled())
	require.False(t, CindyResponsesImageBridgeFeatureEnabled())
	require.True(t, CindyModelSupportsEndpoint("gpt-image-2", CindyEndpointImagesGenerate))
	require.True(t, CindyModelSupportsEndpoint("gemini-3-pro-image", CindyEndpointImagesEdit))
	require.Len(t, CindyImageModelCapabilities(), 2)
	account := &Account{
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Credentials:     cindyCredentials(),
	}
	require.Equal(t, "google/gemini-3-pro-image", account.GetMappedModel("gemini-3-pro-image"))
}
