package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func scopedPromptManager(t *testing.T) *PluginManager {
	t.Helper()
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		switch in.Operation {
		case "prompt.availability":
			return extensionv1.Result{Payload: []byte(`{"ready":true}`)}, nil
		case "prompt.template":
			return extensionv1.Result{Payload: []byte(`{"name":"scoped"}`)}, nil
		default:
			t.Fatalf("unexpected domain operation: %s", in.Operation)
			return extensionv1.Result{}, nil
		}
	})
	installation := manager.extensions.Load().installations[1]
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true}}
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityRequest: {"prompt.availability", "prompt.template", "prompt.source.fetch", "prompt.plan"}}
	return manager
}

func TestPromptDomainUsesActiveScopeWithoutBroadeningRequestBindings(t *testing.T) {
	manager := scopedPromptManager(t)
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	require.NoError(t, promptPolicyAvailability(context.Background()))
	prompts := &BusinessSystemPromptService{}
	prompts.snapshot.Store(&BusinessSystemPromptSnapshot{Enabled: true, Body: "scoped-server"})
	snapshot, ok := prompts.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, "scoped-server", snapshot.Body)
	name := "scoped"
	plan, err := planPromptTemplate(context.Background(), extensionv1.PromptTemplatePolicyRequest{Name: &name})
	require.NoError(t, err)
	require.Equal(t, name, *plan.Name)
	body := []byte(`{"input":"client"}`)
	out, applied, err := ApplyBusinessSystemPromptToJSONContext(context.Background(), body, snapshot,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Protocol: BusinessSystemPromptProtocolResponses})
	require.NoError(t, err)
	require.Equal(t, body, out)
	require.False(t, applied.Applied, "global metadata must not broaden account request scope")
}

func TestPromptDomainAvailabilityDistinguishesDisabledFailureAndAmbiguousOwners(t *testing.T) {
	manager := scopedPromptManager(t)
	query := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.availability", Payload: []byte(`{}`)}
	registry := manager.extensions.Load()
	registry.installations[1].Bindings[0].Enabled = false
	_, err := manager.InvokeDomainOperation(context.Background(), query, true)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	registry.installations[1].Bindings[0].Enabled = true
	registry.runtimes[1].draining.Store(true)
	_, err = manager.InvokeDomainOperation(context.Background(), query, true)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	registry.runtimes[1].draining.Store(false)
	other := *registry.installations[1]
	other.ID = 2
	other.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Enabled: true}}
	registry.installations[2] = &other
	_, err = manager.InvokeDomainOperation(context.Background(), query, true)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
}

func TestPromptDomainSourceContextStillCancelsWhenScopedPluginStops(t *testing.T) {
	manager := scopedPromptManager(t)
	ctx, cancel, err := manager.BindDomainOperationContext(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "prompt.source.fetch",
	})
	require.NoError(t, err)
	defer cancel()
	manager.extensions.Load().runtimes[1].beginDrain()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestResourceContextSelectsItsDeclaredDomainOwner(t *testing.T) {
	manager := scopedPromptManager(t)
	registry := manager.extensions.Load()
	first := registry.installations[1]
	other := *first
	other.ID = 2
	other.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Enabled: true}}
	registry.installations[2] = &other
	input := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.template"}
	_, _, err := manager.domainOperationScope(context.Background(), input)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	platform, kind, err := manager.domainOperationScope(WithPluginExecution(context.Background(), first), input)
	require.NoError(t, err)
	require.Equal(t, PlatformOpenAI, platform)
	require.Equal(t, AccountTypeOAuth, kind)
}
