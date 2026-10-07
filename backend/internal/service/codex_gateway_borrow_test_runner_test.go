//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type codexGatewayBorrowMemoryTestRepo struct {
	mu          sync.Mutex
	tasks       map[string]*CodexGatewayBorrowTestTask
	clients     map[string]string
	resultByID  map[string]*CodexGatewayBorrowTestResult
	resultReads int
	initialized int
	onSave      func(*CodexGatewayBorrowTestResult) error
}

func newCodexGatewayBorrowMemoryTestRepo() *codexGatewayBorrowMemoryTestRepo {
	return &codexGatewayBorrowMemoryTestRepo{tasks: map[string]*CodexGatewayBorrowTestTask{}, clients: map[string]string{}, resultByID: map[string]*CodexGatewayBorrowTestResult{}}
}

func (r *codexGatewayBorrowMemoryTestRepo) Create(_ context.Context, task *CodexGatewayBorrowTestTask) (*CodexGatewayBorrowTestTask, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, found := r.clients[task.ClientTaskID]; found {
		stored := r.tasks[id]
		if stored.RequestHash != task.RequestHash {
			return nil, true, ErrCodexGatewayBorrowTestReplayConflict
		}
		return stored, true, nil
	}
	r.tasks[task.ID], r.clients[task.ClientTaskID] = task, task.ID
	for _, result := range task.Results {
		r.resultByID[result.ID] = result
	}
	return task, false, nil
}

func (r *codexGatewayBorrowMemoryTestRepo) Get(_ context.Context, id string) (*CodexGatewayBorrowTestTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task := r.tasks[id]; task != nil {
		return task, nil
	}
	return nil, ErrCodexGatewayBorrowTestNotFound
}

func (r *codexGatewayBorrowMemoryTestRepo) GetResult(_ context.Context, id string) (*CodexGatewayBorrowTestResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resultReads++
	if result := r.resultByID[id]; result != nil {
		return result, nil
	}
	return nil, ErrCodexGatewayBorrowTestNotFound
}

func (r *codexGatewayBorrowMemoryTestRepo) List(context.Context, int, int) (*CodexGatewayBorrowTestList, error) {
	return &CodexGatewayBorrowTestList{}, nil
}
func (r *codexGatewayBorrowMemoryTestRepo) StartTask(context.Context, string, time.Time) error {
	return nil
}
func (r *codexGatewayBorrowMemoryTestRepo) SaveResult(_ context.Context, result *CodexGatewayBorrowTestResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.onSave != nil {
		return r.onSave(result)
	}
	return nil
}
func (r *codexGatewayBorrowMemoryTestRepo) FinishTask(context.Context, string, string, string, time.Time) error {
	return nil
}
func (r *codexGatewayBorrowMemoryTestRepo) MarkInterrupted(context.Context, time.Time) error {
	r.mu.Lock()
	r.initialized++
	r.mu.Unlock()
	return nil
}
func (r *codexGatewayBorrowMemoryTestRepo) CleanupExpired(context.Context, time.Time) error {
	return nil
}

type codexGatewayBorrowFakeGenerator struct {
	generate func(context.Context, int64, string, string) (*CodexGatewayBorrowPelicanResult, error)
}

func (g codexGatewayBorrowFakeGenerator) GeneratePelican(ctx context.Context, id int64, model, effort string) (*CodexGatewayBorrowPelicanResult, error) {
	return g.generate(ctx, id, model, effort)
}

func codexGatewayBorrowV7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func TestCodexGatewayBorrowRunnerPersistsBeforeDispatchDedupsAndReplays(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	var calls int
	gen := codexGatewayBorrowFakeGenerator{generate: func(_ context.Context, account int64, model, effort string) (*CodexGatewayBorrowPelicanResult, error) {
		calls++
		require.Len(t, repo.tasks, 1)
		require.Equal(t, int64(1), account)
		require.Equal(t, "gpt-6-astra", model)
		require.Equal(t, "high", effort)
		return &CodexGatewayBorrowPelicanResult{Status: "complete", RawAnswer: "```html\n<html><script>animate()</script></html>\n```", ModelID: "gpt-6-astra-reported", Effort: effort}, nil
	}}
	runner := newCodexGatewayBorrowTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	request := CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}, {AccountID: 1, ModelID: "gpt-6-astra"}}}
	task, replayed, err := runner.Create(context.Background(), 7, request)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, CodexGatewayBorrowPelicanPrompt, task.Prompt)
	require.Equal(t, 1, task.Total)
	require.Equal(t, CodexGatewayBorrowTestTTL, task.ExpiresAt.Sub(task.CreatedAt))
	require.NoError(t, runner.Run(context.Background(), task, nil))
	require.Equal(t, 1, calls)
	require.Equal(t, "complete", task.Status)
	require.Equal(t, "gpt-6-astra", task.Results[0].ModelID)
	require.Equal(t, "gpt-6-astra-reported", task.Results[0].UpstreamModel)
	replayedTask, replayed, err := runner.Create(context.Background(), 7, request)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, task.ID, replayedTask.ID)
	require.Equal(t, 1, calls, "reading an existing task must never regenerate")
	request.Targets[0].Effort = "medium"
	_, _, err = runner.Create(context.Background(), 7, request)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowTestReplayConflict)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, repo.initialized)
}

