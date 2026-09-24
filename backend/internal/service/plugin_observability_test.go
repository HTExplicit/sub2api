package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
