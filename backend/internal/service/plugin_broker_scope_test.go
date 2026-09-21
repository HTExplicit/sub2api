package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type brokerScopeRepository struct {
	*pluginTokenRepository
	PluginExtensionStateStore
	writes  int
	reads   int
	readErr error
}

func (r *brokerScopeRepository) GetByID(ctx context.Context, id int64) (*PluginInstallation, error) {
	r.reads++
	if r.readErr != nil {
		return nil, r.readErr
	}
	return r.pluginTokenRepository.GetByID(ctx, id)
}

func (r *brokerScopeRepository) CompareSwapExtensionState(context.Context, string, extensionv1.StateRequest) (extensionv1.StateResult, error) {
	r.writes++
	return extensionv1.StateResult{}, nil
}

type brokerScopeDirectory struct {
	PluginAccountDirectory
	accounts []extensionv1.Account
	resolved int
}

func (d *brokerScopeDirectory) ReadExtensionAccount(_ context.Context, id int64) (*extensionv1.Account, error) {
	for _, account := range d.accounts {
		if account.ID == id {
			copy := account
			return &copy, nil
		}
	}
	return nil, errors.New("synthetic account not found")
}

func (d *brokerScopeDirectory) ListExtensionAccounts(context.Context, extensionv1.AccountQuery) ([]extensionv1.Account, error) {
	return append([]extensionv1.Account(nil), d.accounts...), nil
}

func (d *brokerScopeDirectory) ResolveExtensionIdentity(_ context.Context, query extensionv1.AccountQuery) (*extensionv1.OutboundIdentity, error) {
	d.resolved++
	return &extensionv1.OutboundIdentity{AccountID: query.AccountID, Token: "synthetic-scope-token"}, nil
}

func newBrokerScopeFixture(t *testing.T) (extensionv1.HostHandler, *brokerScopeRepository, *brokerScopeDirectory) {
	t.Helper()
	var included, excluded int64
	for id := int64(1); included == 0 || excluded == 0; id++ {
		if stablePluginBucket(id) < 50 {
			included = id
		} else {
			excluded = id
		}
	}
	installation := &PluginInstallation{ID: 7, PluginKey: "scope.fixture", State: PluginStateEnabled,
		Version: "1.0.0", PackageSHA256: "package-1", BinarySHA256: "binary-1", RuntimeGeneration: 3,
		Manifest: PluginManifest{Requires: PluginRequirements{ExtensionAPI: 1}}}
	for _, capability := range []string{extensionv1.CapabilityCredentials, extensionv1.CapabilityObservability} {
		installation.Manifest.Capabilities = append(installation.Manifest.Capabilities, PluginCapability{ID: capability, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth})
		installation.Bindings = append(installation.Bindings, PluginBinding{Capability: capability, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 50})
	}
	repo := &brokerScopeRepository{pluginTokenRepository: &pluginTokenRepository{installation: installation}, PluginExtensionStateStore: &extensionReadOnlyFixture{}}
	directory := &brokerScopeDirectory{accounts: []extensionv1.Account{
		{ID: included, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Identity: "included"},
		{ID: excluded, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Identity: "excluded"},
	}}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, nil, PluginHostInfo{}, newFakePluginKVStore())
	manager.SetAccountDirectory(directory)
	manager.traffic = &trafficPolicyCache{}
	// A deliberately stale local projection must not override fresh persisted
	// binding intent, nor let a previous process generation access credentials.
	process := *installation
	process.Bindings = append([]PluginBinding(nil), installation.Bindings...)
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{7: &process}, runtimes: map[int64]*pluginRuntime{}})
	host := manager.buildHostServices(&process).(*pluginHostServiceServer)
	return host.extension, repo, directory
}

func TestPluginBrokerHonorsActualAccountRollout(t *testing.T) {
	host, repo, directory := newBrokerScopeFixture(t)
	for _, operation := range []extensionv1.HostOperation{extensionv1.HostAccountRead, extensionv1.HostResolveIdentity, extensionv1.HostMetricsQuery} {
		t.Run(string(operation), func(t *testing.T) {
			raw, err := json.Marshal(extensionv1.AccountQuery{AccountID: directory.accounts[1].ID})
			require.NoError(t, err)
			_, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: operation, Payload: raw})
			require.Equal(t, codes.PermissionDenied, status.Code(err), "excluded cohort cannot be authorized by platform/type alone")
			require.Zero(t, directory.resolved)
		})
	}
	query, err := json.Marshal(extensionv1.AccountQuery{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth})
	require.NoError(t, err)
	result, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostAccountList, Payload: query})
	require.NoError(t, err)
	var accounts []extensionv1.Account
	require.NoError(t, json.Unmarshal(result.Payload, &accounts))
	require.Equal(t, directory.accounts[:1], accounts)
	require.Equal(t, 4, repo.reads, "one current scope read per broker call, not per returned account")
	projection, err := json.Marshal(extensionv1.StateRequest{Namespace: "tickets", Key: "account", Value: json.RawMessage(`{}`),
		Projection: &extensionv1.AccountProjection{AccountID: directory.accounts[1].ID, Identity: "excluded"}})
	require.NoError(t, err)
	_, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostStateCompareSwap, Payload: projection})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Zero(t, repo.writes)

	query, err = json.Marshal(extensionv1.AccountQuery{AccountID: directory.accounts[0].ID})
	require.NoError(t, err)
	result, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostResolveIdentity, Payload: query})
	require.NoError(t, err)
	require.Equal(t, 1, directory.resolved)
	require.Contains(t, string(result.Payload), "synthetic-scope-token")
}

