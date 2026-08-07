package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	RemoteSkillSyncStatusQueued    = "queued"
	RemoteSkillSyncStatusRunning   = "running"
	RemoteSkillSyncStatusSucceeded = "succeeded"
	RemoteSkillSyncStatusFailed    = "failed"
)

var (
	ErrRemoteSkillSeedUnavailable = errors.New("remote skill seed unavailable")
	ErrRemoteSkillVersionNotFound = errors.New("remote skill bundle version not found")
	ErrRemoteSkillSyncNotFound    = errors.New("remote skill sync job not found")
)

type RemoteSkillBundleVersion struct {
	ID             int64      `json:"id"`
	BundleID       string     `json:"bundle_id"`
	SourceCommit   string     `json:"source_commit"`
	OverlaySHA256  string     `json:"overlay_sha256"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	ArchiveSHA256  string     `json:"archive_sha256"`
	FileCount      int        `json:"file_count"`
	TotalBytes     int64      `json:"total_bytes"`
	AddedFiles     int        `json:"added_files"`
	ModifiedFiles  int        `json:"modified_files"`
	DeletedFiles   int        `json:"deleted_files"`
	ScriptChanges  int        `json:"script_changes"`
	BinaryChanges  int        `json:"binary_changes"`
	CreatedBy      int64      `json:"created_by,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	PublishedBy    int64      `json:"published_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type RemoteSkillBundleVersionDetail struct {
	RemoteSkillBundleVersion
	Verified bool `json:"verified"`
}

type RemoteSkillRegistrySnapshot struct {
	Revision       int64                     `json:"revision"`
	Active         *RemoteSkillBundleVersion `json:"active,omitempty"`
	Degraded       bool                      `json:"degraded"`
	DegradedReason string                    `json:"degraded_reason,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type RemoteSkillSyncJob struct {
	ID                       int64      `json:"id"`
	Status                   string     `json:"status"`
	ProgressStage            string     `json:"progress_stage"`
	SourceCommit             string     `json:"source_commit,omitempty"`
	CandidateBundleVersionID int64      `json:"candidate_bundle_version_id,omitempty"`
	ErrorCode                string     `json:"error_code,omitempty"`
	CreatedBy                int64      `json:"created_by,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	StartedAt                *time.Time `json:"started_at,omitempty"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
}

type RemoteSkillCandidate struct {
	Version       RemoteSkillBundleVersion
	Manifest      BusinessSystemPromptBundleManifest
	ManifestBytes []byte
	ArchiveBytes  []byte
	Files         map[string][]byte
}

type RemoteSkillRegistryStore interface {
	EnsureRemoteSkillSeed(context.Context, RemoteSkillBundleVersion) error
	LoadRemoteSkillSnapshot(context.Context) (RemoteSkillRegistrySnapshot, error)
	ListRemoteSkillVersions(context.Context) ([]RemoteSkillBundleVersion, error)
	GetRemoteSkillVersion(context.Context, int64) (RemoteSkillBundleVersion, error)
	CreateRemoteSkillSyncJob(context.Context, int64, int64) (RemoteSkillSyncJob, error)
	UpdateRemoteSkillSyncJobStage(context.Context, int64, string) error
	CompleteRemoteSkillSyncJob(context.Context, int64, RemoteSkillBundleVersion) (RemoteSkillSyncJob, error)
	FailRemoteSkillSyncJob(context.Context, int64, string) error
	GetRemoteSkillSyncJob(context.Context, int64) (RemoteSkillSyncJob, error)
	PublishRemoteSkillVersion(context.Context, int64, int64, int64) (RemoteSkillRegistrySnapshot, error)
}

type RemoteSkillRegistryFiles interface {
	LoadSeed(context.Context) (RemoteSkillBundleVersion, error)
	InstallCandidate(context.Context, RemoteSkillCandidate) error
	ValidateVersion(context.Context, RemoteSkillBundleVersion) error
	PreparePublic(context.Context, RemoteSkillBundleVersion) error
	Activate(context.Context, RemoteSkillRegistrySnapshot) error
	LoadManifest(context.Context, RemoteSkillBundleVersion) (BusinessSystemPromptBundleManifest, error)
}

type RemoteSkillCandidateSource interface {
	Build(context.Context, *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error)
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

	snapshot atomic.Pointer[RemoteSkillRegistrySnapshot]
	stateMu  sync.Mutex
	applyMu  sync.Mutex
	runMu    sync.Mutex
	runCtx   context.Context
	cancel   context.CancelFunc
	started  bool
	wg       sync.WaitGroup
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
	if err == nil {
		if err := s.store.EnsureRemoteSkillSeed(ctx, seed); err != nil {
			return fmt.Errorf("ensure remote skill seed: %w", err)
		}
	} else if !errors.Is(err, ErrRemoteSkillSeedUnavailable) {
		return fmt.Errorf("load remote skill seed: %w", err)
	}
	return s.Reload(ctx)
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
		go func() {
			defer s.wg.Done()
			for runCtx.Err() == nil {
				_ = s.bus.Subscribe(runCtx, func(revision int64, _ string) {
					current := s.CurrentSnapshot()
					if revision == 0 || revision > current.Revision {
						_ = s.Reload(runCtx)
					}
				})
				if runCtx.Err() != nil {
					return
				}
				s.markDegraded("revision_subscription_failed")
				timer := time.NewTimer(time.Second)
				select {
				case <-runCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	return nil
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
	loaded, err := s.store.LoadRemoteSkillSnapshot(ctx)
	if err != nil {
		return s.retainLastKnownGood(err)
	}
	if loaded.Active == nil {
		loaded.Degraded = true
		loaded.DegradedReason = "no_active_bundle"
		s.installSnapshot(loaded)
		return nil
	}
	if err := s.files.ValidateVersion(ctx, *loaded.Active); err != nil {
		return s.retainLastKnownGood(fmt.Errorf("validate active remote skill: %w", err))
	}
	if err := s.files.PreparePublic(ctx, *loaded.Active); err != nil {
		return s.retainLastKnownGood(fmt.Errorf("prepare public remote skill: %w", err))
	}
	if err := s.files.Activate(ctx, loaded); err != nil {
		return s.retainLastKnownGood(fmt.Errorf("activate public remote skill descriptor: %w", err))
	}
	loaded.Degraded = false
	loaded.DegradedReason = ""
	s.installSnapshot(loaded)
	return nil
}

func (s *RemoteSkillRegistryService) StartSync(ctx context.Context, actorID, expectedRevision int64) (RemoteSkillSyncJob, error) {
	if s == nil || s.store == nil || s.source == nil {
		return RemoteSkillSyncJob{}, errors.New("remote skill sync unavailable")
	}
	job, err := s.store.CreateRemoteSkillSyncJob(ctx, actorID, expectedRevision)
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
		s.runSyncJob(runCtx, job)
	}()
	return job, nil
}

func (s *RemoteSkillRegistryService) runSyncJob(ctx context.Context, job RemoteSkillSyncJob) {
	if err := s.store.UpdateRemoteSkillSyncJobStage(ctx, job.ID, "fetching_source"); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
		return
	}
	var activeManifest *BusinessSystemPromptBundleManifest
	current := s.CurrentSnapshot()
	if current.Active != nil {
		if manifest, err := s.files.LoadManifest(ctx, *current.Active); err == nil {
			activeManifest = &manifest
		}
	}
	candidate, err := s.source.Build(ctx, activeManifest)
	if err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, remoteSkillSyncErrorCode(err))
		return
	}
	if err := s.store.UpdateRemoteSkillSyncJobStage(ctx, job.ID, "verifying_candidate"); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
		return
	}
	if err := s.files.InstallCandidate(ctx, candidate); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, remoteSkillSyncErrorCode(err))
		return
	}
	candidate.Version.CreatedBy = job.CreatedBy
	if _, err := s.store.CompleteRemoteSkillSyncJob(ctx, job.ID, candidate.Version); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
	}
}

