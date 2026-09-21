//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// Design probes deliberately stay in their own database. Disable exercises the
// real production path. The remaining cases are test-only isolation models,
// including negative controls which intentionally bypass the production helper.
func registryAtomicityContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	var database string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database))
	if database != "sub2api_test_astra_registry_20260921" {
		t.Skip("registry interleaving design probes require the dedicated local database")
	}
	return ctx
}

func installRegistryAtomicityFixture(t *testing.T, repo *pluginRepository, capability, platform, kind, operation string, enabled bool) (*service.PluginInstallation, *service.PluginInstallation) {
	t.Helper()
	artifact := bundleFixture(t, repo)
	artifact.State = service.PluginStateDisabled
	if enabled {
		artifact.State = service.PluginStateEnabled
	}
	artifact.Manifest.Capabilities = []service.PluginCapability{{ID: capability, Platform: platform, AccountType: kind}}
	artifact.Manifest.Operations = map[string][]string{capability: {operation}}
	installed, err := repo.Install(context.Background(), artifact, []service.PluginBinding{{Capability: capability, Platform: platform, AccountType: kind, Enabled: enabled, RolloutPercent: 100}})
	require.NoError(t, err)
	return installed, artifact
}

type pauseRegistryDisableRepository struct {
	*pluginRepository
	target  int64
	ready   chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (r *pauseRegistryDisableRepository) GetByID(ctx context.Context, id int64) (*service.PluginInstallation, error) {
	installation, err := r.pluginRepository.GetByID(ctx, id)
	if err != nil || id != r.target {
		return installation, err
	}
	// Manager.Disable reaches GetByID only after its real dependency check.
	r.once.Do(func() {
		close(r.ready)
		select {
		case <-r.proceed:
		case <-ctx.Done():
		}
	})
	return installation, ctx.Err()
}

func TestRegistryDisableCannotFollowDependentPromotion(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	provider, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityProvider, "cindy", "apikey", "provider.read", true)
	consumer, artifact := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "consumer.run", true)
	candidate := *artifact
	candidate.Version = "1.0.1"
	candidate.Manifest.Version = candidate.Version
	candidate.Manifest.Dependencies = []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}
	candidate.ArtifactData = []byte("registry atomicity dependent replacement")
	digest := sha256.Sum256(candidate.ArtifactData)
	candidate.PackageSHA256 = hex.EncodeToString(digest[:])
	require.NoError(t, repo.StagePluginUpdate(ctx, consumer, &candidate, service.PluginUpdatePinned))
	pending, err := repo.GetByID(ctx, consumer.ID)
	require.NoError(t, err)
	paused := &pauseRegistryDisableRepository{pluginRepository: repo, target: provider.ID, ready: make(chan struct{}), proceed: make(chan struct{})}
	manager := service.NewPluginManager(paused, nil, nil, service.PluginHostInfo{}, nil)
	finished := make(chan error, 1)
	go func() {
		_, err := manager.Disable(ctx, provider.ID)
		finished <- err
	}()
	select {
	case <-paused.ready:
	case <-ctx.Done():
		t.Fatal("disable did not reach its post-admission barrier")
	}
	// Different target transactions: A's new dependency commits after B's
	// actual service admission but before B's conditional binding write.
	commitErr := repo.CommitPluginUpdate(ctx, pending, &candidate)
	close(paused.proceed)
	require.NoError(t, commitErr)
	var disableErr error
	select {
	case disableErr = <-finished:
	case <-ctx.Done():
		t.Fatal("disable did not finish after the controlled promotion")
	}
	afterConsumer, err := repo.GetByID(ctx, consumer.ID)
	require.NoError(t, err)
	afterProvider, err := repo.GetByID(ctx, provider.ID)
	require.NoError(t, err)
	missing := len(afterConsumer.Manifest.Dependencies) == 1 && !afterProvider.Bindings[0].Enabled
	t.Logf("dependent_promoted=%t disable_returned_nil=%t persisted_missing_dependency=%t", afterConsumer.PackageSHA256 == candidate.PackageSHA256, disableErr == nil, missing)
	require.False(t, missing, "target-only CAS must not admit a provider disable based on a pre-promotion dependency graph")
}

