//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type pelicanMemoryTestRepo struct {
	mu          sync.Mutex
	tasks       map[string]*PelicanTestTask
	clients     map[string]string
	resultByID  map[string]*PelicanTestResult
	resultReads int
	initialized int
	onSave      func(*PelicanTestResult) error
}

func newPelicanMemoryTestRepo() *pelicanMemoryTestRepo {
	return &pelicanMemoryTestRepo{tasks: map[string]*PelicanTestTask{}, clients: map[string]string{}, resultByID: map[string]*PelicanTestResult{}}
}

func (r *pelicanMemoryTestRepo) Create(_ context.Context, task *PelicanTestTask) (*PelicanTestTask, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, found := r.clients[task.ClientTaskID]; found {
		stored := r.tasks[id]
		if stored.RequestHash != task.RequestHash {
			return nil, true, ErrPelicanTestReplayConflict
		}
		return stored, true, nil
	}
	r.tasks[task.ID], r.clients[task.ClientTaskID] = task, task.ID
	for _, result := range task.Results {
		r.resultByID[result.ID] = result
	}
	return task, false, nil
}

func (r *pelicanMemoryTestRepo) Get(_ context.Context, id string) (*PelicanTestTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task := r.tasks[id]; task != nil {
		return task, nil
	}
	return nil, ErrPelicanTestNotFound
}

func (r *pelicanMemoryTestRepo) GetResult(_ context.Context, id string) (*PelicanTestResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resultReads++
	if result := r.resultByID[id]; result != nil {
		return result, nil
	}
	return nil, ErrPelicanTestNotFound
}

func (r *pelicanMemoryTestRepo) List(context.Context, int, int) (*PelicanTestList, error) {
	return &PelicanTestList{}, nil
}
func (r *pelicanMemoryTestRepo) StartTask(context.Context, string, time.Time) error {
	return nil
}
func (r *pelicanMemoryTestRepo) SaveResult(_ context.Context, result *PelicanTestResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.onSave != nil {
		return r.onSave(result)
	}
	return nil
}
func (r *pelicanMemoryTestRepo) FinishTask(context.Context, string, string, string, time.Time) error {
	return nil
}
func (r *pelicanMemoryTestRepo) MarkInterrupted(context.Context, time.Time) error {
	r.mu.Lock()
	r.initialized++
	r.mu.Unlock()
	return nil
}
func (r *pelicanMemoryTestRepo) CleanupExpired(context.Context, time.Time) error {
	return nil
}

type pelicanFakeGenerator struct {
	generate func(context.Context, int64, string, string) (*PelicanResult, error)
}

func (g pelicanFakeGenerator) GeneratePelican(ctx context.Context, id int64, model, effort string) (*PelicanResult, error) {
	return g.generate(ctx, id, model, effort)
}

func pelicanV7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func TestPelicanRunnerPersistsBeforeDispatchDedupsAndReplays(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	var calls int
	gen := pelicanFakeGenerator{generate: func(_ context.Context, account int64, model, effort string) (*PelicanResult, error) {
		calls++
		require.Len(t, repo.tasks, 1)
		require.Equal(t, int64(1), account)
		require.Equal(t, "gpt-6-astra", model)
		require.Empty(t, effort, "the normal model default is preserved")
		return &PelicanResult{Status: "complete", RawAnswer: "```html\n<html><script>animate()</script></html>\n```", ModelID: "gpt-6-astra-reported", Effort: effort}, nil
	}}
	runner := newPelicanTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	request := PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}, {AccountID: 1, ModelID: "gpt-6-astra"}}}
	task, replayed, err := runner.Create(context.Background(), 7, request)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, PelicanPrompt, task.Prompt)
	require.Equal(t, 1, task.Total)
	require.Equal(t, PelicanTestTTL, task.ExpiresAt.Sub(task.CreatedAt))
	require.NoError(t, runner.Run(context.Background(), task, nil))
	require.Equal(t, 1, calls)
	require.Equal(t, "complete", task.Status)
	require.Equal(t, "gpt-6-astra", task.Results[0].ModelID)
	require.Equal(t, "gpt-6-astra-reported", task.Results[0].UpstreamModel)
	replayedTask, replayed, err := runner.Create(context.Background(), 7, request)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, task.ID, replayedTask.ID)
	require.NoError(t, runner.Run(context.Background(), replayedTask, nil))
	require.Equal(t, 1, calls, "reading an existing task must never regenerate")
	request.Targets[0].Effort = "medium"
	_, _, err = runner.Create(context.Background(), 7, request)
	require.ErrorIs(t, err, ErrPelicanTestReplayConflict)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, repo.initialized)
}

