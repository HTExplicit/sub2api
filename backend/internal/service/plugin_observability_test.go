package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type trafficScopeCache struct {
	beginIDs    []int64
	protocols   []AccountTrafficProtocol
	snapshotIDs []int64
	finishedIDs []int64
}

func (c *trafficScopeCache) Begin(_ context.Context, id int64, protocol AccountTrafficProtocol) error {
	c.beginIDs = append(c.beginIDs, id)
	c.protocols = append(c.protocols, protocol)
	return nil
}
func (c *trafficScopeCache) Finish(_ context.Context, id int64, _ AccountTrafficProtocol, _ AccountTrafficOutcome) error {
	c.finishedIDs = append(c.finishedIDs, id)
	return nil
}
func (c *trafficScopeCache) Snapshot(_ context.Context, id int64) (map[AccountTrafficProtocol]AccountTrafficObserveState, error) {
	c.snapshotIDs = append(c.snapshotIDs, id)
	return map[AccountTrafficProtocol]AccountTrafficObserveState{AccountTrafficProtocolHTTP: {Started: 1}}, nil
}

type trafficPolicyCall struct {
	platform, kind string
	invocation     extensionv1.Invocation
}

// The embedded concrete manager supplies the full operation implementation;
// only the already-authorized process boundary is replaced by a fake RPC.
type trafficScopeInvoker struct {
	*PluginManager
	calls []trafficPolicyCall
}

func (f *trafficScopeInvoker) InvokeCachedOperation(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	f.calls = append(f.calls, trafficPolicyCall{platform, kind, in})
	return f.PluginManager.InvokeCachedOperation(ctx, platform, kind, in)
}

var _ extensionv1.OperationInvoker = (*trafficScopeInvoker)(nil)
var _ extensionv1.CachedOperationInvoker = (*trafficScopeInvoker)(nil)

func scopedTrafficObserver(t *testing.T, platform, kind string, percent int) (*AccountTrafficObserver, *trafficScopeCache, *trafficScopeInvoker, *int) {
	t.Helper()
	rpcCalls := new(int)
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		*rpcCalls++
		return (promptPolicyFixture{}).InvokeOperation(context.Background(), "", "", in)
	})
	installation := manager.extensions.Load().installations[1]
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityObservability, Platform: platform, AccountType: kind, Enabled: true, RolloutPercent: percent}}
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityObservability: {"observability.policy"}}
	invoker := &trafficScopeInvoker{PluginManager: manager}
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: invoker})
	cache := &trafficScopeCache{}
	return NewAccountTrafficObserver(cache, nil), cache, invoker, rpcCalls
}

func TestTrafficObservationScopeUsesActualAccountForBeginAndSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, boundPlatform, boundType string
		percent                        int
		account                        Account
		allowed                        bool
	}{
		{"zero-rollout", "*", "*", 0, Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, false},
		{"half-included", "*", "*", 50, Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, true},
		{"half-excluded", "*", "*", 50, Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, false},
		{"full-rollout", "*", "*", 100, Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, true},
		{"scoped-included", PlatformOpenAI, AccountTypeOAuth, 100, Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, true},
		{"other-platform", PlatformOpenAI, AccountTypeOAuth, 100, Account{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeOAuth}, false},
		{"other-type", PlatformOpenAI, AccountTypeOAuth, 100, Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false},
	} {
		for _, protocol := range AccountTrafficProtocols() {
			t.Run(tc.name+"/begin-"+string(protocol), func(t *testing.T) {
				observer, cache, invoker, rpcCalls := scopedTrafficObserver(t, tc.boundPlatform, tc.boundType, tc.percent)
				turn := observer.Begin(context.Background(), &tc.account, protocol)
				if tc.allowed {
					require.NotNil(t, turn)
					require.Equal(t, []int64{tc.account.ID}, cache.beginIDs)
					require.Equal(t, []AccountTrafficProtocol{protocol}, cache.protocols)
					require.Equal(t, 1, *rpcCalls)
				} else {
					require.Nil(t, turn, "out-of-scope account must not open a counted turn")
					require.Empty(t, cache.beginIDs)
					require.Zero(t, *rpcCalls, "excluded account must not reach the policy process")
				}
				require.Len(t, invoker.calls, 1)
				require.Equal(t, tc.account.Platform, invoker.calls[0].platform)
				require.Equal(t, tc.account.Type, invoker.calls[0].kind)
				require.Equal(t, tc.account.ID, invoker.calls[0].invocation.AccountID)
				require.JSONEq(t, `{}`, string(invoker.calls[0].invocation.Payload), "credentials/account bodies must not cross into classification policy")
			})
		}
		t.Run(tc.name+"/snapshot", func(t *testing.T) {
			observer, cache, invoker, rpcCalls := scopedTrafficObserver(t, tc.boundPlatform, tc.boundType, tc.percent)
			snapshot, err := observer.Snapshot(context.Background(), &tc.account)
			if tc.allowed {
				require.NoError(t, err)
				require.EqualValues(t, 1, snapshot[AccountTrafficProtocolHTTP].Started)
				require.Equal(t, []int64{tc.account.ID}, cache.snapshotIDs)
				require.Equal(t, 1, *rpcCalls)
			} else {
				require.ErrorIs(t, err, ErrAccountTrafficTelemetryUnavailable)
				require.Nil(t, snapshot)
				require.Empty(t, cache.snapshotIDs)
				require.Zero(t, *rpcCalls)
			}
			require.Len(t, invoker.calls, 1)
			require.Equal(t, tc.account.Platform, invoker.calls[0].platform)
			require.Equal(t, tc.account.Type, invoker.calls[0].kind)
			require.Equal(t, tc.account.ID, invoker.calls[0].invocation.AccountID)
		})
	}
}

