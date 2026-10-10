package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	CodexGatewayBorrowTestTTL        = 24 * time.Hour
	CodexGatewayBorrowPelicanPrompt  = "创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试"
	codexGatewayBorrowTestMaxTargets = 5000
)

var (
	ErrCodexGatewayBorrowTestNotFound       = errors.New("codex gateway borrow test not found or expired")
	ErrCodexGatewayBorrowTestReplayConflict = errors.New("client_task_id was reused with different test parameters")
	ErrCodexGatewayBorrowTestExpired        = errors.New("codex gateway borrow test has expired")
	ErrCodexGatewayBorrowTestInvalidRequest = errors.New("invalid manual pelican test request")
)

type CodexGatewayBorrowTestTarget struct {
	AccountID int64  `json:"account_id"`
	ModelID   string `json:"model_id"`
	Effort    string `json:"effort"`
}

type CodexGatewayBorrowTestRequest struct {
	ClientTaskID             string                         `json:"client_task_id"`
	GenerationTimeoutSeconds int                            `json:"generation_timeout_seconds"`
	Targets                  []CodexGatewayBorrowTestTarget `json:"targets"`
	Standalone               bool                           `json:"-"`
}

type CodexGatewayBorrowTestResult struct {
	ID                    string     `json:"id"`
	TaskID                string     `json:"task_id"`
	Ordinal               int        `json:"ordinal"`
	AccountID             int64      `json:"account_id"`
	AccountName           string     `json:"account_name"`
	Platform              string     `json:"platform"`
	ModelID               string     `json:"model_id"`
	UpstreamModel         string     `json:"upstream_model"`
	Effort                string     `json:"effort"`
	Status                string     `json:"status"`
	RawAnswer             string     `json:"raw_answer"`
	RawResponse           string     `json:"raw_response"`
	RawHTML               string     `json:"raw_html"`
	HTML                  string     `json:"html"`
	Error                 string     `json:"error"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	DurationMS            int64      `json:"duration_ms"`
	GenerationStartedAt   *time.Time `json:"generation_started_at,omitempty"`
	QueueDurationMS       int64      `json:"queue_duration_ms"`
	PreparationDurationMS int64      `json:"preparation_duration_ms"`
	GenerationDurationMS  int64      `json:"generation_duration_ms"`
	ActualEndpoint        string     `json:"actual_endpoint"`
	ActualProtocol        string     `json:"actual_protocol"`
	ActualTransport       string     `json:"actual_transport"`
	BorrowApplied         bool       `json:"borrow_applied"`
	ExpiresAt             time.Time  `json:"expires_at"`
	PreviewURL            string     `json:"preview_url,omitempty"`
	PreviewExpiresAt      *time.Time `json:"preview_expires_at,omitempty"`
	PreviewUnavailable    string     `json:"preview_unavailable"`
}

type CodexGatewayBorrowTestTask struct {
	ID                       string                          `json:"id"`
	ClientTaskID             string                          `json:"client_task_id"`
	CreatedBy                int64                           `json:"created_by"`
	RequestHash              string                          `json:"-"`
	GenerationTimeoutSeconds int                             `json:"generation_timeout_seconds"`
	ExecutionMode            string                          `json:"execution_mode"`
	Status                   string                          `json:"status"`
	Prompt                   string                          `json:"prompt"`
	CreatedAt                time.Time                       `json:"created_at"`
	ExpiresAt                time.Time                       `json:"expires_at"`
	StartedAt                *time.Time                      `json:"started_at,omitempty"`
	FinishedAt               *time.Time                      `json:"finished_at,omitempty"`
	Total                    int                             `json:"total"`
	Completed                int                             `json:"completed"`
	Error                    string                          `json:"error"`
	Results                  []*CodexGatewayBorrowTestResult `json:"results,omitempty"`
	Replayed                 bool                            `json:"replayed"`
}

type CodexGatewayBorrowTestList struct {
	Items    []*CodexGatewayBorrowTestTask `json:"items"`
	Total    int64                         `json:"total"`
	Page     int                           `json:"page"`
	Size     int                           `json:"size"`
	PageSize int                           `json:"page_size"`
}

// This repository owns only the manual pelican tables. Reads must enforce expiry
// themselves: the minute cleanup is storage maintenance, not authorization.
type CodexGatewayBorrowTestRepository interface {
	Create(context.Context, *CodexGatewayBorrowTestTask) (*CodexGatewayBorrowTestTask, bool, error)
	Get(context.Context, string) (*CodexGatewayBorrowTestTask, error)
	GetResult(context.Context, string) (*CodexGatewayBorrowTestResult, error)
	List(context.Context, int, int) (*CodexGatewayBorrowTestList, error)
	StartTask(context.Context, string, time.Time) error
	SaveResult(context.Context, *CodexGatewayBorrowTestResult) error
	FinishTask(context.Context, string, string, string, time.Time) error
	MarkInterrupted(context.Context, time.Time) error
	CleanupExpired(context.Context, time.Time) error
}

type codexGatewayBorrowPelicanGenerator interface {
	GeneratePelican(context.Context, int64, string, string) (*CodexGatewayBorrowPelicanResult, error)
}

type codexGatewayBorrowAccountReader interface {
	GetByID(context.Context, int64) (*Account, error)
}

// All batches, including legacy cache-only tests, share ten outbound execution
// slots. Account leases surround actual requests rather than route preparation.
type CodexGatewayBorrowTestRunner struct {
	submissionMu        sync.Mutex
	backgroundMu        sync.Mutex
	background          map[string]*pelicanBackgroundTask
	backgroundRunning   sync.WaitGroup
	repo                CodexGatewayBorrowTestRepository
	generator           codexGatewayBorrowPelicanGenerator
	standaloneGenerator codexGatewayBorrowPelicanGenerator
	accounts            codexGatewayBorrowAccountReader
	coordinator         *pelicanExecutionCoordinator
	initMu              sync.Mutex
	initialized         bool
	stopCtx             context.Context
	stop                context.CancelFunc
	stopOnce            sync.Once
	cleanupDone         chan struct{}
	runMu               sync.Mutex
	running             sync.WaitGroup
	stopped             bool
	activeTasks         map[string]bool
	activeCancels       map[string]context.CancelFunc
	now                 func() time.Time
	generationContext   func(context.Context, time.Duration) (context.Context, context.CancelFunc)
}

func NewCodexGatewayBorrowTestRunner(repo CodexGatewayBorrowTestRepository, core *CodexGatewayBorrowService, accounts AccountRepository) *CodexGatewayBorrowTestRunner {
	return newCodexGatewayBorrowTestRunner(repo, core, accounts)
}

func newCodexGatewayBorrowTestRunner(repo CodexGatewayBorrowTestRepository, generator codexGatewayBorrowPelicanGenerator, accounts codexGatewayBorrowAccountReader) *CodexGatewayBorrowTestRunner {
	ctx, cancel := context.WithCancel(context.Background())
	r := &CodexGatewayBorrowTestRunner{repo: repo, generator: generator, accounts: accounts,
		coordinator: sharedPelicanExecution, activeTasks: make(map[string]bool),
		stopCtx: ctx, stop: cancel, cleanupDone: make(chan struct{}), now: time.Now, generationContext: context.WithTimeout}
	go r.cleanupLoop()
	return r
}

// SetStandaloneGenerator is configured once before serving. Both entry points
// use the same runner so restart interruption and outbound capacity stay shared.
func (r *CodexGatewayBorrowTestRunner) SetStandaloneGenerator(generator codexGatewayBorrowPelicanGenerator) {
	r.standaloneGenerator = generator
}

func (r *CodexGatewayBorrowTestRunner) Stop() {
	r.stopOnce.Do(func() {
		r.runMu.Lock()
		r.stopped = true
		r.stop()
		r.runMu.Unlock()
		<-r.cleanupDone
		r.submissionMu.Lock()
		r.backgroundRunning.Wait()
		r.submissionMu.Unlock()
		r.running.Wait()
	})
}

func (r *CodexGatewayBorrowTestRunner) cleanupLoop() {
	defer close(r.cleanupDone)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCtx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(r.stopCtx, 10*time.Second)
			_ = r.repo.CleanupExpired(ctx, r.now().UTC())
			cancel()
		}
	}
}

func (r *CodexGatewayBorrowTestRunner) initialize(ctx context.Context) error {
	r.initMu.Lock()
	defer r.initMu.Unlock()
	if r.initialized {
		return nil
	}
	if err := r.repo.MarkInterrupted(ctx, r.now().UTC()); err != nil {
		return err
	}
	r.initialized = true
	return nil
}

func normalizeCodexGatewayBorrowTestRequest(request CodexGatewayBorrowTestRequest, now time.Time) (CodexGatewayBorrowTestRequest, error) {
	id, err := uuid.Parse(strings.TrimSpace(request.ClientTaskID))
	if err != nil || id.Version() != 7 {
		return request, fmt.Errorf("%w: client_task_id must be a UUIDv7", ErrCodexGatewayBorrowTestInvalidRequest)
	}
	clientMillis := int64(id[0])<<40 | int64(id[1])<<32 | int64(id[2])<<24 | int64(id[3])<<16 | int64(id[4])<<8 | int64(id[5])
	clientCreated := time.UnixMilli(clientMillis)
	if !clientCreated.Add(CodexGatewayBorrowTestTTL).After(now) {
		return request, ErrCodexGatewayBorrowTestExpired
	}
	if clientCreated.After(now) {
		return request, fmt.Errorf("%w: client_task_id timestamp is in the future", ErrCodexGatewayBorrowTestInvalidRequest)
	}
	request.ClientTaskID = id.String()
	if request.GenerationTimeoutSeconds == 0 {
		request.GenerationTimeoutSeconds = PelicanDefaultGenerationTimeoutSeconds
	}
	if request.GenerationTimeoutSeconds < PelicanMinGenerationTimeoutSeconds || request.GenerationTimeoutSeconds > PelicanMaxGenerationTimeoutSeconds {
		return request, fmt.Errorf("%w: generation_timeout_seconds must be between %d and %d", ErrCodexGatewayBorrowTestInvalidRequest,
			PelicanMinGenerationTimeoutSeconds, PelicanMaxGenerationTimeoutSeconds)
	}
	targets := make([]CodexGatewayBorrowTestTarget, 0, len(request.Targets))
	seen := make(map[string]bool, len(request.Targets))
	for _, target := range request.Targets {
		target.ModelID = strings.TrimSpace(target.ModelID)
		target.Effort = strings.ToLower(strings.TrimSpace(target.Effort))
		if target.Effort == "" && !request.Standalone {
			target.Effort = "high"
		}
		if target.AccountID <= 0 || target.ModelID == "" || len(target.ModelID) > 512 || strings.ContainsRune(target.ModelID, '\x00') {
			return request, fmt.Errorf("%w: each target needs a positive account_id and a valid model_id", ErrCodexGatewayBorrowTestInvalidRequest)
		}
		switch target.Effort {
		case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		default:
			return request, fmt.Errorf("%w: invalid reasoning effort %q", ErrCodexGatewayBorrowTestInvalidRequest, target.Effort)
		}
		key := fmt.Sprintf("%d\x00%s", target.AccountID, target.ModelID)
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, target)
	}
	if len(targets) == 0 || len(targets) > codexGatewayBorrowTestMaxTargets {
		return request, fmt.Errorf("%w: targets must contain 1 to %d unique account/model pairs", ErrCodexGatewayBorrowTestInvalidRequest, codexGatewayBorrowTestMaxTargets)
	}
	request.Targets = targets
	return request, nil
}

func codexGatewayBorrowTestRequestHash(request CodexGatewayBorrowTestRequest) string {
	mode := PelicanExecutionModeLegacyCache
	if request.Standalone {
		mode = PelicanExecutionModeAccount
	}
	// Default legacy requests retain their existing hash so bookmarks/replays
	// continue to read records created before this migration. New execution
	// parameters participate in every standalone request's idempotency check.
	raw, _ := json.Marshal(request.Targets)
	if request.Standalone || request.GenerationTimeoutSeconds != PelicanDefaultGenerationTimeoutSeconds {
		raw, _ = json.Marshal(struct {
			Targets                  []CodexGatewayBorrowTestTarget `json:"targets"`
			GenerationTimeoutSeconds int                            `json:"generation_timeout_seconds"`
			ExecutionMode            string                         `json:"execution_mode"`
		}{request.Targets, request.GenerationTimeoutSeconds, mode})
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// Create commits the task and all pending children before Run can dispatch.
// The repository's unique client UUID is the final guard against concurrent POSTs.
func (r *CodexGatewayBorrowTestRunner) Create(ctx context.Context, createdBy int64, request CodexGatewayBorrowTestRequest) (*CodexGatewayBorrowTestTask, bool, error) {
	request, err := normalizeCodexGatewayBorrowTestRequest(request, r.now().UTC())
	if err != nil {
		return nil, false, err
	}
	if err = r.initialize(ctx); err != nil {
		return nil, false, err
	}
	mode := PelicanExecutionModeLegacyCache
	if request.Standalone {
		mode = PelicanExecutionModeAccount
	}
	now := r.now().UTC()
	task := &CodexGatewayBorrowTestTask{ID: uuid.NewString(), ClientTaskID: request.ClientTaskID,
		CreatedBy: createdBy, RequestHash: codexGatewayBorrowTestRequestHash(request), Status: "pending",
		GenerationTimeoutSeconds: request.GenerationTimeoutSeconds, ExecutionMode: mode,
		Prompt: CodexGatewayBorrowPelicanPrompt, CreatedAt: now, ExpiresAt: now.Add(CodexGatewayBorrowTestTTL),
		Total: len(request.Targets), Results: make([]*CodexGatewayBorrowTestResult, 0, len(request.Targets))}
	accountInfo := make(map[int64]*Account)
	for i, target := range request.Targets {
		account, found := accountInfo[target.AccountID]
		if !found && r.accounts != nil {
			if read, readErr := r.accounts.GetByID(ctx, target.AccountID); readErr == nil {
				account = read
			}
			accountInfo[target.AccountID] = account
		}
		name, platform := "", ""
		if account != nil {
			name, platform = account.Name, account.Platform
		}
		task.Results = append(task.Results, &CodexGatewayBorrowTestResult{ID: uuid.NewString(), TaskID: task.ID,
			Ordinal: i + 1, AccountID: target.AccountID, AccountName: name, Platform: platform, ModelID: target.ModelID,
			Effort: target.Effort, Status: "pending", ExpiresAt: task.ExpiresAt,
			PreviewUnavailable: "test has not completed"})
	}
	stored, replayed, err := r.repo.Create(ctx, task)
	if stored != nil {
		copy := *stored
		copy.Replayed = replayed
		stored = &copy
	}
	return stored, replayed, err
}

func (r *CodexGatewayBorrowTestRunner) List(ctx context.Context, page, size int) (*CodexGatewayBorrowTestList, error) {
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	return r.repo.List(ctx, page, size)
}

func (r *CodexGatewayBorrowTestRunner) Get(ctx context.Context, id string) (*CodexGatewayBorrowTestTask, error) {
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	return r.repo.Get(ctx, id)
}

type CodexGatewayBorrowTestEvent struct {
	Type   string                        `json:"type"`
	Phase  string                        `json:"phase,omitempty"`
	TaskID string                        `json:"task_id,omitempty"`
	Task   *CodexGatewayBorrowTestTask   `json:"task,omitempty"`
	Result *CodexGatewayBorrowTestResult `json:"result,omitempty"`
}

// Run is synchronous with the SSE request. Cancellation stops both queued and
// in-flight targets; detached, short writes retain their final observations.
func (r *CodexGatewayBorrowTestRunner) Run(parent context.Context, task *CodexGatewayBorrowTestTask, emit func(CodexGatewayBorrowTestEvent)) error {
	r.runMu.Lock()
	if task.Replayed || task.Status != "pending" || r.activeTasks[task.ID] {
		r.runMu.Unlock()
		return nil
	}
	if r.stopped {
		r.runMu.Unlock()
		return r.finishUnstarted(parent, task, "incomplete", pelicanInterruptedMessage, emit)
	}
	r.running.Add(1)
	r.activeTasks[task.ID] = true
	r.runMu.Unlock()
	defer func() {
		r.runMu.Lock()
		delete(r.activeTasks, task.ID)
		delete(r.activeCancels, task.ID)
		r.runMu.Unlock()
		r.running.Done()
	}()
	ctx, cancel := context.WithDeadline(parent, task.ExpiresAt)
	defer cancel()
	r.runMu.Lock()
	if r.activeCancels == nil {
		r.activeCancels = make(map[string]context.CancelFunc)
	}
	r.activeCancels[task.ID] = cancel
	r.runMu.Unlock()
	stopLink := context.AfterFunc(r.stopCtx, cancel)
	defer stopLink()
	started := r.now().UTC()
	if !task.ExpiresAt.After(started) {
		return ErrCodexGatewayBorrowTestExpired
	}
	if err := r.repo.StartTask(ctx, task.ID, started); err != nil {
		if errors.Is(err, ErrCodexGatewayBorrowTestNotFound) {
			return err
		}
		status := "incomplete"
		if ctx.Err() != nil {
			status = "cancelled"
		}
		return r.finishUnstarted(parent, task, status, err.Error(), emit)
	}
	task.Status, task.StartedAt = "running", &started
	var wg sync.WaitGroup
	var errorsMu sync.Mutex
	var persistenceErrors []string
	for _, result := range task.Results {
		result := result
		wg.Add(1)
		go func() {
			defer wg.Done()
			budget := task.GenerationTimeoutSeconds
			if budget == 0 {
				budget = PelicanDefaultGenerationTimeoutSeconds
			}
			state := newPelicanExecutionState(ctx, r.coordinator, time.Duration(budget)*time.Second, r.now)
			state.withTimeout = r.generationContext
			state.onPhase = func(phase string, snapshot pelicanExecutionSnapshot) {
				if emit != nil {
					copy := *result
					applyPelicanExecutionSnapshot(&copy, snapshot)
					emit(CodexGatewayBorrowTestEvent{Type: "result_phase", Phase: phase, TaskID: task.ID, Result: &copy})
				}
			}
			testCtx := context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
			if emit != nil {
				copy := *result
				emit(CodexGatewayBorrowTestEvent{Type: "result_phase", Phase: "queued", TaskID: task.ID, Result: &copy})
			}
			if task.ExecutionMode == PelicanExecutionModeAccount {
				r.runOne(testCtx, result, r.standaloneGenerator, false, emit)
			} else {
				// The existing cache-only generator never prepares nested routes.
				// Its outer lease also covers legacy callers without sender hooks.
				leasedCtx, release, acquireErr := AcquirePelicanExecution(testCtx, result.AccountID)
				if acquireErr == nil {
					r.runOne(leasedCtx, result, r.generator, true, emit)
					release()
				} else {
					finished := r.now().UTC()
					result.Status, result.Error, result.FinishedAt = "cancelled", "test cancelled before dispatch", &finished
					result.PreviewUnavailable = "test cancelled"
				}
			}
			snapshot := state.finish()
			applyPelicanExecutionSnapshot(result, snapshot)
			if snapshot.generationTimedOut && ctx.Err() == nil && result.Status != "skipped" {
				result.Status = "incomplete"
				result.PreviewUnavailable = "test incomplete"
				if result.Error != "" {
					result.Error += "\n"
				}
				result.Error += "generation budget exhausted"
			}
			if r.stopCtx.Err() != nil && result.Status != "complete" {
				result.Status, result.Error, result.PreviewUnavailable = "incomplete", pelicanInterruptedMessage+"\n"+result.Error, "test interrupted"
			}
			writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
			writeErr := r.repo.SaveResult(writeCtx, result)
			writeCancel()
			if writeErr != nil {
				errorsMu.Lock()
				persistenceErrors = append(persistenceErrors, writeErr.Error())
				errorsMu.Unlock()
				cancel()
			}
			if emit != nil {
				emit(CodexGatewayBorrowTestEvent{Type: "result_complete", TaskID: task.ID, Result: result})
			}
		}()
	}
	wg.Wait()
	finished := r.now().UTC()
	task.FinishedAt, task.Completed = &finished, len(task.Results)
	task.Status = codexGatewayBorrowTaskStatus(task.Results, ctx.Err() != nil)
	if r.stopCtx.Err() != nil && task.Status != "complete" {
		task.Status, task.Error = "incomplete", pelicanInterruptedMessage
	}
	if len(persistenceErrors) > 0 {
		task.Status, task.Error = "incomplete", strings.Join(persistenceErrors, "\n")
	}
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	err := r.repo.FinishTask(writeCtx, task.ID, task.Status, task.Error, finished)
	writeCancel()
	if err != nil {
		return err
	}
	if emit != nil {
		emit(CodexGatewayBorrowTestEvent{Type: "task_complete", Task: task})
	}
	return nil
}

func (r *CodexGatewayBorrowTestRunner) runOne(ctx context.Context, result *CodexGatewayBorrowTestResult, generator codexGatewayBorrowPelicanGenerator, legacy bool, emit func(CodexGatewayBorrowTestEvent)) {
	callerCtx := ctx
	now := r.now().UTC()
	if !result.ExpiresAt.After(now) {
		result.Status, result.Error, result.FinishedAt = "skipped", "test record expired before dispatch", &now
		result.PreviewUnavailable = "test expired"
		return
	}
	result.Status, result.StartedAt = "running", &now
	if err := r.repo.SaveResult(ctx, result); err != nil {
		result.Status, result.Error, result.FinishedAt = "incomplete", err.Error(), &now
		if ctx.Err() != nil {
			result.Status = "cancelled"
		}
		result.PreviewUnavailable = "test could not be persisted before dispatch"
		return
	}
	if emit != nil {
		emit(CodexGatewayBorrowTestEvent{Type: "result_started", TaskID: result.TaskID, Result: result})
	}
	if ctx.Err() != nil {
		result.Status, result.Error, result.FinishedAt = "cancelled", ctx.Err().Error(), &now
		result.PreviewUnavailable = "test cancelled"
		return
	}
	BeginPelicanPreparation(ctx)
	if generator == nil {
		result.Status, result.Error, result.FinishedAt = "skipped", "account Pelican generator is unavailable", &now
		result.PreviewUnavailable = "test skipped"
		return
	}
	if legacy {
		ctx = BeginPelicanGeneration(ctx)
	}
	generated, err := generator.GeneratePelican(ctx, result.AccountID, result.ModelID, result.Effort)
	finished := r.now().UTC()
	result.FinishedAt, result.DurationMS = &finished, nonnegativePelicanDuration(finished.Sub(now)).Milliseconds()
	if generated != nil {
		result.RawAnswer, result.RawResponse, result.Error = generated.RawAnswer, generated.RawResponse, generated.Error
		result.UpstreamModel = generated.ModelID
		if generated.Effort != "" {
			result.Effort = generated.Effort
		}
		result.Status = generated.Status
	}
	if err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		} else if result.Error != err.Error() {
			result.Error += "\n" + err.Error()
		}
		if result.Status != "skipped" && result.Status != "incomplete" && result.Status != "failed" {
			result.Status = "failed"
		}
		if errors.Is(err, context.DeadlineExceeded) && result.Status != "skipped" {
			result.Status = "incomplete"
		}
	}
	if callerCtx.Err() != nil {
		result.Status = "cancelled"
		if errors.Is(context.Cause(callerCtx), context.DeadlineExceeded) {
			result.Status = "incomplete"
		}
		if cause := context.Cause(callerCtx); errors.Is(cause, ErrObservedAccountLeaseLost) {
			result.Status = "incomplete"
			if result.Error != "" {
				result.Error += "\n"
			}
			result.Error += cause.Error()
		}
		if result.Error == "" {
			result.Error = callerCtx.Err().Error()
		}
	}
	switch result.Status {
	case "complete", "failed", "incomplete", "cancelled", "skipped":
	default:
		result.Status = "incomplete"
		if result.Error == "" {
			result.Error = "upstream did not return a valid terminal result"
		}
	}
	result.RawHTML, result.HTML = ExtractCodexGatewayBorrowHTML(result.RawAnswer)
	result.PreviewUnavailable = ""
	if result.Status != "complete" {
		result.PreviewUnavailable = "test " + result.Status
	} else if result.HTML == "" {
		result.PreviewUnavailable = "answer contains no extractable HTML or SVG"
	}
}

func (r *CodexGatewayBorrowTestRunner) finishUnstarted(parent context.Context, task *CodexGatewayBorrowTestTask, status, errorText string, emit func(CodexGatewayBorrowTestEvent)) error {
	finished := r.now().UTC()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	for _, result := range task.Results {
		result.Status, result.Error, result.FinishedAt = status, errorText, &finished
		result.PreviewUnavailable = "test " + status
		if err := r.repo.SaveResult(ctx, result); err != nil {
			return err
		}
		if emit != nil {
			emit(CodexGatewayBorrowTestEvent{Type: "result_complete", TaskID: task.ID, Result: result})
		}
	}
	task.Status, task.Error, task.FinishedAt, task.Completed = status, errorText, &finished, len(task.Results)
	if err := r.repo.FinishTask(ctx, task.ID, status, errorText, finished); err != nil {
		return err
	}
	if emit != nil {
		emit(CodexGatewayBorrowTestEvent{Type: "task_complete", Task: task})
	}
	return nil
}

func codexGatewayBorrowTaskStatus(results []*CodexGatewayBorrowTestResult, cancelled bool) string {
	if cancelled {
		return "cancelled"
	}
	allComplete, allSkipped, anyIncomplete := true, true, false
	for _, result := range results {
		allComplete = allComplete && result.Status == "complete"
		allSkipped = allSkipped && result.Status == "skipped"
		anyIncomplete = anyIncomplete || result.Status == "incomplete" || result.Status == "pending" || result.Status == "running"
	}
	if allComplete {
		return "complete"
	}
	if allSkipped {
		return "skipped"
	}
	if anyIncomplete {
		return "incomplete"
	}
	return "failed"
}