func TestPelicanRunnerRejectsExpiredV7AfterRowsAreGone(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.MustParse(pelicanV7(t))
	millis := uint64(now.Add(-PelicanTestTTL).UnixMilli())
	for i := 5; i >= 0; i-- {
		id[i] = byte(millis)
		millis >>= 8
	}
	request := PelicanTestRequest{ClientTaskID: id.String(), Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}}}
	_, err := normalizePelicanTestRequest(request, now)
	require.ErrorIs(t, err, ErrPelicanTestExpired)
	repo := newPelicanMemoryTestRepo()
	runner := newPelicanTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	runner.now = func() time.Time { return now }
	_, _, err = runner.Create(context.Background(), 1, request)
	require.ErrorIs(t, err, ErrPelicanTestExpired)
	require.Empty(t, repo.tasks, "expired UUIDs must be rejected before creating or dispatching anything, including after cleanup")
	require.Zero(t, repo.initialized)
	millis = uint64(now.Add(time.Millisecond).UnixMilli())
	for i := 5; i >= 0; i-- {
		id[i] = byte(millis)
		millis >>= 8
	}
	request.ClientTaskID = id.String()
	_, err = normalizePelicanTestRequest(request, now)
	require.ErrorIs(t, err, ErrPelicanTestInvalidRequest)
	request.ClientTaskID = uuid.NewString()
	_, err = normalizePelicanTestRequest(request, now)
	require.ErrorIs(t, err, ErrPelicanTestInvalidRequest)
}

func TestPelicanRunnerSharesTenSlotsAndSerializesAccountsAcrossBatches(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	var mu sync.Mutex
	active, maxActive, calls := 0, 0, 0
	accountActive := map[int64]int{}
	maxAccountActive := map[int64]int{}
	started := make(chan struct{}, 16)
	gate := make(chan struct{})
	gen := pelicanFakeGenerator{generate: func(ctx context.Context, id int64, _, effort string) (*PelicanResult, error) {
		ctx, release, acquireErr := AcquirePelicanExecution(ctx, id)
		if acquireErr != nil {
			return nil, acquireErr
		}
		defer release()
		ctx = BeginPelicanGeneration(ctx)
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
		return &PelicanResult{Status: "skipped", Effort: effort, Error: "selected account unavailable"}, nil
	}}
	runner := newPelicanTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	firstTargets, secondTargets := make([]PelicanTestTarget, 7), make([]PelicanTestTarget, 7)
	for i := range firstTargets {
		firstTargets[i] = PelicanTestTarget{AccountID: int64(i + 1), ModelID: "gpt-6-astra"}
		secondTargets[i] = PelicanTestTarget{AccountID: int64(i + 7), ModelID: "gpt-6.1-sol"}
	}
	secondTargets[0].AccountID = 1
	first, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: firstTargets})
	require.NoError(t, err)
	second, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: secondTargets})
	require.NoError(t, err)
	done := make(chan error, 2)
	go func() { done <- runner.Run(context.Background(), first, nil) }()
	go func() { done <- runner.Run(context.Background(), second, nil) }()
	for i := 0; i < PelicanExecutionConcurrency; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("ten dispatches did not start")
		}
	}
	mu.Lock()
	activeBeforeRelease := active
	mu.Unlock()
	require.Equal(t, PelicanExecutionConcurrency, activeBeforeRelease)
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
	require.Equal(t, 14, calls)
	require.Equal(t, PelicanExecutionConcurrency, maxActive)
	for _, max := range maxAccountActive {
		require.Equal(t, 1, max)
	}
	require.Equal(t, "skipped", first.Status)
	require.Equal(t, "skipped", second.Status)
}

