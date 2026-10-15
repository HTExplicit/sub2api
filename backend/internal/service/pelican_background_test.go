//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type pelicanBackgroundRepo struct {
	PelicanTestRepository
	mu        sync.Mutex
	tasks     map[string]*PelicanTestTask
	fullReads int
}

func clonePelicanTestTask(task *PelicanTestTask) *PelicanTestTask {
	copy := *task
	copy.Results = make([]*PelicanTestResult, 0, len(task.Results))
	for _, row := range task.Results {
		item := *row
		copy.Results = append(copy.Results, &item)
	}
	return &copy
}
func (r *pelicanBackgroundRepo) Create(_ context.Context, task *PelicanTestTask) (*PelicanTestTask, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, old := range r.tasks {
		if old.ClientTaskID == task.ClientTaskID {
			if old.RequestHash != task.RequestHash {
				return nil, true, ErrPelicanTestReplayConflict
			}
			return clonePelicanTestTask(old), true, nil
		}
	}
	r.tasks[task.ID] = clonePelicanTestTask(task)
	return clonePelicanTestTask(task), false, nil
}
func (r *pelicanBackgroundRepo) Get(_ context.Context, id string) (*PelicanTestTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fullReads++
	if task := r.tasks[id]; task != nil {
		return clonePelicanTestTask(task), nil
	}
	return nil, ErrPelicanTestNotFound
}
func (r *pelicanBackgroundRepo) GetResult(_ context.Context, id string) (*PelicanTestResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, task := range r.tasks {
		for _, result := range task.Results {
			if result.ID == id {
				copy := *result
				return &copy, nil
			}
		}
	}
	return nil, ErrPelicanTestNotFound
}
func (r *pelicanBackgroundRepo) StartTask(_ context.Context, id string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[id].Status = "running"
	r.tasks[id].StartedAt = &now
	return nil
}
func (r *pelicanBackgroundRepo) SaveResult(_ context.Context, item *PelicanTestResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[item.TaskID]
	for i, row := range task.Results {
		if row.ID == item.ID {
			copy := *item
			task.Results[i] = &copy
		}
	}
	task.Completed = 0
	for _, row := range task.Results {
		if row.Status != "pending" && row.Status != "running" {
			task.Completed++
		}
	}
	return nil
}
func (r *pelicanBackgroundRepo) FinishTask(_ context.Context, id, status, message string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[id]
	task.Status, task.Error, task.FinishedAt = status, message, &now
	return nil
}
func (r *pelicanBackgroundRepo) MarkInterrupted(_ context.Context, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, task := range r.tasks {
		if task.Status == "pending" || task.Status == "running" {
			task.Status, task.Error = "incomplete", pelicanInterruptedMessage
			for _, row := range task.Results {
				if row.Status == "pending" || row.Status == "running" {
					row.Status, row.Error, row.FinishedAt = "incomplete", pelicanInterruptedMessage, &now
				}
			}
		}
	}
	return nil
}
func (r *pelicanBackgroundRepo) CleanupExpired(context.Context, time.Time) error { return nil }
func (r *pelicanBackgroundRepo) GetTaskSnapshot(_ context.Context, id string) (*PelicanTaskSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, task := range r.tasks {
		if task.ID == id || task.ClientTaskID == id {
			if task.DeletedAt != nil {
				return nil, ErrPelicanTestDeleted
			}
			copy := *task
			copy.Results = nil
			counts := map[string]int{}
			for _, row := range task.Results {
				counts[row.Status]++
				if row.Status == "complete" && row.HTML == "" {
					counts["no_preview"]++
				}
			}
			return &PelicanTaskSnapshot{PelicanTestTask: &copy, Counts: counts}, nil
		}
	}
	return nil, ErrPelicanTestNotFound
}

func TestPelicanBackgroundDeletedSubmissionNeverRegenerates(t *testing.T) {
	var calls atomic.Int32
	runner, repo, catalog := newPelicanBackgroundFixture(t, func(context.Context, int64, string, string) (*PelicanResult, error) {
		calls.Add(1)
		return &PelicanResult{RawAnswer: "saved"}, nil
	})
	request := PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "gpt-6.1-sol"}}}
	task, err := runner.StartBackground(context.Background(), 1, request, catalog)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		snapshot, readErr := runner.TaskSnapshot(context.Background(), task.ID)
		return readErr == nil && snapshot.Status != "pending" && snapshot.Status != "running"
	}, time.Second, time.Millisecond)
	repo.mu.Lock()
	now := time.Now()
	repo.tasks[task.ID].DeletedAt = &now
	repo.mu.Unlock()
	_, err = runner.StartBackground(context.Background(), 1, request, nil)
	require.ErrorIs(t, err, ErrPelicanTestDeleted, "a replay must fail before consulting current capabilities")
	require.EqualValues(t, 1, calls.Load())
}
func (r *pelicanBackgroundRepo) ListTaskResults(_ context.Context, id string, filter PelicanResultFilter) (*PelicanResultPage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	page := &PelicanResultPage{Page: 1, PageSize: 50, Items: []*PelicanResultSummary{}}
	for _, row := range r.tasks[id].Results {
		page.Items = append(page.Items, pelicanResultSummary(row, ""))
	}
	page.Total = int64(len(page.Items))
	return page, nil
}