// Both tables are read by the same transaction/query. This represents the
// minimal desired-graph projection a future production graphTx can validate.
func readRegistryGraphPrototype(ctx context.Context, tx *sql.Tx) ([]*service.PluginInstallation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.manifest,COALESCE(jsonb_agg(jsonb_build_object(
		'capability',b.capability,'platform',b.platform,'account_type',b.account_type,'enabled',b.enabled,'rollout_percent',b.rollout_percent)
		ORDER BY b.id) FILTER (WHERE b.id IS NOT NULL),'[]'::jsonb)
		FROM sub2api_plugin_installations p LEFT JOIN sub2api_plugin_bindings b ON b.plugin_id=p.id GROUP BY p.id ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var installations []*service.PluginInstallation
	for rows.Next() {
		installation := &service.PluginInstallation{}
		var manifest, bindings []byte
		if err = rows.Scan(&installation.ID, &manifest, &bindings); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(manifest, &installation.Manifest); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(bindings, &installation.Bindings); err != nil {
			return nil, err
		}
		installations = append(installations, installation)
	}
	return installations, rows.Err()
}

func countRegistryPrototypeOwners(installations []*service.PluginInstallation) int {
	count := 0
	for _, installation := range installations {
		for _, operation := range installation.Manifest.Operations[extensionv1.CapabilityAdmin] {
			if operation != "registry.same-operation" {
				continue
			}
			for _, binding := range installation.Bindings {
				if binding.Enabled && binding.Capability == extensionv1.CapabilityAdmin {
					count++
				}
			}
		}
	}
	return count
}

func registryPrototypeEnable(ctx context.Context, tx *sql.Tx, installation *service.PluginInstallation) error {
	result, err := tx.ExecContext(ctx, `UPDATE sub2api_plugin_installations SET state='starting',revision=revision+1
		WHERE id=$1 AND revision=$2 AND state='disabled' AND binary_sha256=$3`, installation.ID, installation.Revision, installation.BinarySHA256)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return service.ErrPluginStateChanged
	}
	bindings := append([]service.PluginBinding(nil), installation.Bindings...)
	bindings[0].Enabled = true
	return replacePluginBindings(ctx, tx, installation.ID, bindings)
}

func runRegistryEnableInterleaving(t *testing.T, isolation sql.IsolationLevel, rightOptions ...sql.IsolationLevel) (error, int) {
	t.Helper()
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	first, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	second, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	left, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: isolation})
	require.NoError(t, err)
	defer left.Rollback()
	rightIsolation := isolation
	if len(rightOptions) > 0 {
		rightIsolation = rightOptions[0]
	}
	right, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: rightIsolation})
	require.NoError(t, err)
	defer right.Rollback()
	leftSnapshot, err := readRegistryGraphPrototype(ctx, left)
	require.NoError(t, err)
	rightSnapshot, err := readRegistryGraphPrototype(ctx, right)
	require.NoError(t, err)
	require.Zero(t, countRegistryPrototypeOwners(leftSnapshot))
	require.Zero(t, countRegistryPrototypeOwners(rightSnapshot))
	// Both admissions precede either write. The transactions remain open;
	// explicit sequencing, not timing/sleep, chooses the winning commit.
	require.NoError(t, registryPrototypeEnable(ctx, left, first))
	require.NoError(t, left.Commit())
	rightErr := registryPrototypeEnable(ctx, right, second)
	if rightErr == nil {
		rightErr = right.Commit()
	} else {
		_ = right.Rollback()
	}
	current, err := repo.List(ctx)
	require.NoError(t, err)
	owners := countRegistryPrototypeOwners(current)
	t.Logf("left_isolation=%s right_isolation=%s both_preflight_owners=0 second_write_or_commit_error=%t committed_overlapping_owners=%d", isolation, rightIsolation, rightErr != nil, owners)
	return rightErr, owners
}