func TestPelicanRunnerCancellationStopsQueuedAndInflightAndRetainsRawOutput(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	started := make(chan struct{}, PelicanExecutionConcurrency+1)
	gen := pelicanFakeGenerator{generate: func(ctx context.Context, id int64, _, _ string) (*PelicanResult, error) {
		ctx, release, acquireErr := AcquirePelicanExecution(ctx, id)
		if acquireErr != nil {
			return nil, acquireErr
		}
		defer release()
		ctx = BeginPelicanGeneration(ctx)

		started <- struct{}{}
		<-ctx.Done()
		return &PelicanResult{Status: "incomplete", RawAnswer: "<svg>partial", RawResponse: "data: original stream\x00tail", Error: "full upstream failure detail"}, ctx.Err()
	}}
	runner := newPelicanTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	targets := make([]PelicanTestTarget, PelicanExecutionConcurrency+1)
	for i := range targets {
		targets[i] = PelicanTestTarget{AccountID: int64(i + 1), ModelID: "gpt-6-astra"}
	}
	task, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: targets})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, task, nil) }()
	for i := 0; i < PelicanExecutionConcurrency; i++ {
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
	require.Equal(t, PelicanExecutionConcurrency, withPartial)
}

func TestPelicanRequestBudgetBoundsDefaultsAndServerMode(t *testing.T) {
	base := PelicanTestRequest{ClientTaskID: pelicanV7(t),
		Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "local-model"}}}
	for _, seconds := range []int{0, 60, 600, 1800} {
		request := base
		request.GenerationTimeoutSeconds = seconds
		normalized, err := normalizePelicanTestRequest(request, time.Now().UTC())
		require.NoError(t, err)
		if seconds == 0 {
			require.Equal(t, PelicanDefaultGenerationTimeoutSeconds, normalized.GenerationTimeoutSeconds)
		} else {
			require.Equal(t, seconds, normalized.GenerationTimeoutSeconds)
		}
		require.Empty(t, normalized.Targets[0].Effort, "models without high must retain their normal default")
	}
	for _, seconds := range []int{-1, 1, 59, 1801} {
		request := base
		request.GenerationTimeoutSeconds = seconds
		_, err := normalizePelicanTestRequest(request, time.Now().UTC())
		require.ErrorIs(t, err, ErrPelicanTestInvalidRequest)
	}
	var decoded PelicanTestRequest
	require.NoError(t, json.Unmarshal([]byte(`{"standalone":true,"client_task_id":"ignored","generation_timeout_seconds":600}`), &decoded))
	require.Empty(t, decoded.Targets, "unknown execution selectors are ignored")
}

func TestPelicanRunnerReplayIncludesBudgetAndExecutionMode(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	runner := newPelicanTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	request := PelicanTestRequest{ClientTaskID: pelicanV7(t),
		Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "gpt-6-astra", Effort: "high"}}}
	task, replayed, err := runner.Create(context.Background(), 1, request)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, PelicanExecutionModeAccount, task.ExecutionMode)
	require.Equal(t, PelicanDefaultGenerationTimeoutSeconds, task.GenerationTimeoutSeconds)
	require.Equal(t, "2047588fead6a9ed5dfe9cf11020d7120f45e42253b43113dabf28b2435ed566", task.RequestHash, "preserve the previous account-mode request hash")
	request.GenerationTimeoutSeconds = 60
	_, replayed, err = runner.Create(context.Background(), 1, request)
	require.True(t, replayed)
	require.ErrorIs(t, err, ErrPelicanTestReplayConflict)
	request.GenerationTimeoutSeconds = 0
	_, replayed, err = runner.Create(context.Background(), 1, request)
	require.True(t, replayed)
	require.NoError(t, err, "the unchanged account request hash must remain compatible")
}

