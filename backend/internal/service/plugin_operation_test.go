package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestNamedRequestOperationsDoNotReceiveAnotherDomainsHeaders(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		firstCalls++
		require.Equal(t, "inject", in.Operation)
		return extensionv1.Result{Payload: []byte(`{"headers":{"X-Ticket-Fixture":"ready"}}`)}, nil
	})
	second := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		secondCalls++
		require.Equal(t, "prompt.plan", in.Operation)
		return extensionv1.Result{PluginID: 999, Payload: []byte(`{}`)}, nil
	})
	registry := manager.extensions.Load()
	other := second.extensions.Load()
	installation := other.installations[1]
	installation.ID = 2
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityRequest: {"prompt.plan"}}
	registry.installations[2], registry.runtimes[2] = installation, other.runtimes[1]
	manager.extensions.Store(registry)
	headers := http.Header{}
	require.NoError(t, manager.ApplyRequestHeaders(context.Background(), ticketTestAccount(41), "gpt-6-astra", headers))
	require.Equal(t, 1, firstCalls)
	require.Zero(t, secondCalls)
	require.Equal(t, "ready", headers.Get("X-Ticket-Fixture"))
	request := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.plan", Payload: []byte(`{}`)}
	result, err := manager.InvokeOperation(context.Background(), PlatformOpenAI, AccountTypeOAuth, request)
	require.NoError(t, err)
	require.EqualValues(t, 2, result.PluginID, "the host owns operation provenance")
	require.Equal(t, 1, secondCalls)
	_, err = manager.InvokeOperation(context.Background(), PlatformOpenAI, AccountTypeAPIKey, request)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	registry.runtimes[2].draining.Store(true)
	_, err = manager.InvokeOperation(context.Background(), PlatformOpenAI, AccountTypeOAuth, request)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
}

func TestNamedOperationOwnershipRejectsOverlappingWildcardBindings(t *testing.T) {
	one := &PluginInstallation{ID: 1, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "openai", AccountType: "*", Enabled: true}}, Manifest: PluginManifest{Operations: map[string][]string{extensionv1.CapabilityRequest: {"prompt.plan"}}}}
	two := &PluginInstallation{ID: 2, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "openai", AccountType: "oauth", Enabled: true}}, Manifest: PluginManifest{Operations: map[string][]string{extensionv1.CapabilityRequest: {"prompt.plan"}}}}
	require.Error(t, validatePluginRegistry([]*PluginInstallation{one, two}))
	two.Manifest.Operations[extensionv1.CapabilityRequest] = []string{"another.operation"}
	require.NoError(t, validatePluginRegistry([]*PluginInstallation{one, two}))
	two.Manifest.Operations[extensionv1.CapabilityRequest] = []string{"prompt.plan"}
	two.Bindings[0].Enabled = false
	require.NoError(t, validatePluginRegistry([]*PluginInstallation{one, two}))
}
