//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// This single database scenario exercises the new queue's actual row locks,
// pause/restart transitions and durable evidence. It never calls an upstream.
func TestAccountCapabilityJobsLifecycleIntegration(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	actor := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("capability-%d@example.invalid", time.Now().UnixNano())})
	a := mustCreateAccount(t, client, &service.Account{Name: "capability-queue-a", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	b := mustCreateAccount(t, client, &service.Account{Name: "capability-queue-b", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	repo := &accountCapabilityRepository{db: integrationDB}
	var runID int64
	t.Cleanup(func() {
		if runID > 0 {
			_, err := integrationDB.ExecContext(ctx, `DELETE FROM admin_capability_items WHERE run_id=$1`, runID)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, `DELETE FROM admin_capability_runs WHERE id=$1`, runID)
			require.NoError(t, err)
		}
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id IN ($1,$2)`, a.ID, b.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, actor.ID)
		require.NoError(t, err)
	})
	seed := &service.AccountCapabilityRun{CreatedBy: actor.ID, Kind: "probe", IdempotencyKey: "lifecycle", RequestHash: strings.Repeat("a", 64), FolderIDs: []int64{7}, AccountIDs: []int64{a.ID, b.ID}}
	items := []service.AccountCapabilityItem{
		{Ordinal: 1, AccountID: a.ID, AccountName: a.Name, FolderID: 7, ConfigFingerprint: strings.Repeat("b", 64), UpstreamModel: "model-one", Protocol: "responses", Profile: "text"},
		{Ordinal: 2, AccountID: a.ID, AccountName: a.Name, FolderID: 7, ConfigFingerprint: strings.Repeat("b", 64), UpstreamModel: "model-two", Protocol: "responses", Profile: "text"},
		{Ordinal: 3, AccountID: b.ID, AccountName: b.Name, FolderID: 7, ConfigFingerprint: strings.Repeat("c", 64), UpstreamModel: "model-one", Protocol: "responses", Profile: "text"},
	}
	run, replayed, err := repo.Create(ctx, seed, items)
	require.NoError(t, err)
	require.False(t, replayed)
	runID = run.ID
	again, replayed, err := repo.Create(ctx, seed, items)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, runID, again.ID)
	var wg sync.WaitGroup
	claimed := make(chan *service.AccountCapabilityItem, 3)
	failures := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, claimErr := repo.Claim(ctx)
			if claimErr != nil {
				failures <- claimErr
			}
			if item != nil {
				claimed <- item
			}
		}()
	}
	wg.Wait()
	close(claimed)
	close(failures)
	for claimErr := range failures {
		require.NoError(t, claimErr)
	}
	active := []*service.AccountCapabilityItem{}
	for item := range claimed {
		active = append(active, item)
	}
	require.Len(t, active, 2)
	require.NotEqual(t, active[0].AccountID, active[1].AccountID)
	first, unsent := active[0], active[1]
	dispatched, err := repo.MarkDispatched(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, dispatched)
	run, err = repo.Control(ctx, runID, "pause")
	require.NoError(t, err)
	require.Equal(t, "pausing", run.Status)
	dispatched, err = repo.MarkDispatched(ctx, unsent.ID)
	require.NoError(t, err)
	require.False(t, dispatched)
	require.NoError(t, repo.Release(ctx, unsent.ID))
	require.NoError(t, repo.Complete(ctx, first.ID, "succeeded", json.RawMessage(`{"status":"alive"}`), 1))
	run, err = repo.GetRun(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, "paused", run.Status)
	_, err = repo.Control(ctx, runID, "resume")
	require.NoError(t, err)
	interrupted, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, interrupted)
	require.NotEqual(t, first.ID, interrupted.ID)
	dispatched, err = repo.MarkDispatched(ctx, interrupted.ID)
	require.NoError(t, err)
	require.True(t, dispatched)
	require.NoError(t, repo.RecoverInterrupted(ctx))
	run, err = repo.GetRun(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, "paused", run.Status)
	require.Equal(t, 1, run.PossiblySentCount)
	_, err = repo.Control(ctx, runID, "resume")
	require.NoError(t, err)
	last, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, last)
	require.NotEqual(t, first.ID, last.ID)
	require.NotEqual(t, interrupted.ID, last.ID)
	dispatched, err = repo.MarkDispatched(ctx, last.ID)
	require.NoError(t, err)
	require.True(t, dispatched)
	require.NoError(t, repo.Complete(ctx, last.ID, "succeeded", json.RawMessage(`{"status":"alive"}`), 1))
	run, err = repo.GetRun(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, "completed", run.Status)
	require.Equal(t, 3, run.ProcessedCount)
	require.Equal(t, 2, run.RequestCount)
	// Terminal observations remain immutable even if a late completion arrives.
	require.NoError(t, repo.Complete(ctx, interrupted.ID, "succeeded", json.RawMessage(`{"status":"alive"}`), 1))
	observed, err := repo.GetItemsByIDs(ctx, []int64{interrupted.ID})
	require.NoError(t, err)
	require.Equal(t, "indeterminate", observed[0].Status)
	require.True(t, observed[0].RequestCountUnknown)
	_, err = integrationDB.ExecContext(ctx, `UPDATE admin_capability_items SET created_at=NOW()-INTERVAL '365 days',finished_at=NOW()-INTERVAL '365 days' WHERE run_id=$1`, runID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id IN ($1,$2)`, a.ID, b.ID)
	require.NoError(t, err)
	require.NoError(t, repo.RecoverInterrupted(ctx))
	page, err := repo.ListItems(ctx, runID, service.AccountCapabilityFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
}
