//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPluginExtensionStateAndSchedulingProjectionCommitTogether(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: "projection-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusError, Schedulable: false, ErrorMessage: "operator note", Credentials: map[string]any{"chatgpt_account_id": "subject"}, Extra: map[string]any{"user_setting": "preserve"}})
	// The shared fixture helper defaults accounts to schedulable. Establish the
	// operator-disabled state explicitly before exercising the plugin write.
	_, setupErr := integrationDB.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=$1`, account.ID)
	require.NoError(t, setupErr)
	plugin := "local.test.projection-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM sub2api_plugin_state WHERE plugin_key=$1`, plugin)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	refreshed := 0
	repo := &pluginRepository{db: integrationDB, refreshAccount: func(context.Context, int64) { refreshed++ }}
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	request := extensionv1.StateRequest{Namespace: "tickets", Key: "one", Value: json.RawMessage(`{"phase":"ready","private_material":"test-only-secret"}`), Projection: &extensionv1.AccountProjection{AccountID: account.ID, Identity: service.CodexTicketAccountIdentity(account), Scheduling: []extensionv1.SchedulingConstraint{{Model: "gpt-6-astra", Effect: "allow", Until: &until, Reason: "ticket_ready"}}}}
	written, err := repo.CompareSwapExtensionState(ctx, plugin, request)
	require.NoError(t, err)
	require.True(t, written.Applied)
	require.EqualValues(t, 1, written.Revision)
	require.Equal(t, 1, refreshed)
	var extra []byte
	var state, note string
	var schedulable bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra,status,error_message,schedulable FROM accounts WHERE id=$1`, account.ID).Scan(&extra, &state, &note, &schedulable))
	require.Contains(t, string(extra), "ticket_ready")
	require.Contains(t, string(extra), "user_setting")
	require.NotContains(t, string(extra), "test-only-secret")
	require.Equal(t, service.StatusError, state)
	require.Equal(t, "operator note", note)
	require.False(t, schedulable)
	request.Value = json.RawMessage(`{"phase":"stopped"}`)
	request.Projection.Scheduling[0].Effect = "deny"
	stale, err := repo.CompareSwapExtensionState(ctx, plugin, request)
	require.NoError(t, err)
	require.False(t, stale.Applied)
	require.Equal(t, 1, refreshed)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id=$1`, account.ID).Scan(&extra))
	require.Contains(t, string(extra), `"effect": "allow"`)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials='{"chatgpt_account_id":"another-subject"}'::jsonb WHERE id=$1`, account.ID)
	require.NoError(t, err)
	request.ExpectedRevision = 1
	_, err = repo.CompareSwapExtensionState(ctx, plugin, request)
	require.Error(t, err)
	stored, err := repo.ReadExtensionState(ctx, plugin, request)
	require.NoError(t, err)
	require.EqualValues(t, 1, stored.Revision)
}

func TestPluginExtensionLeaseGenerationPreventsStaleRelease(t *testing.T) {
	ctx := context.Background()
	plugin := "local.test.lease-" + uuid.NewString()
	repo := &pluginRepository{db: integrationDB}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM sub2api_plugin_leases WHERE plugin_key=$1`, plugin)
	})
	req := extensionv1.LeaseRequest{Namespace: "tickets", Key: "one", Owner: "worker", TTLSeconds: 30}
	first, err := repo.AcquireExtensionLease(ctx, plugin, req)
	require.NoError(t, err)
	require.True(t, first.Acquired)
	req.Generation = first.Generation
	_, err = repo.ReleaseExtensionLease(ctx, plugin, req)
	require.NoError(t, err)
	second, err := repo.AcquireExtensionLease(ctx, plugin, req)
	require.NoError(t, err)
	require.True(t, second.Acquired)
	require.Greater(t, second.Generation, first.Generation)
	_, err = repo.ReleaseExtensionLease(ctx, plugin, req)
	require.NoError(t, err)
	third, err := repo.AcquireExtensionLease(ctx, plugin, req)
	require.NoError(t, err)
	require.False(t, third.Acquired)
}

func TestDisablingPluginCancelsOwnedJobsInSameTransaction(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: "plugin-job-" + uuid.NewString() + "@example.com", PasswordHash: "test-hash"})
	plugins := &pluginRepository{db: integrationDB}
	plugin, err := plugins.Install(ctx, &service.PluginInstallation{PluginKey: "local.test.cancel-" + uuid.NewString(), Name: "test", Version: "1.0.0", Manifest: service.PluginManifest{ID: "test.plugin"}, BinarySHA256: strings.Repeat("c", 64), SignatureStatus: service.PluginSignatureTrusted, State: service.PluginStateEnabled}, []service.PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: service.PlatformOpenAI, AccountType: service.AccountTypeOAuth, Enabled: true, RolloutPercent: 100}})
	require.NoError(t, err)
	jobs := NewAccountJobRepository(integrationDB)
	var ids []int64
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM admin_account_jobs WHERE id=$1`, id)
		}
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM sub2api_plugin_installations WHERE id=$1`, plugin.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	})
	for _, kind := range []string{service.AccountJobKindExtensionOperation, service.AccountJobKindBatchTest} {
		job, _, err := jobs.Create(ctx, service.CreateAccountJobParams{CreatedBy: user.ID, Kind: kind, IdempotencyKey: uuid.NewString(), RequestHash: strings.Repeat("a", 64), PayloadCipher: "test-payload", PayloadExpires: time.Now().Add(time.Hour), Metadata: json.RawMessage(fmt.Sprintf(`{"plugin_id":%d}`, plugin.ID)), Items: []service.AccountJobItemSeed{{Ordinal: 1, Metadata: json.RawMessage(`{}`)}}, Attempt: 1})
		require.NoError(t, err)
		ids = append(ids, job.ID)
	}
	unowned, _, err := jobs.Create(ctx, service.CreateAccountJobParams{CreatedBy: user.ID, Kind: service.AccountJobKindBatchTest, IdempotencyKey: uuid.NewString(), RequestHash: strings.Repeat("b", 64), PayloadCipher: "other-payload", PayloadExpires: time.Now().Add(time.Hour), Metadata: json.RawMessage(`{}`), Items: []service.AccountJobItemSeed{{Ordinal: 1, Metadata: json.RawMessage(`{}`)}}, Attempt: 1})
	require.NoError(t, err)
	ids = append(ids, unowned.ID)
	_, err = integrationDB.ExecContext(ctx, `UPDATE admin_account_jobs SET status='running',started_at=NOW() WHERE id=$1`, ids[1])
	require.NoError(t, err)
	for i := range plugin.Bindings {
		plugin.Bindings[i].Enabled = false
	}
	require.NoError(t, plugins.UpdateBindingsAndState(ctx, plugin.ID, plugin.Bindings, service.PluginStateDisabled, "", nil, service.PluginStateEnabled, plugin.BinarySHA256))
	pending, err := jobs.Get(ctx, ids[0])
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusCanceled, pending.Status)
	require.Equal(t, pending.TargetCount, pending.CanceledCount)
	running, err := jobs.Get(ctx, ids[1])
	require.NoError(t, err)
	require.NotNil(t, running.CancelRequestedAt)
	unowned, err = jobs.Get(ctx, unowned.ID)
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusPending, unowned.Status)
}
