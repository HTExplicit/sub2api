//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPelicanRepeatSamplesIntegration(t *testing.T) {
	ctx := context.Background()
	db := pelicanPrivateIntegrationDB(t, ctx)
	repo := &pelicanTestRepository{db: db}
	task := pelicanIntegrationTask(t, 42)
	count := 3
	task.TestConfig.RepeatCount = &count
	first := task.Results[0]
	first.RepeatIndex = 1
	for i := 2; i <= 3; i++ {
		row := *first
		row.ID = uuid.NewString()
		row.Ordinal = i
		row.RepeatIndex = i
		task.Results = append(task.Results, &row)
	}
	task.Total = 3
	_, _, err := repo.Create(ctx, task)
	require.NoError(t, err)
	for _, row := range task.Results {
		row.Status = "complete"
		row.RawResponse = "RAW-SAMPLE-CANARY"
		require.NoError(t, repo.SaveResult(ctx, row))
	}
	page, err := repo.ListTaskResults(ctx, task.ID, service.PelicanResultFilter{Page: 1, Size: 2})
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
	require.Len(t, page.Items, 2)
	require.Equal(t, 1, page.Items[0].RepeatIndex)
	require.Equal(t, 2, page.Items[1].RepeatIndex)
	require.Equal(t, 3, page.Items[0].RepeatCount)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "CANARY")
	require.NotContains(t, string(encoded), "raw_response")
	next, err := repo.ListTaskResults(ctx, task.ID, service.PelicanResultFilter{Page: 2, Size: 2})
	require.NoError(t, err)
	require.Equal(t, 3, next.Items[0].RepeatIndex)
	full, err := repo.GetResult(ctx, task.Results[1].ID)
	require.NoError(t, err)
	require.Equal(t, 2, full.RepeatIndex)
	require.Equal(t, 3, full.RepeatCount)
	require.Equal(t, "RAW-SAMPLE-CANARY", full.RawResponse)
	_, err = db.ExecContext(ctx, "INSERT INTO codex_gateway_borrow_test_results (id,task_id,ordinal,account_id,model_id,effort,status,repeat_index) VALUES ($1,$2,4,42,'gpt-6-astra','high','pending',2)", uuid.NewString(), task.ID)
	require.Error(t, err, "one model cannot have the same sample number twice")
	require.NoError(t, repo.FinishTask(ctx, task.ID, "complete", "", time.Now()))
	require.NoError(t, repo.DeleteTaskResult(ctx, task.ID, full.ID))
	_, err = repo.GetResult(ctx, full.ID)
	require.ErrorIs(t, err, service.ErrPelicanTestNotFound)
	page, err = repo.ListTaskResults(ctx, task.ID, service.PelicanResultFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
}
