package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrBusinessSystemPromptTemplateNotFound  = errors.New("business system prompt template not found")
	ErrBusinessSystemPromptVersionNotFound   = errors.New("business system prompt version not found")
	ErrBusinessSystemPromptRevisionConflict  = errors.New("business system prompt revision conflict")
	ErrBusinessSystemPromptSeedProtected     = errors.New("business system prompt seed is protected")
	ErrBusinessSystemPromptActive            = errors.New("active business system prompt cannot be deleted")
	ErrBusinessSystemPromptLegacyComposition = errors.New("legacy business system prompt composition is read-only")
	ErrBusinessSystemPromptSourceNotManaged  = errors.New("business system prompt template is not managed by this source")
)

const (
	businessSystemPromptSeedSlug                   = "codexrip_reverse_skill"
	gpt56InstructPromptSeedSlug                    = "gpt_5_6_instruct"
	businessSystemPromptV7SHA256                   = "0b717f086b1bf25e8300e9f26578ee95cf6f74d5601c06b9f9e493aa8939b0a7"
	businessSystemPromptV8SHA256                   = "9143d8a97727030192a62fb19f732b0823dec9ffe83081ef5ae27fdb1edfea04"
	businessSystemPromptRelease0172Codexrip3SHA256 = "0615d24958a1da11edcf9538aaff989e46fcd296ea86a6c1b1af2b3efa48487f"
	businessSystemPromptRelease0172Codexrip5SHA256 = "5813c55c0763e1472becec874232f3daafb28a69107b94ca8284daf44fceb2a0"
)

type BusinessSystemPromptSeed struct {
	Slug                 string
	Name                 string
	Description          string
	ManagedSource        string
	Body                 string
	Note                 string
	SHA256               string
	ByteLength           int
	CompositionMode      string
	BundleID             string
	BundleManifestSHA256 string
	SourceRepository     string
	SourceCommit         string
	SourceVersion        string
	SourceArtifact       string
	SourceArtifactSHA256 string
	SourceLicenseSHA256  string
	UpgradeExistingSeed  bool
	AutoActivateFromSHA  []string
}

type BusinessSystemPromptTemplate struct {
	ID            int64      `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	IsSeed        bool       `json:"is_seed"`
	ManagedSource string     `json:"managed_source,omitempty"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	CreatedBy     *int64     `json:"created_by,omitempty"`
	UpdatedBy     *int64     `json:"updated_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type BusinessSystemPromptVersion struct {
	ID                   int64      `json:"id"`
	TemplateID           int64      `json:"template_id"`
	Version              int64      `json:"version"`
	Body                 string     `json:"body"`
	SHA256               string     `json:"sha256"`
	ByteLength           int        `json:"byte_length"`
	CompositionMode      string     `json:"composition_mode"`
	BundleID             string     `json:"bundle_id,omitempty"`
	BundleManifestSHA256 string     `json:"bundle_manifest_sha256,omitempty"`
	Note                 string     `json:"note"`
	SourceRepository     string     `json:"source_repository,omitempty"`
	SourceCommit         string     `json:"source_commit,omitempty"`
	SourceVersion        string     `json:"source_version,omitempty"`
	SourceArtifact       string     `json:"source_artifact,omitempty"`
	SourceArtifactSHA256 string     `json:"source_artifact_sha256,omitempty"`
	SourceLicenseSHA256  string     `json:"source_license_sha256,omitempty"`
	CreatedBy            *int64     `json:"created_by,omitempty"`
	PublishedAt          *time.Time `json:"published_at,omitempty"`
	PublishedBy          *int64     `json:"published_by,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	IsActive             bool       `json:"is_active"`
}

type BusinessSystemPromptTemplateDetail struct {
	Template BusinessSystemPromptTemplate  `json:"template"`
	Versions []BusinessSystemPromptVersion `json:"versions"`
}

