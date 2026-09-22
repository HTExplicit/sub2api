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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func accountViewIntegrationMetadata(t *testing.T, primary, origin *service.PluginInstallation) json.RawMessage {
	t.Helper()
	view := service.AccountJobViewMetadata{
		AccountViewIdentityV1: extensionv1.AccountViewIdentityV1{
			Version: 1, PluginID: origin.ID, PluginKey: origin.PluginKey,
			PackageSHA256: origin.PackageSHA256, ViewID: "fixture-view", PresetID: "all",
			ViewDefinitionDigest: strings.Repeat("b", 64),
		},
		RuntimeGeneration: origin.RuntimeGeneration, PolicyRevision: origin.Revision,
		NormalizedQueryDigest: strings.Repeat("c", 64),
	}
	raw, err := json.Marshal(map[string]any{
		"plugin_id": primary.ID, "plugin_generation": primary.RuntimeGeneration, "account_view": view,
	})
	require.NoError(t, err)
	return raw
}

func TestAccountViewDualOwnerFenceIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	plugins, origin, _ := installedUpdateFixture(t)
	_, primary, _ := installedUpdateFixture(t)
	require.Less(t, origin.ID, primary.ID, "the origin is deliberately the lower ID")
	metadata := accountViewIntegrationMetadata(t, primary, origin)

	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer blocker.Rollback()
	var id int64
	require.NoError(t, blocker.QueryRowContext(ctx,
		`SELECT id FROM sub2api_plugin_installations WHERE id=$1 FOR UPDATE`, origin.ID).Scan(&id))
	fenced, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer fenced.Rollback()
	var fencePID int
	require.NoError(t, fenced.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&fencePID))
	lockResult := make(chan error, 1)
	go func() {
		lockResult <- lockAccountJobPlugin(ctx, fenced, metadata, service.AccountJobKindBulkUpdate)
	}()
	// Observe the server-side wait before testing the other row; a sleep alone
	// could pass while the production helper had not started its first query.
	require.Eventually(t, func() bool {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx,
			`SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, fencePID).Scan(&waiting)
		return err == nil && waiting
	}, 2*time.Second, 10*time.Millisecond)
	probe, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer probe.Rollback()
	require.NoError(t, probe.QueryRowContext(ctx,
		`SELECT id FROM sub2api_plugin_installations WHERE id=$1 FOR UPDATE NOWAIT`, primary.ID).Scan(&id),
		"the higher primary row must remain unlocked while the lower origin row is blocked")
	require.NoError(t, probe.Rollback())
	require.NoError(t, blocker.Rollback())
	select {
	case err := <-lockResult:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("production dual-owner fence did not finish after the blocker released")
	}
	for _, ownerID := range []int64{origin.ID, primary.ID} {
		other, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = other.ExecContext(ctx, `SET LOCAL lock_timeout='150ms'`)
		require.NoError(t, err)
		_, err = other.ExecContext(ctx,
			`UPDATE sub2api_plugin_installations SET runtime_generation=runtime_generation+1 WHERE id=$1`, ownerID)
		var pgError *pgconn.PgError
		require.ErrorAs(t, err, &pgError, "each actual FOR SHARE row lock must block a second connection's update")
		require.Equal(t, "55P03", pgError.Code)
		require.NoError(t, other.Rollback())
	}
	require.NoError(t, fenced.Commit())
	_, err = integrationDB.ExecContext(ctx,
		`UPDATE sub2api_plugin_installations SET runtime_generation=runtime_generation+1 WHERE id=$1`, origin.ID)
	require.NoError(t, err, "the second connection may update after fence commit")
	staleOrigin := testTx(t)
	require.ErrorIs(t, lockAccountJobPlugin(ctx, staleOrigin, metadata), service.ErrAccountViewUnavailable)
	require.NoError(t, staleOrigin.Rollback())
	freshOrigin, err := plugins.GetByID(ctx, origin.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx,
		`UPDATE sub2api_plugin_installations SET runtime_generation=runtime_generation+1 WHERE id=$1`, primary.ID)
	require.NoError(t, err)
	stalePrimary := testTx(t)
	require.ErrorIs(t, lockAccountJobPlugin(ctx, stalePrimary, accountViewIntegrationMetadata(t, primary, freshOrigin)), service.ErrAccountJobPluginUnavailable)
	require.NoError(t, stalePrimary.Rollback())
}

func TestAccountViewScopedProbeTerminalAtomicIntegration(t *testing.T) {
	ctx := context.Background()
	fixture := newCindyBalanceProbeFinalizeFixture(t, false, "terra_running")
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	fingerprint, err := service.AccountCredentialFingerprint(service.ProviderProfileCindyLaxaV1,
		service.AccountTypeAPIKey, "https://api.laxarouter.ai", fixture.account.GetCredential("api_key"))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx,
		`UPDATE accounts SET cindy_credential_generation=1 WHERE id=$1`, fixture.account.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO account_credential_identities
		(account_id,provider_profile,auth_type,normalized_base_url,fingerprint,generation,active)
		VALUES ($1,'cindy_laxa_v1','apikey','https://api.laxarouter.ai',$2,1,true)`, fixture.account.ID, fingerprint)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM cindy_health_states WHERE account_id=$1`, fixture.account.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM account_credential_identities WHERE account_id=$1`, fixture.account.ID)
		require.NoError(t, err)
	})
	_, err = integrationDB.ExecContext(ctx,
		`UPDATE cindy_balance_probe_jobs SET consecutive_upstream_failures=2 WHERE id=$1`, fixture.reservation.JobID)
	require.NoError(t, err)
	var outboxBefore int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, fixture.account.ID).Scan(&outboxBefore))
	// The failure occurs after the shared health and marker writer, at the item
	// update. Its condition names only this fixture's item, never another job.
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("account_view_item_fail_%d", suffix)
	triggerName := fmt.Sprintf("account_view_item_fail_trigger_%d", suffix)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.id=%d AND NEW.state='exhausted' THEN
				RAISE EXCEPTION 'synthetic scoped item failure';
			END IF;
			RETURN NEW;
		END; $$`, functionName, fixture.reservation.ItemID))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(
		`CREATE TRIGGER %s BEFORE UPDATE ON cindy_balance_probe_items FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName))
	require.NoError(t, err)
	removeFailure := func() {
		_, err := integrationDB.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON cindy_balance_probe_items`, triggerName))
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
		require.NoError(t, err)
	}
	t.Cleanup(removeFailure)
	observedAt := time.Now().UTC()
	failedTx := testTx(t)
	var failedEpisode service.CindyHealthEpisode
	state, err := repo.finalizeAccountMarkerTx(ctx, failedTx, fixture.reservation, nil,
		fixture.leaseToken, observedAt, 5*time.Minute, true, true, &failedEpisode)
	require.ErrorContains(t, err, "synthetic scoped item failure")
	require.Empty(t, state)
	require.Zero(t, failedEpisode.AccountID)
	require.NoError(t, failedTx.Rollback())
	var markerMissing bool
	var itemState string
	var healthCount, outboxAfter, failures int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT cindy_balance_insufficient_at IS NULL FROM accounts WHERE id=$1`, fixture.account.ID).Scan(&markerMissing))
	require.True(t, markerMissing)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT count(*) FROM cindy_health_states WHERE account_id=$1`, fixture.account.ID).Scan(&healthCount))
	require.Zero(t, healthCount)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT state FROM cindy_balance_probe_items WHERE id=$1`, fixture.reservation.ItemID).Scan(&itemState))
	require.Equal(t, "terra_running", itemState)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT consecutive_upstream_failures FROM cindy_balance_probe_jobs WHERE id=$1`, fixture.reservation.JobID).Scan(&failures))
	require.Equal(t, 2, failures)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, fixture.account.ID).Scan(&outboxAfter))
	require.Equal(t, outboxBefore, outboxAfter)
	removeFailure()

	successTx := testTx(t)
	var committed service.CindyHealthEpisode
	state, err = repo.finalizeAccountMarkerTx(ctx, successTx, fixture.reservation, nil,
		fixture.leaseToken, observedAt, 5*time.Minute, true, true, &committed)
	require.NoError(t, err)
	require.Equal(t, "exhausted", state)
	require.Equal(t, fixture.account.ID, committed.AccountID)
	require.EqualValues(t, 1, committed.Generation)
	require.Equal(t, fingerprint, committed.Fingerprint)
	var healthStatus, finalOutcome string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT a.cindy_balance_insufficient_at IS NULL,h.status,i.state,i.final_outcome
		FROM accounts a JOIN cindy_health_states h ON h.account_id=a.id
		JOIN cindy_balance_probe_items i ON i.account_id=a.id AND i.id=$2 WHERE a.id=$1`,
		fixture.account.ID, fixture.reservation.ItemID).Scan(&markerMissing, &healthStatus, &itemState, &finalOutcome))
	require.False(t, markerMissing)
	require.Equal(t, service.CindyHealthStatusBalanceInsufficient, healthStatus)
	require.Equal(t, "exhausted", itemState)
	require.Equal(t, "exhausted", finalOutcome)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT count(*) FROM cindy_health_states WHERE account_id=$1`, fixture.account.ID).Scan(&healthCount))
	require.Equal(t, 1, healthCount)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, fixture.account.ID).Scan(&outboxAfter))
	require.Equal(t, outboxBefore+1, outboxAfter, "scoped terminal uses one shared writer, without a second legacy marker/outbox write")
}

func TestAccountViewProbeResumeJSONBCASIntegration(t *testing.T) {
	ctx := context.Background()
	first := newCindyBalanceProbeLifecycleAccount(t, "view-resume-first")
	second := newCindyBalanceProbeLifecycleAccount(t, "view-resume-second")
	jobID := insertCindyBalanceProbeLifecycleJob(t, "paused", "", time.Time{})
	insertCindyBalanceProbeLifecycleItem(t, jobID, first, 1)
	insertCindyBalanceProbeLifecycleItem(t, jobID, second, 2)
	ids := []int64{first.ID, second.ID}
	origin := &service.CindyBalanceProbeOrigin{
		Version: 1, PluginID: 101, PluginKey: service.CindyAccountViewPluginKey,
		PackageSHA256: strings.Repeat("a", 64), RuntimeGeneration: 4,
		FrozenAccountIDs: ids, OperationKey: "synthetic-view-resume", RequestDigest: strings.Repeat("d", 64),
		View: service.AccountJobViewMetadata{
			AccountViewIdentityV1: extensionv1.AccountViewIdentityV1{
				Version: 1, PluginID: 102, PluginKey: "fixture.origin-view", PackageSHA256: strings.Repeat("b", 64),
				ViewID: "fixture-view", PresetID: "all", ViewDefinitionDigest: strings.Repeat("c", 64),
			},
			RuntimeGeneration: 5, PolicyRevision: 7, NormalizedQueryDigest: strings.Repeat("e", 64),
		},
	}
	scope := service.CindyBalanceProbeScope{Mode: "selected", AccountIDs: ids, Origin: origin}
	_, err := integrationDB.ExecContext(ctx,
		`UPDATE cindy_balance_probe_jobs SET scope=$2::jsonb,consecutive_upstream_failures=2 WHERE id=$1`,
		jobID, service.EncodeCindyBalanceProbeScope(scope))
	require.NoError(t, err)
	next := *origin
	next.RuntimeGeneration, next.View.RuntimeGeneration, next.View.PolicyRevision = 6, 8, 9
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	invalid := next
	invalid.FrozenAccountIDs = []int64{first.ID}
	_, err = repo.ResumeScoped(ctx, jobID, origin, &invalid)
	require.ErrorIs(t, err, service.ErrAccountViewUnavailable, "the public repository gate rejects target replacement before transaction admission")
	expectedJSON, err := json.Marshal(origin)
	require.NoError(t, err)
	tx := testTx(t)
	require.NoError(t, resumeScopedProbeTx(ctx, tx, jobID, expectedJSON, &next))
	job, err := repo.GetJob(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "queued", job.Status)
	require.Zero(t, job.ConsecutiveFailures)
	require.Equal(t, &next, job.Scope.Origin, "only generation and revision fields may change")
	require.Equal(t, ids, job.Scope.AccountIDs)
	require.Equal(t, origin.FrozenAccountIDs, job.Scope.Origin.FrozenAccountIDs)
	require.Equal(t, origin.OperationKey, job.Scope.Origin.OperationKey)
	require.Equal(t, origin.RequestDigest, job.Scope.Origin.RequestDigest)
	require.Equal(t, origin.View.NormalizedQueryDigest, job.Scope.Origin.View.NormalizedQueryDigest)
	rows, err := integrationDB.QueryContext(ctx, `SELECT account_id FROM cindy_balance_probe_items WHERE job_id=$1 ORDER BY ordinal`, jobID)
	require.NoError(t, err)
	var persistedIDs []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		persistedIDs = append(persistedIDs, id)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, ids, persistedIDs, "resume neither reselects nor reseeds items")
	_, err = repo.Pause(ctx, jobID)
	require.NoError(t, err)
	staleTx := testTx(t)
	require.ErrorIs(t, resumeScopedProbeTx(ctx, staleTx, jobID, expectedJSON, &next), service.ErrCindyBalanceProbeChanged)
	require.NoError(t, staleTx.Rollback())
	unchanged, err := repo.GetJob(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "paused", unchanged.Status)
	require.Equal(t, &next, unchanged.Scope.Origin, "stale expected JSON must not overwrite the already refreshed origin")
}
