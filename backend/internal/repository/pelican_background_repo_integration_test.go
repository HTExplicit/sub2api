//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestPelicanBackgroundSummaryPaginationRetainsFullDetails(t *testing.T) {
	ctx := context.Background()
	db := codexGatewayBorrowPrivateIntegrationDB(t, ctx)
	repo := NewCodexGatewayBorrowTestRepository(db)
	reader, ok := repo.(service.PelicanTaskReader)
	require.True(t, ok)
	task := codexGatewayBorrowIntegrationTask(t, 42)
	first := task.Results[0]
	for i := 2; i <= 3; i++ {
		copy := *first
		copy.ID = uuid.NewString()
		copy.Ordinal = i
		copy.ModelID = first.ModelID + strings.Repeat("x", i)
		task.Results = append(task.Results, &copy)
	}
	task.Total = 3
	_, _, err := repo.Create(ctx, task)
	require.NoError(t, err)
	first.Status = "complete"
	first.RawResponse = "RAW-CANARY" + strings.Repeat("large response", 10000)
	first.Error = string([]byte{0xff, 0, 1})
	require.NoError(t, repo.SaveResult(ctx, first))
	task.Results[1].Status = "failed"
	require.NoError(t, repo.SaveResult(ctx, task.Results[1]))
	snapshot, err := reader.GetTaskSnapshot(ctx, task.ClientTaskID)
	require.NoError(t, err)
	require.Nil(t, snapshot.Results)
	require.Equal(t, 1, snapshot.Counts["complete"])
	require.Equal(t, 1, snapshot.Counts["no_preview"])
	require.Equal(t, 1, snapshot.Counts["failed"])
	page, err := reader.ListTaskResults(ctx, task.ID, service.PelicanResultFilter{Page: 1, Size: 2})
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
	require.Len(t, page.Items, 2)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "RAW-CANARY")
	require.NotContains(t, string(encoded), "raw_response")
	failed, err := reader.ListTaskResults(ctx, task.ID, service.PelicanResultFilter{Page: 1, Size: 50, Status: "failed"})
	require.NoError(t, err)
	require.Len(t, failed.Items, 1)
	full, err := repo.GetResult(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, first.RawResponse, full.RawResponse)
	require.Equal(t, first.Error, full.Error)
	require.NoError(t, repo.MarkInterrupted(ctx, time.Now()))
	interrupted, err := reader.ListTaskResults(ctx, task.ID, service.PelicanResultFilter{Status: "incomplete"})
	require.NoError(t, err)
	require.Len(t, interrupted.Items, 1)
	require.True(t, interrupted.Items[0].Interrupted)
	full, err = repo.GetResult(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "complete", full.Status)
	_, err = db.ExecContext(ctx, "UPDATE codex_gateway_borrow_test_tasks SET created_at=$2::timestamptz - INTERVAL '24 hours',expires_at=$2::timestamptz WHERE id=$1", task.ID, time.Now().Add(-time.Second))
	require.NoError(t, err)
	_, err = reader.GetTaskSnapshot(ctx, task.ID)
	require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestNotFound)
}