func TestPluginBrokerRejectsPersistedScopeAndGenerationDrift(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*PluginInstallation)
	}{
		{"binding revoked", func(p *PluginInstallation) { p.Bindings[0].Enabled = false }},
		{"rollout removed", func(p *PluginInstallation) { p.Bindings[0].RolloutPercent = 0 }},
		{"scope changed", func(p *PluginInstallation) { p.Bindings[0].AccountType = AccountTypeAPIKey }},
		{"disabled", func(p *PluginInstallation) { p.State = PluginStateDisabled }},
		{"updating", func(p *PluginInstallation) { p.State = PluginStateUpdating }},
		{"starting", func(p *PluginInstallation) { p.State = PluginStateStarting }},
		{"unhealthy", func(p *PluginInstallation) { p.State = PluginStateError }},
		{"incompatible", func(p *PluginInstallation) { p.State = PluginStateIncompatible }},
		{"new generation", func(p *PluginInstallation) { p.RuntimeGeneration++ }},
		{"new package", func(p *PluginInstallation) { p.PackageSHA256 = "package-2" }},
		{"new configuration", func(p *PluginInstallation) { p.ConfigEncrypted = "updated-config" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			host, repo, directory := newBrokerScopeFixture(t)
			change.apply(repo.installation)
			query, err := json.Marshal(extensionv1.AccountQuery{AccountID: directory.accounts[0].ID})
			var result extensionv1.Result
			require.NoError(t, err)
			result, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostResolveIdentity, Payload: query})
			require.Equal(t, codes.PermissionDenied, status.Code(err), "stale in-memory bindings must not authorize sensitive broker IO")
			require.Empty(t, result.Payload)
			require.Zero(t, directory.resolved)
		})
	}
}

func TestPluginBrokerFailsClosedWhenCurrentScopeCannotBeRead(t *testing.T) {
	host, repo, directory := newBrokerScopeFixture(t)
	repo.readErr = errors.New("synthetic current installation unavailable")
	raw, err := json.Marshal(extensionv1.AccountQuery{AccountID: directory.accounts[0].ID})
	require.NoError(t, err)
	result, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostResolveIdentity, Payload: raw})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Empty(t, result.Payload)
	require.Zero(t, directory.resolved)
}

func TestPluginBrokerFinishesOwnedObservationAfterCapabilityStop(t *testing.T) {
	broker, repo, directory := newBrokerScopeFixture(t)
	host := broker.(*pluginExtensionHost)
	store := &quotaActivityMemoryStore{}
	host.activity = NewQuotaActivityService(store)
	accountID := directory.accounts[0].ID
	id, err := host.activity.BeginUnbilled(context.Background(), accountID, host.key)
	require.NoError(t, err)
	repo.installation.State = PluginStateDisabled
	for index := range repo.installation.Bindings {
		repo.installation.Bindings[index].Enabled = false
	}
	raw, err := json.Marshal(extensionv1.AccountQuery{AccountID: accountID, ObservationID: id})
	require.NoError(t, err)
	_, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostFinishObservation, Payload: raw})
	require.NoError(t, err)
	require.Zero(t, store.stamp.Active, "stopping new work must not strand an issued observation")
	require.EqualValues(t, 1, store.stamp.Gaps, "unbilled finish remains an uncalibrated gap, not a fabricated bill")
	require.Zero(t, repo.reads, "owned cleanup does not require a currently enabled account binding")
	require.Zero(t, directory.resolved)

	raw, err = json.Marshal(extensionv1.AccountQuery{AccountID: accountID, ObservationID: "another-owner.00000000000000000000000000000000"})
	require.NoError(t, err)
	_, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostFinishObservation, Payload: raw})
	require.Error(t, err, "the retained cleanup path still requires observation namespace ownership")
	require.EqualValues(t, 1, store.stamp.Gaps)
}