type BusinessSystemPromptTemplateCreate struct {
	Slug                 string `json:"slug"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Body                 string `json:"body"`
	Note                 string `json:"note"`
	CompositionMode      string `json:"composition_mode"`
	BundleID             string `json:"bundle_id"`
	BundleManifestSHA256 string `json:"bundle_manifest_sha256"`
}

type BusinessSystemPromptVersionCreate struct {
	Body                 string `json:"body"`
	Note                 string `json:"note"`
	CompositionMode      string `json:"composition_mode"`
	BundleID             string `json:"bundle_id"`
	BundleManifestSHA256 string `json:"bundle_manifest_sha256"`
}

type BusinessSystemPromptTemplateUpdate struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type BusinessSystemPromptRuntimeUpdate struct {
	ExpectedRevision   int64 `json:"expected_revision"`
	Enabled            bool  `json:"enabled"`
	ExposeServerPrompt bool  `json:"expose_server_prompt"`
	CompactEnabled     bool  `json:"compact_enabled"`
	ActorID            int64 `json:"-"`
}

// BusinessSystemPromptStore contains both the durable catalog and the
// singleton runtime transaction. Implementations must make publish/update
// operations atomic with the expected revision check.
type BusinessSystemPromptStore interface {
	EnsureBusinessSystemPromptSeed(context.Context, BusinessSystemPromptSeed) error
	LoadBusinessSystemPrompt(context.Context) (BusinessSystemPromptSnapshot, error)
	ListBusinessSystemPromptTemplates(context.Context) ([]BusinessSystemPromptTemplate, error)
	GetBusinessSystemPromptTemplate(context.Context, int64) (BusinessSystemPromptTemplateDetail, error)
	CreateBusinessSystemPromptTemplate(context.Context, BusinessSystemPromptTemplateCreate, int64, int64) (BusinessSystemPromptTemplateDetail, error)
	UpdateBusinessSystemPromptTemplate(context.Context, int64, BusinessSystemPromptTemplateUpdate, int64, int64) (BusinessSystemPromptTemplate, error)
	CreateBusinessSystemPromptVersion(context.Context, int64, string, string, int64, int64, int64) (BusinessSystemPromptVersion, error)
	DuplicateBusinessSystemPromptTemplate(context.Context, int64, string, string, int64, int64) (BusinessSystemPromptTemplateDetail, error)
	SoftDeleteBusinessSystemPromptTemplate(context.Context, int64, int64, int64) error
	PublishBusinessSystemPromptVersion(context.Context, int64, int64, int64, int64) (BusinessSystemPromptSnapshot, error)
	UpdateBusinessSystemPromptRuntime(context.Context, BusinessSystemPromptRuntimeUpdate) (BusinessSystemPromptSnapshot, error)
}

// BusinessSystemPromptCompositionStore is implemented by stores that can
// persist content-addressed offline bundle references. It is separate from the
// legacy store method so existing integrations remain source compatible.
type BusinessSystemPromptCompositionStore interface {
	CreateBusinessSystemPromptVersionWithComposition(context.Context, int64, BusinessSystemPromptVersionCreate, int64, int64, int64) (BusinessSystemPromptVersion, error)
}

type BusinessSystemPromptSourceSyncStatus string

const (
	BusinessSystemPromptSourceSyncUpToDate         BusinessSystemPromptSourceSyncStatus = "up_to_date"
	BusinessSystemPromptSourceSyncNoPromptChange   BusinessSystemPromptSourceSyncStatus = "no_prompt_change"
	BusinessSystemPromptSourceSyncCandidateCreated BusinessSystemPromptSourceSyncStatus = "candidate_created"
)

type BusinessSystemPromptSourceSyncResult struct {
	Status  BusinessSystemPromptSourceSyncStatus `json:"status"`
	Version *BusinessSystemPromptVersion         `json:"version,omitempty"`
}

type BusinessSystemPromptSourceStore interface {
	SyncBusinessSystemPromptSourceVersion(context.Context, int64, BusinessSystemPromptSourceCandidate, int64, int64, int64) (BusinessSystemPromptSourceSyncResult, error)
}

type BusinessSystemPromptRevisionBus interface {
	Publish(context.Context, int64) error
	Subscribe(context.Context, func(int64)) error
}

type BusinessSystemPromptRouteMetadataStore interface {
	StoreBusinessSystemPromptRouteMetadata(context.Context, string, BusinessSystemPromptBundleMetadata, time.Duration) error
	LoadBusinessSystemPromptRouteMetadata(context.Context, string) (BusinessSystemPromptBundleMetadata, bool, error)
}

type BusinessSystemPromptService struct {
	store    BusinessSystemPromptStore
	bus      BusinessSystemPromptRevisionBus
	route    BusinessSystemPromptRouteMetadataStore
	bundles  *BusinessSystemPromptBundleRegistry
	registry *RemoteSkillRegistryService
	source   BusinessSystemPromptSource

	snapshot atomic.Pointer[BusinessSystemPromptSnapshot]
	stateMu  sync.Mutex

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	started     bool
}

func NewBusinessSystemPromptService(store BusinessSystemPromptStore, bus BusinessSystemPromptRevisionBus) *BusinessSystemPromptService {
	service := &BusinessSystemPromptService{
		store: store, bus: bus,
		bundles: NewBusinessSystemPromptBundleRegistry(DefaultBusinessSystemPromptBundleRoot()),
		source:  NewGitHubGPT56PromptSource(nil),
	}
	if routeStore, ok := bus.(BusinessSystemPromptRouteMetadataStore); ok {
		service.route = routeStore
	}
	return service
}

func (s *BusinessSystemPromptService) SetBusinessSystemPromptSource(source BusinessSystemPromptSource) {
	if s != nil {
		s.source = source
	}
}

func (s *BusinessSystemPromptService) SetRemoteSkillRegistryService(registry *RemoteSkillRegistryService) {
	if s != nil {
		s.registry = registry
	}
}

func (s *BusinessSystemPromptService) SyncManagedSource(
	ctx context.Context,
	templateID, actorID, expectedLatestVersion, expectedRevision int64,
) (BusinessSystemPromptSourceSyncResult, error) {
	if s == nil || s.store == nil || s.source == nil {
		return BusinessSystemPromptSourceSyncResult{}, ErrBusinessSystemPromptSourceUnavailable
	}
	if templateID <= 0 || actorID <= 0 || expectedLatestVersion <= 0 || expectedRevision <= 0 {
		return BusinessSystemPromptSourceSyncResult{}, ErrBusinessSystemPromptRevisionConflict
	}
	sourceStore, ok := s.store.(BusinessSystemPromptSourceStore)
	if !ok {
		return BusinessSystemPromptSourceSyncResult{}, ErrBusinessSystemPromptSourceUnavailable
	}
	candidate, err := s.source.Fetch(ctx)
	if err != nil {
		return BusinessSystemPromptSourceSyncResult{}, err
	}
	if err := ValidateBusinessSystemPromptSourceCandidate(candidate); err != nil {
		return BusinessSystemPromptSourceSyncResult{}, err
	}
	return sourceStore.SyncBusinessSystemPromptSourceVersion(
		ctx, templateID, candidate, actorID, expectedLatestVersion, expectedRevision,
	)
}

func (s *BusinessSystemPromptService) loadBusinessSystemPromptBundle(bundleID, manifestSHA256 string) (*BusinessSystemPromptBundle, error) {
	// A direct path is retained for isolated tests and single-bundle operators.
	// Production uses the content-addressed registry root so no symlink or
	// mutable "current" directory participates in validation.
	if directPath := strings.TrimSpace(os.Getenv(BusinessSystemPromptBundlePathEnv)); directPath != "" {
		return loadBusinessSystemPromptBundleIdentity(directPath, bundleID, manifestSHA256)
	}
	if s == nil || s.bundles == nil {
		return nil, fmt.Errorf("%w: bundle registry unavailable", ErrBusinessSystemPromptBundleUnavailable)
	}
	return s.bundles.Load(bundleID, manifestSHA256)
}

func (s *BusinessSystemPromptService) StoreResponseRouteMetadata(ctx context.Context, responseID string, application BusinessSystemPromptApplication, ttl time.Duration) error {
	if s == nil || s.route == nil || !application.Applied ||
		(application.CompositionMode != BusinessSystemPromptCompositionOfflineBundle && application.CompositionMode != BusinessSystemPromptCompositionCodexSkillHybrid) {
		return nil
	}
	metadata := BusinessSystemPromptBundleMetadata{
		BundleID: application.BundleID, ManifestSHA256: application.BundleManifestSHA256,
		BaseSHA256: application.BaseSHA256, EffectiveSHA256: application.EffectiveSHA256,
		ByteLength:     application.EffectiveByteLength,
		RouteIDs:       append([]string(nil), application.RouteIDs...),
		DocumentPaths:  append([]string(nil), application.DocumentIDs...),
		ReferencePaths: append([]string(nil), application.ReferenceIDs...),
		Degraded:       application.Degraded,
	}
	if err := ValidateBusinessSystemPromptBundleMetadata(metadata); err != nil {
		return err
	}
	return s.route.StoreBusinessSystemPromptRouteMetadata(ctx, responseID, metadata, ttl)
}

func (s *BusinessSystemPromptService) LoadResponseRouteMetadata(ctx context.Context, responseID string) (*BusinessSystemPromptBundleMetadata, error) {
	if s == nil || s.route == nil || strings.TrimSpace(responseID) == "" {
		return nil, nil
	}
	metadata, found, err := s.route.LoadBusinessSystemPromptRouteMetadata(ctx, responseID)
	if err != nil || !found {
		return nil, err
	}
	if err := ValidateBusinessSystemPromptBundleMetadata(metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func (s *BusinessSystemPromptService) Initialize(ctx context.Context) error {
	if s == nil || s.store == nil {
		return errors.New("business system prompt store unavailable")
	}
	seeds := []BusinessSystemPromptSeed{
		{
			Slug:                businessSystemPromptSeedSlug,
			Name:                "CodexRip Reverse-Skill System Prompt",
			Description:         "高保真 CodexRip 逆向提示词与本机完整 Skill 混合策略。",
			Body:                embeddedBusinessSystemPrompt,
			Note:                "captured reverse-engineered seed",
			CompositionMode:     BusinessSystemPromptCompositionCodexSkillHybrid,
			BundleID:            BusinessSystemPromptRemoteSkillBundleID,
			UpgradeExistingSeed: true,
			AutoActivateFromSHA: []string{
				businessSystemPromptV7SHA256,
				businessSystemPromptV8SHA256,
				businessSystemPromptRelease0172Codexrip3SHA256,
				businessSystemPromptRelease0172Codexrip5SHA256,
			},
		},
		{
			Slug:                 gpt56InstructPromptSeedSlug,
			Name:                 "GPT-5.6 Instruct v45",
			Description:          "MDX-Tom/gpt-5.6-instruct 的内置可选提示词。",
			ManagedSource:        BusinessSystemPromptManagedSourceGPT56,
			Body:                 embeddedGPT56InstructPrompt,
			Note:                 "Imported from MDX-Tom/gpt-5.6-instruct v45",
			CompositionMode:      BusinessSystemPromptCompositionInline,
			SourceRepository:     gpt56PromptRepository,
			SourceCommit:         "77e7a649903f9556f2d7bfa0223fa99e123aad52",
			SourceVersion:        "v45",
			SourceArtifact:       "gpt-5.6-sol-unrestricted-v45.zip",
			SourceArtifactSHA256: "c86c2c6d20a4d1155d87422f485eb37b77539132270918c002b5d8237a5adf54",
			SourceLicenseSHA256:  GPT56PromptLicenseSHA256,
		},
	}
	for i := range seeds {
		hash, byteLength, err := ValidateBusinessSystemPromptBody(seeds[i].Body)
		if err != nil {
			return fmt.Errorf("validate embedded business system prompt %q: %w", seeds[i].Slug, err)
		}
		seeds[i].SHA256 = hash
		seeds[i].ByteLength = byteLength
		if err := s.store.EnsureBusinessSystemPromptSeed(ctx, seeds[i]); err != nil {
			return fmt.Errorf("ensure business system prompt seed %q: %w", seeds[i].Slug, err)
		}
	}
	return s.Reload(ctx)
}

func (s *BusinessSystemPromptService) Start(ctx context.Context) error {
	if err := s.Initialize(ctx); err != nil {
		if _, ok := s.CurrentSnapshot(); !ok || !errors.Is(err, ErrBusinessSystemPromptUnavailable) {
			return err
		}
	}
	s.lifecycleMu.Lock()
	if s.started {
		s.lifecycleMu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	s.lifecycleMu.Unlock()
	if s.bus != nil {
		go func() {
			for runCtx.Err() == nil {
				err := s.bus.Subscribe(runCtx, func(_ int64) {
					if err := s.Reload(runCtx); err != nil {
						// Reload deliberately retains the previous snapshot and marks it degraded.
						return
					}
				})
				if runCtx.Err() != nil {
					return
				}
				if err == nil {
					err = errors.New("business system prompt revision subscription ended")
				}
				_ = s.retainLastGood(fmt.Errorf("business system prompt revision subscription: %w", err))
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

func (s *BusinessSystemPromptService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = nil
	s.started = false
	s.lifecycleMu.Unlock()
}

func (s *BusinessSystemPromptService) CurrentSnapshot() (BusinessSystemPromptSnapshot, bool) {
	if s == nil {
		return BusinessSystemPromptSnapshot{}, false
	}
	current := s.snapshot.Load()
	if current == nil {
		return BusinessSystemPromptSnapshot{}, false
	}
	return *current, true
}

func (s *BusinessSystemPromptService) Reload(ctx context.Context) error {
	if s == nil || s.store == nil {
		return errors.New("business system prompt store unavailable")
	}
	loaded, err := s.store.LoadBusinessSystemPrompt(ctx)
	if err != nil {
		return s.retainLastGood(fmt.Errorf("load business system prompt: %w", err))
	}
	loaded.Degraded = false
	if err := s.prepareBusinessSystemPromptSnapshot(&loaded); err != nil {
		s.stateMu.Lock()
		if s.snapshot.Load() == nil {
			loaded.Degraded = true
			s.snapshot.Store(&loaded)
			s.stateMu.Unlock()
			return err
		}
		s.stateMu.Unlock()
		return s.retainLastGood(err)
	}
	s.stateMu.Lock()
	if current := s.snapshot.Load(); current == nil || loaded.Revision >= current.Revision {
		s.snapshot.Store(&loaded)
	}
	s.stateMu.Unlock()
	return nil
}

func (s *BusinessSystemPromptService) prepareBusinessSystemPromptSnapshot(snapshot *BusinessSystemPromptSnapshot) error {
	if err := validateBusinessSystemPromptSnapshot(snapshot); err != nil {
		return err
	}
	snapshot.RegistryRevision = 0
	snapshot.RegistryManifestSHA256 = ""
	snapshot.RegistryArchiveSHA256 = ""
	snapshot.RegistrySourceCommit = ""
	snapshot.RegistrySourceID = ""
	snapshot.RegistryRemoteRoot = ""
	if snapshot.CompositionMode == BusinessSystemPromptCompositionInline {
		snapshot.requestBundle = nil
		snapshot.BundleAvailable = false
		snapshot.BundleDegraded = false
		snapshot.DegradedReason = ""
		return nil
	}
	if snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
		snapshot.requestBundle = nil
		snapshot.BundleAvailable = false
		snapshot.BundleDegraded = false
		snapshot.DegradedReason = ""
		if s == nil || s.registry == nil {
			return nil
		}
		registrySnapshot := s.registry.CurrentSnapshot()
		if registrySnapshot.Active == nil {
			snapshot.BundleDegraded = registrySnapshot.Degraded
			snapshot.DegradedReason = registrySnapshot.DegradedReason
			snapshot.Degraded = snapshot.Degraded || registrySnapshot.Degraded
			return nil
		}
		snapshot.BundleAvailable = true
		snapshot.BundleDegraded = registrySnapshot.Degraded
		snapshot.DegradedReason = registrySnapshot.DegradedReason
		snapshot.RegistryRevision = registrySnapshot.Revision
		snapshot.RegistryManifestSHA256 = registrySnapshot.Active.ManifestSHA256
		snapshot.RegistryArchiveSHA256 = registrySnapshot.Active.ArchiveSHA256
		snapshot.RegistrySourceCommit = registrySnapshot.Active.SourceCommit
		snapshot.RegistrySourceID = registrySnapshot.Active.SourceID
		snapshot.RegistryRemoteRoot = remoteSkillPublishedRoot(
			registrySnapshot.Active.SourceID,
			registrySnapshot.Active.ManifestSHA256,
		)
		if snapshot.BundleDegraded {
			snapshot.Degraded = true
		}
		return nil
	}
	snapshot.requestBundle = nil
	snapshot.BundleAvailable = false
	snapshot.BundleDegraded = true
	snapshot.DegradedReason = "legacy_composition_read_only"
	snapshot.Degraded = true
	if !snapshot.Enabled {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrBusinessSystemPromptUnavailable, ErrBusinessSystemPromptLegacyComposition)
}

// PrepareBusinessSystemPromptPreviewSnapshot verifies the selected immutable
// version and performs the same deterministic offline compilation used by live
// requests. It never mutates the installed runtime snapshot.
func (s *BusinessSystemPromptService) PrepareBusinessSystemPromptPreviewSnapshot(
	snapshot BusinessSystemPromptSnapshot,
	requestText string,
) (BusinessSystemPromptSnapshot, error) {
	snapshot.Enabled = true
	if snapshot.Revision < 1 {
		snapshot.Revision = 1
	}
	if err := s.prepareBusinessSystemPromptSnapshot(&snapshot); err != nil {
		return BusinessSystemPromptSnapshot{}, err
	}
	return s.compileBusinessSystemPromptSnapshot(snapshot, requestText, false, nil, true)
}

func (s *BusinessSystemPromptService) PrepareBusinessSystemPromptPreviewSnapshotForClient(
	snapshot BusinessSystemPromptSnapshot,
	requestText string,
	clientMode string,
) (BusinessSystemPromptSnapshot, error) {
	snapshot.Enabled = true
	if snapshot.Revision < 1 {
		snapshot.Revision = 1
	}
	if err := s.prepareBusinessSystemPromptSnapshot(&snapshot); err != nil {
		return BusinessSystemPromptSnapshot{}, err
	}
	return s.compileBusinessSystemPromptSnapshot(snapshot, requestText, false, nil, false)
}

func (s *BusinessSystemPromptService) compileBusinessSystemPromptSnapshot(
	snapshot BusinessSystemPromptSnapshot,
	requestText string,
	continuation bool,
	previous *BusinessSystemPromptBundleMetadata,
	inlineDocuments bool,
) (BusinessSystemPromptSnapshot, error) {
	if snapshot.CompositionMode == BusinessSystemPromptCompositionInline {
		return snapshot, nil
	}
	if snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
		snapshot.Body = applyRemoteSkillRoot(snapshot.Body, snapshot.RegistryRemoteRoot)
		baseHash := hashBusinessSystemPromptBundleBytes([]byte(snapshot.Body))
		snapshot.baseSHA256 = baseHash
		snapshot.effectiveSHA256 = baseHash
		snapshot.effectiveByteLength = len([]byte(snapshot.Body))
		snapshot.routeIDs = nil
		snapshot.documentIDs = nil
		snapshot.referenceIDs = nil
		snapshot.omittedDocumentIDs = nil
		return snapshot, nil
	}
	return BusinessSystemPromptSnapshot{}, fmt.Errorf("%w: %v", ErrBusinessSystemPromptUnavailable, ErrBusinessSystemPromptLegacyComposition)
}

func applyRemoteSkillRoot(body, remoteRoot string) string {
	remoteRoot = strings.TrimSpace(remoteRoot)
	if remoteRoot == "" {
		return body
	}
	body = strings.ReplaceAll(body, RemoteSkillGitHubRoot, remoteRoot)
	body = strings.ReplaceAll(body, RemoteSkillMoxinggangRoot, remoteRoot)
	return body
}

func (s *BusinessSystemPromptService) retainLastGood(err error) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if current := s.snapshot.Load(); current != nil {
		degraded := *current
		degraded.Degraded = true
		s.snapshot.Store(&degraded)
	}
	return err
}

func validateBusinessSystemPromptSnapshot(snapshot *BusinessSystemPromptSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("%w: nil snapshot", ErrBusinessSystemPromptUnavailable)
	}
	if snapshot.Revision < 1 {
		return fmt.Errorf("%w: invalid revision", ErrBusinessSystemPromptUnavailable)
	}
	composition, compositionErr := NormalizeBusinessSystemPromptComposition(snapshot.CompositionMode, snapshot.BundleID, snapshot.BundleManifestSHA256)
	if compositionErr != nil {
		if !snapshot.Enabled {
			snapshot.Degraded = true
			return nil
		}
		return fmt.Errorf("%w: %v", ErrBusinessSystemPromptUnavailable, compositionErr)
	}
	snapshot.CompositionMode = composition.Mode
	snapshot.BundleID = composition.BundleID
	snapshot.BundleManifestSHA256 = composition.BundleManifestSHA256
	if strings.TrimSpace(snapshot.Body) == "" {
		if snapshot.Enabled {
			return fmt.Errorf("%w: enabled snapshot has no active body", ErrBusinessSystemPromptUnavailable)
		}
		return nil
	}
	hash, byteLength, err := ValidateBusinessSystemPromptBody(snapshot.Body)
	if err != nil {
		if !snapshot.Enabled {
			snapshot.Degraded = true
			return nil
		}
		return fmt.Errorf("%w: %v", ErrBusinessSystemPromptUnavailable, err)
	}
	if snapshot.SHA256 != "" && !strings.EqualFold(snapshot.SHA256, hash) {
		if !snapshot.Enabled {
			snapshot.Degraded = true
			return nil
		}
		return fmt.Errorf("%w: snapshot hash mismatch", ErrBusinessSystemPromptUnavailable)
	}
	if snapshot.ByteLength != 0 && snapshot.ByteLength != byteLength {
		if !snapshot.Enabled {
			snapshot.Degraded = true
			return nil
		}
		return fmt.Errorf("%w: snapshot byte length mismatch", ErrBusinessSystemPromptUnavailable)
	}
	snapshot.SHA256 = hash
	snapshot.ByteLength = byteLength
	return nil
}

func (s *BusinessSystemPromptService) CreateVersion(ctx context.Context, templateID int64, body, note string, actorID, expectedLatestVersion, expectedRevision int64) (BusinessSystemPromptVersion, error) {
	return s.CreateVersionWithComposition(ctx, templateID, BusinessSystemPromptVersionCreate{
		Body:            body,
		Note:            note,
		CompositionMode: BusinessSystemPromptCompositionInline,
	}, actorID, expectedLatestVersion, expectedRevision)
}

func (s *BusinessSystemPromptService) CreateVersionWithComposition(ctx context.Context, templateID int64, req BusinessSystemPromptVersionCreate, actorID, expectedLatestVersion, expectedRevision int64) (BusinessSystemPromptVersion, error) {
	if _, _, err := ValidateBusinessSystemPromptBody(req.Body); err != nil {
		return BusinessSystemPromptVersion{}, err
	}
	composition, err := NormalizeBusinessSystemPromptComposition(req.CompositionMode, req.BundleID, req.BundleManifestSHA256)
	if err != nil {
		return BusinessSystemPromptVersion{}, err
	}
	if s == nil || s.store == nil {
		return BusinessSystemPromptVersion{}, errors.New("business system prompt store unavailable")
	}
	if composition.Mode == BusinessSystemPromptCompositionOfflineBundle || composition.Mode == BusinessSystemPromptCompositionRemoteSkill {
		return BusinessSystemPromptVersion{}, ErrBusinessSystemPromptLegacyComposition
	}
	req.Note = strings.TrimSpace(req.Note)
	req.CompositionMode = composition.Mode
	req.BundleID = composition.BundleID
	req.BundleManifestSHA256 = composition.BundleManifestSHA256
	if err := s.validateBusinessSystemPromptBundleReference(composition); err != nil {
		return BusinessSystemPromptVersion{}, err
	}
	if compositionStore, ok := s.store.(BusinessSystemPromptCompositionStore); ok {
		return compositionStore.CreateBusinessSystemPromptVersionWithComposition(ctx, templateID, req, actorID, expectedLatestVersion, expectedRevision)
	}
	if composition.Mode != BusinessSystemPromptCompositionInline {
		return BusinessSystemPromptVersion{}, fmt.Errorf("%w: store does not support composition %q", ErrBusinessSystemPromptUnavailable, composition.Mode)
	}
	return s.store.CreateBusinessSystemPromptVersion(ctx, templateID, req.Body, req.Note, actorID, expectedLatestVersion, expectedRevision)
}

func (s *BusinessSystemPromptService) PublishVersion(ctx context.Context, templateID, versionID, expectedRevision, actorID int64) (BusinessSystemPromptSnapshot, error) {
	if s == nil || s.store == nil {
		return BusinessSystemPromptSnapshot{}, errors.New("business system prompt store unavailable")
	}
	if err := s.validateBusinessSystemPromptPublishTarget(ctx, templateID, versionID); err != nil {
		return BusinessSystemPromptSnapshot{}, err
	}
	snapshot, err := s.store.PublishBusinessSystemPromptVersion(ctx, templateID, versionID, expectedRevision, actorID)
	if err != nil {
		return BusinessSystemPromptSnapshot{}, err
	}
	snapshot.Degraded = false
	if err := s.prepareBusinessSystemPromptSnapshot(&snapshot); err != nil {
		_ = s.retainLastGood(err)
		return BusinessSystemPromptSnapshot{}, err
	}
	s.installBusinessSystemPromptSnapshot(snapshot)
	if s.bus != nil {
		if err := s.bus.Publish(ctx, snapshot.Revision); err != nil {
			snapshot = s.markBusinessSystemPromptRevisionDegraded(snapshot)
		}
	}
	return snapshot, nil
}

func (s *BusinessSystemPromptService) UpdateRuntime(ctx context.Context, update BusinessSystemPromptRuntimeUpdate) (BusinessSystemPromptSnapshot, error) {
	if s == nil || s.store == nil {
		return BusinessSystemPromptSnapshot{}, errors.New("business system prompt store unavailable")
	}
	if update.Enabled {
		current, ok := s.CurrentSnapshot()
		if !ok {
			return BusinessSystemPromptSnapshot{}, ErrBusinessSystemPromptUnavailable
		}
		current.Enabled = true
		if err := s.prepareBusinessSystemPromptSnapshot(&current); err != nil {
			return BusinessSystemPromptSnapshot{}, err
		}
	}
	snapshot, err := s.store.UpdateBusinessSystemPromptRuntime(ctx, update)
	if err != nil {
		return BusinessSystemPromptSnapshot{}, err
	}
	snapshot.Degraded = false
	if err := s.prepareBusinessSystemPromptSnapshot(&snapshot); err != nil {
		_ = s.retainLastGood(err)
		return BusinessSystemPromptSnapshot{}, err
	}
	s.installBusinessSystemPromptSnapshot(snapshot)
	if s.bus != nil {
		if err := s.bus.Publish(ctx, snapshot.Revision); err != nil {
			snapshot = s.markBusinessSystemPromptRevisionDegraded(snapshot)
		}
	}
	return snapshot, nil
}

func (s *BusinessSystemPromptService) validateBusinessSystemPromptBundleReference(composition BusinessSystemPromptComposition) error {
	if composition.Mode == BusinessSystemPromptCompositionRemoteSkill {
		return ErrBusinessSystemPromptLegacyComposition
	}
	if composition.Mode == BusinessSystemPromptCompositionCodexSkillHybrid {
		if composition.BundleID != BusinessSystemPromptRemoteSkillBundleID || s == nil || s.registry == nil {
			return fmt.Errorf("%w: active CodexRip registry unavailable", ErrBusinessSystemPromptUnavailable)
		}
		_, _, err := s.registry.ActiveBundle(context.Background())
		return err
	}
	if composition.Mode != BusinessSystemPromptCompositionOfflineBundle {
		return nil
	}
	_, err := s.loadBusinessSystemPromptBundle(composition.BundleID, composition.BundleManifestSHA256)
	if err != nil {
		return fmt.Errorf("%w: offline bundle unavailable: %v", ErrBusinessSystemPromptUnavailable, err)
	}
	return nil
}

func (s *BusinessSystemPromptService) validateBusinessSystemPromptPublishTarget(ctx context.Context, templateID, versionID int64) error {
	detail, err := s.store.GetBusinessSystemPromptTemplate(ctx, templateID)
	if err != nil {
		return err
	}
	if len(detail.Versions) == 0 {
		// Compatibility for lightweight legacy stores used by embedders. The
		// durable publish operation remains authoritative for existence/CAS; the
		// PostgreSQL repository always returns immutable versions here.
		return nil
	}
	for _, version := range detail.Versions {
		if version.ID != versionID {
			continue
		}
		composition, err := NormalizeBusinessSystemPromptComposition(version.CompositionMode, version.BundleID, version.BundleManifestSHA256)
		if err != nil {
			return err
		}
		if composition.Mode == BusinessSystemPromptCompositionOfflineBundle || composition.Mode == BusinessSystemPromptCompositionRemoteSkill {
			return ErrBusinessSystemPromptLegacyComposition
		}
		return s.validateBusinessSystemPromptBundleReference(composition)
	}
	return ErrBusinessSystemPromptVersionNotFound
}

func (s *BusinessSystemPromptService) installBusinessSystemPromptSnapshot(snapshot BusinessSystemPromptSnapshot) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if current := s.snapshot.Load(); current != nil && current.Revision > snapshot.Revision {
		return
	}
	s.snapshot.Store(&snapshot)
}

func (s *BusinessSystemPromptService) markBusinessSystemPromptRevisionDegraded(snapshot BusinessSystemPromptSnapshot) BusinessSystemPromptSnapshot {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	snapshot.Degraded = true
	if current := s.snapshot.Load(); current != nil && current.Revision == snapshot.Revision {
		degraded := *current
		degraded.Degraded = true
		s.snapshot.Store(&degraded)
		return degraded
	}
	return snapshot
}

func (s *BusinessSystemPromptService) ListTemplates(ctx context.Context) ([]BusinessSystemPromptTemplate, error) {
	return s.store.ListBusinessSystemPromptTemplates(ctx)
}

func (s *BusinessSystemPromptService) ListBundles() []BusinessSystemPromptBundleSummary {
	detail := s.inspectBusinessSystemPromptSeedBundle()
	return []BusinessSystemPromptBundleSummary{detail.BusinessSystemPromptBundleSummary}
}

func (s *BusinessSystemPromptService) GetBundle(bundleID string) (BusinessSystemPromptBundleDetail, error) {
	if strings.TrimSpace(bundleID) != BusinessSystemPromptSeedBundleID {
		return BusinessSystemPromptBundleDetail{}, ErrBusinessSystemPromptVersionNotFound
	}
	return s.inspectBusinessSystemPromptSeedBundle(), nil
}

func (s *BusinessSystemPromptService) inspectBusinessSystemPromptSeedBundle() BusinessSystemPromptBundleDetail {
	detail := BusinessSystemPromptBundleDetail{BusinessSystemPromptBundleSummary: BusinessSystemPromptBundleSummary{
		BundleID: BusinessSystemPromptSeedBundleID, Name: "codexrip Reverse-Skill",
		Description:    "Content-addressed, read-only codexrip reverse-skill package.",
		ManifestSHA256: BusinessSystemPromptSeedBundleManifestSHA256,
	}}
	bundle, err := s.loadBusinessSystemPromptBundle(BusinessSystemPromptSeedBundleID, BusinessSystemPromptSeedBundleManifestSHA256)
	if err != nil {
		detail.Degraded = true
		detail.DegradedReason = "bundle_unavailable"
		return detail
	}
	detail.Available = true
	detail.Degraded = bundle.Degraded
	if bundle.Degraded {
		detail.DegradedReason = "bundle_missing_optional_documents"
	}
	detail.Version = bundle.Manifest.Version
	detail.DocumentCount = len(bundle.Manifest.Files)
	detail.RouteCount = len(bundle.Manifest.Domains)
	detail.LoadedAt = bundle.LoadedAt
	detail.Documents = make([]BusinessSystemPromptBundleDocument, 0, len(bundle.Manifest.Files))
	for _, document := range bundle.Manifest.Files {
		detail.TotalBytes += int64(document.ByteLength)
		detail.Documents = append(detail.Documents, BusinessSystemPromptBundleDocument{
			Path: document.Path, SHA256: strings.ToLower(document.SHA256), ByteLength: document.ByteLength,
			Kind: bundleFileKind(document), Required: document.Required,
		})
	}
	detail.Routes = make([]BusinessSystemPromptBundleRoute, 0, len(bundle.Manifest.Domains))
	for _, route := range bundle.Manifest.Domains {
		detail.Routes = append(detail.Routes, BusinessSystemPromptBundleRoute{
			ID: route.ID, Keywords: append([]string(nil), route.Keywords...), Entry: route.Entry,
			References: append([]string(nil), route.References...), Priority: route.Priority,
		})
	}
	return detail
}

func (s *BusinessSystemPromptService) GetTemplate(ctx context.Context, id int64) (BusinessSystemPromptTemplateDetail, error) {
	return s.store.GetBusinessSystemPromptTemplate(ctx, id)
}

func (s *BusinessSystemPromptService) CreateTemplate(ctx context.Context, req BusinessSystemPromptTemplateCreate, actorID, expectedRevision int64) (BusinessSystemPromptTemplateDetail, error) {
	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Slug == "" || req.Name == "" {
		return BusinessSystemPromptTemplateDetail{}, fmt.Errorf("%w: slug and name are required", ErrBusinessSystemPromptInvalid)
	}
	if _, _, err := ValidateBusinessSystemPromptBody(req.Body); err != nil {
		return BusinessSystemPromptTemplateDetail{}, err
	}
	composition, err := NormalizeBusinessSystemPromptComposition(req.CompositionMode, req.BundleID, req.BundleManifestSHA256)
	if err != nil {
		return BusinessSystemPromptTemplateDetail{}, err
	}
	if composition.Mode == BusinessSystemPromptCompositionOfflineBundle || composition.Mode == BusinessSystemPromptCompositionRemoteSkill {
		return BusinessSystemPromptTemplateDetail{}, ErrBusinessSystemPromptLegacyComposition
	}
	req.CompositionMode = composition.Mode
	req.BundleID = composition.BundleID
	req.BundleManifestSHA256 = composition.BundleManifestSHA256
	if err := s.validateBusinessSystemPromptBundleReference(composition); err != nil {
		return BusinessSystemPromptTemplateDetail{}, err
	}
	return s.store.CreateBusinessSystemPromptTemplate(ctx, req, actorID, expectedRevision)
}

func (s *BusinessSystemPromptService) UpdateTemplate(ctx context.Context, id int64, req BusinessSystemPromptTemplateUpdate, actorID, expectedRevision int64) (BusinessSystemPromptTemplate, error) {
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return BusinessSystemPromptTemplate{}, fmt.Errorf("%w: name is required", ErrBusinessSystemPromptInvalid)
		}
		req.Name = &name
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		req.Description = &description
	}
	return s.store.UpdateBusinessSystemPromptTemplate(ctx, id, req, actorID, expectedRevision)
}

func (s *BusinessSystemPromptService) DuplicateTemplate(ctx context.Context, id int64, slug, name string, actorID, expectedRevision int64) (BusinessSystemPromptTemplateDetail, error) {
	slug = strings.TrimSpace(slug)
	name = strings.TrimSpace(name)
	if slug == "" || name == "" {
		return BusinessSystemPromptTemplateDetail{}, fmt.Errorf("%w: slug and name are required", ErrBusinessSystemPromptInvalid)
	}
	detail, err := s.store.GetBusinessSystemPromptTemplate(ctx, id)
	if err != nil {
		return BusinessSystemPromptTemplateDetail{}, err
	}
	for _, version := range detail.Versions {
		composition, normalizeErr := NormalizeBusinessSystemPromptComposition(version.CompositionMode, version.BundleID, version.BundleManifestSHA256)
		if normalizeErr != nil {
			return BusinessSystemPromptTemplateDetail{}, normalizeErr
		}
		if composition.Mode == BusinessSystemPromptCompositionOfflineBundle || composition.Mode == BusinessSystemPromptCompositionRemoteSkill {
			return BusinessSystemPromptTemplateDetail{}, ErrBusinessSystemPromptLegacyComposition
		}
	}
	return s.store.DuplicateBusinessSystemPromptTemplate(ctx, id, slug, name, actorID, expectedRevision)
}

func (s *BusinessSystemPromptService) DeleteTemplate(ctx context.Context, id, actorID, expectedRevision int64) error {
	return s.store.SoftDeleteBusinessSystemPromptTemplate(ctx, id, actorID, expectedRevision)
}
