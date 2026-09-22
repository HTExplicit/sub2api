//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPluginRegistryTxRejectsConcurrentOperationOwners(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	first, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	second, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	left, err := repo.beginPluginRegistryTx(ctx)
	require.NoError(t, err)
	defer left.Rollback()
	right, err := repo.beginPluginRegistryTx(ctx)
	require.NoError(t, err)
	defer right.Rollback()
	// Both real production transaction helpers see their own enabled target
	// and the other transaction's old disabled version. SSI chooses a loser.
	require.NoError(t, registryPrototypeEnable(ctx, left, first))
	require.NoError(t, registryPrototypeEnable(ctx, right, second))
	leftErr := commitPluginRegistryTx(ctx, left)
	if leftErr != nil {
		_ = left.Rollback()
	}
	rightErr := commitPluginRegistryTx(ctx, right)
	if rightErr != nil {
		_ = right.Rollback()
	}
	commits, conflicts := 0, 0
	for _, transactionErr := range []error{leftErr, rightErr} {
		if transactionErr == nil {
			commits++
		} else {
			require.ErrorIs(t, transactionErr, service.ErrPluginStateChanged)
			conflicts++
		}
	}
	require.Equal(t, 1, commits)
	require.Equal(t, 1, conflicts)
	current, err := repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, countRegistryPrototypeOwners(current))
	t.Log("production_graph_tx_commits=1 retryable_conflicts=1 committed_overlapping_owners=1")
}

func TestPluginRegistryBindingAndInstallConflictsRollback(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	_, _ = installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", true)
	disabled, artifact := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	bindings := append([]service.PluginBinding(nil), disabled.Bindings...)
	bindings[0].Enabled = true
	err := repo.UpdateBindingsAndState(service.WithPluginExpectedRevision(ctx, disabled.Revision), disabled.ID, bindings, service.PluginStateStarting, "", nil, disabled.State, disabled.BinarySHA256)
	require.ErrorContains(t, err, "overlapping enabled owners")
	after, err := repo.GetByID(ctx, disabled.ID)
	require.NoError(t, err)
	require.Equal(t, disabled, after)
	other := bundleFixture(t, repo)
	other.State = service.PluginStateEnabled
	other.Manifest = artifact.Manifest
	other.Manifest.ID = other.PluginKey
	_, err = repo.Install(ctx, other, bindings)
	require.ErrorContains(t, err, "overlapping enabled owners")
	_, err = repo.GetByKey(ctx, other.PluginKey)
	require.ErrorIs(t, err, sql.ErrNoRows, "failed final admission must not leave a partially installed identity")
}

func TestPluginRegistryPromotionConflictPreservesPendingConfigAndIntent(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo, consumer, candidate := installedUpdateFixture(t)
	provider, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityProvider, "cindy", "apikey", "provider.read", true)
	candidate.Manifest.Dependencies = []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}
	require.NoError(t, repo.StagePluginUpdate(ctx, consumer, candidate, service.PluginUpdatePinned))
	pending, err := repo.GetByID(ctx, consumer.ID)
	require.NoError(t, err)
	journal := bootstrapJournalSnapshot(t, repo, consumer.PluginKey)
	bindings := append([]service.PluginBinding(nil), provider.Bindings...)
	bindings[0].Enabled = false
	require.NoError(t, repo.UpdateBindingsAndState(ctx, provider.ID, bindings, service.PluginStateDisabled, "", nil, provider.State, provider.BinarySHA256))
	err = repo.CommitPluginUpdate(ctx, pending, candidate)
	require.ErrorContains(t, err, "missing enabled plugin dependency")
	after, err := repo.GetByID(ctx, consumer.ID)
	require.NoError(t, err)
	require.Equal(t, pending, after)
	require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, consumer.PluginKey))
	artifact, err := repo.PendingPluginArtifact(ctx, consumer.ID)
	require.NoError(t, err)
	require.Equal(t, candidate.ArtifactData, artifact)
	bindings[0].Enabled = true
	require.NoError(t, repo.UpdateBindingsAndState(ctx, provider.ID, bindings, service.PluginStateEnabled, "", nil, service.PluginStateDisabled, provider.BinarySHA256))
	require.NoError(t, repo.CommitPluginUpdate(ctx, pending, candidate))
	updated, err := repo.GetByID(ctx, consumer.ID)
	require.NoError(t, err)
	require.Equal(t, pending.ConfigEncrypted, updated.ConfigEncrypted)
	require.Equal(t, candidate.PackageSHA256, updated.PackageSHA256)
	for _, binding := range updated.Bindings {
		if binding.Capability == extensionv1.CapabilityAdmin {
			require.True(t, binding.Enabled)
			require.Equal(t, 37, binding.RolloutPercent)
		} else {
			require.False(t, binding.Enabled)
		}
	}
}

