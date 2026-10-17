package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

type PelicanResultSummary struct {
	Evaluation            *PelicanEvaluation `json:"evaluation,omitempty"`
	PreviewURL            string             `json:"preview_url,omitempty"`
	ID                    string             `json:"id"`
	TaskID                string             `json:"task_id"`
	Ordinal               int                `json:"ordinal"`
	AccountID             int64              `json:"account_id"`
	AccountName           string             `json:"account_name"`
	Platform              string             `json:"platform"`
	ModelID               string             `json:"model_id"`
	Effort                string             `json:"effort"`
	Status                string             `json:"status"`
	Phase                 string             `json:"phase,omitempty"`
	HasPreview            bool               `json:"has_preview"`
	Interrupted           bool               `json:"interrupted"`
	StartedAt             *time.Time         `json:"started_at,omitempty"`
	FinishedAt            *time.Time         `json:"finished_at,omitempty"`
	GenerationStartedAt   *time.Time         `json:"generation_started_at,omitempty"`
	DurationMS            int64              `json:"duration_ms"`
	QueueDurationMS       int64              `json:"queue_duration_ms"`
	PreparationDurationMS int64              `json:"preparation_duration_ms"`
	GenerationDurationMS  int64              `json:"generation_duration_ms"`
	ExpiresAt             time.Time          `json:"expires_at"`
}

type PelicanTaskSnapshot struct {
	*PelicanTestTask
	Counts      map[string]int `json:"counts"`
	FailedCount int            `json:"failed_count"`
}

