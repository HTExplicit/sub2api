package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	RemoteSkillUpstreamSourceID = "moxinggang"
	RemoteSkillUpstreamRoot     = "https://moxinggang.com/skills/security-research/current"
	RemoteSkillMoxinggangPath   = "/skills/security-research/current"
	RemoteSkillMoxinggangRoot   = RemoteSkillUpstreamRoot
	RemoteSkillSourceMoxinggang = RemoteSkillUpstreamSourceID

	RemoteSkillSyncStatusQueued    = "queued"
	RemoteSkillSyncStatusRunning   = "running"
	RemoteSkillSyncStatusSucceeded = "succeeded"
	RemoteSkillSyncStatusFailed    = "failed"
)

var (
	ErrRemoteSkillSeedUnavailable = errors.New("remote skill seed unavailable")
	ErrRemoteSkillVersionNotFound = errors.New("remote skill candidate not found")
	ErrRemoteSkillSyncNotFound    = errors.New("remote skill sync job not found")
)

type RemoteSkillFileChange struct {
	Path                    string `json:"path"`
	Change                  string `json:"change"`
	Kind                    string `json:"kind"`
	RawSHA256               string `json:"raw_sha256,omitempty"`
	EffectiveSHA256         string `json:"effective_sha256,omitempty"`
	PreviousEffectiveSHA256 string `json:"previous_effective_sha256,omitempty"`
}

type RemoteSkillPromptVersion struct {
	ID              int64     `json:"id"`
	RawSHA256       string    `json:"raw_sha256"`
	EffectiveSHA256 string    `json:"effective_sha256"`
	Diff            string    `json:"diff"`
	CreatedBy       int64     `json:"created_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	RawBody         string    `json:"-"`
	EffectiveBody   string    `json:"-"`
}

type RemoteSkillBundleVersion struct {
	ID                  int64      `json:"id"`
	UpstreamSourceID    string     `json:"upstream_source_id"`
	UpstreamRoot        string     `json:"upstream_root"`
	PublicRoot          string     `json:"public_root"`
	RawTreeSHA256       string     `json:"raw_tree_sha256"`
	EffectiveTreeSHA256 string     `json:"effective_tree_sha256"`
	PromptVersionID     int64      `json:"prompt_version_id"`
	FileCount           int        `json:"file_count"`
	RawTotalBytes       int64      `json:"raw_total_bytes"`
	EffectiveTotalBytes int64      `json:"effective_total_bytes"`
	AddedFiles          int        `json:"added_files"`
	ModifiedFiles       int        `json:"modified_files"`
	DeletedFiles        int        `json:"deleted_files"`
	ScriptChanges       int        `json:"script_changes"`
	BinaryChanges       int        `json:"binary_changes"`
	FetchedAt           time.Time  `json:"fetched_at"`
	CreatedBy           int64      `json:"created_by,omitempty"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	PublishedBy         int64      `json:"published_by,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type RemoteSkillBundleVersionDetail struct {
	RemoteSkillBundleVersion
	Prompt      RemoteSkillPromptVersion `json:"prompt"`
	FileChanges []RemoteSkillFileChange  `json:"file_changes"`
	Verified    bool                     `json:"verified"`
}

type RemoteSkillRegistrySnapshot struct {
	Revision       int64                     `json:"revision"`
	Active         *RemoteSkillBundleVersion `json:"active,omitempty"`
	ActivePrompt   *RemoteSkillPromptVersion `json:"active_prompt,omitempty"`
	Degraded       bool                      `json:"degraded"`
	DegradedReason string                    `json:"degraded_reason,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type RemoteSkillSyncJob struct {
	ID                       int64      `json:"id"`
	Status                   string     `json:"status"`
	ProgressStage            string     `json:"progress_stage"`
	CandidateBundleVersionID int64      `json:"candidate_bundle_version_id,omitempty"`
	PromptCaptureProvided    bool       `json:"prompt_capture_provided"`
	ErrorCode                string     `json:"error_code,omitempty"`
	CreatedBy                int64      `json:"created_by,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	StartedAt                *time.Time `json:"started_at,omitempty"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
}

type RemoteSkillCandidate struct {
	Version        RemoteSkillBundleVersion
	Prompt         RemoteSkillPromptVersion
	RawFiles       map[string][]byte
	EffectiveFiles map[string][]byte
	FileChanges    []RemoteSkillFileChange
}

