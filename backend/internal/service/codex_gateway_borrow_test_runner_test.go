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
	require.NoError(t, runner.Run(context.Background(), replayedTask, nil))
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

func TestCodexGatewayBorrowRunnerSharesTenSlotsAndSerializesAccountsAcrossBatches(t *testing.T) {
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
	firstTargets, secondTargets := make([]CodexGatewayBorrowTestTarget, 7), make([]CodexGatewayBorrowTestTarget, 7)
	for i := range firstTargets {
		firstTargets[i] = CodexGatewayBorrowTestTarget{AccountID: int64(i + 1), ModelID: "gpt-6-astra"}
		secondTargets[i] = CodexGatewayBorrowTestTarget{AccountID: int64(i + 7), ModelID: "gpt-6.1-sol"}
	}
	secondTargets[0].AccountID = 1
	first, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: firstTargets})
	require.NoError(t, err)
	second, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: secondTargets})
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

func TestCodexGatewayBorrowRunnerCancellationStopsQueuedAndInflightAndRetainsRawOutput(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	started := make(chan struct{}, PelicanExecutionConcurrency+1)
	gen := codexGatewayBorrowFakeGenerator{generate: func(ctx context.Context, _ int64, _, _ string) (*CodexGatewayBorrowPelicanResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return &CodexGatewayBorrowPelicanResult{Status: "incomplete", RawAnswer: "<svg>partial", RawResponse: "data: original stream\x00tail", Error: "full upstream failure detail"}, ctx.Err()
	}}
	runner := newCodexGatewayBorrowTestRunner(repo, gen, nil)
	t.Cleanup(runner.Stop)
	targets := make([]CodexGatewayBorrowTestTarget, PelicanExecutionConcurrency+1)
	for i := range targets {
		targets[i] = CodexGatewayBorrowTestTarget{AccountID: int64(i + 1), ModelID: "gpt-6-astra"}
	}
	task, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Targets: targets})
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
	base := CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Standalone: true,
		Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "local-model"}}}
	for _, seconds := range []int{0, 60, 600, 1800} {
		request := base
		request.GenerationTimeoutSeconds = seconds
		normalized, err := normalizeCodexGatewayBorrowTestRequest(request, time.Now().UTC())
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
		_, err := normalizeCodexGatewayBorrowTestRequest(request, time.Now().UTC())
		require.ErrorIs(t, err, ErrCodexGatewayBorrowTestInvalidRequest)
	}
	var decoded CodexGatewayBorrowTestRequest
	require.NoError(t, json.Unmarshal([]byte(`{"standalone":true,"client_task_id":"ignored","generation_timeout_seconds":600}`), &decoded))
	require.False(t, decoded.Standalone, "the legacy endpoint cannot be promoted to account mode by request JSON")
}

func TestPelicanRunnerReplayIncludesBudgetAndExecutionMode(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	runner := newCodexGatewayBorrowTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	request := CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Standalone: true,
		Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "gpt-6-astra", Effort: "high"}}}
	task, replayed, err := runner.Create(context.Background(), 1, request)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, PelicanExecutionModeAccount, task.ExecutionMode)
	require.Equal(t, PelicanDefaultGenerationTimeoutSeconds, task.GenerationTimeoutSeconds)
	request.GenerationTimeoutSeconds = 60
	_, replayed, err = runner.Create(context.Background(), 1, request)
	require.True(t, replayed)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowTestReplayConflict)
	request.GenerationTimeoutSeconds, request.Standalone = 0, false
	_, replayed, err = runner.Create(context.Background(), 1, request)
	require.True(t, replayed)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowTestReplayConflict)
}

