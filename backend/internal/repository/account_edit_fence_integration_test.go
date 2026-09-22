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

// One local PostgreSQL method covers the newly introduced edit fence/codec
// persistence seam. It invokes no provider process, model or external service.
func TestAccountEditEntFenceNullDeletionAndStateCASIntegration(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../../plugins/cindy-provider/manifest.source.json")
	require.NoError(t, err)
	var source service.PluginManifest
	require.NoError(t, json.Unmarshal(raw, &source))
	var edit extensionv1.Contribution
	for _, contribution := range source.Contributions {
		if contribution.Slot == extensionv1.AccountEditSlot {
			edit = contribution
		}
	}
	require.NotNil(t, edit.AccountEdit)
	manifest := service.PluginManifest{ID: service.CindyAccountViewPluginKey, Contributions: []extensionv1.Contribution{edit}, Capabilities: []service.PluginCapability{
		{ID: extensionv1.CapabilityProvider, Platform: service.PlatformCindy, AccountType: service.AccountTypeAPIKey},
		{ID: extensionv1.CapabilityAdmin, Platform: service.PlatformCindy, AccountType: service.AccountTypeAPIKey},
	}}
	plugins := &pluginRepository{db: integrationDB}
	artifact := &service.PluginInstallation{PluginKey: manifest.ID, Name: "synthetic-edit-provider", Version: "1.0.0", Manifest: manifest,
		BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted, ArtifactData: []byte("synthetic edit fence package")}
	installed, err := plugins.PrepareBundledPlugin(ctx, artifact, "account-edit-fixture", "", true, "synthetic-edit-config")
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, table := range []string{"sub2api_plugin_state", "sub2api_plugin_bootstrap", "sub2api_plugin_installations"} {
			_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE plugin_key=$1", manifest.ID)
			require.NoError(t, err)
		}
	})
	require.NoError(t, plugins.CompleteBundledPlugin(ctx, installed.ID, "account-edit-fixture"))
	current, err := plugins.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	for index := range current.Bindings {
		current.Bindings[index].Enabled, current.Bindings[index].RolloutPercent = true, 100
	}
	require.NoError(t, plugins.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateEnabled, "", nil, current.State, current.BinarySHA256))
	current, err = plugins.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	accountRepo := NewAccountRepository(testEntClient(t), integrationDB, nil)
	account := &service.Account{Name: "synthetic-edit-original", Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Concurrency: 1, Schedulable: false,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "synthetic-edit-only", "model_mapping": map[string]any{"custom": "upstream"}, "compact_model_mapping": nil},
		Extra: map[string]any{service.CindyDeviceIDExtraKey: strings.Repeat("b", 64), service.CindyDeviceIDSourceExtraKey: "input-preserved",
			"openai_responses_mode": "force_responses", "openai_compact_mode": nil, "openai_apikey_responses_websockets_v2_mode": "dedicated",
			"openai_apikey_responses_websockets_v2_enabled": false, "responses_websockets_v2_enabled": true, "openai_ws_enabled": true,
			"openai_ws_force_http": true, "openai_passthrough": true, "openai_oauth_responses_websockets_v2_mode": "passthrough", "openai_compact_supported": false}}
	require.NoError(t, accountRepo.Create(ctx, account))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM scheduler_outbox WHERE account_id=$1`, account.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	account, err = accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	configDigest := sha256.Sum256([]byte(current.ConfigEncrypted))
	fence := service.PluginExecutionFence{ID: current.ID, Generation: current.RuntimeGeneration, PluginKey: current.PluginKey, PackageSHA256: current.PackageSHA256,
		PolicyRevision: current.Revision, OriginEdit: true, EditAccountID: account.ID, EditContributionID: edit.ID, EditDefinitionSHA256: service.AccountEditDefinitionDigest(&edit), ConfigSHA256: hex.EncodeToString(configDigest[:])}
	denied := func(wanted service.PluginExecutionFence) {
		t.Helper()
		tx, err := testEntClient(t).Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		require.ErrorIs(t, lockAccountEditPolicy(ctx, tx.Client(), wanted), service.ErrAccountEditUnavailable)
	}
	wrong := fence
	wrong.ConfigSHA256 = strings.Repeat("0", 64)
	denied(wrong)
	wrong = fence
	wrong.EditDefinitionSHA256 = strings.Repeat("0", 64)
	denied(wrong)
	wrong = fence
	wrong.Generation++
	denied(wrong)
	for _, capability := range []string{extensionv1.CapabilityProvider, extensionv1.CapabilityAdmin} {
		_, err := integrationDB.ExecContext(ctx, `UPDATE sub2api_plugin_bindings SET rollout_percent=0 WHERE plugin_id=$1 AND capability=$2`, current.ID, capability)
		require.NoError(t, err)
		denied(fence)
		_, err = integrationDB.ExecContext(ctx, `UPDATE sub2api_plugin_bindings SET rollout_percent=100 WHERE plugin_id=$1 AND capability=$2`, current.ID, capability)
		require.NoError(t, err)
	}
	// Demonstrate real-account rollout (not Create's full-100 rule) using the
	// exact stable bucket algorithm on the newly inserted positive account ID.
	value := uint64(account.ID)
	value ^= value >> 33
	value *= 0xff51afd7ed558ccd
	value ^= value >> 33
	percent := int(value%100) + 1
	_, err = integrationDB.ExecContext(ctx, `UPDATE sub2api_plugin_bindings SET rollout_percent=$2 WHERE plugin_id=$1`, current.ID, percent)
	require.NoError(t, err)
	noopExtra := map[string]any{"openai_compact_mode": nil}
	bound, release, err := service.PrepareAccountEdit(ctx, account, nil, noopExtra, nil)
	require.NoError(t, err)
	defer release()
	beforeDigest := service.AccountEditStateDigest(account)
	// Account creation queued its own synthetic event. Drain only that exact
	// fixture row so this test can observe the update event's rollback/commit
	// without the normal pending-event coalescing hiding the insertion.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, account.ID)
	require.NoError(t, err)
	var outboxBefore int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, account.ID).Scan(&outboxBefore))
	applyStorage := func(txCtx context.Context, stored *service.Account) {
		stored.Name = "synthetic-edit-cleared"
		for _, key := range []string{"openai_responses_mode", "openai_apikey_responses_websockets_v2_mode", "openai_apikey_responses_websockets_v2_enabled", "responses_websockets_v2_enabled", "openai_ws_enabled"} {
			delete(stored.Extra, key)
		}
		stored.Credentials["model_mapping"] = nil
		delete(stored.Credentials, "compact_model_mapping")
		require.NoError(t, accountRepo.Update(txCtx, stored))
	}
	checkStored := func(q pluginFenceQuerier, cleared bool) {
		t.Helper()
		var isCleared, compactNull, normalNull, compactRemoved, independent bool
		err := scanPluginFenceRow(ctx, q, `SELECT NOT(extra ? 'openai_responses_mode') AND NOT(extra ? 'openai_apikey_responses_websockets_v2_mode') AND NOT(extra ? 'openai_apikey_responses_websockets_v2_enabled') AND NOT(extra ? 'responses_websockets_v2_enabled') AND NOT(extra ? 'openai_ws_enabled'),
			extra ? 'openai_compact_mode' AND extra->'openai_compact_mode'='null'::jsonb,
			credentials ? 'model_mapping' AND credentials->'model_mapping'='null'::jsonb,
			NOT(credentials ? 'compact_model_mapping'),
			extra->'openai_ws_force_http'='true'::jsonb AND extra->'openai_passthrough'='true'::jsonb AND extra->>'openai_oauth_responses_websockets_v2_mode'='passthrough' AND extra->'openai_compact_supported'='false'::jsonb
			FROM accounts WHERE id=$1`, []any{account.ID}, &isCleared, &compactNull, &normalNull, &compactRemoved, &independent)
		require.NoError(t, err)
		require.Equal(t, cleared, isCleared)
		require.True(t, compactNull)
		require.Equal(t, cleared, normalNull)
		require.Equal(t, cleared, compactRemoved)
		require.True(t, independent)
	}
	tx, err := testEntClient(t).Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, lockAccountEditPolicy(ctx, tx.Client(), fence))
	require.NoError(t, lockCindyAccountJobTarget(ctx, tx.Client(), account.ID))
	txCtx := dbent.NewTxContext(bound, tx)
	locked, err := accountRepo.GetByID(txCtx, account.ID)
	require.NoError(t, err)
	_, err = service.AccountEditOwnedForUpdate(txCtx, locked, nil, noopExtra, nil)
	require.NoError(t, err)
	locked.CindyCredentialGeneration++
	_, err = service.AccountEditOwnedForUpdate(txCtx, locked, nil, noopExtra, nil)
	require.ErrorIs(t, err, service.ErrAccountEditStateChanged)
	locked.CindyCredentialGeneration--
	other, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer other.Rollback()
	_, err = other.ExecContext(ctx, `SET LOCAL lock_timeout='150ms'`)
	require.NoError(t, err)
	_, err = other.ExecContext(ctx, `UPDATE sub2api_plugin_installations SET config_encrypted='late' WHERE id=$1`, current.ID)
	var pgError *pgconn.PgError
	require.ErrorAs(t, err, &pgError)
	require.Equal(t, "55P03", pgError.Code)
	require.NoError(t, other.Rollback())
	applyStorage(txCtx, locked)
	checkStored(tx.Client(), true)
	checkStored(integrationDB, false)
	require.NoError(t, tx.Rollback())
	checkStored(integrationDB, false)
	unchanged, err := accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, beforeDigest, service.AccountEditStateDigest(unchanged))
	var outboxAfter int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, account.ID).Scan(&outboxAfter))
	require.Equal(t, outboxBefore, outboxAfter)
	committed, err := testEntClient(t).Tx(ctx)
	require.NoError(t, err)
	defer committed.Rollback()
	require.NoError(t, lockAccountEditPolicy(ctx, committed.Client(), fence))
	require.NoError(t, lockCindyAccountJobTarget(ctx, committed.Client(), account.ID))
	applyStorage(dbent.NewTxContext(ctx, committed), unchanged)
	require.NoError(t, committed.Commit())
	checkStored(integrationDB, true)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, account.ID).Scan(&outboxAfter))
	require.Equal(t, outboxBefore+1, outboxAfter)
	latest, err := accountRepo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	_, err = service.AccountEditOwnedForUpdate(ctx, latest, nil, noopExtra, nil)
	require.NoError(t, err, "an unrelated current-row no-op does not demand a provider")
	_, err = service.AccountEditOwnedForUpdate(bound, latest, nil, noopExtra, nil)
	require.ErrorIs(t, err, service.ErrAccountEditStateChanged, "the frozen old state cannot be applied after commit")
}