type RemoteSkillRegistryStore interface {
	EnsureRemoteSkillSeed(context.Context, RemoteSkillCandidate) (RemoteSkillRegistrySnapshot, error)
	LoadRemoteSkillSnapshot(context.Context) (RemoteSkillRegistrySnapshot, error)
	ListRemoteSkillVersions(context.Context) ([]RemoteSkillBundleVersion, error)
	GetRemoteSkillVersion(context.Context, int64) (RemoteSkillBundleVersionDetail, error)
	CreateRemoteSkillSyncJob(context.Context, int64, int64, bool) (RemoteSkillSyncJob, error)
	UpdateRemoteSkillSyncJobStage(context.Context, int64, string) error
	CompleteRemoteSkillSyncJob(context.Context, int64, RemoteSkillCandidate) (RemoteSkillSyncJob, error)
	FailRemoteSkillSyncJob(context.Context, int64, string) error
	GetRemoteSkillSyncJob(context.Context, int64) (RemoteSkillSyncJob, error)
	PublishRemoteSkillVersion(context.Context, int64, int64, int64) (RemoteSkillRegistrySnapshot, error)
	CleanupLegacyRemoteSkillData(context.Context) error
}

type RemoteSkillRegistryFiles interface {
	LoadSeed(context.Context) (RemoteSkillCandidate, error)
	InstallCandidate(context.Context, RemoteSkillCandidate) error
	LoadCandidate(context.Context, RemoteSkillBundleVersion, RemoteSkillPromptVersion, []RemoteSkillFileChange) (RemoteSkillCandidate, error)
	CleanupLegacy(context.Context) error
}

type RemoteSkillCandidateSource interface {
	Build(context.Context, RemoteSkillPromptCapture, *RemoteSkillCandidate) (RemoteSkillCandidate, error)
}

type RemoteSkillRegistryRevisionBus interface {
	Publish(context.Context, int64, string) error
	Subscribe(context.Context, func(int64, string)) error
}

type RemoteSkillRegistryService struct {
	store  RemoteSkillRegistryStore
	bus    RemoteSkillRegistryRevisionBus
	files  RemoteSkillRegistryFiles
	source RemoteSkillCandidateSource

	snapshot    atomic.Pointer[RemoteSkillRegistrySnapshot]
	publication atomic.Pointer[RemoteSkillPublication]
	stateMu     sync.Mutex
	applyMu     sync.Mutex
	runMu       sync.Mutex
	runCtx      context.Context
	cancel      context.CancelFunc
	started     bool
	wg          sync.WaitGroup
}

func NewRemoteSkillRegistryService(
	store RemoteSkillRegistryStore,
	bus RemoteSkillRegistryRevisionBus,
	files RemoteSkillRegistryFiles,
	source RemoteSkillCandidateSource,
) *RemoteSkillRegistryService {
	return &RemoteSkillRegistryService{store: store, bus: bus, files: files, source: source}
}

func (s *RemoteSkillRegistryService) Initialize(ctx context.Context) error {
	if s == nil || s.store == nil || s.files == nil {
		return errors.New("remote skill registry unavailable")
	}
	seed, err := s.files.LoadSeed(ctx)
	if err != nil {
		return fmt.Errorf("load paired remote skill seed: %w", err)
	}
	if err := s.files.InstallCandidate(ctx, seed); err != nil {
		return fmt.Errorf("install paired remote skill seed: %w", err)
	}
	if _, err := s.store.EnsureRemoteSkillSeed(ctx, seed); err != nil {
		return fmt.Errorf("ensure paired remote skill seed: %w", err)
	}
	if err := s.Reload(ctx); err != nil {
		return err
	}
	if err := s.store.CleanupLegacyRemoteSkillData(ctx); err != nil {
		return fmt.Errorf("remove legacy remote skill database state: %w", err)
	}
	if err := s.files.CleanupLegacy(ctx); err != nil {
		return fmt.Errorf("remove legacy remote skill files: %w", err)
	}
	return nil
}

