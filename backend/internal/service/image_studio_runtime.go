package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ImageStudioRequestOrigin is the client address and User-Agent of the request
// that submitted or retried an Image Studio job.
type ImageStudioRequestOrigin struct {
	ClientIP  string
	UserAgent string
}

type imageStudioRequestOriginKey struct{}

// WithImageStudioRequestOrigin records the submitting client for Create/Retry.
func WithImageStudioRequestOrigin(ctx context.Context, origin ImageStudioRequestOrigin) context.Context {
	return context.WithValue(ctx, imageStudioRequestOriginKey{}, origin)
}

func imageStudioRequestOriginFromContext(ctx context.Context) ImageStudioRequestOrigin {
	if ctx == nil {
		return ImageStudioRequestOrigin{}
	}
	origin, _ := ctx.Value(imageStudioRequestOriginKey{}).(ImageStudioRequestOrigin)
	return origin
}

// ImageStudioRequestOriginFromContext returns the Image Studio submitter that
// an in-process gateway request carries for its usage and Ops records. The
// gateway request itself carries no client headers, because the images
// handler forwards request headers to API-key upstreams.
func ImageStudioRequestOriginFromContext(ctx context.Context) (ImageStudioRequestOrigin, bool) {
	origin := imageStudioRequestOriginFromContext(ctx)
	return origin, strings.TrimSpace(origin.ClientIP) != "" || strings.TrimSpace(origin.UserAgent) != ""
}

// ImageStudioClientRequestIDPrefix starts the client request ID of every Image
// Studio gateway request. It marks the Ops rows that stay admin-only.
const ImageStudioClientRequestIDPrefix = "image-studio-"

// imageStudioClientRequestIDMaxLen keeps "client:"+ID, the usage billing
// dedupe key, within the 64-character request ID columns.
const imageStudioClientRequestIDMaxLen = 57

// NewImageStudioClientRequestID returns the client request ID of one gateway
// attempt of a job item. It ties Ops and usage rows to the item and is the
// usage billing dedupe key, so every attempt gets its own random suffix: a
// retried or re-run item is billed again instead of being deduplicated.
func NewImageStudioClientRequestID(jobID, itemID int64) string {
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	id := fmt.Sprintf("%sjob-%d-item-%d-%s", ImageStudioClientRequestIDPrefix, jobID, itemID, hex.EncodeToString(nonce[:]))
	if len(id) > imageStudioClientRequestIDMaxLen {
		var wide [16]byte
		_, _ = rand.Read(wide[:])
		id = ImageStudioClientRequestIDPrefix + hex.EncodeToString(wide[:])
	}
	return id
}

// imageStudioLogLimit bounds the error text an Image Studio failure writes to
// the server log; a response body can be an entire image.
const imageStudioLogLimit = 4 << 10

// ImageStudioLogText returns at most the first 4 KiB of a text for the server
// log, noting how long the full text was.
func ImageStudioLogText(text string) string {
	if len(text) <= imageStudioLogLimit {
		return strings.ToValidUTF8(text, "\uFFFD")
	}
	return strings.ToValidUTF8(text[:imageStudioLogLimit], "\uFFFD") + fmt.Sprintf("...<%d bytes in total>", len(text))
}

// imageStudioOrigins keeps the submitting client of recent jobs in memory, so
// the gateway request of a job item carries the real client IP and User-Agent
// into usage and Ops records. Without a database column it does not survive a
// restart, and a job processed by another instance falls back to no origin.
type imageStudioOrigins struct {
	mu      sync.Mutex
	entries map[int64]imageStudioOriginEntry
}

type imageStudioOriginEntry struct {
	origin ImageStudioRequestOrigin
	at     time.Time
}

func (o *imageStudioOrigins) remember(jobID int64, origin ImageStudioRequestOrigin, now time.Time) {
	if o == nil || jobID <= 0 || (origin.ClientIP == "" && origin.UserAgent == "") {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.entries == nil {
		o.entries = make(map[int64]imageStudioOriginEntry)
	}
	for id, entry := range o.entries {
		if now.Sub(entry.at) > ImageStudioFileRetention {
			delete(o.entries, id)
		}
	}
	o.entries[jobID] = imageStudioOriginEntry{origin: origin, at: now}
}

func (o *imageStudioOrigins) lookup(jobID int64) ImageStudioRequestOrigin {
	if o == nil {
		return ImageStudioRequestOrigin{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.entries[jobID].origin
}

// ImageStudioGatewayError keeps the HTTP status and body the image gateway
// returned for a failed Image Studio request. The server log and the Ops error
// record keep them; the job item shows its user-facing message.
type ImageStudioGatewayError struct {
	StatusCode int
	Body       string
}

func (e *ImageStudioGatewayError) Error() string {
	return fmt.Sprintf("image gateway returned %d: %s", e.StatusCode, e.Body)
}

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
	runtime := &ImageStudioRuntime{
		repo: repo, studio: studio, store: store, executor: executor, options: options.normalized(),
	}
	if studio != nil {
		studio.runtime = runtime
	}
	return runtime
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
	ctx, release := bindImageStudioEnabled(ctx)
	defer release()
	if err := EnsureImageStudioAvailable(ctx); err != nil {
		r.failClaim(ctx, job.ID, claim.Item.ID, err)
		return
	}
	key, err := r.studio.eligibleAPIKey(ctx, job.UserID, job.APIKeyID)
	if err != nil {
		r.failClaim(ctx, job.ID, claim.Item.ID, err)
		return
	}
	request := ImageStudioExecutionRequest{Job: job, Item: claim.Item, APIKey: key, Origin: r.studio.origins.lookup(job.ID)}
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
	artifact, err := r.store.Save(ctx, job.UserID, ImageStudioArtifactOutput, result.Data, result.ContentType)
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
	if err != nil {
		// The job item keeps its user-facing message; administrators get the
		// original error (for a gateway failure its status and body) here.
		slog.Warn("image_studio.item_failed", "job_id", jobID, "item_id", itemID, "code", code, "error", ImageStudioLogText(err.Error()))
	}
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
	if !ImageStudioFeatureEnabled() {
		return
	}
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
