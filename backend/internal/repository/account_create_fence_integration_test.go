//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestAccountCreateEntFenceIntegration(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../../plugins/cindy-provider/manifest.source.json")
	require.NoError(t, err)
	var source service.PluginManifest
	require.NoError(t, json.Unmarshal(raw, &source))
	var create extensionv1.Contribution
	for _, contribution := range source.Contributions {
		if contribution.Slot == extensionv1.AccountCreateSlot {
			create = contribution
		}
	}
	require.NotNil(t, create.AccountCreate)
	manifest := service.PluginManifest{ID: service.CindyAccountViewPluginKey,
		Capabilities: []service.PluginCapability{
			{ID: extensionv1.CapabilityProvider, Platform: service.PlatformCindy, AccountType: service.AccountTypeAPIKey},
			{ID: extensionv1.CapabilityAdmin, Platform: service.PlatformCindy, AccountType: service.AccountTypeAPIKey},
		}, Contributions: []extensionv1.Contribution{create}}
	plugins := &pluginRepository{db: integrationDB}
	artifact := &service.PluginInstallation{PluginKey: manifest.ID, Name: "synthetic-create-provider", Version: "1.0.0",
		BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted,
		Manifest: manifest, ArtifactData: []byte("synthetic create fence package")}
	installed, err := plugins.PrepareBundledPlugin(ctx, artifact, "account-create-fixture", "", true, "synthetic-encrypted-config")
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, table := range []string{"sub2api_plugin_state", "sub2api_plugin_bootstrap", "sub2api_plugin_installations"} {
			_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE plugin_key=$1", manifest.ID)
			require.NoError(t, err)
		}
	})
	require.NoError(t, plugins.CompleteBundledPlugin(ctx, installed.ID, "account-create-fixture"))
	current, err := plugins.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	for index := range current.Bindings {
		current.Bindings[index].Enabled, current.Bindings[index].RolloutPercent = true, 100
	}
	require.NoError(t, plugins.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateEnabled, "", nil, current.State, current.BinarySHA256))
	current, err = plugins.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	configDigest := sha256.Sum256([]byte(current.ConfigEncrypted))
	fence := service.PluginExecutionFence{ID: current.ID, Generation: current.RuntimeGeneration, PluginKey: current.PluginKey,
		PackageSHA256: current.PackageSHA256, PolicyRevision: current.Revision, OriginCreate: true,
		CreateContributionID: create.ID, CreateDefinitionSHA256: service.AccountCreateDefinitionDigest(&create), ConfigSHA256: hex.EncodeToString(configDigest[:])}
	var writes int
	tryDenied := func(wanted service.PluginExecutionFence) {
		t.Helper()
		tx, err := testEntClient(t).Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		err = lockAccountCreatePolicy(ctx, tx.Client(), wanted)
		if err == nil {
			writes++
		}
		require.ErrorIs(t, err, service.ErrAccountCreateUnavailable)
	}
	badConfig := fence
	badConfig.ConfigSHA256 = strings.Repeat("0", 64)
	tryDenied(badConfig)
	badDefinition := fence
	badDefinition.CreateDefinitionSHA256 = strings.Repeat("0", 64)
	tryDenied(badDefinition)
	for _, capability := range []string{extensionv1.CapabilityProvider, extensionv1.CapabilityAdmin} {
		_, err = integrationDB.ExecContext(ctx,
			`UPDATE sub2api_plugin_bindings SET rollout_percent=99 WHERE plugin_id=$1 AND capability=$2`, current.ID, capability)
		require.NoError(t, err)
		tryDenied(fence)
		_, err = integrationDB.ExecContext(ctx,
			`UPDATE sub2api_plugin_bindings SET rollout_percent=100 WHERE plugin_id=$1 AND capability=$2`, current.ID, capability)
		require.NoError(t, err)
	}
	require.Zero(t, writes, "invalid config, definition or partial binding never reaches account persistence")
	var before int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&before))
	require.Zero(t, before)

	tx, err := testEntClient(t).Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, lockAccountCreatePolicy(ctx, tx.Client(), fence))
	// The Ent adapter must hold the same installation lock through the account
	// transaction; a separate completed guard transaction would not block this.
	other, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer other.Rollback()
	_, err = other.ExecContext(ctx, `SET LOCAL lock_timeout='150ms'`)
	require.NoError(t, err)
	_, err = other.ExecContext(ctx, `UPDATE sub2api_plugin_installations SET config_encrypted='changed' WHERE id=$1`, current.ID)
	var pgError *pgconn.PgError
	require.ErrorAs(t, err, &pgError)
	require.Equal(t, "55P03", pgError.Code)
	require.NoError(t, other.Rollback())
	account := &service.Account{Name: "synthetic-create-fenced", Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 3,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "synthetic-create-only"},
		Extra:       map[string]any{service.CindyDeviceIDExtraKey: strings.Repeat("b", 64), service.CindyDeviceIDSourceExtraKey: "input-preserved"}}
	accountRepo := NewAccountRepository(testEntClient(t), integrationDB, nil)
	require.NoError(t, accountRepo.Create(dbent.NewTxContext(ctx, tx), account))
	require.Positive(t, account.ID)
	inside, err := tx.Client().Account.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, inside)
	var outside int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&outside))
	require.Zero(t, outside, "account persistence shares the still-open fenced transaction")
	require.NoError(t, tx.Rollback())
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&outside))
	require.Zero(t, outside)
}