func (s *RemoteSkillRegistryService) Start(ctx context.Context) error {
	if err := s.Initialize(ctx); err != nil {
		return err
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.started {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.runCtx = runCtx
	s.cancel = cancel
	s.started = true
	if s.bus != nil {
		s.wg.Add(1)
		go s.subscribeRevisions(runCtx)
	}
	return nil
}

func (s *RemoteSkillRegistryService) subscribeRevisions(ctx context.Context) {
	defer s.wg.Done()
	for ctx.Err() == nil {
		_ = s.bus.Subscribe(ctx, func(revision int64, _ string) {
			if current := s.CurrentSnapshot(); revision == 0 || revision > current.Revision {
				_ = s.Reload(ctx)
			}
		})
		if ctx.Err() != nil {
			return
		}
		s.markDegraded("revision_subscription_failed")
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *RemoteSkillRegistryService) Stop() {
	if s == nil {
		return
	}
	s.runMu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.runCtx = nil
	s.cancel = nil
	s.started = false
	s.runMu.Unlock()
	s.wg.Wait()
}

func (s *RemoteSkillRegistryService) CurrentSnapshot() RemoteSkillRegistrySnapshot {
	if s == nil {
		return RemoteSkillRegistrySnapshot{Degraded: true, DegradedReason: "service_unavailable"}
	}
	current := s.snapshot.Load()
	if current == nil {
		return RemoteSkillRegistrySnapshot{Degraded: true, DegradedReason: "not_loaded"}
	}
	return cloneRemoteSkillRegistrySnapshot(*current)
}

func (s *RemoteSkillRegistryService) Reload(ctx context.Context) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	snapshot, err := s.store.LoadRemoteSkillSnapshot(ctx)
	if err != nil {
		return s.retainLastKnownGood(err)
	}
	if err := s.loadAndInstallSnapshot(ctx, snapshot); err != nil {
		return s.retainLastKnownGood(err)
	}
	return nil
}

func (s *RemoteSkillRegistryService) loadAndInstallSnapshot(ctx context.Context, snapshot RemoteSkillRegistrySnapshot) error {
	if snapshot.Active == nil || snapshot.ActivePrompt == nil || snapshot.Active.PromptVersionID != snapshot.ActivePrompt.ID {
		return fmt.Errorf("%w: active candidate and prompt are not paired", ErrBusinessSystemPromptBundleUnavailable)
	}
	detail, err := s.store.GetRemoteSkillVersion(ctx, snapshot.Active.ID)
	if err != nil {
		return err
	}
	if detail.Prompt.ID != snapshot.ActivePrompt.ID {
		return fmt.Errorf("%w: active prompt identity mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	candidate, err := s.files.LoadCandidate(ctx, detail.RemoteSkillBundleVersion, detail.Prompt, detail.FileChanges)
	if err != nil {
		return err
	}
	publication, err := remoteSkillPublicationFromCandidate(snapshot.Revision, candidate)
	if err != nil {
		return err
	}
	snapshot.Active = &candidate.Version
	snapshot.ActivePrompt = &candidate.Prompt
	snapshot.Degraded = false
	snapshot.DegradedReason = ""
	s.installPairedSnapshot(snapshot, publication)
	return nil
}

func (s *RemoteSkillRegistryService) StartSync(ctx context.Context, promptCapture []byte, actorID, expectedRevision int64) (RemoteSkillSyncJob, error) {
	if s == nil || s.store == nil || s.source == nil {
		return RemoteSkillSyncJob{}, errors.New("remote skill sync unavailable")
	}
	capture, provided, err := s.resolvePromptCapture(promptCapture)
	if err != nil {
		return RemoteSkillSyncJob{}, err
	}
	job, err := s.store.CreateRemoteSkillSyncJob(ctx, actorID, expectedRevision, provided)
	if err != nil {
		return RemoteSkillSyncJob{}, err
	}
	s.runMu.Lock()
	if !s.started || s.runCtx == nil {
		s.runMu.Unlock()
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "service_stopped")
		return RemoteSkillSyncJob{}, errors.New("remote skill sync service is not running")
	}
	runCtx := s.runCtx
	s.wg.Add(1)
	s.runMu.Unlock()
	go func() {
		defer s.wg.Done()
		s.runSyncJob(runCtx, job, capture)
	}()
	return job, nil
}

func (s *RemoteSkillRegistryService) resolvePromptCapture(raw []byte) (RemoteSkillPromptCapture, bool, error) {
	if len(raw) > 0 {
		capture, err := buildRemoteSkillPromptCapture(raw)
		return capture, true, err
	}
	publication := s.publication.Load()
	if publication == nil || strings.TrimSpace(publication.RawPromptBody) == "" {
		return RemoteSkillPromptCapture{}, false, fmt.Errorf("%w: active prompt capture unavailable", ErrBusinessSystemPromptUnavailable)
	}
	capture, err := buildRemoteSkillPromptCapture([]byte(publication.RawPromptBody))
	return capture, false, err
}

func (s *RemoteSkillRegistryService) runSyncJob(ctx context.Context, job RemoteSkillSyncJob, prompt RemoteSkillPromptCapture) {
	if err := s.store.UpdateRemoteSkillSyncJobStage(ctx, job.ID, "fetching_source"); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
		return
	}
	var active *RemoteSkillCandidate
	if publication := s.publication.Load(); publication != nil {
		active = remoteSkillCandidateFromPublication(*publication)
	}
	candidate, err := s.source.Build(ctx, prompt, active)
	if err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, remoteSkillSyncErrorCode(err))
		return
	}
	if err := s.store.UpdateRemoteSkillSyncJobStage(ctx, job.ID, "verifying_candidate"); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
		return
	}
	candidate.Version.CreatedBy = job.CreatedBy
	candidate.Prompt.CreatedBy = job.CreatedBy
	if err := s.files.InstallCandidate(ctx, candidate); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, remoteSkillSyncErrorCode(err))
		return
	}
	if _, err := s.store.CompleteRemoteSkillSyncJob(ctx, job.ID, candidate); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
	}
}

