//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanRepeatExpansionValidationAndReplay(t *testing.T) {
	runner := newPelicanTestRunner(newPelicanMemoryTestRepo(), nil, nil)
	t.Cleanup(runner.Stop)
	count := 3
	request := PelicanTestRequest{ClientTaskID: pelicanV7(t), PelicanTestConfig: PelicanTestConfig{RepeatCount: &count, TestItems: []string{"pelican", "candy"}}, Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "a"}, {AccountID: 1, ModelID: "b"}}}
	task, replayed, err := runner.Create(context.Background(), 7, request)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, 6, task.Total)
	ids := map[string]bool{}
	for i, row := range task.Results {
		require.Equal(t, i%3+1, row.RepeatIndex)
		require.Equal(t, 3, row.RepeatCount)
		require.False(t, ids[row.ID])
		ids[row.ID] = true
		require.Empty(t, row.Evaluation.Nonce)
		require.Empty(t, row.Evaluation.Scene)
	}
	_, replayed, err = runner.Create(context.Background(), 7, request)
	require.NoError(t, err)
	require.True(t, replayed)
	changed := 2
	request.RepeatCount = &changed
	_, _, err = runner.Create(context.Background(), 7, request)
	require.ErrorIs(t, err, ErrPelicanTestReplayConflict)
	for _, bad := range []int{0, -1, 5001, 2501} {
		request.RepeatCount = &bad
		_, err = normalizePelicanTestRequest(request, time.Now())
		require.ErrorIs(t, err, ErrPelicanTestInvalidRequest)
	}
	boundary := 2500
	request.RepeatCount = &boundary
	_, err = normalizePelicanTestRequest(request, time.Now())
	require.NoError(t, err)
	legacy := PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "model"}}}
	normalized, err := normalizePelicanTestRequest(legacy, time.Now())
	require.NoError(t, err)
	oldPayload := "{\"targets\":[{\"account_id\":1,\"model_id\":\"model\",\"effort\":\"\"}],\"generation_timeout_seconds\":600,\"execution_mode\":\"account\"}"
	digest := sha256.Sum256([]byte(oldPayload))
	require.Equal(t, hex.EncodeToString(digest[:]), pelicanTestRequestHash(normalized))
	one := 1
	legacy.RepeatCount = &one
	normalizedOne, err := normalizePelicanTestRequest(legacy, time.Now())
	require.NoError(t, err)
	require.Equal(t, pelicanTestRequestHash(normalized), pelicanTestRequestHash(normalizedOne))
}

type pelicanParallelEvaluator struct {
	call func(context.Context, int64, string, PelicanModelInput) (*PelicanResult, error)
}

func (g pelicanParallelEvaluator) GeneratePelican(context.Context, int64, string, string) (*PelicanResult, error) {
	return nil, fmt.Errorf("legacy generator unused")
}
func (g pelicanParallelEvaluator) GeneratePelicanInput(ctx context.Context, id int64, model, _ string, input PelicanModelInput) (*PelicanResult, error) {
	return g.call(ctx, id, model, input)
}
func (g pelicanParallelEvaluator) PelicanImageCapability(context.Context, int64, string) (bool, error) {
	return true, nil
}

func TestPelicanRepeatModelFlowsParallelAndOrdered(t *testing.T) {
	var mu sync.Mutex
	stages := map[string][]string{}
	started := make(chan string, 2)
	gate := make(chan struct{})
	evaluator := pelicanParallelEvaluator{call: func(ctx context.Context, id int64, model string, input PelicanModelInput) (*PelicanResult, error) {
		ctx, release, err := AcquirePelicanExecution(ctx, id)
		if err != nil {
			return nil, err
		}
		defer release()
		_ = BeginPelicanGeneration(ctx)
		stage := "generating"
		answer := ""
		if input.Text == PelicanCandyPrompt {
			stage = "candy"
			answer = "21"
		} else if len(input.Images) > 0 {
			stage = "reviewing"
			answer = "{\"checks\":{\"pelican\":true,\"bicycle\":true,\"riding\":true,\"motion\":true}}"
		} else {
			if input.Text != PelicanPrompt {
				return nil, fmt.Errorf("unexpected prompt: %q", input.Text)
			}
			answer = `<html><body><svg><animate attributeName="opacity" values="0;1" dur="2s"/></svg></body></html>`
		}
		mu.Lock()
		first := len(stages[model]) == 0
		stages[model] = append(stages[model], stage)
		mu.Unlock()
		if first {
			started <- model
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &PelicanResult{Status: "complete", RawAnswer: answer}, nil
	}}
	runner := newPelicanTestRunner(newPelicanMemoryTestRepo(), evaluator, nil)
	t.Cleanup(runner.Stop)
	runner.renderer = pelicanFixedRenderer{}
	count := 2
	task, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t), PelicanTestConfig: PelicanTestConfig{RepeatCount: &count, TestItems: []string{"pelican", "candy"}}, Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "a"}, {AccountID: 1, ModelID: "b"}}})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), task, nil) }()
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("different models on one account did not overlap")
		}
	}
	close(gate)
	require.NoError(t, <-done)
	for _, model := range []string{"a", "b"} {
		require.Equal(t, []string{"candy", "generating", "reviewing", "candy", "generating", "reviewing"}, stages[model])
	}
	for _, row := range task.Results {
		require.Equal(t, "passed", row.Evaluation.Verdict)
		require.NotEmpty(t, row.HTML)
	}
}

func TestPelicanRepeatFailureContinuesAndCancellationPersistsTail(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			generator := pelicanFakeGenerator{generate: func(context.Context, int64, string, string) (*PelicanResult, error) {
				calls++
				if stop {
					cancel()
				}
				return &PelicanResult{Status: "failed", RawResponse: "retained"}, nil
			}}
			runner := newPelicanTestRunner(newPelicanMemoryTestRepo(), generator, nil)
			t.Cleanup(runner.Stop)
			count := 3
			task, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t), PelicanTestConfig: PelicanTestConfig{RepeatCount: &count}, Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "a"}}})
			require.NoError(t, err)
			require.NoError(t, runner.Run(ctx, task, nil))
			if stop {
				require.Equal(t, 1, calls)
				for _, row := range task.Results[1:] {
					require.Equal(t, "cancelled", row.Status)
				}
			} else {
				require.Equal(t, 3, calls)
			}
			require.Equal(t, "retained", task.Results[0].RawResponse)
			require.Equal(t, 3, task.Completed)
		})
	}
}

func TestPelicanRepeatModelTurnCancellationAcrossTasks(t *testing.T) {
	coordinator := newPelicanExecutionCoordinator(10)
	release, err := coordinator.acquireModel(context.Background(), 1, "a")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan error, 1)
	go func() {
		next, err := coordinator.acquireModel(ctx, 1, "a")
		if next != nil {
			next()
		}
		blocked <- err
	}()
	parallel, err := coordinator.acquireModel(context.Background(), 1, "b")
	require.NoError(t, err)
	parallel()
	coordinator.mu.Lock()
	require.Zero(t, coordinator.active, "model turns never hold outbound slots")
	coordinator.mu.Unlock()
	cancel()
	require.ErrorIs(t, <-blocked, context.Canceled)
	release()
	release()
	next, err := coordinator.acquireModel(context.Background(), 1, "a")
	require.NoError(t, err)
	next()
	coordinator.mu.Lock()
	require.Empty(t, coordinator.models)
	coordinator.mu.Unlock()
}
