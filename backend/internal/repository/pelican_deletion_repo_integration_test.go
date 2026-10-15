//go:build integration

package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPelicanDeletionIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := pelicanPrivateIntegrationDB(t, ctx)
	repo := NewPelicanTestRepository(db)
	deleter := repo.(service.PelicanTaskDeleter)
	reader := repo.(service.PelicanTaskReader)
	task := pelicanIntegrationTask(t, 42)
	for i := 2; i <= 3; i++ {
		copy := *task.Results[0]
		copy.ID, copy.Ordinal, copy.ModelID = uuid.NewString(), i, "model-"+strings.Repeat("x", i)
		task.Results = append(task.Results, &copy)
	}
	task.Total = 3
	_, _, err := repo.Create(ctx, task)
	require.NoError(t, err)
	require.ErrorIs(t, deleter.DeleteTask(ctx, task.ID), service.ErrPelicanTestRunning)
	require.NoError(t, repo.StartTask(ctx, task.ID, time.Now()))
	require.ErrorIs(t, deleter.DeleteTaskResult(ctx, task.ID, task.Results[0].ID), service.ErrPelicanTestRunning)
	for i, result := range task.Results {
		result.Status, result.HTML, result.RawAnswer, result.RawResponse, result.Error = "complete", "<svg />", "original answer", "original response", "original error"
		if i == 1 {
			result.Status = "failed"
		}
		require.NoError(t, repo.SaveResult(ctx, result))
	}
	require.NoError(t, repo.FinishTask(ctx, task.ID, "complete", "original task error", time.Now()))
	keeper := pelicanIntegrationTask(t, 43)
	_, _, err = repo.Create(ctx, keeper)
	require.NoError(t, err)

	preview := service.NewPelicanPreviewService(repo)
	t.Cleanup(preview.Stop)
	decorated := preview.DecorateResult(task.Results[0])
	token := strings.Split(decorated.PreviewURL, "/")[5]
	_, err = preview.Resolve(ctx, token)
	require.NoError(t, err)

	// Concurrent duplicate deletes serialize on the parent and only decrement once.
	deleteConcurrently := func(ids ...string) {
		t.Helper()
		var group sync.WaitGroup
		errors := make(chan error, len(ids))
		for _, id := range ids {
			group.Add(1)
			go func(id string) { defer group.Done(); errors <- deleter.DeleteTaskResult(ctx, task.ID, id) }(id)
		}
		group.Wait()
		close(errors)
		for err := range errors {
			require.NoError(t, err)
		}
	}
	deleteConcurrently(task.Results[0].ID, task.Results[0].ID)
	snapshot, err := reader.GetTaskSnapshot(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.Total)
	require.Equal(t, 2, snapshot.Completed)
	require.Equal(t, 1, snapshot.Counts["failed"])
	require.Equal(t, 1, snapshot.Counts["complete"])
	_, err = repo.GetResult(ctx, task.Results[0].ID)
	require.ErrorIs(t, err, service.ErrPelicanTestNotFound)
	_, err = preview.Resolve(ctx, token)
	require.ErrorIs(t, err, service.ErrPelicanPreviewUnavailable)
	require.ErrorIs(t, deleter.DeleteTaskResult(ctx, task.ID, keeper.Results[0].ID), service.ErrPelicanTestNotFound)
	remaining, err := repo.Get(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, remaining.Results, 2)
	require.Equal(t, "original response", remaining.Results[0].RawResponse)
	require.Equal(t, task.RequestHash, remaining.RequestHash)

	// Deleting both remaining children concurrently removes the final empty task.
	deleteConcurrently(task.Results[1].ID, task.Results[2].ID)
	require.NoError(t, deleter.DeleteTask(ctx, task.ID))
	_, err = repo.Get(ctx, task.ID)
	require.ErrorIs(t, err, service.ErrPelicanTestDeleted)
	_, err = reader.GetTaskSnapshot(ctx, task.ClientTaskID)
	require.ErrorIs(t, err, service.ErrPelicanTestDeleted)
	list, err := repo.List(ctx, 1, 12)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, keeper.ID, list.Items[0].ID)
	var children int
	var hash string
	var expiry time.Time
	var rawError []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_hash,expires_at,error FROM codex_gateway_borrow_test_tasks WHERE id=$1`, task.ID).Scan(&hash, &expiry, &rawError))
	require.Equal(t, task.RequestHash, hash)
	require.True(t, task.ExpiresAt.Equal(expiry))
	require.Empty(t, rawError)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_gateway_borrow_test_results WHERE task_id=$1`, task.ID).Scan(&children))
	require.Zero(t, children)
	_, replayed, err := repo.Create(ctx, task)
	require.True(t, replayed)
	require.ErrorIs(t, err, service.ErrPelicanTestDeleted)

	// The whole-task endpoint also purges children, without deleting the identity.
	require.NoError(t, repo.FinishTask(ctx, keeper.ID, "cancelled", "", time.Now()))
	require.NoError(t, deleter.DeleteTask(ctx, keeper.ID))
	_, err = repo.GetResult(ctx, keeper.Results[0].ID)
	require.ErrorIs(t, err, service.ErrPelicanTestNotFound)
}
