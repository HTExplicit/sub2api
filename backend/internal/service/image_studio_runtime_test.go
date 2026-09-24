//go:build unit

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"

	"github.com/stretchr/testify/require"
)

type imageStudioRuntimeRepoFake struct {
	ImageStudioRepository
	claims    chan *ImageStudioClaim
	recovered atomic.Int32
	completed atomic.Int32
	failed    atomic.Int32
}

func TestImageStudioDisableCancelsHostExecutionAndKeepsSavedInputs(t *testing.T) {
	ConfigureImageTools(&extensionv1.ImageToolsConfig{StudioEnabled: true})
	t.Cleanup(func() { ConfigureImageTools(nil) })
	group, key, account := canonicalImageStudioFixture()
	repo := &imageStudioRuntimeRepoFake{}
	store := &imageStudioStoreFake{}
	executor := &blockingImageStudioExecutor{started: make(chan struct{}, 1), release: make(chan struct{})}
	studio := NewImageStudioService(repo, &imageStudioAPIKeyRepoFake{keys: []APIKey{key}}, &imageStudioAccountReaderFake{accounts: map[int64][]Account{group.ID: {account}}}, store)
	runtime := NewImageStudioRuntime(repo, studio, store, executor, ImageStudioRuntimeOptions{})
	claim := &ImageStudioClaim{Job: ImageStudioJob{ID: 1, UserID: key.UserID, APIKeyID: key.ID, Model: ImageStudioModelGPTImage2, Mode: ImageStudioModeGenerate, Prompt: "draw", Count: 1}, Item: ImageStudioItem{ID: 1, JobID: 1}}
	done := make(chan struct{})
	go func() { defer close(done); runtime.processClaim(context.Background(), claim) }()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("execution did not start")
	}
	ConfigureImageTools(&extensionv1.ImageToolsConfig{})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabling Image Studio did not cancel execution")
	}
	require.EqualValues(t, 1, repo.failed.Load())
	require.Zero(t, repo.completed.Load())
	require.Empty(t, store.removed)
	runtime.processClaim(context.Background(), claim)
	require.EqualValues(t, 2, repo.failed.Load(), "pending work fails without starting more upstream IO")
	require.Zero(t, executor.active.Load())
}

func (f *imageStudioRuntimeRepoFake) RecoverInterrupted(context.Context) error {
	f.recovered.Add(1)
	return nil
}

func (f *imageStudioRuntimeRepoFake) ClaimNext(ctx context.Context) (*ImageStudioClaim, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case claim := <-f.claims:
		if claim == nil {
			return nil, ErrImageStudioNoWork
		}
		return claim, nil
	default:
		return nil, ErrImageStudioNoWork
	}
}

func (f *imageStudioRuntimeRepoFake) CompleteSuccess(context.Context, int64, ImageStudioInputArtifact, string, time.Time) error {
	f.completed.Add(1)
	return nil
}

func (f *imageStudioRuntimeRepoFake) CompleteFailure(context.Context, int64, string, string) error {
	f.failed.Add(1)
	return nil
}

func (f *imageStudioRuntimeRepoFake) Finalize(_ context.Context, jobID int64) (*ImageStudioJob, error) {
	return &ImageStudioJob{ID: jobID, Status: ImageStudioJobSucceeded}, nil
}

func (f *imageStudioRuntimeRepoFake) ExpireRequests(context.Context, time.Time) error { return nil }
func (f *imageStudioRuntimeRepoFake) ListExpiredArtifacts(context.Context, time.Time, int) ([]ImageStudioArtifact, error) {
	return nil, nil
}
func (f *imageStudioRuntimeRepoFake) DeleteExpiredJobs(context.Context, time.Time) error { return nil }

type blockingImageStudioExecutor struct {
	active  atomic.Int32
	max     atomic.Int32
	started chan struct{}
	release <-chan struct{}
	mu      sync.Mutex
}

func (e *blockingImageStudioExecutor) Execute(ctx context.Context, _ ImageStudioExecutionRequest) (*ImageStudioExecutionResult, error) {
	active := e.active.Add(1)
	defer e.active.Add(-1)
	for {
		current := e.max.Load()
		if active <= current || e.max.CompareAndSwap(current, active) {
			break
		}
	}
	e.started <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.release:
		return &ImageStudioExecutionResult{
			Data: []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1}, ContentType: "image/png",
		}, nil
	}
}

func TestImageStudioRuntimeRecoversInterruptedAndCapsGlobalExecutionAtFour(t *testing.T) {
	previous := cindyRolloutFeatures.imageStudio
	cindyRolloutFeatures.imageStudio = true
	t.Cleanup(func() { cindyRolloutFeatures.imageStudio = previous })
	group, key, account := canonicalImageStudioFixture()
	repo := &imageStudioRuntimeRepoFake{claims: make(chan *ImageStudioClaim, 8)}
	for i := 1; i <= 8; i++ {
		repo.claims <- &ImageStudioClaim{
			Job:  ImageStudioJob{ID: int64(i), UserID: 7, APIKeyID: key.ID, Mode: ImageStudioModeGenerate, Model: ImageStudioModelGPTImage2, Prompt: "draw", Count: 1},
			Item: ImageStudioItem{ID: int64(i), JobID: int64(i), Ordinal: 1, Status: ImageStudioItemRunning},
		}
	}
	release := make(chan struct{})
	executor := &blockingImageStudioExecutor{started: make(chan struct{}, 8), release: release}
	store := &imageStudioStoreFake{}
	studio := NewImageStudioService(
		repo,
		&imageStudioAPIKeyRepoFake{keys: []APIKey{key}},
		&imageStudioAccountReaderFake{accounts: map[int64][]Account{group.ID: {account}}},
		store,
	)
	runtime := NewImageStudioRuntime(repo, studio, store, executor, ImageStudioRuntimeOptions{
		Workers: 4, PollInterval: time.Millisecond, CleanupInterval: time.Hour,
	})

	require.NoError(t, runtime.Start(context.Background()))
	for i := 0; i < 4; i++ {
		select {
		case <-executor.started:
		case <-time.After(time.Second):
			t.Fatal("four workers did not start")
		}
	}
	select {
	case <-executor.started:
		t.Fatal("more than four image calls ran concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.Eventually(t, func() bool { return repo.completed.Load() == 8 }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(4), executor.max.Load())
	require.Equal(t, int32(1), repo.recovered.Load())

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, runtime.Stop(stopCtx))
}
