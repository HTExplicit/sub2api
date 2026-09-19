//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func bundleFixture(t *testing.T, repo *pluginRepository) *service.PluginInstallation {
	t.Helper()
	key := "codexrip.test-" + uuid.NewString()
	t.Cleanup(func() {
		for _, table := range []string{"sub2api_plugin_state", "sub2api_plugin_bootstrap", "sub2api_plugin_installations"} {
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE plugin_key=$1", key)
		}
	})
	return &service.PluginInstallation{PluginKey: key, Name: "bundle fixture", Version: "1.0.0", BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted, Manifest: service.PluginManifest{ID: key, Capabilities: []service.PluginCapability{{ID: extensionv1.CapabilityAdmin, Platform: service.PlatformOpenAI, AccountType: service.AccountTypeOAuth}}}, ArtifactData: []byte("fixture")}
}

func TestPluginBundleResumesWithoutResettingActivationOrReinstallingRemovedPlugin(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	artifact := bundleFixture(t, repo)
	first, err := repo.PrepareBundledPlugin(ctx, artifact, "bundle-one", "", true, "first-config")
	require.NoError(t, err)
	require.Equal(t, service.PluginStateDisabled, first.State)
	resumed, err := repo.PrepareBundledPlugin(ctx, artifact, "bundle-one", "", false, "replacement-config")
	require.NoError(t, err)
	require.Equal(t, "first-config", resumed.ConfigEncrypted)
	require.NoError(t, repo.CompleteBundledPlugin(ctx, first.ID, "bundle-one"))
	current, err := repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, current.Bindings[0].Enabled)
	current.Bindings[0].Enabled = false
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateDisabled, "", nil, service.PluginStateEnabled, current.BinarySHA256))
	artifact.Version = "1.0.1"
	artifact.BinarySHA256 = strings.Repeat("b", 64)
	updated, err := repo.PrepareBundledPlugin(ctx, artifact, "bundle-two", "", true, "new-defaults")
	require.NoError(t, err)
	require.Equal(t, "first-config", updated.ConfigEncrypted)
	require.NoError(t, repo.CompleteBundledPlugin(ctx, updated.ID, "bundle-two"))
	updated, err = repo.GetByID(ctx, updated.ID)
	require.NoError(t, err)
	require.False(t, updated.Bindings[0].Enabled)
	require.NoError(t, repo.Delete(ctx, updated.ID, updated.BinarySHA256))
	applied, err := repo.BundleApplied(ctx, artifact.PluginKey, "a-future-bundle")
	require.NoError(t, err)
	require.True(t, applied, "explicit removal must survive later bundle upgrades")
}

func TestPluginBundleDoesNotResurrectStoppedTicketWhenLegacyRowsChange(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	artifact := bundleFixture(t, repo)
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(time.Hour)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "migration-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "owner"}})
	identity := service.CodexTicketAccountIdentity(account)
	ticket := service.CodexTicketRecord{State: "gAAAAA" + strings.Repeat("x", 286), AccountID: account.ID, Identity: identity, Model: "gpt-6-astra", CapturedAt: now, ExpiresAt: expiry, Length: 292}
	extra, _ := json.Marshal(map[string]any{"codex_turn_ticket:gpt-6-astra": ticket})
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, account.ID, extra)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO openai_codex_ticket_runtime(account_id,model,identity,phase,expires_at) VALUES($1,'gpt-6-astra',$2,'stopped',$3)`, account.ID, identity, expiry)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	installation, err := repo.PrepareBundledPlugin(ctx, artifact, "migration-bundle", "codex-tickets-v1", true, "config")
	require.NoError(t, err)
	configuration := json.RawMessage(`{"models":["gpt-6-astra"],"proxy_url":""}`)
	require.NoError(t, repo.ImportLegacyPluginState(ctx, installation, "codex-tickets-v1", configuration))
	require.NoError(t, repo.CompleteBundledPlugin(ctx, installation.ID, "migration-bundle"))
	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_codex_ticket_runtime SET phase='ready',next_at=NOW() WHERE account_id=$1`, account.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ImportLegacyPluginState(ctx, installation, "codex-tickets-v1", configuration))
	modelHash := sha256.Sum256([]byte("gpt-6-astra"))
	stored, err := repo.ReadExtensionState(ctx, artifact.PluginKey, extensionv1.StateRequest{Namespace: "tickets", Key: strconv.FormatInt(account.ID, 10) + "." + hex.EncodeToString(modelHash[:])})
	require.NoError(t, err)
	var state map[string]any
	require.NoError(t, json.Unmarshal(stored.Value, &state))
	require.Equal(t, "stopped", state["phase"])
	require.Nil(t, state["next_attempt_at"])
}
