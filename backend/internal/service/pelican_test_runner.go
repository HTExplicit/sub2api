package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	PelicanTestTTL        = 24 * time.Hour
	PelicanPrompt         = "创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试"
	pelicanTestMaxTargets = 5000
)

var (
	ErrPelicanTestNotFound       = errors.New("pelican test not found or expired")
	ErrPelicanTestReplayConflict = errors.New("client_task_id was reused with different test parameters")
	ErrPelicanTestExpired        = errors.New("pelican test has expired")
	ErrPelicanTestDeleted        = errors.New("pelican test was deleted")
	ErrPelicanTestRunning        = errors.New("stop the pelican task and wait for it to finish before deleting")
	ErrPelicanTestInvalidRequest = errors.New("invalid manual pelican test request")
)

type PelicanTestTarget struct {
	Scene     string `json:"scene,omitempty"`
	AccountID int64  `json:"account_id"`
	ModelID   string `json:"model_id"`
	Effort    string `json:"effort"`
}

type pelicanTestSample struct {
	PelicanTestTarget
	RepeatIndex int
}

type PelicanTestRequest struct {
	samples []pelicanTestSample
	PelicanTestConfig
	ClientTaskID             string              `json:"client_task_id"`
	GenerationTimeoutSeconds int                 `json:"generation_timeout_seconds"`
	Targets                  []PelicanTestTarget `json:"targets"`
}

type PelicanTestResult struct {
	RepeatIndex           int                              `json:"repeat_index"`
	RepeatCount           int                              `json:"repeat_count"`
	Evaluation            *PelicanEvaluation               `json:"evaluation,omitempty"`
	Evidence              map[string]*PelicanStageEvidence `json:"evidence,omitempty"`
	SVG                   string                           `json:"raw_svg,omitempty"`
	ReviewImage           []byte                           `json:"review_image,omitempty"`
	ID                    string                           `json:"id"`
	TaskID                string                           `json:"task_id"`
	Ordinal               int                              `json:"ordinal"`
	AccountID             int64                            `json:"account_id"`
	AccountName           string                           `json:"account_name"`
	Platform              string                           `json:"platform"`
	ModelID               string                           `json:"model_id"`
	UpstreamModel         string                           `json:"upstream_model"`
	Effort                string                           `json:"effort"`
	Status                string                           `json:"status"`
	RawAnswer             string                           `json:"raw_answer"`
	RawResponse           string                           `json:"raw_response"`
	RawHTML               string                           `json:"raw_html"`
	HTML                  string                           `json:"html"`
	Error                 string                           `json:"error"`
	StartedAt             *time.Time                       `json:"started_at,omitempty"`
	FinishedAt            *time.Time                       `json:"finished_at,omitempty"`
	DurationMS            int64                            `json:"duration_ms"`
	GenerationStartedAt   *time.Time                       `json:"generation_started_at,omitempty"`
	QueueDurationMS       int64                            `json:"queue_duration_ms"`
	PreparationDurationMS int64                            `json:"preparation_duration_ms"`
	GenerationDurationMS  int64                            `json:"generation_duration_ms"`
	ActualEndpoint        string                           `json:"actual_endpoint"`
	ActualProtocol        string                           `json:"actual_protocol"`
	ActualTransport       string                           `json:"actual_transport"`
	BorrowApplied         bool                             `json:"borrow_applied"`
	ExpiresAt             time.Time                        `json:"expires_at"`
	PreviewURL            string                           `json:"preview_url,omitempty"`
	PreviewExpiresAt      *time.Time                       `json:"preview_expires_at,omitempty"`
	PreviewUnavailable    string                           `json:"preview_unavailable"`
}

