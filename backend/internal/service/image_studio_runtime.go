package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type ImageStudioRuntimeOptions struct {
	Workers         int
	PollInterval    time.Duration
	CleanupInterval time.Duration
}

func (o ImageStudioRuntimeOptions) normalized() ImageStudioRuntimeOptions {
	if o.Workers <= 0 || o.Workers > 4 {
		o.Workers = 4
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 250 * time.Millisecond
	}
	if o.CleanupInterval <= 0 {
		o.CleanupInterval = 10 * time.Minute
	}
	return o
}

type ImageStudioRuntime struct {
	repo     ImageStudioRepository
	studio   *ImageStudioService
	store    ImageStudioFileStorage
	executor ImageStudioExecutor
	options  ImageStudioRuntimeOptions

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

func NewImageStudioRuntime(
	repo ImageStudioRepository,
	studio *ImageStudioService,
	store ImageStudioFileStorage,
	executor ImageStudioExecutor,
	options ImageStudioRuntimeOptions,
) *ImageStudioRuntime {
	return &ImageStudioRuntime{
		repo: repo, studio: studio, store: store, executor: executor, options: options.normalized(),
	}
}

func (r *ImageStudioRuntime) Start(parent context.Context) error {
	if r == nil || r.repo == nil || r.studio == nil || r.store == nil || r.executor == nil {
		return errors.New("image studio runtime dependencies are unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	if err := r.repo.RecoverInterrupted(parent); err != nil {
		return fmt.Errorf("recover interrupted image studio jobs: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r.cancel = cancel
	r.done = done
	r.started = true

	var workers sync.WaitGroup
	workers.Add(r.options.Workers + 1)
	for workerID := 0; workerID < r.options.Workers; workerID++ {
		go func() {
			defer workers.Done()
			r.worker(ctx)
		}()
	}
	go func() {
		defer workers.Done()
		r.cleanupLoop(ctx)
	}()
	go func() {
		workers.Wait()
		close(done)
	}()
	return nil
}

func (r *ImageStudioRuntime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	cancel()
	select {
	case <-done:
		r.mu.Lock()
		r.started = false
		r.cancel = nil
		r.done = nil
		r.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *ImageStudioRuntime) worker(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		claim, err := r.repo.ClaimNext(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			if !waitForImageStudioPoll(ctx, r.options.PollInterval) {
				return
			}
			continue
		}
		r.processClaim(ctx, claim)
	}
}

func waitForImageStudioPoll(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *ImageStudioRuntime) processClaim(ctx context.Context, claim *ImageStudioClaim) {
	if claim == nil {
		return
	}
	job := claim.Job
	key, err := r.studio.eligibleAPIKey(ctx, job.UserID, job.APIKeyID)
	if err != nil {
		r.failClaim(ctx, job.ID, claim.Item.ID, err)
		return
	}
	request := ImageStudioExecutionRequest{Job: job, Item: claim.Item, APIKey: key}
	for _, artifact := range claim.Inputs {
		data, readErr := r.store.Read(artifact.StorageKey)
		if readErr != nil {
			r.failClaim(ctx, job.ID, claim.Item.ID, newImageStudioError(409, "input_expired", "Image Studio input is unavailable"))
			return
		}
		switch artifact.Kind {
		case ImageStudioArtifactReference:
			request.Reference = data
			request.ReferenceContentType = artifact.ContentType
		case ImageStudioArtifactMask:
			request.Mask = data
			request.MaskContentType = artifact.ContentType
		}
	}
	result, err := r.executor.Execute(ctx, request)
	if err != nil || result == nil {
		if err == nil {
			err = errors.New("empty image studio result")
		}
		r.failClaim(ctx, job.ID, claim.Item.ID, err)
		return
	}
	expiresAt := time.Now().Add(ImageStudioFileRetention)
	artifact, err := r.store.Save(ctx, job.UserID, ImageStudioArtifactOutput, result.Data, result.ContentType, expiresAt)
	if err != nil {
		r.failClaim(ctx, job.ID, claim.Item.ID, err)
		return
	}
	if err = r.repo.CompleteSuccess(ctx, claim.Item.ID, artifact, result.RevisedPrompt, expiresAt); err != nil {
		_ = r.store.Remove(artifact.StorageKey)
		r.failClaim(ctx, job.ID, claim.Item.ID, errors.New("persist image result"))
		return
	}
	_, _ = r.repo.Finalize(ctx, job.ID)
}

func (r *ImageStudioRuntime) failClaim(ctx context.Context, jobID, itemID int64, err error) {
	code, message := imageStudioSafeExecutionError(ctx, err)
	finishCtx := context.WithoutCancel(ctx)
	if completeErr := r.repo.CompleteFailure(finishCtx, itemID, code, message); completeErr != nil {
		return
	}
	_, _ = r.repo.Finalize(finishCtx, jobID)
}

func imageStudioSafeExecutionError(ctx context.Context, err error) (string, string) {
	if ctx != nil && ctx.Err() != nil {
		return "interrupted", "Image generation was interrupted"
	}
	var studioErr *ImageStudioError
	if errors.As(err, &studioErr) && studioErr != nil && studioErr.Code != "" {
		return studioErr.Code, studioErr.Message
	}
	return "generation_failed", "Image generation failed"
}

func (r *ImageStudioRuntime) cleanupLoop(ctx context.Context) {
	r.cleanup(ctx)
	ticker := time.NewTicker(r.options.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.cleanup(ctx)
		}
	}
}

func (r *ImageStudioRuntime) cleanup(ctx context.Context) {
	now := time.Now()
	_ = r.repo.ExpireRequests(ctx, now)
	artifacts, err := r.repo.ListExpiredArtifacts(ctx, now, 100)
	if err == nil {
		for _, artifact := range artifacts {
			if removeErr := r.store.Remove(artifact.StorageKey); removeErr != nil {
				continue
			}
			_ = r.repo.DeleteArtifact(ctx, artifact.ID)
		}
	}
	_ = r.repo.DeleteExpiredJobs(ctx, now)
}
