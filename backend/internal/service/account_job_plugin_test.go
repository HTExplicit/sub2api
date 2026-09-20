//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
)

type accountJobLeaseFixture struct {
	pluginTokenRepository
	held atomic.Int32
}

func (r *accountJobLeaseFixture) HoldPluginRuntime(context.Context, *PluginInstallation) (func(), error) {
	r.held.Add(1)
	return func() { r.held.Add(-1) }, nil
}
func accountJobPolicyManager() (*PluginManager, *pluginRuntime, *accountJobLeaseFixture) {
	installation := &PluginInstallation{ID: 7, PluginKey: "fixture.jobs", Version: "1.0.0", RuntimeGeneration: 3, PackageSHA256: strings.Repeat("a", 64), State: PluginStateEnabled, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}}
	repo := &accountJobLeaseFixture{pluginTokenRepository: pluginTokenRepository{installation: installation}}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	runtime := &pluginRuntime{installation: installation, client: hcplugin.NewClient(&hcplugin.ClientConfig{}), done: make(chan struct{})}
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{7: installation}, runtimes: map[int64]*pluginRuntime{7: runtime}})
	return manager, runtime, repo
}

type accountJobHostFixture struct{ calls int }

func (f *accountJobHostFixture) ExecuteAccountJob(context.Context, *AccountJob, json.RawMessage, []AccountJobItem) ([]AccountJobExecutionResult, error) {
	f.calls++
	return nil, nil
}

func TestPluginOwnedHostJobHoldsUpdateLeaseUntilCleanup(t *testing.T) {
	manager, runtime, repo := accountJobPolicyManager()
	core := &accountJobHostFixture{}
	executor := NewPluginJobExecutor(manager, core)
	job := &AccountJob{Kind: AccountJobKindImportData, Metadata: []byte(`{"plugin_id":7,"plugin_generation":2}`)}
	_, _, err := executor.PrepareAccountJob(context.Background(), job, []byte(`{}`))
	require.ErrorIs(t, err, ErrAccountJobPluginUnavailable)
	require.Zero(t, repo.held.Load())
	job.Metadata = []byte(`{"plugin_id":7,"plugin_generation":3}`)
	ctx, release, err := executor.PrepareAccountJob(context.Background(), job, []byte(`{}`))
	require.NoError(t, err)
	require.EqualValues(t, 1, repo.held.Load())
	runtime.beginDrain()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	_, err = executor.ExecuteAccountJob(ctx, job, []byte(`{}`), []AccountJobItem{{ID: 1}})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, core.calls)
	require.EqualValues(t, 1, repo.held.Load(), "the old host task still owns its fence until cleanup")
	release()
	require.Zero(t, repo.held.Load())
	require.Zero(t, runtime.inFlight.Load())
}

func TestAccountJobRetryPreservesOwnerAndAdmitsCurrentGeneration(t *testing.T) {
	manager, _, plugins := accountJobPolicyManager()
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	ctx := WithPluginExecution(context.Background(), plugins.installation)
	job, _, err := jobs.Submit(ctx, 9, AccountJobKindImportData, "original", []byte(`{"fixture":true}`), []byte(`{"operation":"fixture"}`), []AccountJobItemSeed{{Ordinal: 1}})
	require.NoError(t, err)
	owner, err := AccountJobPluginExecution(job.Metadata)
	require.NoError(t, err)
	require.Equal(t, PluginExecution{ID: 7, Generation: 3}, owner)
	repo.jobs[job.ID].Status = AccountJobStatusFailed
	repo.items[job.ID][0].Status = AccountJobItemStatusFailed
	plugins.installation.RuntimeGeneration = 4
	retry, _, err := jobs.RetryFailed(context.Background(), job.ID, 9, "retry")
	require.NoError(t, err)
	owner, err = AccountJobPluginExecution(retry.Metadata)
	require.NoError(t, err)
	require.Equal(t, PluginExecution{ID: 7, Generation: 4}, owner)
	require.Contains(t, string(retry.Metadata), `"operation":"fixture"`)
	require.Zero(t, plugins.held.Load())
	plugins.installation.Bindings[0].RolloutPercent = 50
	_, _, err = jobs.RetryFailed(context.Background(), job.ID, 9, "narrowed-retry")
	require.ErrorIs(t, err, ErrAccountJobPluginUnavailable, "retry cannot expand a narrowed import binding")
	plugins.installation.State = PluginStateDisabled
	_, _, err = jobs.RetryFailed(context.Background(), job.ID, 9, "disabled-retry")
	require.ErrorIs(t, err, ErrAccountJobPluginUnavailable)
	require.Len(t, repo.jobs, 2)
}

func TestPluginAdminSubmissionUsesTheActualAccountScopeAndRollout(t *testing.T) {
	manager, _, plugins := accountJobPolicyManager()
	plugins.installation.Manifest.Contributions = []extensionv1.Contribution{{ID: "fixture", Slot: "account.actions", Action: "fixture.run", Permission: "admin", AccountFilter: &extensionv1.AccountFilter{Platforms: []string{PlatformOpenAI}, Types: []string{AccountTypeOAuth}, ExcludeShadows: true}}}
	manager.accountDirectory = &resourceAccountDirectory{accounts: map[int64]extensionv1.Account{
		37: {ID: 37, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		38: {ID: 38, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	}}
	require.NoError(t, manager.ValidateAdminExtension(context.Background(), 7, 37, "fixture.run"))
	require.Error(t, manager.ValidateAdminExtension(context.Background(), 7, 38, "fixture.run"))
	plugins.installation.Bindings[0].RolloutPercent = int(stablePluginBucket(37))
	require.Error(t, manager.ValidateAdminExtension(context.Background(), 7, 37, "fixture.run"))
}
