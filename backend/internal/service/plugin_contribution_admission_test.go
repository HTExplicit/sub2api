package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestContributionAdmissionIntersectsScopesAndPreservesSharedLegacy(t *testing.T) {
	var vectors []struct {
		ID     int64  `json:"id"`
		Bucket uint64 `json:"bucket"`
	}
	raw, err := os.ReadFile("testdata/plugin_contribution_buckets.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &vectors))
	for _, vector := range vectors {
		require.Equal(t, vector.Bucket, stablePluginBucket(vector.ID))
	}
	contribution := &extensionv1.Contribution{Capability: extensionv1.CapabilityCredentials, Action: "harvest", Permission: "admin", Slot: "account.actions"}
	installation := &PluginInstallation{Bindings: []PluginBinding{
		{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 70},
		{Capability: extensionv1.CapabilityAdmin, Platform: PlatformOpenAI, AccountType: AccountTypeSetupToken, Enabled: true, RolloutPercent: 100},
		{Capability: extensionv1.CapabilityCredentials, Platform: PlatformOpenAI, AccountType: "*", Enabled: true, RolloutPercent: 50},
		{Capability: extensionv1.CapabilityCredentials, Platform: PlatformOpenAI, AccountType: AccountTypeSetupToken, Enabled: true, RolloutPercent: 80},
	}}
	require.Equal(t, []PluginContributionScope{{PlatformOpenAI, AccountTypeOAuth, 50}, {PlatformOpenAI, AccountTypeSetupToken, 80}}, contributionEffectiveBindings(installation, contribution))
	require.True(t, pluginUIContributionBindingEnabled(installation, contribution), "partial scope must not globally hide a contribution")
	require.True(t, contributionAccountAllowed(installation, contribution, extensionv1.Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.False(t, contributionAccountAllowed(installation, contribution, extensionv1.Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.True(t, contributionAccountAllowed(installation, contribution, extensionv1.Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeSetupToken}))
	legacy := &extensionv1.Contribution{Slot: "admin.settings", Action: "legacy.describe", Permission: "admin"}
	installation.Bindings = installation.Bindings[2:]
	require.True(t, pluginUIContributionBindingEnabled(installation, legacy), "old missing-primary page admission remains compatible")
	require.False(t, contributionAccountAllowed(installation, legacy, extensionv1.Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}), "legacy action execution still requires Admin")
	shared := &extensionv1.Contribution{Capability: extensionv1.CapabilityCredentials, Slot: "admin.settings", Permission: "admin"}
	require.True(t, pluginUIContributionBindingEnabled(installation, shared), "shared management does not require wildcard scope or an invented account 0")
}

func TestContributionAdminAdmissionRequiresDeclaredBusinessCapability(t *testing.T) {
	for _, business := range []string{extensionv1.CapabilityObservability, extensionv1.CapabilityCredentials} {
		manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, nil)
		installation := manager.extensions.Load().installations[1]
		installation.State = PluginStateEnabled
		installation.Manifest.Contributions = []extensionv1.Contribution{{ID: "fixture", Slot: "account.actions", Permission: "admin", Capability: business, Action: "fixture.run"}}
		installation.Bindings = []PluginBinding{
			{Capability: extensionv1.CapabilityAdmin, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100},
			{Capability: business, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 50},
		}
		repo := &pluginTokenRepository{installation: installation}
		manager.repo = repo
		manager.accountDirectory = &resourceAccountDirectory{accounts: map[int64]extensionv1.Account{
			2: {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, 3: {ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		}}
		visible := manager.Contributions()
		require.Len(t, visible, 1, "partial rollout must remain present in the host registry")
		require.True(t, visible[0].Available)
		require.Equal(t, &PluginContributionAccountScope{Version: 1, Bindings: []PluginContributionScope{{PlatformOpenAI, AccountTypeOAuth, 50}}}, visible[0].AccountScope)
		require.NoError(t, manager.ValidateAdminExtension(context.Background(), 1, 2, "fixture.run"))
		require.Error(t, manager.ValidateAdminExtension(context.Background(), 1, 3, "fixture.run"), business)
		changed := *installation
		changed.Bindings = append([]PluginBinding(nil), installation.Bindings...)
		changed.Bindings[1].Enabled = false
		repo.installation = &changed
		require.Error(t, manager.ValidateAdminExtension(context.Background(), 1, 2, "fixture.run"), "persisted business-cap disable must reject before dispatch")
	}
}

func TestContributionAdminAdmissionRejectsUnappliedConfiguration(t *testing.T) {
	calls := 0
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		calls++
		require.Equal(t, extensionv1.CapabilityAdmin, in.Capability)
		require.Equal(t, "telemetry.read", in.Operation)
		require.EqualValues(t, 2, in.AccountID)
		return extensionv1.Result{Payload: json.RawMessage(`{"invoked":true}`)}, nil
	})
	registry := manager.extensions.Load()
	installation := registry.installations[1]
	installation.State, installation.RuntimeGeneration, installation.Revision = PluginStateEnabled, 1, 1
	installation.PackageSHA256, installation.ConfigEncrypted = strings.Repeat("a", 64), "fixture-telemetry-enabled"
	installation.Manifest.Contributions = []extensionv1.Contribution{{ID: "account-traffic", Slot: "account.details", Permission: "admin", Capability: extensionv1.CapabilityObservability, Action: "telemetry.read", ConfigFlag: "telemetry_enabled"}}
	installation.Bindings = []PluginBinding{
		{Capability: extensionv1.CapabilityAdmin, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100},
		{Capability: extensionv1.CapabilityObservability, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100},
	}
	// This repository explicitly implements every port method. The positive
	// control must reach the serialized RPC before any rejection is asserted.
	repo := &hostIOAdmissionRepository{t: t, current: cloneHostIOInstallation(installation)}
	manager.repo = repo
	manager.accountDirectory = &resourceAccountDirectory{accounts: map[int64]extensionv1.Account{2: {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}}
	runtime := registry.runtimes[1]
	runtime.installation = cloneHostIOInstallation(installation)
	enabled := json.RawMessage(`{"telemetry_enabled":true}`)
	disabled := json.RawMessage(`{"telemetry_enabled":false}`)
	runtime.configSnapshot.Store(&enabled)

	result, err := manager.InvokeAdminExtension(context.Background(), 1, 2, "telemetry.read", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"invoked":true}`, string(result.Payload))
	require.Equal(t, 1, calls, "the declared operation and repository must work before testing stale configuration")
	calls = 0

	repo.current = cloneHostIOInstallation(installation)
	repo.current.Revision++
	repo.current.ConfigEncrypted = "fixture-telemetry-disabled"
	result, err = manager.InvokeAdminExtension(context.Background(), 1, 2, "telemetry.read", json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "fresh disabled configuration cannot be authorized by the old true snapshot")
	require.Empty(t, result.Payload)
	require.Zero(t, calls)

	// Once the new configuration has actually been applied, its false flag
	// remains decisive; an applied-snapshot check must not bypass flag policy.
	runtime.installation = cloneHostIOInstallation(repo.current)
	runtime.configSnapshot.Store(&disabled)
	result, err = manager.InvokeAdminExtension(context.Background(), 1, 2, "telemetry.read", json.RawMessage(`{}`))
	require.Error(t, err)
	require.Empty(t, result.Payload)
	require.Zero(t, calls)

	repo.current = cloneHostIOInstallation(installation)
	runtime.installation = cloneHostIOInstallation(installation)
	runtime.configSnapshot.Store(&enabled)
	runtime.configuring.Store(true)
	result, err = manager.InvokeAdminExtension(context.Background(), 1, 2, "telemetry.read", json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	require.Empty(t, result.Payload)
	require.Zero(t, calls)
	runtime.configuring.Store(false)
	_, err = manager.InvokeAdminExtension(context.Background(), 1, 2, "telemetry.read", json.RawMessage(`{}`))
	require.NoError(t, err, "matching enabled configuration should remain callable")
	require.Equal(t, 1, calls)
}

func TestAccountTestOptionsKeepActualAccountScopeAndNativeDefaults(t *testing.T) {
	var calls []extensionv1.Invocation
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		calls = append(calls, in)
		return extensionv1.Result{Payload: json.RawMessage(`{}`)}, nil
	})
	installation := manager.extensions.Load().installations[1]
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityAdmin: {"test.prompt", "test.reasoning"}}
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 50}}
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	for _, id := range []int64{2, 3} {
		account := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		promptErr := validateAccountPromptExtension(context.Background(), account, "custom", "gpt-5.4", "default")
		reasoningErr := ValidateAccountTestReasoningContext(context.Background(), account, "gpt-5.4", "default", "high")
		require.Equal(t, id == 3, promptErr != nil)
		require.Equal(t, id == 3, reasoningErr != nil)
		require.NoError(t, validateAccountPromptExtension(context.Background(), account, "", "gpt-5.4", "default"))
		require.NoError(t, ValidateAccountTestReasoningContext(context.Background(), account, "gpt-5.4", "default", ""))
	}
	require.Len(t, calls, 2)
	for _, call := range calls {
		require.EqualValues(t, 2, call.AccountID)
	}
}
