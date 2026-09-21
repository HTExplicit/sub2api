package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func cindyHealthScopeFixture(t *testing.T, percent int, calls *[]extensionv1.Invocation) *PluginManager {
	t.Helper()
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		*calls = append(*calls, in)
		raw, err := json.Marshal(extensionv1.CindyResponseDecision{
			Health: uint8(CindyHealthSignalExactBudget), Balance: uint8(CindyBalanceSignalHTTP429),
		})
		return extensionv1.Result{Payload: raw}, err
	})
	installation := manager.extensions.Load().installations[1]
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: PlatformCindy,
		AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: percent}}
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityProvider: {"cindy.health"}}
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	return manager
}

func cindyHealthScopeAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformCindy, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "synthetic-account-credential"}}
}

func TestCindyResponseClassifiersCarryAccountScope(t *testing.T) {
	actions := []struct {
		name string
		call func(*Account, int, []byte) uint8
	}{
		{"health", func(account *Account, status int, body []byte) uint8 {
			return uint8(ClassifyCindyHealthSignal(account, status, body))
		}},
		{"balance", func(account *Account, status int, body []byte) uint8 {
			return uint8(ClassifyCindyBalanceInsufficient(account, status, body))
		}},
	}
	for _, action := range actions {
		for _, percent := range []int{0, 50, 100} {
			for _, id := range []int64{2, 3} {
				t.Run(fmt.Sprintf("%s/rollout_%d/account_%d", action.name, percent, id), func(t *testing.T) {
					var calls []extensionv1.Invocation
					cindyHealthScopeFixture(t, percent, &calls)
					account := cindyHealthScopeAccount(id)
					require.True(t, hasCanonicalCindyProviderIdentity(account), "fixture must reach the real classifier")
					body := []byte(`{"error":{"type":"insufficient_quota","code":"budget_exceeded","message":"synthetic-upstream-private-detail"},"other":"synthetic-unneeded-field"}`)
					got := action.call(account, http.StatusTooManyRequests, body)
					if int(stablePluginBucket(id)) >= percent {
						require.Zero(t, got, "outside-rollout observations must not acquire a domain policy decision")
						require.Empty(t, calls, "unadmitted account facts must not reach the plugin")
						return
					}
					require.Equal(t, uint8(1), got)
					require.Len(t, calls, 1)
					require.Equal(t, id, calls[0].AccountID)
					require.Equal(t, "cindy.health", calls[0].Operation)
					require.NotContains(t, string(calls[0].Payload), "synthetic-")
				})
			}
		}
	}
}

func TestCindyResponseClassifierDoesNotReusePolicyOutsideCurrentScope(t *testing.T) {
	for _, state := range []string{"rollout_revoked", "runtime_unavailable"} {
		t.Run(state, func(t *testing.T) {
			var calls []extensionv1.Invocation
			manager := cindyHealthScopeFixture(t, 100, &calls)
			account := cindyHealthScopeAccount(2)
			body := []byte(`{"error":{"code":"budget_exceeded"}}`)
			require.Equal(t, CindyHealthSignalExactBudget, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
			require.Len(t, calls, 1)
			registry := manager.extensions.Load()
			if state == "rollout_revoked" {
				registry.installations[1].Bindings[0].RolloutPercent = 0
			} else {
				registry.runtimes = map[int64]*pluginRuntime{}
			}
			manager.extensions.Store(registry)
			require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
			require.Equal(t, CindyBalanceSignalNone, ClassifyCindyBalanceInsufficient(account, http.StatusTooManyRequests, body))
			require.Len(t, calls, 1, "fresh scope/runtime admission must precede a cache hit")
		})
	}
}

func TestCindyResponseClassifiersKeepNonAccountAndOrdinaryBoundaries(t *testing.T) {
	var calls []extensionv1.Invocation
	cindyHealthScopeFixture(t, 0, &calls)
	account := cindyHealthScopeAccount(0)
	body := []byte(`{"error":{"code":"budget_exceeded"}}`)
	// A pre-create observation has no persisted account identity to invent.
	// This preserves the existing shared-domain pure-classification contract.
	require.Equal(t, CindyHealthSignalExactBudget, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
	require.Len(t, calls, 1)
	require.Zero(t, calls[0].AccountID)
	account.Platform = PlatformOpenAI
	require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
	require.Equal(t, CindyBalanceSignalNone, ClassifyCindyBalanceInsufficient(account, http.StatusTooManyRequests, body))
	require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(nil, http.StatusTooManyRequests, body))
	require.Len(t, calls, 1)
}