func newPelicanBackgroundFixture(t *testing.T, generate func(context.Context, int64, string, string) (*PelicanResult, error)) (*PelicanTestRunner, *pelicanBackgroundRepo, *PelicanTestCatalog) {
	t.Helper()
	repo := &pelicanBackgroundRepo{tasks: map[string]*PelicanTestTask{}}
	runner := newPelicanTestRunner(repo, nil, nil)
	runner.generator = pelicanFakeGenerator{generate: generate}
	t.Cleanup(runner.Stop)
	catalog := &PelicanTestCatalog{now: time.Now, accounts: &pelicanTestCatalogReaderStub{accounts: []*Account{pelicanFixtureAccount(1)}}}
	return runner, repo, catalog
}

func TestPelicanBackgroundSurvivesCallerAndReplaysOnlySnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	runner, repo, catalog := newPelicanBackgroundFixture(t, func(ctx context.Context, _ int64, _ string, effort string) (*PelicanResult, error) {
		require.Empty(t, effort)
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &PelicanResult{Status: "complete", RawAnswer: "<svg><text>saved work</text></svg>", RawResponse: "complete raw response"}, nil
	})
	caller, cancel := context.WithCancel(context.Background())
	request := PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}}}
	task, err := runner.StartBackground(caller, 7, request, catalog)
	require.NoError(t, err)
	<-entered
	cancel()
	replay, err := runner.StartBackground(context.Background(), 7, request, catalog)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, task.ID, replay.ID)
	require.EqualValues(t, 1, calls.Load())
	request.GenerationTimeoutSeconds = 900
	_, err = runner.StartBackground(context.Background(), 7, request, catalog)
	require.ErrorIs(t, err, ErrPelicanTestReplayConflict)
	page, err := runner.TaskResults(context.Background(), task.ID, PelicanResultFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "raw_response")
	require.NotContains(t, string(raw), "raw_answer")
	close(release)
	require.Eventually(t, func() bool {
		snapshot, _ := runner.TaskSnapshot(context.Background(), task.ID)
		return snapshot.Status == "complete"
	}, time.Second, time.Millisecond)
	result, err := runner.TaskResult(context.Background(), task.ID, page.Items[0].ID)
	require.NoError(t, err)
	require.Equal(t, "complete raw response", result.RawResponse)
	require.Contains(t, result.HTML, "saved work")
	repo.mu.Lock()
	require.Zero(t, repo.fullReads)
	repo.mu.Unlock()
}

func TestPelicanBackgroundExplicitCancelAndShutdownInterrupt(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit_cancel", true: "shutdown"}[shutdown], func(t *testing.T) {
			entered := make(chan struct{})
			runner, _, catalog := newPelicanBackgroundFixture(t, func(ctx context.Context, _ int64, _ string, _ string) (*PelicanResult, error) {
				close(entered)
				<-ctx.Done()
				return &PelicanResult{RawAnswer: "partial work", RawResponse: "partial response"}, ctx.Err()
			})
			task, err := runner.StartBackground(context.Background(), 7, PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}}}, catalog)
			require.NoError(t, err)
			<-entered
			expected := "cancelled"
			if shutdown {
				runner.Stop()
				expected = "incomplete"
			} else {
				_, err = runner.CancelBackground(context.Background(), task.ID)
				require.NoError(t, err)
			}
			require.Eventually(t, func() bool {
				snapshot, _ := runner.TaskSnapshot(context.Background(), task.ID)
				return snapshot.Status == expected
			}, time.Second, time.Millisecond)
			page, err := runner.TaskResults(context.Background(), task.ID, PelicanResultFilter{})
			require.NoError(t, err)
			result, err := runner.TaskResult(context.Background(), task.ID, page.Items[0].ID)
			require.NoError(t, err)
			require.Equal(t, "partial work", result.RawAnswer)
			if shutdown {
				require.Contains(t, result.Error, pelicanInterruptedMessage)
			}
		})
	}
}

func TestPelicanBackgroundRejectsDuplicateAndUnsupportedBeforeCreating(t *testing.T) {
	runner, repo, catalog := newPelicanBackgroundFixture(t, func(context.Context, int64, string, string) (*PelicanResult, error) {
		t.Error("must not generate")
		return nil, errors.New("unexpected")
	})
	for _, targets := range [][]PelicanTestTarget{
		{{AccountID: 1, ModelID: "gpt-6-astra", Effort: "low"}, {AccountID: 1, ModelID: "gpt-6-astra", Effort: "high"}},
		{{AccountID: 1, ModelID: "codex-auto-review"}},
	} {
		_, err := runner.StartBackground(context.Background(), 7, PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: targets}, catalog)
		require.ErrorIs(t, err, ErrPelicanTestInvalidRequest)
	}
	require.Empty(t, repo.tasks)
}
