package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type extensionReadOnlyFixture struct{ PluginExtensionStateStore }

func (*extensionReadOnlyFixture) ReadExtensionState(context.Context, string, extensionv1.StateRequest) (extensionv1.StateResult, error) {
	return extensionv1.StateResult{}, nil
}
func (*extensionReadOnlyFixture) DueExtensionStates(context.Context, string, extensionv1.DueStateRequest) ([]extensionv1.DueState, error) {
	return nil, nil
}

func TestExtensionRuntimeUsesOfficialProcessAndHostBroker(t *testing.T) {
	binary := os.Getenv("SUB2API_EXTENSION_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_EXTENSION_TEST_BINARY to the locally built codex runtime")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 7, PluginKey: "codexrip.codex-runtime", Version: "0.2.7", BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}}}
	installation.Manifest.Capabilities = []PluginCapability{{ID: extensionv1.CapabilityScheduling, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil)
	host.extension = &pluginExtensionHost{key: installation.PluginKey, state: &extensionReadOnlyFixture{}}
	socketDir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, socketDir, host)
	require.NoError(t, err)
	t.Cleanup(runtime.kill)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{"enabled":true,"fail_closed":true}`)))
	require.NotNil(t, runtime.scheduling.Load())
	require.Len(t, *runtime.scheduling.Load(), 1)
	health, err := runtime.status(context.Background())
	require.NoError(t, err)
	require.True(t, health.Healthy)
	raw, err := json.Marshal(extensionv1.SchedulingRequest{Account: extensionv1.Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Identity: "test-principal"}, Model: "gpt-6-astra", Now: time.Now().UTC()})
	require.NoError(t, err)
	result, err := runtime.extension.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityScheduling, Operation: "admit", Payload: raw})
	require.NoError(t, err)
	var decision extensionv1.SchedulingDecision
	require.NoError(t, json.Unmarshal(result.Payload, &decision))
	require.False(t, decision.Allowed)
	require.Equal(t, "ticket_missing", decision.Reason)
	// Only a read-only missing-state fixture is exposed. No token resolver or
	// HTTP upstream exists in this test, so it cannot issue a real model request.
}

func TestCatalogExtensionRuntimeUsesIndependentProcess(t *testing.T) {
	binary := os.Getenv("SUB2API_MODEL_POLICY_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_MODEL_POLICY_TEST_BINARY to the independent model-policy program")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 8, PluginKey: "codexrip.model-policy", Version: "0.2.7", BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityCatalog, Platform: "*", AccountType: "*"}}}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil)
	host.extension = &pluginExtensionHost{key: installation.PluginKey, state: &extensionReadOnlyFixture{}}
	socketDir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, socketDir, host)
	require.NoError(t, err)
	t.Cleanup(runtime.kill)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{}`)))
	raw, _ := json.Marshal(extensionv1.CatalogQuery{Candidates: []string{"gpt-6-astra"}, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Scheme: "https", Host: "api.openai.com"})
	result, err := runtime.extension.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityCatalog, Operation: "resolve", Payload: raw})
	require.NoError(t, err)
	var match extensionv1.CatalogMatch
	require.NoError(t, json.Unmarshal(result.Payload, &match))
	require.NotNil(t, match.Entry)
	require.EqualValues(t, 1050000, match.Entry.ContextWindow)
	require.Equal(t, "gpt-6-astra", match.Entry.ModelID)
}

