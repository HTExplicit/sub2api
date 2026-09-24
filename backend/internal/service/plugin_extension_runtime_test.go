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

func firstPartyTestVersion(t *testing.T, domain string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "plugins", domain, "manifest.source.json"))
	require.NoError(t, err)
	var manifest PluginManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NotEmpty(t, manifest.Version)
	return manifest.Version
}

func TestExtensionRuntimeUsesOfficialProcessAndHostBroker(t *testing.T) {
	binary := os.Getenv("SUB2API_EXTENSION_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_EXTENSION_TEST_BINARY to the locally built codex runtime")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 7, PluginKey: "codexrip.codex-runtime", Version: firstPartyTestVersion(t, "codex-runtime"), BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}}}
	installation.Manifest.Capabilities = []PluginCapability{{ID: extensionv1.CapabilityScheduling, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil, PluginAccountScope{})
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

func TestCindyProviderExtensionRuntimeUsesIndependentProcess(t *testing.T) {
	binary := os.Getenv("SUB2API_CINDY_PROVIDER_TEST_BINARY")
	if binary == "" {
		t.Skip("set SUB2API_CINDY_PROVIDER_TEST_BINARY to the independent Cindy provider program")
	}
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{ID: 11, PluginKey: "codexrip.cindy-provider", Version: firstPartyTestVersion(t, "cindy-provider"), BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey}}}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil, PluginAccountScope{})
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
	installation := &PluginInstallation{ID: 10, PluginKey: "codexrip.account-tools", Version: firstPartyTestVersion(t, "account-tools"), BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:]), Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*"}}}}
	host := newPluginHostServiceServer(installation.PluginKey, nil, nil, PluginAccountScope{})
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