func (s *RemoteSkillRegistryService) PublishVersion(ctx context.Context, versionID, expectedRevision, actorID int64) (RemoteSkillRegistrySnapshot, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	detail, err := s.store.GetRemoteSkillVersion(ctx, versionID)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, err
	}
	candidate, err := s.files.LoadCandidate(ctx, detail.RemoteSkillBundleVersion, detail.Prompt, detail.FileChanges)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: paired candidate validation failed", ErrBusinessSystemPromptUnavailable)
	}
	// Validate and materialize the paired public snapshot before the database
	// CAS.  A post-CAS conversion failure would otherwise leave the database
	// pointing at a pair that the gateway cannot serve.
	publication, err := remoteSkillPublicationFromCandidate(expectedRevision+1, candidate)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: paired candidate validation failed", ErrBusinessSystemPromptUnavailable)
	}
	snapshot, err := s.store.PublishRemoteSkillVersion(ctx, versionID, expectedRevision, actorID)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, err
	}
	publication.Revision = snapshot.Revision
	snapshot.Active = &candidate.Version
	snapshot.ActivePrompt = &candidate.Prompt
	s.installPairedSnapshot(snapshot, publication)
	if s.bus != nil {
		if err := s.bus.Publish(ctx, snapshot.Revision, candidate.Version.EffectiveTreeSHA256); err != nil {
			snapshot.Degraded = true
			snapshot.DegradedReason = "revision_broadcast_failed"
			s.installPairedSnapshot(snapshot, publication)
		}
	}
	return snapshot, nil
}

func (s *RemoteSkillRegistryService) ListVersions(ctx context.Context) ([]RemoteSkillBundleVersion, error) {
	return s.store.ListRemoteSkillVersions(ctx)
}

func (s *RemoteSkillRegistryService) GetVersion(ctx context.Context, id int64) (RemoteSkillBundleVersion, error) {
	detail, err := s.store.GetRemoteSkillVersion(ctx, id)
	return detail.RemoteSkillBundleVersion, err
}

func (s *RemoteSkillRegistryService) InspectVersion(ctx context.Context, id int64) (RemoteSkillBundleVersionDetail, error) {
	detail, err := s.store.GetRemoteSkillVersion(ctx, id)
	if err != nil {
		return RemoteSkillBundleVersionDetail{}, err
	}
	if _, err := s.files.LoadCandidate(ctx, detail.RemoteSkillBundleVersion, detail.Prompt, detail.FileChanges); err != nil {
		return RemoteSkillBundleVersionDetail{}, fmt.Errorf("%w: paired candidate validation failed", ErrBusinessSystemPromptUnavailable)
	}
	detail.Verified = true
	return detail, nil
}

func (s *RemoteSkillRegistryService) GetSyncJob(ctx context.Context, id int64) (RemoteSkillSyncJob, error) {
	return s.store.GetRemoteSkillSyncJob(ctx, id)
}

func (s *RemoteSkillRegistryService) installPairedSnapshot(snapshot RemoteSkillRegistrySnapshot, publication RemoteSkillPublication) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if current := s.snapshot.Load(); current != nil && current.Revision > snapshot.Revision {
		return
	}
	clonedSnapshot := cloneRemoteSkillRegistrySnapshot(snapshot)
	clonedPublication := cloneRemoteSkillPublication(publication)
	s.publication.Store(&clonedPublication)
	s.snapshot.Store(&clonedSnapshot)
}

func (s *RemoteSkillRegistryService) retainLastKnownGood(err error) error {
	s.markDegraded("reload_failed")
	return err
}

func (s *RemoteSkillRegistryService) markDegraded(reason string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if current := s.snapshot.Load(); current != nil {
		degraded := cloneRemoteSkillRegistrySnapshot(*current)
		degraded.Degraded = true
		degraded.DegradedReason = reason
		s.snapshot.Store(&degraded)
	}
}

func cloneRemoteSkillRegistrySnapshot(snapshot RemoteSkillRegistrySnapshot) RemoteSkillRegistrySnapshot {
	if snapshot.Active != nil {
		active := *snapshot.Active
		snapshot.Active = &active
	}
	if snapshot.ActivePrompt != nil {
		prompt := *snapshot.ActivePrompt
		snapshot.ActivePrompt = &prompt
	}
	return snapshot
}

func remoteSkillSyncErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "sync_timeout"
	case errors.Is(err, ErrBusinessSystemPromptInvalid):
		return "prompt_invalid"
	case errors.Is(err, ErrBusinessSystemPromptBundleInvalid):
		return "candidate_invalid"
	case errors.Is(err, ErrBusinessSystemPromptBundleUnavailable):
		return "source_unavailable"
	default:
		return "sync_failed"
	}
}