func TestPromptExtensionRuntimeUsesIndependentProcess(t *testing.T) {
	binary := os.Getenv("SUB2API_PROMPT_SKILLS_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_PROMPT_SKILLS_TEST_BINARY to the independent prompt policy program")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 9, PluginKey: "codexrip.prompt-skills", Version: "0.2.7", BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityRequest, Platform: PlatformOpenAI, AccountType: "*"}}}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil)
	host.extension = &pluginExtensionHost{key: installation.PluginKey, state: &extensionReadOnlyFixture{}}
	socketDir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, socketDir, host)
	require.NoError(t, err)
	t.Cleanup(runtime.kill)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{}`)))
	raw, _ := json.Marshal(extensionv1.PromptPlanRequest{Snapshot: BusinessSystemPromptSnapshot{Enabled: true, Body: "fixture-server"}, Target: BusinessSystemPromptTarget{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Protocol: BusinessSystemPromptProtocolResponses}})
	out, err := runtime.extension.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.plan", Payload: raw})
	require.NoError(t, err)
	require.Empty(t, out.Code)
	var application BusinessSystemPromptApplication
	require.NoError(t, json.Unmarshal(out.Payload, &application))
	require.True(t, application.Applied)
	require.Equal(t, "fixture-server", application.ServerInstructions)
}

func TestImageToolsExtensionRuntimeUsesIndependentProcess(t *testing.T) {
	binary := os.Getenv("SUB2API_IMAGE_TOOLS_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_IMAGE_TOOLS_TEST_BINARY to the independent image tools program")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 12, PluginKey: "codexrip.image-tools", Version: "0.2.7", BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityRequest, Platform: "*", AccountType: "*"}}}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil)
	host.extension = &pluginExtensionHost{key: installation.PluginKey, state: &extensionReadOnlyFixture{}}
	socketDir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, socketDir, host)
	require.NoError(t, err)
	t.Cleanup(runtime.kill)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{}`)))
	raw, _ := json.Marshal(extensionv1.ImageStudioPlanRequest{APIKeySelected: true, Mode: "generate", Model: "gpt-image-2", PromptBytes: 5, Count: 4})
	invocation := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "image.studio.plan", Payload: raw}
	out, err := runtime.extension.Invoke(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, "studio_disabled", out.Code)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{"studio_enabled":true}`)))
	out, err = runtime.extension.Invoke(context.Background(), invocation)
	require.NoError(t, err)
	require.Empty(t, out.Code)
	var plan extensionv1.ImageStudioPlan
	require.NoError(t, json.Unmarshal(out.Payload, &plan))
	require.Equal(t, 1, plan.OutputPerRequest)
	require.Equal(t, "/v1/images/generations", plan.Endpoint)
}

func TestCindyProviderExtensionRuntimeUsesIndependentProcess(t *testing.T) {
	binary := os.Getenv("SUB2API_CINDY_PROVIDER_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_CINDY_PROVIDER_TEST_BINARY to the independent Cindy provider program")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 11, PluginKey: "codexrip.cindy-provider", Version: "0.2.7", BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey}}}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil)
	host.extension = &pluginExtensionHost{key: installation.PluginKey, state: &extensionReadOnlyFixture{}}
	socketDir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, socketDir, host)
	require.NoError(t, err)
	t.Cleanup(runtime.kill)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{"catalog_enabled":true,"search_enabled":true}`)))
	out, err := runtime.extension.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.pricing", Payload: []byte(`{}`)})
	require.NoError(t, err)
	require.Empty(t, out.Code)
	var snapshot extensionv1.CindyPricingSnapshot
	require.NoError(t, json.Unmarshal(out.Payload, &snapshot))
	require.True(t, snapshot.Config.CatalogEnabled)
	var pricing CindyTextPricing
	var known bool
	require.True(t, queryCindyPricingSnapshot(&snapshot, "CindyTextPricingForModel", "gpt-5.6-luna", []any{&pricing, &known}))
	require.True(t, known)
	require.Positive(t, pricing.InputCostPerToken)
}

func TestAccountToolsExtensionRuntimeUsesIndependentProcess(t *testing.T) {
	binary := os.Getenv("SUB2API_ACCOUNT_TOOLS_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_ACCOUNT_TOOLS_TEST_BINARY to the independent account-tools program")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 10, PluginKey: "codexrip.account-tools", Version: "0.2.7", BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*"}}}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil)
	host.extension = &pluginExtensionHost{key: installation.PluginKey, state: &extensionReadOnlyFixture{}}
	socketDir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, socketDir, host)
	require.NoError(t, err)
	t.Cleanup(runtime.kill)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{}`)))
	out, err := runtime.extension.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "taxonomy.name", Payload: []byte(`{"name":"  Production  "}`)})
	require.NoError(t, err)
	require.Empty(t, out.Code)
	var name extensionv1.TaxonomyName
	require.NoError(t, json.Unmarshal(out.Payload, &name))
	require.Equal(t, "Production", name.Name)
	require.Equal(t, "production", name.Normalized)
}
