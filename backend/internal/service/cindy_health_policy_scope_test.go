package service

import (
	"context"
	"encoding/json"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func cindyHealthScopeFixture(t *testing.T, calls *[]extensionv1.Invocation) {
	t.Helper()
	previous := captureNativeCindyTestInvoker()
	t.Cleanup(func() { restoreNativeCindyTestInvoker(previous) })
	setNativeCindyTestInvoker(nativeCindyTestInvoker(func(_ context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		*calls = append(*calls, in)
		raw, err := json.Marshal(extensionv1.CindyResponseDecision{Health: uint8(CindyHealthSignalExactBudget), Balance: uint8(CindyBalanceSignalHTTP429)})
		return extensionv1.Result{Payload: raw}, err
	}))
}

func TestCindyResponseClassifiersCarryAccountScope(t *testing.T) {
	for _, id := range []int64{2, 3} {
		var calls []extensionv1.Invocation
		cindyHealthScopeFixture(t, &calls)
		account := cindyHealthScopeAccount(id)
		body := []byte(`{"error":{"type":"insufficient_quota","code":"budget_exceeded","message":"synthetic-upstream-private-detail"},"other":"synthetic-unneeded-field"}`)
		require.Equal(t, CindyHealthSignalExactBudget, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
		require.Equal(t, CindyBalanceSignalHTTP429, ClassifyCindyBalanceInsufficient(account, http.StatusTooManyRequests, body))
		require.Len(t, calls, 2)
		for _, call := range calls {
			require.Equal(t, id, call.AccountID)
			require.Equal(t, "cindy.health", call.Operation)
			require.NotContains(t, string(call.Payload), "synthetic-")
		}
	}
}

func TestCindyResponseClassifierDoesNotReusePolicyAfterNativeSwitchChange(t *testing.T) {
	previous := captureNativeCindyTestInvoker()
	t.Cleanup(func() { restoreNativeCindyTestInvoker(previous) })
	invokeCindyProvider = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		return cindyProvider.Load().module.Invoke(ctx, in)
	}
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{BalanceDetection: true})
	account := cindyHealthScopeAccount(2)
	account.WirePlatform, account.ProviderProfile = WirePlatformOpenAI, ProviderProfileCindyLaxaV1
	require.True(t, hasCanonicalCindyProviderIdentity(account))
	body := []byte(`{"error":{"type":"budget_exceeded","code":"429"}}`)
	require.Equal(t, CindyHealthSignalExactBudget, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
	require.Equal(t, CindyBalanceSignalHTTP429, ClassifyCindyBalanceInsufficient(account, http.StatusTooManyRequests, body))
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{})
	require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
	require.Equal(t, CindyBalanceSignalNone, ClassifyCindyBalanceInsufficient(account, http.StatusTooManyRequests, body))
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{BalanceDetection: true})
	require.Equal(t, CindyHealthSignalExactBudget, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
	require.Equal(t, CindyBalanceSignalHTTP429, ClassifyCindyBalanceInsufficient(account, http.StatusTooManyRequests, body))
}

func cindyHealthScopeAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformCindy, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "synthetic-account-credential"}}
}

func TestCindyResponseClassifiersKeepNonAccountAndOrdinaryBoundaries(t *testing.T) {
	var calls []extensionv1.Invocation
	cindyHealthScopeFixture(t, &calls)
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