func TestTrafficObservationRejectsMissingAccountIdentity(t *testing.T) {
	observer, cache, invoker, rpcCalls := scopedTrafficObserver(t, "*", "*", 100)
	for _, account := range []*Account{
		nil,
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{ID: 2, Type: AccountTypeOAuth},
		{ID: 2, Platform: PlatformOpenAI},
		{ID: 2, Platform: "*", Type: AccountTypeOAuth},
		{ID: 2, Platform: PlatformOpenAI, Type: "*"},
	} {
		require.Nil(t, observer.Begin(context.Background(), account, AccountTrafficProtocolHTTP))
		snapshot, err := observer.Snapshot(context.Background(), account)
		require.ErrorIs(t, err, ErrAccountTrafficTelemetryUnavailable)
		require.Nil(t, snapshot)
	}
	require.Empty(t, invoker.calls, "missing account facts must not become a shared policy lookup")
	require.Zero(t, *rpcCalls)
	require.Empty(t, cache.beginIDs)
	require.Empty(t, cache.snapshotIDs)
}

type trafficPolicyCache struct {
	started  int
	outcomes []AccountTrafficOutcome
}

func (c *trafficPolicyCache) Begin(context.Context, int64, AccountTrafficProtocol) error {
	c.started++
	return nil
}
func (c *trafficPolicyCache) Finish(_ context.Context, _ int64, _ AccountTrafficProtocol, outcome AccountTrafficOutcome) error {
	c.outcomes = append(c.outcomes, outcome)
	return nil
}
func (*trafficPolicyCache) Snapshot(context.Context, int64) (map[AccountTrafficProtocol]AccountTrafficObserveState, error) {
	return map[AccountTrafficProtocol]AccountTrafficObserveState{AccountTrafficProtocolHTTP: {Started: 3, Completed2xx: 2, Upstream429: 1}, "unrecognized": {Started: 42}}, nil
}

func TestTrafficObservationFinishesWithCapturedPolicyAfterPluginDisable(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	cache := &trafficPolicyCache{}
	observer := NewAccountTrafficObserver(cache, nil)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	turn := observer.Begin(context.Background(), account, AccountTrafficProtocolHTTP)
	require.NotNil(t, turn)
	processExtensionOperations.Store(nil)
	turn.Finish(&OpenAIForwardResult{}, nil, false)
	turn.Finish(nil, nil, false)
	require.Equal(t, []AccountTrafficOutcome{AccountTrafficOutcomeCompleted2xx}, cache.outcomes)
	require.Nil(t, observer.Begin(context.Background(), account, AccountTrafficProtocolHTTP))
	require.Equal(t, 1, cache.started)
}

func TestMetricsBrokerRequiresCurrentObservabilityCapabilityAndExposesOnlyCounters(t *testing.T) {
	directory := &credentialScopeDirectory{}
	installation := &PluginInstallation{Manifest: PluginManifest{Capabilities: []PluginCapability{{ID: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*"}}}}
	allowed := true
	host := &pluginExtensionHost{directory: directory, installation: installation, traffic: &trafficPolicyCache{}, allows: func(string, string, string, int64) bool { return allowed }}
	raw, _ := json.Marshal(extensionv1.AccountQuery{AccountID: 7})
	invocation := extensionv1.HostInvocation{Operation: extensionv1.HostMetricsQuery, Payload: raw}
	_, err := host.Call(context.Background(), invocation)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	installation.Manifest.Capabilities = append(installation.Manifest.Capabilities, PluginCapability{ID: extensionv1.CapabilityObservability, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth})
	result, err := host.Call(context.Background(), invocation)
	require.NoError(t, err)
	var snapshot extensionv1.AccountTrafficSnapshot
	require.NoError(t, json.Unmarshal(result.Payload, &snapshot))
	require.EqualValues(t, 7, snapshot.AccountID)
	require.Len(t, snapshot.Protocols, 1)
	require.EqualValues(t, 3, snapshot.Protocols["http"].Started)
	require.Zero(t, directory.calls)
	allowed = false
	_, err = host.Call(context.Background(), invocation)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