type PelicanResultPage struct {
	Items    []*PelicanResultSummary `json:"items"`
	Total    int64                   `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
}

type PelicanResultFilter struct {
	TaskID, Effort, Scene, Verdict string
	Page, Size                     int
	AccountID                      int64
	Model, Status                  string
}

// Production reads select metadata explicitly. They never load all raw works
// just to discard them before serializing a list or progress event.
type PelicanTaskReader interface {
	GetTaskSnapshot(context.Context, string) (*PelicanTaskSnapshot, error)
	ListTaskResults(context.Context, string, PelicanResultFilter) (*PelicanResultPage, error)
}

type PelicanTaskDeleter interface {
	DeleteTask(context.Context, string) error
	DeleteTaskResult(context.Context, string, string) error
}

func (r *PelicanTestRunner) DeleteTask(ctx context.Context, id string) error {
	return r.deleteTask(ctx, id, "")
}

func (r *PelicanTestRunner) DeleteTaskResult(ctx context.Context, taskID, resultID string) error {
	return r.deleteTask(ctx, taskID, resultID)
}

func (r *PelicanTestRunner) deleteTask(ctx context.Context, taskID, resultID string) error {
	if err := r.initialize(ctx); err != nil {
		return err
	}
	deleter, ok := r.repo.(PelicanTaskDeleter)
	if !ok {
		return errors.New("pelican deletion repository is unavailable")
	}
	if resultID == "" {
		return deleter.DeleteTask(ctx, taskID)
	}
	return deleter.DeleteTaskResult(ctx, taskID, resultID)
}

type pelicanBackgroundTask struct {
	cancel context.CancelFunc
	phases map[string]*PelicanResultSummary
}

const pelicanInterruptedMessage = "process stopped before the test finished; this task will not be replayed"

func pelicanResultSummary(result *PelicanTestResult, phase string) *PelicanResultSummary {
	return &PelicanResultSummary{ID: result.ID, TaskID: result.TaskID, Ordinal: result.Ordinal,
		Evaluation: clonePelicanEvaluation(result.Evaluation),
		AccountID:  result.AccountID, AccountName: result.AccountName, Platform: result.Platform,
		ModelID: result.ModelID, Effort: result.Effort, Status: result.Status, Phase: phase,
		HasPreview: result.SVG != "" || result.Status == "complete" && result.HTML != "", Interrupted: strings.Contains(result.Error, pelicanInterruptedMessage),
		StartedAt: result.StartedAt, FinishedAt: result.FinishedAt, GenerationStartedAt: result.GenerationStartedAt,
		DurationMS: result.DurationMS, QueueDurationMS: result.QueueDurationMS,
		PreparationDurationMS: result.PreparationDurationMS, GenerationDurationMS: result.GenerationDurationMS, ExpiresAt: result.ExpiresAt}
}

func (r *PelicanTestRunner) StartBackground(ctx context.Context, createdBy int64, request PelicanTestRequest, catalog *PelicanTestCatalog) (*PelicanTaskSnapshot, error) {
	normalized, err := normalizePelicanTestRequest(request, r.now().UTC())
	if err != nil {
		return nil, err
	}
	// Legacy normalization deduplicates; the new workbench must reject conflicting
	// efforts instead of silently dropping a later row.
	if len(normalized.Targets) != len(request.Targets) {
		return nil, fmt.Errorf("%w: one effort per account/model; edit the existing combination", ErrPelicanTestInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Once a valid submission is accepted, its database commit and handoff also
	// survive disconnects; no request context is retained by the server-owned job.
	submissionCtx, cancelSubmission := context.WithTimeout(r.stopCtx, 30*time.Second)
	defer cancelSubmission()
	ctx = submissionCtx
	// Serialize submission with cancellation so an immediate Stop cannot miss an
	// accepted task between its database commit and background registration.
	r.submissionMu.Lock()
	defer r.submissionMu.Unlock()
	if r.stopCtx.Err() != nil {
		return nil, errors.New("test runner stopped")
	}
	// Replays compare their immutable normalized parameters before consulting
	// today's account capabilities, and read summaries rather than every raw work.
	if existing, readErr := r.TaskSnapshot(ctx, normalized.ClientTaskID); readErr == nil {
		if existing.RequestHash != pelicanTestRequestHash(normalized) {
			return nil, ErrPelicanTestReplayConflict
		}
		existing.Replayed = true
		return existing, nil
	} else if !errors.Is(readErr, ErrPelicanTestNotFound) {
		return nil, readErr
	}
	if catalog == nil {
		return nil, errors.New("pelican catalog is unavailable")
	}
	if err := catalog.ValidateTargets(ctx, normalized.Targets); err != nil {
		return nil, err
	}
	task, replayed, err := r.Create(ctx, createdBy, normalized)
	if err != nil {
		return nil, err
	}
	if replayed {
		snapshot, err := r.TaskSnapshot(ctx, task.ID)
		if snapshot != nil {
			snapshot.Replayed = true
		}
		return snapshot, err
	}
	copyTask := *task
	copyTask.Results = nil
	snapshot := &PelicanTaskSnapshot{PelicanTestTask: &copyTask, Counts: map[string]int{"pending": task.Total}}
	// No HTTP request values, Gin writer or observer cancellation enters the run.
	runCtx, cancel := context.WithCancel(r.stopCtx)
	control := &pelicanBackgroundTask{cancel: cancel, phases: make(map[string]*PelicanResultSummary)}
	r.backgroundMu.Lock()
	if r.background == nil {
		r.background = make(map[string]*pelicanBackgroundTask)
	}
	r.background[task.ID] = control
	r.backgroundMu.Unlock()
	r.backgroundRunning.Add(1)
	go func() {
		defer r.backgroundRunning.Done()
		defer cancel()
		runErr := r.Run(runCtx, task, func(event PelicanTestEvent) {
			if event.Result == nil {
				return
			}
			summary := pelicanResultSummary(event.Result, event.Phase)
			r.backgroundMu.Lock()
			control.phases[summary.ID] = summary
			r.backgroundMu.Unlock()
		})
		if runErr != nil {
			slog.Error("pelican_background_task_failed", "task_id", task.ID, "error", runErr)
			if !errors.Is(runErr, ErrPelicanTestNotFound) {
				writeCtx, cancelWrite := context.WithTimeout(context.Background(), 10*time.Second)
				_ = r.repo.FinishTask(writeCtx, task.ID, "incomplete", runErr.Error(), r.now().UTC())
				cancelWrite()
			}
		}
		r.backgroundMu.Lock()
		delete(r.background, task.ID)
		r.backgroundMu.Unlock()
	}()
	return snapshot, nil
}

func (r *PelicanTestRunner) TaskSnapshot(ctx context.Context, id string) (*PelicanTaskSnapshot, error) {
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	reader, ok := r.repo.(PelicanTaskReader)
	if !ok {
		return nil, errors.New("pelican summary repository is unavailable")
	}
	return reader.GetTaskSnapshot(ctx, id)
}

func (r *PelicanTestRunner) TaskResults(ctx context.Context, id string, filter PelicanResultFilter) (*PelicanResultPage, error) {
	if _, err := r.TaskSnapshot(ctx, id); err != nil {
		return nil, err
	}
	reader, ok := r.repo.(PelicanTaskReader)
	if !ok {
		return nil, errors.New("pelican summary repository is unavailable")
	}
	page, err := reader.ListTaskResults(ctx, id, filter)
	if err != nil {
		return nil, err
	}
	r.backgroundMu.Lock()
	defer r.backgroundMu.Unlock()
	if task := r.background[id]; task != nil {
		for i, row := range page.Items {
			if latest := task.phases[row.ID]; latest != nil && (row.Status == "pending" || row.Status == "running") {
				copy := *latest
				page.Items[i] = &copy
			}
		}
	}
	return page, nil
}

func (r *PelicanTestRunner) CancelBackground(ctx context.Context, id string) (*PelicanTaskSnapshot, error) {
	r.submissionMu.Lock()
	defer r.submissionMu.Unlock()
	snapshot, err := r.TaskSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	id = snapshot.ID
	r.backgroundMu.Lock()
	if task := r.background[id]; task != nil {
		task.cancel()
	}
	r.backgroundMu.Unlock()
	r.runMu.Lock()
	if cancel := r.activeCancels[id]; cancel != nil {
		cancel()
	}
	r.runMu.Unlock()
	return r.TaskSnapshot(ctx, id)
}

func (r *PelicanTestRunner) TaskResult(ctx context.Context, taskID, resultID string) (*PelicanTestResult, error) {
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	result, err := r.repo.GetResult(ctx, resultID)
	if err != nil {
		return nil, err
	}
	if result.TaskID != taskID {
		return nil, ErrPelicanTestNotFound
	}
	return result, nil
}

// Validate exactly the capabilities used again by the native generator. Reads
// are local; paused/error/limited accounts stay explicitly selectable.
func (s *PelicanTestCatalog) ValidateTargets(ctx context.Context, targets []PelicanTestTarget) error {
	ids := make([]int64, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.AccountID)
	}
	if len(ids) == 0 || len(ids) > pelicanTestCatalogMaxAccounts {
		return ErrPelicanTestInvalidRequest
	}
	accounts, err := s.accounts.GetByIDs(ctx, ids)
	if err != nil {
		return err
	}
	byID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	for _, target := range targets {
		option := PelicanTestModelOptions(byID[target.AccountID], target.ModelID)
		if !option.TextSupported {
			return fmt.Errorf("%w: account %d / %s: %s", ErrPelicanTestInvalidRequest, target.AccountID, target.ModelID, option.CapabilityReason)
		}
		if target.Effort != "" && !slices.Contains(option.ReasoningEfforts, strings.ToLower(strings.TrimSpace(target.Effort))) {
			return fmt.Errorf("%w: account %d / %s does not support effort %q", ErrPelicanTestInvalidRequest, target.AccountID, target.ModelID, target.Effort)
		}
	}
	return nil
}
