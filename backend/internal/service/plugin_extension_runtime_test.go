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
