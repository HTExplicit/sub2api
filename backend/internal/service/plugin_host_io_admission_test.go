package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
)

// Explicitly implements every repository method: an omitted embedded port
// cannot manufacture a failure that would make an admission test pass.
type hostIOAdmissionRepository struct {
	t             *testing.T
	current       *PluginInstallation
	holds         int
	businessHolds int
	active        int
}

var _ PluginRepository = (*hostIOAdmissionRepository)(nil)
var _ PluginRuntimeLocker = (*hostIOAdmissionRepository)(nil)

func (r *hostIOAdmissionRepository) unexpected(method string) error {
	r.t.Helper()
	r.t.Errorf("unexpected repository call: %s", method)
	return errors.New("unexpected repository call")
}
func (r *hostIOAdmissionRepository) List(context.Context) ([]*PluginInstallation, error) {
	return nil, r.unexpected("List")
}
func (r *hostIOAdmissionRepository) GetByID(_ context.Context, id int64) (*PluginInstallation, error) {
	if r.current == nil || r.current.ID != id {
		return nil, ErrPluginStateChanged
	}
	return cloneHostIOInstallation(r.current), nil
}
func (r *hostIOAdmissionRepository) GetByKey(context.Context, string) (*PluginInstallation, error) {
	return nil, r.unexpected("GetByKey")
}
func (r *hostIOAdmissionRepository) Install(context.Context, *PluginInstallation, []PluginBinding) (*PluginInstallation, error) {
	return nil, r.unexpected("Install")
}
func (r *hostIOAdmissionRepository) GetArtifact(context.Context, int64) ([]byte, error) {
	return nil, r.unexpected("GetArtifact")
}
func (r *hostIOAdmissionRepository) Delete(context.Context, int64, string) error {
	return r.unexpected("Delete")
}
func (r *hostIOAdmissionRepository) BeginEnable(context.Context, int64, string, string) error {
	return r.unexpected("BeginEnable")
}
func (r *hostIOAdmissionRepository) MarkRuntimeHealthy(context.Context, int64, string, string) error {
	return r.unexpected("MarkRuntimeHealthy")
}
func (r *hostIOAdmissionRepository) UpdateState(context.Context, int64, string, string, *time.Time, string, string) error {
	return r.unexpected("UpdateState")
}
func (r *hostIOAdmissionRepository) UpdateConfig(context.Context, int64, string, string) error {
	return r.unexpected("UpdateConfig")
}
func (r *hostIOAdmissionRepository) UpdateBindingsAndState(context.Context, int64, []PluginBinding, string, string, *time.Time, string, string) error {
	return r.unexpected("UpdateBindingsAndState")
}
func (r *hostIOAdmissionRepository) HoldPluginRuntime(ctx context.Context, expected *PluginInstallation) (func(), error) {
	r.holds++
	if r.current == nil || !samePluginRuntime(r.current, expected) || r.current.State == PluginStateUpdating {
		return nil, ErrPluginStateChanged
	}
	if PluginBusinessIOLeaseRequired(ctx) {
		r.businessHolds++
		if r.current.State != PluginStateEnabled || r.current.Revision != expected.Revision || r.current.ConfigEncrypted != expected.ConfigEncrypted {
			return nil, ErrPluginStateChanged
		}
	}
	r.active++
	var once sync.Once
	return func() { once.Do(func() { r.active-- }) }, nil
}

func cloneHostIOInstallation(in *PluginInstallation) *PluginInstallation {
	copy := *in
	copy.Bindings = append([]PluginBinding(nil), in.Bindings...)
	return &copy
}

func hostIOAdmissionManager(t *testing.T) (*PluginManager, *hostIOAdmissionRepository, *pluginRuntime) {
	t.Helper()
	installation := &PluginInstallation{ID: 7, PluginKey: "fixture.host-io", Version: "1.0.0", Revision: 1, RuntimeGeneration: 1,
		PackageSHA256: strings.Repeat("a", 64), BinarySHA256: strings.Repeat("b", 64), ConfigEncrypted: "old-config", State: PluginStateEnabled,
		Manifest: PluginManifest{Operations: map[string][]string{extensionv1.CapabilityRequest: {"host.io"}}},
		Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}}
	repo := &hostIOAdmissionRepository{t: t, current: cloneHostIOInstallation(installation)}
	manager := &PluginManager{repo: repo}
	runtime := &pluginRuntime{installation: cloneHostIOInstallation(installation), client: hcplugin.NewClient(&hcplugin.ClientConfig{}), done: make(chan struct{})}
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{7: installation}, runtimes: map[int64]*pluginRuntime{7: runtime}})
	return manager, repo, runtime
}

func TestHostIOAdmissionRejectsPersistedIntentChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*PluginInstallation)
	}{
		{"disabled", func(i *PluginInstallation) { i.State = PluginStateDisabled; i.Bindings[0].Enabled = false }},
		{"config", func(i *PluginInstallation) { i.ConfigEncrypted = "new-config" }},
		{"binding", func(i *PluginInstallation) { i.Bindings[0].RolloutPercent = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, repo, runtime := hostIOAdmissionManager(t)
			invoke := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "host.io", AccountID: 42}
			ctx, release, err := manager.BindOperationContext(context.Background(), PlatformOpenAI, AccountTypeAPIKey, invoke)
			require.NoError(t, err, "positive control must reach a functioning repository/locker")
			require.NoError(t, ctx.Err())
			require.Equal(t, 1, repo.active)
			require.False(t, PluginBusinessIOLeaseRequired(ctx), "purpose marker must not leak into later diagnostic/process calls")
			release()
			repo.current.Revision++
			tc.edit(repo.current)
			ctx, release, err = manager.BindOperationContext(context.Background(), PlatformOpenAI, AccountTypeAPIKey, invoke)
			if release != nil {
				t.Cleanup(release)
			}
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
			require.Nil(t, ctx)
			require.Equal(t, 2, repo.businessHolds, "both positive and negative control must use the business admission contract")
			require.Zero(t, repo.active)
			require.Zero(t, runtime.inFlight.Load())
		})
	}
}

func TestHostIOAdmissionRejectsFreshSnapshotForOldAppliedRuntime(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*PluginInstallation)
	}{
		{"config", func(i *PluginInstallation) { i.ConfigEncrypted = "new-config" }},
		{"generation", func(i *PluginInstallation) { i.RuntimeGeneration++ }},
		{"package", func(i *PluginInstallation) { i.PackageSHA256 = strings.Repeat("c", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, repo, runtime := hostIOAdmissionManager(t)
			repo.current.Revision++
			tc.edit(repo.current)
			fresh, err := repo.GetByID(context.Background(), repo.current.ID)
			require.NoError(t, err)
			ctx, release, err := manager.bindHostPolicyContext(context.Background(), fresh, runtime)
			if release != nil {
				t.Cleanup(release)
			}
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "fresh storage cannot certify an old applied runtime")
			require.Nil(t, ctx)
			require.Zero(t, repo.holds, "runtime mismatch must fail before asking storage for a lease")
			require.Zero(t, runtime.inFlight.Load())
		})
	}
}