func TestRegistryReadCommittedPrototypeDoesNotProtectAdmission(t *testing.T) {
	err, owners := runRegistryEnableInterleaving(t, sql.LevelReadCommitted)
	require.NoError(t, err)
	require.Equal(t, 2, owners, "negative control: the old two-transaction mechanism has no graph fence")
}

func TestRegistrySerializablePrototypeRejectsOverlappingOwners(t *testing.T) {
	err, owners := runRegistryEnableInterleaving(t, sql.LevelSerializable)
	require.Error(t, err)
	var postgres *pgconn.PgError
	require.ErrorAs(t, err, &postgres)
	require.Equal(t, "40001", postgres.Code)
	require.Equal(t, 1, owners)
}

func TestRegistryMixedIsolationPrototypeDoesNotProtectAdmission(t *testing.T) {
	err, owners := runRegistryEnableInterleaving(t, sql.LevelSerializable, sql.LevelReadCommitted)
	require.NoError(t, err)
	require.Equal(t, 2, owners, "an older nonparticipating writer defeats a global serializability claim")
}

func TestRegistrySerializableOutsideReadPrototypeDoesNotProtectAdmission(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	first, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	second, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	leftOutside, err := repo.List(ctx)
	require.NoError(t, err)
	rightOutside, err := repo.List(ctx)
	require.NoError(t, err)
	require.Zero(t, countRegistryPrototypeOwners(leftOutside))
	require.Zero(t, countRegistryPrototypeOwners(rightOutside))
	left, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	require.NoError(t, err)
	defer left.Rollback()
	right, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	require.NoError(t, err)
	defer right.Rollback()
	require.NoError(t, registryPrototypeEnable(ctx, left, first))
	require.NoError(t, left.Commit())
	require.NoError(t, registryPrototypeEnable(ctx, right, second))
	require.NoError(t, right.Commit())
	current, err := repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, countRegistryPrototypeOwners(current))
	t.Log("both_transactions_serializable=true graph_read_outside_transactions=true both_committed=true overlapping_owners=2")
}

func TestRegistrySerializablePostWriteValidationPrototypeProtectsAdmission(t *testing.T) {
	ctx := registryAtomicityContext(t)
	repo := &pluginRepository{db: integrationDB}
	first, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	second, _ := installRegistryAtomicityFixture(t, repo, extensionv1.CapabilityAdmin, "openai", "oauth", "registry.same-operation", false)
	left, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	require.NoError(t, err)
	defer left.Rollback()
	right, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	require.NoError(t, err)
	defer right.Rollback()
	require.NoError(t, registryPrototypeEnable(ctx, left, first))
	require.NoError(t, registryPrototypeEnable(ctx, right, second))
	leftGraph, leftErr := readRegistryGraphPrototype(ctx, left)
	if leftErr == nil {
		require.Equal(t, 1, countRegistryPrototypeOwners(leftGraph))
	}
	rightGraph, rightErr := readRegistryGraphPrototype(ctx, right)
	if rightErr == nil {
		require.Equal(t, 1, countRegistryPrototypeOwners(rightGraph))
	}
	if leftErr == nil {
		leftErr = left.Commit()
	} else {
		_ = left.Rollback()
	}
	if rightErr == nil {
		rightErr = right.Commit()
	} else {
		_ = right.Rollback()
	}
	conflicts, commits := 0, 0
	for _, transactionErr := range []error{leftErr, rightErr} {
		if transactionErr == nil {
			commits++
			continue
		}
		var postgres *pgconn.PgError
		require.ErrorAs(t, transactionErr, &postgres)
		require.Equal(t, "40001", postgres.Code)
		conflicts++
	}
	require.Equal(t, 1, commits)
	require.Equal(t, 1, conflicts)
	current, err := repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, countRegistryPrototypeOwners(current))
	t.Log("post_write_graph_read_inside_each_transaction=true commits=1 serialization_conflicts=1 overlapping_owners=1")
}