func TestPelicanRunnerGenerationClockStartsAfterPreparationAndRecordsActualInvocation(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	runner := newPelicanTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	now := time.Now().UTC().Add(time.Second)
	runner.now = func() time.Time { return now }
	budgetStarts := 0
	runner.generationContext = func(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
		budgetStarts++
		require.Equal(t, 10*time.Minute, budget)
		return context.WithCancel(ctx)
	}
	runner.generator = pelicanFakeGenerator{generate: func(ctx context.Context, id int64, _, _ string) (*PelicanResult, error) {
		require.Zero(t, budgetStarts, "neither queue nor preparation may start generation's timer")
		now = now.Add(2 * time.Minute)
		requestCtx, release, err := AcquirePelicanExecution(ctx, id)
		require.NoError(t, err)
		defer release()
		finishBusinessWait := BeginPelicanQueueWait(requestCtx)
		now = now.Add(10 * time.Second)
		finishBusinessWait()
		finishBusinessWait()
		requestCtx = BeginPelicanGeneration(requestCtx)
		require.Equal(t, 1, budgetStarts)
		RecordPelicanInvocation(requestCtx, PelicanInvocation{Platform: "openai", Model: "gpt-6.1-sol", Effort: "high",
			Endpoint: "https://upstream.test/backend-api/codex/responses", Protocol: "responses", Transport: "http"})
		now = now.Add(30 * time.Second)
		_ = BeginPelicanGeneration(requestCtx)
		require.Equal(t, 1, budgetStarts, "same-account compatibility retries use the original generation clock")
		now = now.Add(5 * time.Second)
		return &PelicanResult{Status: "complete", ModelID: "response-reported-model", RawAnswer: "<svg>original</svg>", RawResponse: "original response\x00tail"}, nil
	}}
	task, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t),
		Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "mapped-alias"}}})
	require.NoError(t, err)
	var phases []string
	require.NoError(t, runner.Run(context.Background(), task, func(event PelicanTestEvent) {
		if event.Type == "result_phase" {
			phases = append(phases, event.Phase)
		}
	}))
	result := task.Results[0]
	require.Equal(t, []string{"queued", "preparing", "generating"}, phases)
	require.Equal(t, "complete", result.Status)
	require.Equal(t, "mapped-alias", result.ModelID)
	require.Equal(t, "gpt-6.1-sol", result.UpstreamModel)
	require.Equal(t, "openai", result.Platform)
	require.Equal(t, "high", result.Effort)
	require.Equal(t, "responses", result.ActualProtocol)
	require.Equal(t, "http", result.ActualTransport)
	require.Equal(t, "https://upstream.test/backend-api/codex/responses", result.ActualEndpoint)
	require.False(t, result.BorrowApplied)
	require.Equal(t, int64(10000), result.QueueDurationMS)
	require.Equal(t, int64(120000), result.PreparationDurationMS)
	require.Equal(t, int64(35000), result.GenerationDurationMS)
	require.Equal(t, int64(165000), result.DurationMS)
	require.NotNil(t, result.GenerationStartedAt)
	require.Equal(t, "original response\x00tail", result.RawResponse)
}

func TestPelicanExecutionIsScopedAndRejectsIncorrectCrossAccountNesting(t *testing.T) {
	ordinary := context.WithValue(context.Background(), struct{}{}, "ordinary context")
	ctx, release, err := AcquirePelicanExecution(ordinary, 1)
	require.NoError(t, err)
	require.Equal(t, ordinary, ctx)
	release()
	require.Equal(t, ordinary, BeginPelicanGeneration(ordinary))
	BeginPelicanPreparation(ordinary)
	RecordPelicanInvocation(ordinary, PelicanInvocation{Model: "ignored"})
	BeginPelicanQueueWait(ordinary)()
	coordinator := newPelicanExecutionCoordinator(1)
	state := newPelicanExecutionState(context.Background(), coordinator, 10*time.Minute, time.Now)
	ctx = context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
	leasing, release, err := AcquirePelicanExecution(ctx, 1)
	require.NoError(t, err)
	_, nestedRelease, err := AcquirePelicanExecution(leasing, 1)
	require.NoError(t, err)
	nestedRelease()
	coordinator.mu.Lock()
	active := coordinator.active
	coordinator.mu.Unlock()
	require.Equal(t, 1, active)
	_, _, err = AcquirePelicanExecution(leasing, 2)
	require.ErrorContains(t, err, "preparation must finish")
	release()
	release()
	coordinator.mu.Lock()
	require.Zero(t, coordinator.active)
	require.Empty(t, coordinator.accounts)
	require.Empty(t, coordinator.waiting)
	coordinator.mu.Unlock()
	state.finish()
}

