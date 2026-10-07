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
	codexGatewayBorrowTestTimeout    = 90 * time.Second
	codexGatewayBorrowTestMaxTargets = 5000
)

var (
	ErrCodexGatewayBorrowTestNotFound       = errors.New("codex gateway borrow test not found or expired")
	ErrCodexGatewayBorrowTestReplayConflict = errors.New("client_task_id was reused with different targets")
	ErrCodexGatewayBorrowTestExpired        = errors.New("codex gateway borrow test has expired")
	ErrCodexGatewayBorrowTestInvalidRequest = errors.New("invalid manual pelican test request")
)

type CodexGatewayBorrowTestTarget struct {
	AccountID int64  `json:"account_id"`
	ModelID   string `json:"model_id"`
	Effort    string `json:"effort"`
}

type CodexGatewayBorrowTestRequest struct {
	ClientTaskID string                         `json:"client_task_id"`
	Targets      []CodexGatewayBorrowTestTarget `json:"targets"`
}

type CodexGatewayBorrowTestResult struct {
	ID                 string     `json:"id"`
	TaskID             string     `json:"task_id"`
	Ordinal            int        `json:"ordinal"`
	AccountID          int64      `json:"account_id"`
	AccountName        string     `json:"account_name"`
	ModelID            string     `json:"model_id"`
	UpstreamModel      string     `json:"upstream_model"`
	Effort             string     `json:"effort"`
	Status             string     `json:"status"`
	RawAnswer          string     `json:"raw_answer"`
	RawResponse        string     `json:"raw_response"`
	RawHTML            string     `json:"raw_html"`
	HTML               string     `json:"html"`
	Error              string     `json:"error"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	DurationMS         int64      `json:"duration_ms"`
	ExpiresAt          time.Time  `json:"expires_at"`
	PreviewURL         string     `json:"preview_url,omitempty"`
	PreviewExpiresAt   *time.Time `json:"preview_expires_at,omitempty"`
	PreviewUnavailable string     `json:"preview_unavailable"`
}

type CodexGatewayBorrowTestTask struct {
	ID           string                          `json:"id"`
	ClientTaskID string                          `json:"client_task_id"`
	CreatedBy    int64                           `json:"created_by"`
	RequestHash  string                          `json:"-"`
	Status       string                          `json:"status"`
	Prompt       string                          `json:"prompt"`
	CreatedAt    time.Time                       `json:"created_at"`
	ExpiresAt    time.Time                       `json:"expires_at"`
	StartedAt    *time.Time                      `json:"started_at,omitempty"`
	FinishedAt   *time.Time                      `json:"finished_at,omitempty"`
	Total        int                             `json:"total"`
	Completed    int                             `json:"completed"`
	Error        string                          `json:"error"`
	Results      []*CodexGatewayBorrowTestResult `json:"results,omitempty"`
	Replayed     bool                            `json:"replayed"`
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

type codexGatewayBorrowAccountSlot struct {
	slot chan struct{}
	refs int
}

// All batches share these three slots and account locks. No native test service,
// recovery service, rate-limit writer or account-state writer is used here.
type CodexGatewayBorrowTestRunner struct {
	repo         CodexGatewayBorrowTestRepository
	generator    codexGatewayBorrowPelicanGenerator
	accounts     codexGatewayBorrowAccountReader
	slots        chan struct{}
	accountMu    sync.Mutex
	accountSlots map[int64]*codexGatewayBorrowAccountSlot
	initMu       sync.Mutex
	initialized  bool
	stopCtx      context.Context
	stop         context.CancelFunc
	stopOnce     sync.Once
	cleanupDone  chan struct{}
	runMu        sync.Mutex
	running      sync.WaitGroup
	stopped      bool
	now          func() time.Time
}

func NewCodexGatewayBorrowTestRunner(repo CodexGatewayBorrowTestRepository, core *CodexGatewayBorrowService, accounts AccountRepository) *CodexGatewayBorrowTestRunner {
	return newCodexGatewayBorrowTestRunner(repo, core, accounts)
}

func newCodexGatewayBorrowTestRunner(repo CodexGatewayBorrowTestRepository, generator codexGatewayBorrowPelicanGenerator, accounts codexGatewayBorrowAccountReader) *CodexGatewayBorrowTestRunner {
	ctx, cancel := context.WithCancel(context.Background())
	r := &CodexGatewayBorrowTestRunner{repo: repo, generator: generator, accounts: accounts,
		slots: make(chan struct{}, 3), accountSlots: make(map[int64]*codexGatewayBorrowAccountSlot),
		stopCtx: ctx, stop: cancel, cleanupDone: make(chan struct{}), now: time.Now}
	go r.cleanupLoop()
	return r
}

func (r *CodexGatewayBorrowTestRunner) Stop() {
	r.stopOnce.Do(func() {
		r.runMu.Lock()
		r.stopped = true
		r.stop()
		r.runMu.Unlock()
		<-r.cleanupDone
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
	targets := make([]CodexGatewayBorrowTestTarget, 0, len(request.Targets))
	seen := make(map[string]bool, len(request.Targets))
	for _, target := range request.Targets {
		target.ModelID = strings.TrimSpace(target.ModelID)
		target.Effort = strings.ToLower(strings.TrimSpace(target.Effort))
		if target.Effort == "" {
			target.Effort = "high"
		}
		if target.AccountID <= 0 || target.ModelID == "" || len(target.ModelID) > 512 || strings.ContainsRune(target.ModelID, '\x00') {
			return request, fmt.Errorf("%w: each target needs a positive account_id and a valid model_id", ErrCodexGatewayBorrowTestInvalidRequest)
		}
		switch target.Effort {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
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
	raw, _ := json.Marshal(request.Targets)
	hash := sha256.Sum256(raw)
	now := r.now().UTC()
	task := &CodexGatewayBorrowTestTask{ID: uuid.NewString(), ClientTaskID: request.ClientTaskID,
		CreatedBy: createdBy, RequestHash: hex.EncodeToString(hash[:]), Status: "pending",
		Prompt: CodexGatewayBorrowPelicanPrompt, CreatedAt: now, ExpiresAt: now.Add(CodexGatewayBorrowTestTTL),
		Total: len(request.Targets), Results: make([]*CodexGatewayBorrowTestResult, 0, len(request.Targets))}
	names := make(map[int64]string)
	for i, target := range request.Targets {
		name, found := names[target.AccountID]
		if !found && r.accounts != nil {
			if account, readErr := r.accounts.GetByID(ctx, target.AccountID); readErr == nil && account != nil {
				name = account.Name
			}
			names[target.AccountID] = name
		}
		task.Results = append(task.Results, &CodexGatewayBorrowTestResult{ID: uuid.NewString(), TaskID: task.ID,
			Ordinal: i + 1, AccountID: target.AccountID, AccountName: name, ModelID: target.ModelID,
			Effort: target.Effort, Status: "pending", ExpiresAt: task.ExpiresAt,
			PreviewUnavailable: "test has not completed"})
	}
	stored, replayed, err := r.repo.Create(ctx, task)
	if stored != nil {
		stored.Replayed = replayed
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
	TaskID string                        `json:"task_id,omitempty"`
	Task   *CodexGatewayBorrowTestTask   `json:"task,omitempty"`
	Result *CodexGatewayBorrowTestResult `json:"result,omitempty"`
}

func (r *CodexGatewayBorrowTestRunner) acquire(ctx context.Context, accountID int64) (func(), bool) {
	r.accountMu.Lock()
	entry := r.accountSlots[accountID]
	if entry == nil {
		entry = &codexGatewayBorrowAccountSlot{slot: make(chan struct{}, 1)}
		r.accountSlots[accountID] = entry
	}
	entry.refs++
	r.accountMu.Unlock()
	dropReference := func() {
		r.accountMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(r.accountSlots, accountID)
		}
		r.accountMu.Unlock()
	}
	select {
	case entry.slot <- struct{}{}:
	case <-ctx.Done():
		dropReference()
		return nil, false
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		<-entry.slot
		dropReference()
		return nil, false
	}
	if ctx.Err() != nil {
		<-r.slots
		<-entry.slot
		dropReference()
		return nil, false
	}
	return func() { <-r.slots; <-entry.slot; dropReference() }, true
}

// Run is synchronous with the SSE request. Cancellation stops both queued and
// in-flight targets; detached, short writes retain their final observations.
func (r *CodexGatewayBorrowTestRunner) Run(parent context.Context, task *CodexGatewayBorrowTestTask, emit func(CodexGatewayBorrowTestEvent)) error {
	r.runMu.Lock()
	if r.stopped {
		r.runMu.Unlock()
		return r.finishUnstarted(parent, task, "cancelled", "test runner stopped", emit)
	}
	r.running.Add(1)
	r.runMu.Unlock()
	defer r.running.Done()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopLink := context.AfterFunc(r.stopCtx, cancel)
	defer stopLink()
	started := r.now().UTC()
	if !task.ExpiresAt.After(started) {
		return ErrCodexGatewayBorrowTestExpired
	}
	if err := r.repo.StartTask(ctx, task.ID, started); err != nil {
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
			if release, ok := r.acquire(ctx, result.AccountID); ok {
				defer release()
				r.runOne(ctx, result, emit)
			} else {
				finished := r.now().UTC()
				result.Status, result.Error, result.FinishedAt = "cancelled", "test cancelled before dispatch", &finished
				result.PreviewUnavailable = "test cancelled"
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

func (r *CodexGatewayBorrowTestRunner) runOne(ctx context.Context, result *CodexGatewayBorrowTestResult, emit func(CodexGatewayBorrowTestEvent)) {
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
	testCtx, cancel := context.WithTimeout(ctx, codexGatewayBorrowTestTimeout)
	generated, err := r.generator.GeneratePelican(testCtx, result.AccountID, result.ModelID, result.Effort)
	cancel()
	finished := r.now().UTC()
	result.FinishedAt, result.DurationMS = &finished, finished.Sub(now).Milliseconds()
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
	}
	if ctx.Err() != nil {
		result.Status = "cancelled"
		if result.Error == "" {
			result.Error = ctx.Err().Error()
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