func TestPluginRegistryBundleFinalAdmissionPreservesStagedIntent(t *testing.T) {
	t.Run("prepare_cannot_remove_required_provider", func(t *testing.T) {
		ctx := registryAtomicityContext(t)
		repo := &pluginRepository{db: integrationDB}
		provider := bundleFixture(t, repo)
		provider.Manifest.Capabilities = []service.PluginCapability{{ID: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}
		prepared, err := repo.PrepareBundledPlugin(ctx, provider, "provider-one", "", true, "saved-provider-config")
		require.NoError(t, err)
		require.NoError(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "provider-one"))
		current, err := repo.GetByID(ctx, prepared.ID)
		require.NoError(t, err)
		consumer := bundleFixture(t, repo)
		consumer.State = service.PluginStateEnabled
		consumer.Manifest.Dependencies = []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}
		_, err = repo.Install(ctx, consumer, []service.PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "openai", AccountType: "oauth", Enabled: true, RolloutPercent: 100}})
		require.NoError(t, err)
		journal := bootstrapJournalSnapshot(t, repo, provider.PluginKey)
		provider.Version = "1.0.1"
		_, err = repo.PrepareBundledPlugin(ctx, provider, "provider-two", "", true, "replacement-default")
		require.ErrorContains(t, err, "missing enabled plugin dependency")
		after, err := repo.GetByID(ctx, current.ID)
		require.NoError(t, err)
		require.Equal(t, current, after)
		require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, provider.PluginKey))
	})
	t.Run("completion_conflict_keeps_journal_pending", func(t *testing.T) {
		ctx := registryAtomicityContext(t)
		repo := &pluginRepository{db: integrationDB}
		owner, artifact := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", true)
		bundle := bundleFixture(t, repo)
		bundle.Manifest = artifact.Manifest
		bundle.Manifest.ID = bundle.PluginKey
		prepared, err := repo.PrepareBundledPlugin(ctx, bundle, "conflicting-bundle", "", true, "saved-bundle-config")
		require.NoError(t, err)
		journal := bootstrapJournalSnapshot(t, repo, bundle.PluginKey)
		err = repo.CompleteBundledPlugin(ctx, prepared.ID, "conflicting-bundle")
		require.ErrorContains(t, err, "overlapping enabled owners")
		after, err := repo.GetByID(ctx, prepared.ID)
		require.NoError(t, err)
		require.Equal(t, prepared, after)
		require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, bundle.PluginKey))
		owner.Bindings[0].Enabled = false
		require.NoError(t, repo.UpdateBindingsAndState(ctx, owner.ID, owner.Bindings, service.PluginStateDisabled, "", nil, owner.State, owner.BinarySHA256))
		require.NoError(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "conflicting-bundle"))
	})
}

func TestPluginRegistryFaultStateDoesNotRemoveDependencyGraph(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	provider, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityProvider, "cindy", "apikey", "provider.read", true)
	require.NoError(t, repo.UpdateState(ctx, provider.ID, service.PluginStateError, "runtime unavailable", nil, provider.BinarySHA256, provider.State))
	consumer := bundleFixture(t, repo)
	consumer.State = service.PluginStateEnabled
	consumer.Manifest.Dependencies = []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}
	_, err := repo.Install(ctx, consumer, []service.PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "openai", AccountType: "oauth", Enabled: true, RolloutPercent: 100}})
	require.NoError(t, err, "desired graph admission does not require a live provider process")
	require.NoError(t, repo.UpdateConfig(ctx, provider.ID, "new-provider-config", provider.BinarySHA256))
	spare, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "spare.operation", false)
	require.NoError(t, repo.Delete(ctx, spare.ID, spare.BinarySHA256))
	current, err := repo.GetByID(ctx, provider.ID)
	require.NoError(t, err)
	require.Equal(t, service.PluginStateError, current.State)
	require.True(t, current.Bindings[0].Enabled)
	require.Equal(t, "new-provider-config", current.ConfigEncrypted)
}

