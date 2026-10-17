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

func TestPelicanRepeatRetestsDoNotMultiplySamples(t *testing.T) {
	for _, mode := range []string{"all", "failed", "selected"} {
		t.Run(mode, func(t *testing.T) {
			runner, repo, catalog := newPelicanBackgroundFixture(t, func(context.Context, int64, string, string) (*PelicanResult, error) {
				return &PelicanResult{Status: "complete"}, nil
			})
			count := 3
			source := &PelicanTestTask{ID: "source", Status: "complete", ExecutionMode: PelicanExecutionModeAccount, GenerationTimeoutSeconds: 600, ExpiresAt: time.Now().Add(time.Hour), TestConfig: PelicanTestConfig{RepeatCount: &count, TestItems: []string{"pelican"}}}
			for i := 1; i <= 3; i++ {
				verdict := "passed"
				if i == 2 {
					verdict = "invalid"
				}
				source.Results = append(source.Results, &PelicanTestResult{ID: string(rune('a' + i)), TaskID: source.ID, Ordinal: i, AccountID: 1, ModelID: "gpt-6.1-sol", Status: "complete", RepeatIndex: i, RepeatCount: 3, Evaluation: &PelicanEvaluation{TestItems: []string{"pelican"}, Scene: PelicanScenes[i], Nonce: "OLD", Verdict: verdict}})
			}
			repo.tasks[source.ID] = source
			request := PelicanRetestRequest{ClientTaskID: pelicanV7(t)}
			expected := []int{1, 2, 3}
			if mode == "failed" {
				request.FailedOnly = true
				expected = []int{2}
			}
			if mode == "selected" {
				request.ResultIDs = []string{source.Results[0].ID, source.Results[2].ID}
				expected = []int{1, 3}
			}
			created, err := runner.Retest(context.Background(), 7, source.ID, request, catalog)
			require.NoError(t, err)
			require.Equal(t, len(expected), created.Total)
			repo.mu.Lock()
			defer repo.mu.Unlock()
			for i, row := range repo.tasks[created.ID].Results {
				require.Equal(t, expected[i], row.RepeatIndex)
				require.Equal(t, 3, row.RepeatCount)
				require.Equal(t, PelicanScenes[expected[i]], row.Evaluation.Scene)
				require.NotEqual(t, "OLD", row.Evaluation.Nonce)
			}
		})
	}
}
