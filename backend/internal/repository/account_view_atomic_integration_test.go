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
	"github.com/stretchr/testify/require"
)

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
	var jobStatus, itemState string
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
		`SELECT status,consecutive_upstream_failures FROM cindy_balance_probe_jobs WHERE id=$1`, fixture.reservation.JobID).Scan(&jobStatus, &failures))
	require.Equal(t, "running", jobStatus)
	require.Equal(t, 2, failures)
	done, err := repo.FinishIfDone(ctx, fixture.reservation.JobID, fixture.leaseToken)
	require.NoError(t, err)
	require.False(t, done, "the rolled-back item must keep its job unfinished")
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
	done, err = repo.FinishIfDone(ctx, fixture.reservation.JobID, fixture.leaseToken)
	require.NoError(t, err)
	require.True(t, done)
	var jobFinished, leaseReleased bool
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT status,consecutive_upstream_failures,finished_at IS NOT NULL,lease_token IS NULL AND lease_until IS NULL
		FROM cindy_balance_probe_jobs WHERE id=$1`, fixture.reservation.JobID).Scan(&jobStatus, &failures, &jobFinished, &leaseReleased))
	require.Equal(t, "completed", jobStatus)
	require.Zero(t, failures)
	require.True(t, jobFinished)
	require.True(t, leaseReleased)
}

func TestAccountViewProbeResumeJSONBCASIntegration(t *testing.T) {
	ctx := context.Background()
	previousConfig := service.EffectiveCindyProviderConfig()
	nativeConfig := previousConfig
	nativeConfig.BalanceDetection = true
	service.ConfigureCindyProvider(&nativeConfig)
	t.Cleanup(func() { service.ConfigureCindyProvider(&previousConfig) })
	first := newCindyBalanceProbeLifecycleAccount(t, "view-resume-first")
	second := newCindyBalanceProbeLifecycleAccount(t, "view-resume-second")
	jobID := insertCindyBalanceProbeLifecycleJob(t, "paused", "", time.Time{})
	insertCindyBalanceProbeLifecycleItem(t, jobID, first, 1)
	insertCindyBalanceProbeLifecycleItem(t, jobID, second, 2)
	ids := []int64{first.ID, second.ID}
	viewJSON, err := json.Marshal(map[string]any{
		"version": 1, "plugin_id": 102, "plugin_key": "fixture.origin-view", "package_sha256": strings.Repeat("b", 64),
		"view_id": "fixture-view", "preset_id": "all", "view_definition_digest": strings.Repeat("c", 64),
		"runtime_generation": 5, "policy_revision": 7, "normalized_query_digest": strings.Repeat("e", 64),
		"opaque_future_field": true,
	})
	require.NoError(t, err)
	origin := &service.CindyBalanceProbeOrigin{
		Version: 1, PluginID: 101, PluginKey: service.CindyAccountViewPluginKey,
		PackageSHA256: strings.Repeat("a", 64), RuntimeGeneration: 4,
		FrozenAccountIDs: ids, OperationKey: "synthetic-view-resume", RequestDigest: strings.Repeat("d", 64),
		View: json.RawMessage(viewJSON),
	}
	originJSON, err := json.Marshal(origin)
	require.NoError(t, err)
	scope := service.CindyBalanceProbeScope{Mode: "selected", AccountIDs: ids, Origin: origin}
	_, err = integrationDB.ExecContext(ctx,
		`UPDATE cindy_balance_probe_jobs SET scope=$2::jsonb,consecutive_upstream_failures=2 WHERE id=$1`,
		jobID, service.EncodeCindyBalanceProbeScope(scope))
	require.NoError(t, err)
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	invalid := *origin
	invalid.FrozenAccountIDs = []int64{first.ID}
	_, err = repo.ResumeScoped(ctx, jobID, origin, &invalid)
	require.ErrorIs(t, err, service.ErrCindyBalanceProbeChanged, "the public repository gate rejects target replacement before transaction admission")
	_, err = repo.ResumeScoped(ctx, jobID, origin, origin)
	require.ErrorIs(t, err, service.ErrCindyBalanceProbeChanged, "historical metadata alone cannot supply the private native worker context")
	// Resume binds the private native context through the real service. The
	// worker is never started, so this fixture cannot send a probe request.
	probe := service.NewCindyBalanceProbeService(repo, nil, nil, nil)
	t.Cleanup(probe.Stop)
	job, err := probe.Resume(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "queued", job.Status)
	require.Zero(t, job.ConsecutiveFailures)
	require.NotNil(t, job.Scope.Origin)
	expectedJSON, err := json.Marshal(job.Scope.Origin)
	require.NoError(t, err)
	require.JSONEq(t, string(originJSON), string(expectedJSON), "native resume preserves the entire historical origin")
	require.Equal(t, ids, job.Scope.AccountIDs)
	require.Equal(t, origin.FrozenAccountIDs, job.Scope.Origin.FrozenAccountIDs)
	require.Equal(t, origin.OperationKey, job.Scope.Origin.OperationKey)
	require.Equal(t, origin.RequestDigest, job.Scope.Origin.RequestDigest)
	require.JSONEq(t, string(origin.View), string(job.Scope.Origin.View))
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
	// Native resume leaves generations and revisions untouched. Model a real
	// concurrent origin change on this job so the saved expected JSON is stale.
	next := *job.Scope.Origin
	changed := next
	changed.RequestDigest = strings.Repeat("f", 64)
	changedScope := job.Scope
	changedScope.Origin = &changed
	_, err = integrationDB.ExecContext(ctx,
		`UPDATE cindy_balance_probe_jobs SET scope=$2::jsonb WHERE id=$1 AND status='paused'`,
		jobID, service.EncodeCindyBalanceProbeScope(changedScope))
	require.NoError(t, err)
	staleTx := testTx(t)
	require.ErrorIs(t, resumeScopedProbeTx(ctx, staleTx, jobID, expectedJSON, &next), service.ErrCindyBalanceProbeChanged)
	require.NoError(t, staleTx.Rollback())
	unchanged, err := repo.GetJob(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "paused", unchanged.Status)
	changedJSON, err := json.Marshal(&changed)
	require.NoError(t, err)
	unchangedJSON, err := json.Marshal(unchanged.Scope.Origin)
	require.NoError(t, err)
	require.JSONEq(t, string(changedJSON), string(unchangedJSON), "stale expected JSON must not overwrite the concurrent origin")
	require.Equal(t, ids, unchanged.Scope.AccountIDs)
}