func TestPelicanRunnerGenerationClockStartsAfterPreparationAndRecordsActualInvocation(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	runner := newCodexGatewayBorrowTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	now := time.Now().UTC().Add(time.Second)
	runner.now = func() time.Time { return now }
	budgetStarts := 0
	runner.generationContext = func(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
		budgetStarts++
		require.Equal(t, 10*time.Minute, budget)
		return context.WithCancel(ctx)
	}
	runner.SetStandaloneGenerator(codexGatewayBorrowFakeGenerator{generate: func(ctx context.Context, id int64, _, _ string) (*CodexGatewayBorrowPelicanResult, error) {
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
			Endpoint: "https://upstream.test/backend-api/codex/responses", Protocol: "responses", Transport: "http", BorrowApplied: true})
		now = now.Add(30 * time.Second)
		_ = BeginPelicanGeneration(requestCtx)
		require.Equal(t, 1, budgetStarts, "same-account compatibility retries use the original generation clock")
		now = now.Add(5 * time.Second)
		return &CodexGatewayBorrowPelicanResult{Status: "complete", ModelID: "response-reported-model", RawAnswer: "<svg>original</svg>", RawResponse: "original response\x00tail"}, nil
	}})
	task, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Standalone: true,
		Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "mapped-alias"}}})
	require.NoError(t, err)
	var phases []string
	require.NoError(t, runner.Run(context.Background(), task, func(event CodexGatewayBorrowTestEvent) {
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
	require.True(t, result.BorrowApplied)
	require.Equal(t, int64(10000), result.QueueDurationMS)
	require.Equal(t, int64(120000), result.PreparationDurationMS)
	require.Equal(t, int64(35000), result.GenerationDurationMS)
	require.Equal(t, int64(165000), result.DurationMS)
	require.NotNil(t, result.GenerationStartedAt)
	require.Equal(t, "original response\x00tail", result.RawResponse)
}

func TestPelicanRunnerNestedPreparationDoesNotReserveAllExecutionSlots(t *testing.T) {
	repo := newCodexGatewayBorrowMemoryTestRepo()
	runner := newCodexGatewayBorrowTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	preparing := make(chan struct{}, PelicanExecutionConcurrency+1)
	allowPreparation := make(chan struct{})
	var mu sync.Mutex
	active, maxActive, probes, generations := 0, 0, 0, 0
	accountActive := map[int64]int{}
	maxAccountActive := map[int64]int{}
	observe := func(ctx context.Context, id int64, generation bool) error {
		requestCtx, release, err := AcquirePelicanExecution(ctx, id)
		if err != nil {
			return err
		}
		defer release()
		// Sender wrappers for this exact account/request are reentrant and do
		// not take another global permit or wait on their own account lease.
		_, nestedRelease, err := AcquirePelicanExecution(requestCtx, id)
		if err != nil {
			return err
		}
		defer nestedRelease()
		mu.Lock()
		active++
		accountActive[id]++
		if active > maxActive {
			maxActive = active
		}
		if accountActive[id] > maxAccountActive[id] {
			maxAccountActive[id] = accountActive[id]
		}
		if generation {
			generations++
		} else {
			probes++
		}
		accountActive[id]--
		active--
		mu.Unlock()
		return nil
	}
	runner.SetStandaloneGenerator(codexGatewayBorrowFakeGenerator{generate: func(ctx context.Context, id int64, model, effort string) (*CodexGatewayBorrowPelicanResult, error) {
		preparing <- struct{}{}
		select {
		case <-allowPreparation:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Every target needs the same source; that source is also selected as
		// its own Pelican target. None of these outer preparations may retain
		// the final target's account/global lease while requesting the source.
		if err := observe(ctx, 1, false); err != nil {
			return nil, err
		}
		if err := observe(ctx, id, true); err != nil {
			return nil, err
		}
		return &CodexGatewayBorrowPelicanResult{Status: "complete", ModelID: model, Effort: effort, RawAnswer: "<svg>synthetic</svg>"}, nil
	}})
	targets := make([]CodexGatewayBorrowTestTarget, PelicanExecutionConcurrency+1)
	for i := range targets {
		targets[i] = CodexGatewayBorrowTestTarget{AccountID: int64(i + 1), ModelID: "local"}
	}
	task, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Standalone: true, Targets: targets})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, task, nil) }()
	for range targets {
		select {
		case <-preparing:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("outer preparations reserved execution permits before their nested source requests")
		}
	}
	close(allowPreparation)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("source/target preparation deadlocked with the selected source's own Pelican generation")
	}
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, len(targets), probes)
	require.Equal(t, len(targets), generations)
	require.LessOrEqual(t, maxActive, PelicanExecutionConcurrency)
	for _, maximum := range maxAccountActive {
		require.Equal(t, 1, maximum)
	}
	require.Equal(t, "complete", task.Status)
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
	repo := newCodexGatewayBorrowMemoryTestRepo()
	runner := newCodexGatewayBorrowTestRunner(repo, nil, nil)
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
	runner.SetStandaloneGenerator(codexGatewayBorrowFakeGenerator{generate: func(ctx context.Context, id int64, _, _ string) (*CodexGatewayBorrowPelicanResult, error) {
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
		return &CodexGatewayBorrowPelicanResult{Status: "incomplete", RawAnswer: "<svg>partial", RawResponse: "original partial\x00tail"}, requestCtx.Err()
	}})
	task, _, err := runner.Create(context.Background(), 1, CodexGatewayBorrowTestRequest{ClientTaskID: codexGatewayBorrowV7(t), Standalone: true, GenerationTimeoutSeconds: 60,
		Targets: []CodexGatewayBorrowTestTarget{{AccountID: 1, ModelID: "local"}}})
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