func TestPelicanRunnerGenerationTimeoutRetainsIncompleteOutput(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	runner := newPelicanTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	now := time.Now().UTC().Add(time.Second)
	runner.now = func() time.Time { return now }
	var timeout context.CancelCauseFunc
	runner.generationContext = func(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
		require.Equal(t, time.Minute, budget)
		budgetCtx, cancel := context.WithCancelCause(ctx)
		timeout = cancel
		return budgetCtx, func() { cancel(context.Canceled) }
	}
	runner.generator = pelicanFakeGenerator{generate: func(ctx context.Context, id int64, _, _ string) (*PelicanResult, error) {
		now = now.Add(90 * time.Second)
		requestCtx, release, err := AcquirePelicanExecution(ctx, id)
		require.NoError(t, err)
		defer release()
		requestCtx = BeginPelicanGeneration(requestCtx)
		now = now.Add(time.Minute)
		timeout(context.DeadlineExceeded)
		<-requestCtx.Done()
		<-ctx.Done()
		require.ErrorIs(t, context.Cause(ctx), context.DeadlineExceeded, "the entire generator, including retries and later preparation, inherits the budget")
		_, _, err = AcquirePelicanExecution(ctx, id+1)
		require.ErrorIs(t, err, context.Canceled, "budget expiry cannot start another probe or compatibility retry")
		return &PelicanResult{Status: "incomplete", RawAnswer: "<svg>partial", RawResponse: "original partial\x00tail"}, requestCtx.Err()
	}}
	task, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t), GenerationTimeoutSeconds: 60,
		Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "local"}}})
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), task, nil))
	require.Equal(t, "incomplete", task.Status)
	result := task.Results[0]
	require.Equal(t, "incomplete", result.Status)
	require.Contains(t, result.Error, "generation budget exhausted")
	require.Equal(t, "<svg>partial", result.RawAnswer)
	require.Equal(t, "original partial\x00tail", result.RawResponse)
	require.Equal(t, int64(90000), result.PreparationDurationMS)
	require.Equal(t, int64(60000), result.GenerationDurationMS)
	require.NotEmpty(t, result.PreviewUnavailable)
}

func TestPelicanRunnerDoesNotDispatchWhenRunningRecordCannotPersist(t *testing.T) {
	repo := newPelicanMemoryTestRepo()
	repo.onSave = func(result *PelicanTestResult) error {
		if result.Status == "running" {
			return errors.New("database write failed before dispatch")
		}
		return nil
	}
	calls := 0
	gen := pelicanFakeGenerator{generate: func(context.Context, int64, string, string) (*PelicanResult, error) {
		calls++
		return nil, nil
	}}
	runner := newPelicanTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	task, _, err := runner.Create(context.Background(), 1, PelicanTestRequest{ClientTaskID: pelicanV7(t), Targets: []PelicanTestTarget{{AccountID: 1, ModelID: "gpt-6-astra"}}})
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), task, nil))
	require.Zero(t, calls)
	require.Equal(t, "incomplete", task.Status)
	require.Equal(t, "database write failed before dispatch", task.Results[0].Error)
}

func TestPelicanRunnerHistoricalTasksAreReadOnly(t *testing.T) {
	calls := 0
	runner := newPelicanTestRunner(newPelicanMemoryTestRepo(), pelicanFakeGenerator{generate: func(context.Context, int64, string, string) (*PelicanResult, error) {
		calls++
		return nil, nil
	}}, nil)
	t.Cleanup(runner.Stop)
	task := &PelicanTestTask{ID: "historical", ExecutionMode: "legacy_cache", Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}
	require.ErrorIs(t, runner.Run(context.Background(), task, nil), ErrPelicanTestInvalidRequest)
	require.Zero(t, calls)
	require.Equal(t, "pending", task.Status, "historical data is read-only")
}