func TestPluginRegistryFirstBundleOrderKeepsTemporaryDisabledBindings(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	var source struct {
		Plugins []struct {
			Directory      string `json:"directory"`
			DefaultEnabled bool   `json:"default_enabled"`
		} `json:"plugins"`
	}
	root := filepath.Join("..", "..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "plugins", "bundle.source.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &source))
	require.NotEmpty(t, source.Plugins)
	for _, entry := range source.Plugins {
		manifestRaw, err := os.ReadFile(filepath.Join(root, "plugins", entry.Directory, "manifest.source.json"))
		require.NoError(t, err)
		var manifest service.PluginManifest
		require.NoError(t, json.Unmarshal(manifestRaw, &manifest))
		artifact := bundleFixture(t, repo)
		manifest.ID = artifact.PluginKey
		artifact.Manifest, artifact.Version, artifact.ArtifactData = manifest, manifest.Version, manifestRaw
		prepared, err := repo.PrepareBundledPlugin(ctx, artifact, "first-bundle-order", "", entry.DefaultEnabled, "saved-config")
		require.NoError(t, err, entry.Directory)
		for _, binding := range prepared.Bindings {
			require.False(t, binding.Enabled)
		}
		require.NoError(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "first-bundle-order"), entry.Directory)
	}
	t.Logf("actual_bundle_source_order_entries=%d initial_prepare_disabled=true final_admission_passed=true", len(source.Plugins))
}

func TestPluginRegistryRejectedDisableRollsBackOwnedJobCancellation(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	provider, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "provider.admin", true)
	consumer := bundleFixture(t, repo)
	consumer.State = service.PluginStateEnabled
	consumer.Manifest.Capabilities = []service.PluginCapability{{ID: extensionv1.CapabilityRequest, Platform: "openai", AccountType: "oauth"}}
	consumer.Manifest.Dependencies = []extensionv1.Dependency{{Capability: extensionv1.CapabilityAdmin, Platform: "openai", AccountType: "oauth"}}
	consumerInstalled, err := repo.Install(ctx, consumer, []service.PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "openai", AccountType: "oauth", Enabled: true, RolloutPercent: 100}})
	require.NoError(t, err)
	user := mustCreateUser(t, testEntClient(t), &service.User{Email: "registry-cancel-" + uuid.NewString() + "@example.com", PasswordHash: "fixture"})
	jobs := NewAccountJobRepository(integrationDB)
	var jobID int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM admin_account_jobs WHERE id=$1`, jobID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id=$1`, user.ID)
	})
	job, _, err := jobs.Create(ctx, service.CreateAccountJobParams{
		CreatedBy: user.ID, Kind: service.AccountJobKindImportData, IdempotencyKey: uuid.NewString(), RequestHash: strings.Repeat("b", 64), PayloadCipher: "fixture", PayloadExpires: time.Now().Add(time.Hour),
		Metadata: json.RawMessage(fmt.Sprintf(`{"plugin_id":%d,"plugin_generation":%d}`, provider.ID, provider.RuntimeGeneration)), Items: []service.AccountJobItemSeed{{Ordinal: 1, Metadata: []byte(`{}`)}}, Attempt: 1,
	})
	require.NoError(t, err)
	jobID = job.ID
	disabled := append([]service.PluginBinding(nil), provider.Bindings...)
	disabled[0].Enabled = false
	err = repo.UpdateBindingsAndState(ctx, provider.ID, disabled, service.PluginStateDisabled, "", nil, provider.State, provider.BinarySHA256)
	require.ErrorContains(t, err, "missing enabled plugin dependency")
	retained, err := jobs.Get(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusPending, retained.Status, "invalid graph rollback includes job cancellation")
	consumerInstalled.Bindings[0].Enabled = false
	require.NoError(t, repo.UpdateBindingsAndState(ctx, consumerInstalled.ID, consumerInstalled.Bindings, service.PluginStateDisabled, "", nil, consumerInstalled.State, consumerInstalled.BinarySHA256))
	require.NoError(t, repo.UpdateBindingsAndState(ctx, provider.ID, disabled, service.PluginStateDisabled, "", nil, provider.State, provider.BinarySHA256))
	canceled, err := jobs.Get(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusCanceled, canceled.Status)
}
