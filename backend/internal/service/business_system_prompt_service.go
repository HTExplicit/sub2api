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

var (
	ErrBusinessSystemPromptTemplateNotFound = errors.New("business system prompt template not found")
	ErrBusinessSystemPromptVersionNotFound  = errors.New("business system prompt version not found")
	ErrBusinessSystemPromptRevisionConflict = errors.New("business system prompt revision conflict")
	ErrBusinessSystemPromptSeedProtected    = errors.New("business system prompt seed is protected")
	ErrBusinessSystemPromptActive           = errors.New("active business system prompt cannot be deleted")
)

const businessSystemPromptSeedSlug = "moxinggang_reverse_skill"

type BusinessSystemPromptSeed struct {
	Slug        string
	Name        string
	Description string
	Body        string
	SHA256      string
	ByteLength  int
}

type BusinessSystemPromptTemplate struct {
	ID          int64      `json:"id"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsSeed      bool       `json:"is_seed"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	CreatedBy   *int64     `json:"created_by,omitempty"`
	UpdatedBy   *int64     `json:"updated_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type BusinessSystemPromptVersion struct {
	ID          int64      `json:"id"`
	TemplateID  int64      `json:"template_id"`
	Version     int64      `json:"version"`
	Body        string     `json:"body"`
	SHA256      string     `json:"sha256"`
	ByteLength  int        `json:"byte_length"`
	Note        string     `json:"note"`
	CreatedBy   *int64     `json:"created_by,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	PublishedBy *int64     `json:"published_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	IsActive    bool       `json:"is_active"`
}

type BusinessSystemPromptTemplateDetail struct {
	Template BusinessSystemPromptTemplate  `json:"template"`
	Versions []BusinessSystemPromptVersion `json:"versions"`
}

type BusinessSystemPromptTemplateCreate struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Note        string `json:"note"`
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

type BusinessSystemPromptRevisionBus interface {
	Publish(context.Context, int64) error
	Subscribe(context.Context, func(int64)) error
}

type BusinessSystemPromptService struct {
	store BusinessSystemPromptStore
	bus   BusinessSystemPromptRevisionBus

	snapshot atomic.Pointer[BusinessSystemPromptSnapshot]
	stateMu  sync.Mutex

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	started     bool
}

func NewBusinessSystemPromptService(store BusinessSystemPromptStore, bus BusinessSystemPromptRevisionBus) *BusinessSystemPromptService {
	return &BusinessSystemPromptService{store: store, bus: bus}
}

func (s *BusinessSystemPromptService) Initialize(ctx context.Context) error {
	if s == nil || s.store == nil {
		return errors.New("business system prompt store unavailable")
	}
	hash, byteLength, err := ValidateBusinessSystemPromptBody(embeddedBusinessSystemPrompt)
	if err != nil {
		return fmt.Errorf("validate embedded business system prompt: %w", err)
	}
	seed := BusinessSystemPromptSeed{
		Slug:        businessSystemPromptSeedSlug,
		Name:        "模型港 Reverse-Skill System Prompt",
		Description: "固定导入的逆向还原 System Prompt；部署后默认关闭。",
		Body:        embeddedBusinessSystemPrompt,
		SHA256:      hash,
		ByteLength:  byteLength,
	}
	if err := s.store.EnsureBusinessSystemPromptSeed(ctx, seed); err != nil {
		return fmt.Errorf("ensure business system prompt seed: %w", err)
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
	if err := validateBusinessSystemPromptSnapshot(&loaded); err != nil {
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
	if _, _, err := ValidateBusinessSystemPromptBody(body); err != nil {
		return BusinessSystemPromptVersion{}, err
	}
	if s == nil || s.store == nil {
		return BusinessSystemPromptVersion{}, errors.New("business system prompt store unavailable")
	}
	return s.store.CreateBusinessSystemPromptVersion(ctx, templateID, body, strings.TrimSpace(note), actorID, expectedLatestVersion, expectedRevision)
}

func (s *BusinessSystemPromptService) PublishVersion(ctx context.Context, templateID, versionID, expectedRevision, actorID int64) (BusinessSystemPromptSnapshot, error) {
	if s == nil || s.store == nil {
		return BusinessSystemPromptSnapshot{}, errors.New("business system prompt store unavailable")
	}
	snapshot, err := s.store.PublishBusinessSystemPromptVersion(ctx, templateID, versionID, expectedRevision, actorID)
	if err != nil {
		return BusinessSystemPromptSnapshot{}, err
	}
	snapshot.Degraded = false
	if err := validateBusinessSystemPromptSnapshot(&snapshot); err != nil {
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
	snapshot, err := s.store.UpdateBusinessSystemPromptRuntime(ctx, update)
	if err != nil {
		return BusinessSystemPromptSnapshot{}, err
	}
	snapshot.Degraded = false
	if err := validateBusinessSystemPromptSnapshot(&snapshot); err != nil {
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
	return s.store.DuplicateBusinessSystemPromptTemplate(ctx, id, slug, name, actorID, expectedRevision)
}

func (s *BusinessSystemPromptService) DeleteTemplate(ctx context.Context, id, actorID, expectedRevision int64) error {
	return s.store.SoftDeleteBusinessSystemPromptTemplate(ctx, id, actorID, expectedRevision)
}
