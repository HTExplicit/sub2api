//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanRetestPreservesSceneBudgetAndReplayIdentity(t *testing.T) {
	runner, repo, catalog := newPelicanBackgroundFixture(t, func(context.Context, int64, string, string) (*PelicanResult, error) {
		return &PelicanResult{Status: "complete"}, nil
	})
	source := &PelicanTestTask{ID: "source", Status: "complete", ExecutionMode: PelicanExecutionModeAccount, GenerationTimeoutSeconds: 1200, ExpiresAt: time.Now().Add(time.Hour), TestConfig: PelicanTestConfig{TestItems: []string{"pelican", "candy"}}}
	source.Results = []*PelicanTestResult{{ID: "failed-quality", TaskID: source.ID, AccountID: 1, ModelID: "gpt-6.1-sol", Effort: "high", Status: "complete", Evaluation: &PelicanEvaluation{TestItems: source.TestConfig.TestItems, Scene: "雪山谷地", Nonce: "OLD-NONCE", Verdict: "invalid"}}, {ID: "passed", TaskID: source.ID, AccountID: 1, ModelID: "gpt-6-astra", Status: "complete", Evaluation: &PelicanEvaluation{Verdict: "passed"}}}
	repo.tasks[source.ID] = source
	request := PelicanRetestRequest{ClientTaskID: pelicanV7(t), FailedOnly: true}
	created, err := runner.Retest(context.Background(), 7, source.ID, request, catalog)
	require.NoError(t, err)
	require.Equal(t, 1200, created.GenerationTimeoutSeconds)
	require.Equal(t, 1, created.Total)
	repo.mu.Lock()
	row := repo.tasks[created.ID].Results[0]
	require.Equal(t, "雪山谷地", row.Evaluation.Scene)
	require.NotEqual(t, "OLD-NONCE", row.Evaluation.Nonce)
	require.Equal(t, "high", row.Effort)
	delete(repo.tasks, source.ID)
	repo.mu.Unlock()
	replayed, err := runner.Retest(context.Background(), 7, source.ID, request, nil)
	require.NoError(t, err)
	require.Equal(t, created.ID, replayed.ID)
	require.True(t, replayed.Replayed)
	request.FailedOnly = false
	_, err = runner.Retest(context.Background(), 7, source.ID, request, nil)
	require.ErrorIs(t, err, ErrPelicanTestReplayConflict)
}