func TestCodexGatewayBorrowRunnerRejectsExpiredV7AfterRowsAreGone(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.MustParse(codexGatewayBorrowV7(t))
	millis := uint64(now.Add(-CodexGatewayBorrowTestTTL).UnixMilli())
	for i := 5; i >= 0; i-- {
		id[i] = byte(millis)
		millis >>= 8
	}
	request := CodexGatewayBorrowTestRequest{ClientTaskID: id.String(), Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}}}
	_, err := normalizeCodexGatewayBorrowTestRequest(request, now)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowTestExpired)
	repo := newCodexGatewayBorrowMemoryTestRepo()
	runner := newCodexGatewayBorrowTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	runner.now = func() time.Time { return now }
	_, _, err = runner.Create(context.Background(), 1, request)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowTestExpired)
	require.Empty(t, repo.tasks, "expired UUIDs must be rejected before creating or dispatching anything, including after cleanup")
	require.Zero(t, repo.initialized)
	millis = uint64(now.Add(time.Millisecond).UnixMilli())
	for i := 5; i >= 0; i-- {
		id[i] = byte(millis)
		millis >>= 8
	}
	request.ClientTaskID = id.String()
	_, err = normalizeCodexGatewayBorrowTestRequest(request, now)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowTestInvalidRequest)
	request.ClientTaskID = uuid.NewString()
	_, err = normalizeCodexGatewayBorrowTestRequest(request, now)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowTestInvalidRequest)
}

func TestCodexGatewayBorrowRunnerSharesThreeSlotsAndSerializesAccountsAcrossBatches(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	var mu sync.Mutex
	active, maxActive, calls := 0, 0, 0
	accountActive := map[int64]int{}
	maxAccountActive := map[int64]int{}
	started := make(chan struct{}, 16)
	gate := make(chan struct{})
	gen := codexGatewayBorrowFakeGenerator{generate: func(ctx context.Context, id int64, _, effort string) (*CodexGatewayBorrowPelicanResult, error) {
		mu.Lock()
		calls++
		active++
		accountActive[id]++
		if active > maxActive {
			maxActive = active
		}
		if accountActive[id] > maxAccountActive[id] {
			maxAccountActive[id] = accountActive[id]
		}
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		mu.Lock()
		active--
		accountActive[id]--
		mu.Unlock()
		return &CodexGatewayBorrowPelicanResult{Status: "skipped", Effort: effort, Error: "cached qualification unavailable"}, nil
	}}
	runner := newCodexGatewayBorrowTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	first, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}, {AccountID: 2, ModelID: "gpt-6-astra"}, {AccountID: 3, ModelID: "gpt-6-astra"}}})
	require.NoError(t, err)
	second, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "gpt-6.1-sol"}, {AccountID: 4, ModelID: "gpt-6-astra"}, {AccountID: 5, ModelID: "gpt-6-astra"}}})
	require.NoError(t, err)
	done := make(chan error, 2)
	go func() { done <- runner.Run(context.Background(), first, nil) }()
	go func() { done <- runner.Run(context.Background(), second, nil) }()
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("three dispatches did not start")
		}
	}
	mu.Lock()
	activeBeforeRelease := active
	mu.Unlock()
	require.Equal(t, 3, activeBeforeRelease)
	close(gate)
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("batch did not finish")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 6, calls)
	require.Equal(t, 3, maxActive)
	for _, max := range maxAccountActive {
		require.Equal(t, 1, max)
	}
	require.Equal(t, "skipped", first.Status)
	require.Equal(t, "skipped", second.Status)
}

func TestCodexGatewayBorrowRunnerCancellationStopsQueuedAndInflightAndRetainsRawOutput(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	started := make(chan struct{}, 4)
	gen := codexGatewayBorrowFakeGenerator{generate: func(ctx context.Context, _ int64, _, _ string) (*CodexGatewayBorrowPelicanResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return &CodexGatewayBorrowPelicanResult{Status: "incomplete", RawAnswer: "<svg>partial", RawResponse: "data: original stream\x00tail", Error: "full upstream failure detail"}, ctx.Err()
	}}
	runner := newCodexGatewayBorrowTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	targets := make([]CodexGatewayBorrowTestTarget, 4)
	for i := range targets {
		targets[i] = CodexGatewayBorrowTestTarget{AccountID: int64(i + 1), ModelID: "gpt-6-astra"}
	}
	task, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: targets})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, task, nil) }()
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("dispatch did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled test did not finish")
	}
	require.Empty(t, started, "queued target must not dispatch after cancellation")
	require.Equal(t, "cancelled", task.Status)
	withPartial := 0
	for _, result := range task.Results {
		require.Equal(t, "cancelled", result.Status)
		require.NotEmpty(t, result.PreviewUnavailable)
		if result.RawAnswer != "" {
			withPartial++
			require.Equal(t, "data: original stream\x00tail", result.RawResponse)
			require.Contains(t, result.Error, "full upstream failure detail")
		}
	}
	require.Equal(t, 3, withPartial)
}

func TestCodexGatewayBorrowRunnerDoesNotDispatchWhenRunningRecordCannotPersist(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	repo.onSave = func(result *CodexGatewayBorrowTestResult) error {
		if result.Status == "running" {
			return errors.New("database write failed before dispatch")
		}
		return nil
	}
	calls := 0
	gen := codexGatewayBorrowFakeGenerator{generate: func(context.Context, int64, string, string) (*CodexGatewayBorrowPelicanResult, error) {
		calls++
		return nil, nil
	}}
	runner := newCodexGatewayBorrowTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	task, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}}})
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), task, nil))
	require.Zero(t, calls)
	require.Equal(t, "incomplete", task.Status)
	require.Equal(t, "database write failed before dispatch", task.Results[0].Error)
}