func (s *RemoteSkillRegistryService) PublishVersion(ctx context.Context, versionID, expectedRevision, actorID int64) (RemoteSkillRegistrySnapshot, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	version, err := s.store.GetRemoteSkillVersion(ctx, versionID)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, err
	}
	if err := s.files.ValidateVersion(ctx, version); err != nil {
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: candidate validation failed", ErrBusinessSystemPromptUnavailable)
	}
	if err := s.files.PreparePublic(ctx, version); err != nil {
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: public skill preparation failed", ErrBusinessSystemPromptUnavailable)
	}
	snapshot, err := s.store.PublishRemoteSkillVersion(ctx, versionID, expectedRevision, actorID)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, err
	}
	if err := s.files.Activate(ctx, snapshot); err != nil {
		_ = s.retainLastKnownGood(err)
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: public descriptor activation failed", ErrBusinessSystemPromptUnavailable)
	}
	s.installSnapshot(snapshot)
	if s.bus != nil && snapshot.Active != nil {
		if err := s.bus.Publish(ctx, snapshot.Revision, snapshot.Active.ManifestSHA256); err != nil {
			snapshot.Degraded = true
			snapshot.DegradedReason = "revision_broadcast_failed"
			s.installSnapshot(snapshot)
		}
	}
	return snapshot, nil
}

func (s *RemoteSkillRegistryService) ListVersions(ctx context.Context) ([]RemoteSkillBundleVersion, error) {
	return s.store.ListRemoteSkillVersions(ctx)
}

func (s *RemoteSkillRegistryService) GetVersion(ctx context.Context, id int64) (RemoteSkillBundleVersion, error) {
	return s.store.GetRemoteSkillVersion(ctx, id)
}

func (s *RemoteSkillRegistryService) InspectVersion(ctx context.Context, id int64) (RemoteSkillBundleVersionDetail, error) {
	version, err := s.store.GetRemoteSkillVersion(ctx, id)
	if err != nil {
		return RemoteSkillBundleVersionDetail{}, err
	}
	if err := s.files.ValidateVersion(ctx, version); err != nil {
		return RemoteSkillBundleVersionDetail{}, fmt.Errorf("%w: candidate validation failed", ErrBusinessSystemPromptUnavailable)
	}
	return RemoteSkillBundleVersionDetail{RemoteSkillBundleVersion: version, Verified: true}, nil
}

func (s *RemoteSkillRegistryService) GetSyncJob(ctx context.Context, id int64) (RemoteSkillSyncJob, error) {
	return s.store.GetRemoteSkillSyncJob(ctx, id)
}

func (s *RemoteSkillRegistryService) installSnapshot(snapshot RemoteSkillRegistrySnapshot) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	current := s.snapshot.Load()
	if current != nil && current.Revision > snapshot.Revision {
		return
	}
	cloned := cloneRemoteSkillRegistrySnapshot(snapshot)
	s.snapshot.Store(&cloned)
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
	return snapshot
}

func remoteSkillSyncErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "sync_timeout"
	case errors.Is(err, ErrBusinessSystemPromptBundleInvalid):
		return "bundle_invalid"
	case errors.Is(err, ErrBusinessSystemPromptBundleUnavailable):
		return "source_unavailable"
	default:
		return "sync_failed"
	}
}