type PelicanTestTask struct {
	TestConfig               PelicanTestConfig    `json:"test_config"`
	ID                       string               `json:"id"`
	ClientTaskID             string               `json:"client_task_id"`
	CreatedBy                int64                `json:"created_by"`
	RequestHash              string               `json:"-"`
	GenerationTimeoutSeconds int                  `json:"generation_timeout_seconds"`
	ExecutionMode            string               `json:"execution_mode"`
	Status                   string               `json:"status"`
	Prompt                   string               `json:"prompt"`
	CreatedAt                time.Time            `json:"created_at"`
	ExpiresAt                time.Time            `json:"expires_at"`
	StartedAt                *time.Time           `json:"started_at,omitempty"`
	FinishedAt               *time.Time           `json:"finished_at,omitempty"`
	Total                    int                  `json:"total"`
	Completed                int                  `json:"completed"`
	Error                    string               `json:"error"`
	Results                  []*PelicanTestResult `json:"results,omitempty"`
	Replayed                 bool                 `json:"replayed"`
	DeletedAt                *time.Time           `json:"-"`
}

type PelicanTestList struct {
	Items    []*PelicanTestTask `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	Size     int                `json:"size"`
	PageSize int                `json:"page_size"`
}

// This repository owns only the manual pelican tables. Reads must enforce expiry
// themselves: the minute cleanup is storage maintenance, not authorization.
type PelicanTestRepository interface {
	Create(context.Context, *PelicanTestTask) (*PelicanTestTask, bool, error)
	Get(context.Context, string) (*PelicanTestTask, error)
	GetResult(context.Context, string) (*PelicanTestResult, error)
	List(context.Context, int, int) (*PelicanTestList, error)
	StartTask(context.Context, string, time.Time) error
	SaveResult(context.Context, *PelicanTestResult) error
	FinishTask(context.Context, string, string, string, time.Time) error
	MarkInterrupted(context.Context, time.Time) error
	CleanupExpired(context.Context, time.Time) error
}

type pelicanGenerator interface {
	GeneratePelican(context.Context, int64, string, string) (*PelicanResult, error)
}

type pelicanAccountReader interface {
	GetByID(context.Context, int64) (*Account, error)
}

// All account-test batches share ten outbound execution slots.
// Model turns cover whole flows; outbound leases cover only actual requests.
type PelicanTestRunner struct {
	renderer          pelicanFrameRenderer
	submissionMu      sync.Mutex
	backgroundMu      sync.Mutex
	background        map[string]*pelicanBackgroundTask
	backgroundRunning sync.WaitGroup
	repo              PelicanTestRepository
	generator         pelicanGenerator
	accounts          pelicanAccountReader
	coordinator       *pelicanExecutionCoordinator
	initMu            sync.Mutex
	initialized       bool
	stopCtx           context.Context
	stop              context.CancelFunc
	stopOnce          sync.Once
	cleanupDone       chan struct{}
	runMu             sync.Mutex
	running           sync.WaitGroup
	stopped           bool
	activeTasks       map[string]bool
	activeCancels     map[string]context.CancelFunc
	now               func() time.Time
	generationContext func(context.Context, time.Duration) (context.Context, context.CancelFunc)
}

func NewPelicanTestRunner(repo PelicanTestRepository, generator pelicanGenerator, accounts AccountRepository) *PelicanTestRunner {
	return newPelicanTestRunner(repo, generator, accounts)
}

func newPelicanTestRunner(repo PelicanTestRepository, generator pelicanGenerator, accounts pelicanAccountReader) *PelicanTestRunner {
	ctx, cancel := context.WithCancel(context.Background())
	r := &PelicanTestRunner{repo: repo, generator: generator, accounts: accounts,
		renderer:    &pelicanChromiumRenderer{},
		coordinator: sharedPelicanExecution, activeTasks: make(map[string]bool),
		stopCtx: ctx, stop: cancel, cleanupDone: make(chan struct{}), now: time.Now, generationContext: context.WithTimeout}
	go r.cleanupLoop()
	return r
}

func (r *PelicanTestRunner) Stop() {
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

func (r *PelicanTestRunner) cleanupLoop() {
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

func (r *PelicanTestRunner) initialize(ctx context.Context) error {
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

func normalizePelicanTestRequest(request PelicanTestRequest, now time.Time) (PelicanTestRequest, error) {
	config, configErr := normalizePelicanConfig(request.PelicanTestConfig)
	if configErr != nil {
		return request, configErr
	}
	request.PelicanTestConfig = config
	id, err := uuid.Parse(strings.TrimSpace(request.ClientTaskID))
	if err != nil || id.Version() != 7 {
		return request, fmt.Errorf("%w: client_task_id must be a UUIDv7", ErrPelicanTestInvalidRequest)
	}
	clientMillis := int64(id[0])<<40 | int64(id[1])<<32 | int64(id[2])<<24 | int64(id[3])<<16 | int64(id[4])<<8 | int64(id[5])
	clientCreated := time.UnixMilli(clientMillis)
	if !clientCreated.Add(PelicanTestTTL).After(now) {
		return request, ErrPelicanTestExpired
	}
	if clientCreated.After(now) {
		return request, fmt.Errorf("%w: client_task_id timestamp is in the future", ErrPelicanTestInvalidRequest)
	}
	request.ClientTaskID = id.String()
	if request.GenerationTimeoutSeconds == 0 {
		request.GenerationTimeoutSeconds = PelicanDefaultGenerationTimeoutSeconds
	}
	if request.GenerationTimeoutSeconds < PelicanMinGenerationTimeoutSeconds || request.GenerationTimeoutSeconds > PelicanMaxGenerationTimeoutSeconds {
		return request, fmt.Errorf("%w: generation_timeout_seconds must be between %d and %d", ErrPelicanTestInvalidRequest,
			PelicanMinGenerationTimeoutSeconds, PelicanMaxGenerationTimeoutSeconds)
	}
	targets := make([]PelicanTestTarget, 0, len(request.Targets))
	seen := make(map[string]bool, len(request.Targets))
	for _, target := range request.Targets {
		if target.Scene != "" && !slices.Contains(PelicanScenes, target.Scene) {
			return request, fmt.Errorf("%w: unknown target scene", ErrPelicanTestInvalidRequest)
		}
		target.ModelID = strings.TrimSpace(target.ModelID)
		target.Effort = strings.ToLower(strings.TrimSpace(target.Effort))

		if target.AccountID <= 0 || target.ModelID == "" || len(target.ModelID) > 512 || strings.ContainsRune(target.ModelID, '\x00') {
			return request, fmt.Errorf("%w: each target needs a positive account_id and a valid model_id", ErrPelicanTestInvalidRequest)
		}
		switch target.Effort {
		case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		default:
			return request, fmt.Errorf("%w: invalid reasoning effort %q", ErrPelicanTestInvalidRequest, target.Effort)
		}
		key := fmt.Sprintf("%d\x00%s", target.AccountID, target.ModelID)
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, target)
	}
	if len(targets) == 0 || len(targets) > pelicanTestMaxTargets {
		return request, fmt.Errorf("%w: targets must contain 1 to %d unique account/model pairs", ErrPelicanTestInvalidRequest, pelicanTestMaxTargets)
	}
	if len(request.samples) == 0 && request.repetitions() > pelicanTestMaxTargets/len(targets) {
		return request, fmt.Errorf("%w: repeat_count allows at most %d for these combinations (5000 total tests)", ErrPelicanTestInvalidRequest, pelicanTestMaxTargets/len(targets))
	}
	if len(request.samples) > pelicanTestMaxTargets {
		return request, ErrPelicanTestInvalidRequest
	}
	request.Targets = targets
	return request, nil
}

func pelicanTestRequestHash(request PelicanTestRequest) string {
	repeatCount := request.repetitions()
	if repeatCount == 1 {
		repeatCount = 0
	} // Keep existing single-run request hashes valid.
	raw, _ := json.Marshal(struct {
		RepeatCount              int                 `json:"repeat_count,omitempty"`
		Targets                  []PelicanTestTarget `json:"targets"`
		GenerationTimeoutSeconds int                 `json:"generation_timeout_seconds"`
		ExecutionMode            string              `json:"execution_mode"`
		TestItems                []string            `json:"test_items,omitempty"`
		Scene                    string              `json:"scene,omitempty"`
		RetestKey                string              `json:"retest_key,omitempty"`
	}{repeatCount, request.Targets, request.GenerationTimeoutSeconds, PelicanExecutionModeAccount, request.TestItems, request.Scene, request.RetestKey})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// Create commits the task and all pending children before Run can dispatch.
// The repository's unique client UUID is the final guard against concurrent POSTs.
func (r *PelicanTestRunner) Create(ctx context.Context, createdBy int64, request PelicanTestRequest) (*PelicanTestTask, bool, error) {
	request, err := normalizePelicanTestRequest(request, r.now().UTC())
	if err != nil {
		return nil, false, err
	}
	if err = r.initialize(ctx); err != nil {
		return nil, false, err
	}
	samples := request.samples
	if len(samples) == 0 {
		for _, target := range request.Targets {
			for index := 1; index <= request.repetitions(); index++ {
				samples = append(samples, pelicanTestSample{PelicanTestTarget: target, RepeatIndex: index})
			}
		}
	}
	config := request.PelicanTestConfig
	config.Scene = "" // Deprecated scene inputs remain in the replay hash, never in the new generation.
	now := r.now().UTC()
	task := &PelicanTestTask{ID: uuid.NewString(), ClientTaskID: request.ClientTaskID,
		TestConfig: config,
		CreatedBy:  createdBy, RequestHash: pelicanTestRequestHash(request), Status: "pending",
		GenerationTimeoutSeconds: request.GenerationTimeoutSeconds, ExecutionMode: PelicanExecutionModeAccount,
		Prompt: PelicanPrompt, CreatedAt: now, ExpiresAt: now.Add(PelicanTestTTL),
		Total: len(samples), Results: make([]*PelicanTestResult, 0, len(samples))}
	accountInfo := make(map[int64]*Account)
	for i, target := range samples {
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
		task.Results = append(task.Results, &PelicanTestResult{ID: uuid.NewString(), TaskID: task.ID,
			Ordinal: i + 1, RepeatIndex: target.RepeatIndex, RepeatCount: request.repetitions(), AccountID: target.AccountID, AccountName: name, Platform: platform, ModelID: target.ModelID,
			Effort: target.Effort, Status: "pending", ExpiresAt: task.ExpiresAt,
			PreviewUnavailable: "test has not completed"})
		if len(request.TestItems) > 0 {
			evaluation, evaluationErr := newPelicanEvaluation(request.PelicanTestConfig, target.PelicanTestTarget)
			if evaluationErr != nil {
				return nil, false, evaluationErr
			}
			task.Results[len(task.Results)-1].Evaluation = evaluation
		}
	}
	stored, replayed, err := r.repo.Create(ctx, task)
	if stored != nil {
		copy := *stored
		copy.Replayed = replayed
		stored = &copy
	}
	return stored, replayed, err
}

func (r *PelicanTestRunner) List(ctx context.Context, page, size int) (*PelicanTestList, error) {
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	return r.repo.List(ctx, page, size)
}

func (r *PelicanTestRunner) Get(ctx context.Context, id string) (*PelicanTestTask, error) {
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	return r.repo.Get(ctx, id)
}

type PelicanTestEvent struct {
	Type   string             `json:"type"`
	Phase  string             `json:"phase,omitempty"`
	TaskID string             `json:"task_id,omitempty"`
	Task   *PelicanTestTask   `json:"task,omitempty"`
	Result *PelicanTestResult `json:"result,omitempty"`
}

// Run is synchronous with the SSE request. Cancellation stops both queued and
// in-flight targets; detached, short writes retain their final observations.
func (r *PelicanTestRunner) Run(parent context.Context, task *PelicanTestTask, emit func(PelicanTestEvent)) error {
	if task.ExecutionMode != PelicanExecutionModeAccount {
		return fmt.Errorf("%w: historical task is read-only", ErrPelicanTestInvalidRequest)
	}
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
		return ErrPelicanTestExpired
	}
	if err := r.repo.StartTask(ctx, task.ID, started); err != nil {
		if errors.Is(err, ErrPelicanTestNotFound) {
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
	runResult := func(result *PelicanTestResult) {
		modelQueueMS := nonnegativePelicanDuration(r.now().Sub(started)).Milliseconds()
		result.QueueDurationMS = modelQueueMS
		if result.Evaluation != nil {
			r.runEvaluation(ctx, result, time.Duration(task.GenerationTimeoutSeconds)*time.Second, emit)
			if r.stopCtx.Err() != nil && result.Status != "complete" {
				result.Status = "incomplete"
				result.Error = pelicanInterruptedMessage + "\n" + result.Error
			}
			writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
			defer cancelWrite()
			if err := r.repo.SaveResult(writeCtx, result); err != nil {
				errorsMu.Lock()
				persistenceErrors = append(persistenceErrors, err.Error())
				errorsMu.Unlock()
			}
			if emit != nil {
				emit(PelicanTestEvent{Type: "result_complete", TaskID: task.ID, Result: result})
			}
			return
		}
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
				emit(PelicanTestEvent{Type: "result_phase", Phase: phase, TaskID: task.ID, Result: &copy})
			}
		}
		testCtx := context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
		if emit != nil {
			copy := *result
			emit(PelicanTestEvent{Type: "result_phase", Phase: "queued", TaskID: task.ID, Result: &copy})
		}
		r.runOne(testCtx, result, r.generator, emit)
		snapshot := state.finish()
		applyPelicanExecutionSnapshot(result, snapshot)
		result.QueueDurationMS += modelQueueMS
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
			emit(PelicanTestEvent{Type: "result_complete", TaskID: task.ID, Result: result})
		}
	}
	groups := make(map[pelicanModelKey][]*PelicanTestResult)
	var order []pelicanModelKey
	for _, result := range task.Results {
		key := pelicanModelKey{result.AccountID, result.ModelID}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], result)
	}
	for _, key := range order {
		results := groups[key]
		slices.SortStableFunc(results, func(a, b *PelicanTestResult) int { return a.RepeatIndex - b.RepeatIndex })
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := r.coordinator.acquireModel(ctx, key.accountID, key.modelID)
			if err == nil {
				defer release()
			}
			// Cancelled groups still persist every pending sample without dispatching.
			for _, result := range results {
				runResult(result)
			}
		}()
	}
	wg.Wait()
	finished := r.now().UTC()
	task.FinishedAt, task.Completed = &finished, len(task.Results)
	task.Status = pelicanTaskStatus(task.Results, ctx.Err() != nil)
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
		emit(PelicanTestEvent{Type: "task_complete", Task: task})
	}
	return nil
}

func (r *PelicanTestRunner) runOne(ctx context.Context, result *PelicanTestResult, generator pelicanGenerator, emit func(PelicanTestEvent)) {
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
		emit(PelicanTestEvent{Type: "result_started", TaskID: result.TaskID, Result: result})
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
	// The task parent is marked before cancellation reaches every target child.
	// Admission may therefore stop a queued send before callerCtx.Err is set.
	cancellationCtx := callerCtx
	if callerCtx.Err() == nil {
		if state := pelicanExecutionFromContext(callerCtx); state != nil && state.admission != nil && state.admission.Err() != nil {
			cancellationCtx = state.admission
		}
	}
	if cancellationCtx.Err() != nil {
		result.Status = "cancelled"
		if errors.Is(context.Cause(cancellationCtx), context.DeadlineExceeded) {
			result.Status = "incomplete"
		}
		if cause := context.Cause(cancellationCtx); errors.Is(cause, ErrObservedAccountLeaseLost) {
			result.Status = "incomplete"
			if result.Error != "" {
				result.Error += "\n"
			}
			result.Error += cause.Error()
		}
		if result.Error == "" {
			result.Error = cancellationCtx.Err().Error()
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
	result.RawHTML, result.HTML = ExtractPelicanHTML(result.RawAnswer)
	result.PreviewUnavailable = ""
	if result.Status != "complete" {
		result.PreviewUnavailable = "test " + result.Status
	} else if result.HTML == "" {
		result.PreviewUnavailable = "answer contains no extractable HTML or SVG"
	}
}

func (r *PelicanTestRunner) finishUnstarted(parent context.Context, task *PelicanTestTask, status, errorText string, emit func(PelicanTestEvent)) error {
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
			emit(PelicanTestEvent{Type: "result_complete", TaskID: task.ID, Result: result})
		}
	}
	task.Status, task.Error, task.FinishedAt, task.Completed = status, errorText, &finished, len(task.Results)
	if err := r.repo.FinishTask(ctx, task.ID, status, errorText, finished); err != nil {
		return err
	}
	if emit != nil {
		emit(PelicanTestEvent{Type: "task_complete", Task: task})
	}
	return nil
}

func pelicanTaskStatus(results []*PelicanTestResult, cancelled bool) string {
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
