//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

// This is a local, disposable-database observation of the legacy release-only
// API, not a claim that losing its session cancels a process or host operation.
// The exact database gate prevents terminating a shared/CI/production session.
func TestPluginRuntimeLeaseSessionLossObservation(t *testing.T) {
	const database = "sub2api_test_astra_lease_loss_20260921"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var currentDatabase string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT current_database()`).Scan(&currentDatabase))
	if currentDatabase != database {
		t.Skip("lease session termination is restricted to the dedicated local investigation database")
	}
	var address string
	var port int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT host(inet_server_addr()),inet_server_port()`).Scan(&address, &port))
	require.Equal(t, "127.0.0.1", address)
	require.Equal(t, 55437, port)
	leasePIDs := func() []int {
		rows, err := integrationDB.QueryContext(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=$1 AND application_name='sub2api-plugin-lease' ORDER BY pid`, database)
		require.NoError(t, err)
		defer rows.Close()
		var pids []int
		for rows.Next() {
			var pid int
			require.NoError(t, rows.Scan(&pid))
			pids = append(pids, pid)
		}
		require.NoError(t, rows.Err())
		return pids
	}
	require.Empty(t, leasePIDs(), "the investigation owns every lease session in its isolated database")
	repo, current, candidate := installedUpdateFixture(t)
	processCtx, cancelProcess := context.WithCancel(ctx)
	defer cancelProcess()
	processRelease, err := repo.HoldPluginRuntime(processCtx, current)
	require.NoError(t, err)
	defer processRelease()
	pids := leasePIDs()
	require.Len(t, pids, 1)
	processPID := pids[0]
	ioCtx, cancelIO := context.WithCancel(ctx)
	defer cancelIO()
	ioRelease, err := repo.HoldPluginRuntime(service.WithPluginBusinessIOLease(ioCtx), current)
	require.NoError(t, err)
	defer ioRelease()
	pids = leasePIDs()
	require.Len(t, pids, 2)
	ioPID := pids[0]
	if ioPID == processPID {
		ioPID = pids[1]
	}
	require.NotEqual(t, processPID, ioPID)
	leaseRequest := extensionv1.LeaseRequest{Namespace: "lease-loss", Key: "one-ticket-attempt", Owner: "old-owner", TTLSeconds: 30}
	oldExecution := service.WithPluginExecution(ctx, current)
	durableLease, err := repo.AcquireExtensionLease(oldExecution, current.PluginKey, leaseRequest)
	require.NoError(t, err)
	require.True(t, durableLease.Acquired)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM sub2api_plugin_leases WHERE plugin_key=$1 AND namespace='lease-loss'`, current.PluginKey)
	})
	stateRequest := extensionv1.StateRequest{Namespace: "lease-loss", Key: "one-ticket-attempt", Value: []byte(`{"phase":"pre_running","operation_id":"spent"}`)}
	written, err := repo.CompareSwapExtensionState(oldExecution, current.PluginKey, stateRequest)
	require.NoError(t, err)
	require.True(t, written.Applied)
	require.NoError(t, repo.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned))
	pending, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginUpdateWaiting)
	terminateOwnedLease := func(pid int) {
		var terminated bool
		// Recheck database, role and application on the captured PID at the
		// termination statement, rather than trusting an earlier inventory.
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT pg_terminate_backend(pid,1000) FROM pg_stat_activity WHERE pid=$1 AND datname=$2 AND usename=current_user AND application_name='sub2api-plugin-lease'`, pid, database).Scan(&terminated))
		require.True(t, terminated)
	}
	terminateOwnedLease(processPID)
	require.Len(t, leasePIDs(), 1)
	require.ErrorIs(t, repo.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginUpdateWaiting, "one surviving host-IO session still fences promotion")
	terminateOwnedLease(ioPID)
	require.Empty(t, leasePIDs())
	require.NoError(t, repo.CommitPluginUpdate(ctx, pending, candidate), "database session loss removes both advisory locks before caller cancellation")
	updated, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, current.RuntimeGeneration+1, updated.RuntimeGeneration)
	require.NoError(t, processCtx.Err())
	require.NoError(t, ioCtx.Err())
	stateRequest.ExpectedRevision = written.Revision
	stateRequest.Value = []byte(`{"phase":"ready","operation_id":"late"}`)
	_, err = repo.CompareSwapExtensionState(oldExecution, current.PluginKey, stateRequest)
	require.ErrorIs(t, err, service.ErrPluginStateChanged, "the old generation still cannot persist a late result")
	newExecution := service.WithPluginExecution(ctx, updated)
	leaseRequest.Owner = "new-owner"
	newLease, err := repo.AcquireExtensionLease(newExecution, current.PluginKey, leaseRequest)
	require.NoError(t, err)
	require.False(t, newLease.Acquired, "session loss must not delete the independent 30-second domain lease")
	retained, err := repo.ReadExtensionState(newExecution, current.PluginKey, stateRequest)
	require.NoError(t, err)
	require.Equal(t, written.Revision, retained.Revision)
	require.JSONEq(t, `{"phase":"pre_running","operation_id":"spent"}`, string(retained.Value))
	t.Logf("own_process_pid=%d own_io_pid=%d promoted_generation=%d process_context_canceled=%t io_context_canceled=%t durable_lease_still_busy=true old_generation_write_rejected=true", processPID, ioPID, updated.RuntimeGeneration, processCtx.Err() != nil, ioCtx.Err() != nil)
}

func TestObservedPluginRuntimeLeaseReportsOnlyOwnedSessionLoss(t *testing.T) {
	const database = "sub2api_test_astra_lease_loss_20260921"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var currentDatabase, address string
	var port int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT current_database(),host(inet_server_addr()),inet_server_port()`).Scan(&currentDatabase, &address, &port))
	if currentDatabase != database {
		t.Skip("lease session termination is restricted to the dedicated local investigation database")
	}
	require.Equal(t, "127.0.0.1", address)
	require.Equal(t, 55437, port)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND application_name='sub2api-plugin-lease'`, database).Scan(&count))
	require.Zero(t, count)
	repo, current, candidate := installedUpdateFixture(t)
	lease, err := repo.HoldObservedPluginRuntime(service.WithPluginBusinessIOLease(ctx), current)
	require.NoError(t, err)
	defer lease.Release()
	var pid int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=$1 AND usename=current_user AND application_name='sub2api-plugin-lease'`, database).Scan(&pid))
	require.NoError(t, repo.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned))
	pending, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginUpdateWaiting)
	var terminated bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT pg_terminate_backend(pid,1000) FROM pg_stat_activity WHERE pid=$1 AND datname=$2 AND usename=current_user AND application_name='sub2api-plugin-lease'`, pid, database).Scan(&terminated))
	require.True(t, terminated)
	// A PostgreSQL lock is already gone when its session ends. Cancellation is
	// observable later; the detector must not pretend to order it before commit.
	require.NoError(t, repo.CommitPluginUpdate(ctx, pending, candidate))
	observedBeforeCommit := false
	select {
	case <-lease.Done():
		observedBeforeCommit = true
	default:
	}
	select {
	case <-lease.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("dedicated-session loss was not observed by the configured monitor")
	}
	require.ErrorIs(t, lease.Err(), service.ErrPluginRuntimeLeaseLost)
	released := make(chan struct{})
	go func() { lease.Release(); close(released) }()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("observed loss could not finish Release")
	}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND application_name='sub2api-plugin-lease'`, database).Scan(&count))
	require.Zero(t, count, "the lost lease must not reconnect or re-acquire")
	updated, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	normal, err := repo.HoldObservedPluginRuntime(ctx, updated)
	require.NoError(t, err)
	normal.Release()
	normal.Release()
	require.NoError(t, normal.Err())
	select {
	case <-normal.Done():
	default:
		t.Fatal("normal release left its observer open")
	}
	t.Logf("own_lease_pid=%d loss_observed_at_commit_return=%t loss_event_received=true release_completed=true normal_release_error=false reconnect_count=0", pid, observedBeforeCommit)
}
