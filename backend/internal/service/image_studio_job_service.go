package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type ImageStudioUpload struct {
	Data        []byte
	ContentType string
}

type ImageStudioEligibleKeyGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ImageStudioEligibleAPIKey struct {
	ID      int64                       `json:"id"`
	Name    string                      `json:"name"`
	GroupID int64                       `json:"group_id"`
	Group   ImageStudioEligibleKeyGroup `json:"group"`
}

type ImageStudioEligibleKey struct {
	APIKey       ImageStudioEligibleAPIKey `json:"api_key"`
	Capabilities []CindyModelCapability    `json:"capabilities"`
}

type ImageStudioArtifactDownload struct {
	Artifact *ImageStudioArtifact
	Reader   io.ReadCloser
}

type ImageStudioService struct {
	runtime  *ImageStudioRuntime
	repo     ImageStudioRepository
	apiKeys  APIKeyRepository
	accounts AccountRepository
	store    ImageStudioFileStorage
	now      func() time.Time
}

func NewImageStudioService(
	repo ImageStudioRepository,
	apiKeys APIKeyRepository,
	accounts AccountRepository,
	store ImageStudioFileStorage,
) *ImageStudioService {
	return &ImageStudioService{
		repo: repo, apiKeys: apiKeys, accounts: accounts, store: store, now: time.Now,
	}
}

func (s *ImageStudioService) EligibleKeys(ctx context.Context, userID int64) ([]ImageStudioEligibleKey, error) {
	if s == nil || s.apiKeys == nil || s.accounts == nil || userID <= 0 {
		return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	lister, ok := s.apiKeys.(apiKeyAllByUserIDLister)
	if !ok {
		return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	keys, err := lister.ListAllByUserID(ctx, userID, APIKeyListFilters{Status: StatusActive})
	if err != nil {
		return nil, fmt.Errorf("list image studio keys: %w", err)
	}
	capabilities := imageStudioModelCapabilities()
	if len(capabilities) == 0 {
		return []ImageStudioEligibleKey{}, nil
	}
	items := make([]ImageStudioEligibleKey, 0, len(keys))
	groupEligibility := make(map[int64]bool)
	groupChecked := make(map[int64]bool)
	for i := range keys {
		key := &keys[i]
		if key.UserID != userID || !key.IsActive() || key.IsExpired() || key.IsQuotaExhausted() || key.Group == nil {
			continue
		}
		group := key.Group
		if !imageStudioGroupIdentityEligible(group) {
			continue
		}
		if !groupChecked[group.ID] {
			groupChecked[group.ID] = true
			accounts, listErr := s.accounts.ListSchedulableByGroupID(ctx, group.ID)
			if listErr != nil {
				return nil, fmt.Errorf("load image studio accounts: %w", listErr)
			}
			for accountIndex := range accounts {
				account := &accounts[accountIndex]
				if account.Status == StatusActive && account.Schedulable &&
					hasCanonicalCindyProviderIdentity(account) && ProviderIdentityCompatible(account, group) {
					groupEligibility[group.ID] = true
					break
				}
			}
		}
		if !groupEligibility[group.ID] {
			continue
		}
		items = append(items, ImageStudioEligibleKey{
			APIKey: ImageStudioEligibleAPIKey{
				ID: key.ID, Name: key.Name, GroupID: group.ID,
				Group: ImageStudioEligibleKeyGroup{ID: group.ID, Name: group.Name},
			},
			Capabilities: append([]CindyModelCapability(nil), capabilities...),
		})
	}
	return items, nil
}

func imageStudioGroupIdentityEligible(group *Group) bool {
	return group != nil && group.ID > 0 && group.IsActive() && group.AllowImageGeneration &&
		group.Platform == PlatformCindy && group.EffectiveWirePlatform() == WirePlatformOpenAI &&
		group.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1
}

func imageStudioModelCapabilities() []CindyModelCapability {
	capabilities := make([]CindyModelCapability, 0)
	for _, capability := range CindyCapabilities() {
		if capability.PublicModel && capability.Kind == CindyModelKindImage {
			capabilities = append(capabilities, cindyModelCapabilityFromCapability(capability))
		}
	}
	return imageStudioModelChoices(capabilities)
}

func (s *ImageStudioService) eligibleAPIKey(ctx context.Context, userID, apiKeyID int64) (*APIKey, error) {
	if s == nil || s.apiKeys == nil || s.accounts == nil {
		return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	key, err := s.apiKeys.GetByID(ctx, apiKeyID)
	if err != nil {
		return nil, newImageStudioError(404, "api_key_unavailable", "Image Studio API key is unavailable")
	}
	if key == nil || key.UserID != userID || !key.IsActive() || key.IsExpired() || key.IsQuotaExhausted() || !imageStudioGroupIdentityEligible(key.Group) {
		return nil, newImageStudioError(404, "api_key_unavailable", "Image Studio API key is unavailable")
	}
	accounts, err := s.accounts.ListSchedulableByGroupID(ctx, key.Group.ID)
	if err != nil {
		return nil, fmt.Errorf("load image studio accounts: %w", err)
	}
	for i := range accounts {
		account := &accounts[i]
		if account.Status == StatusActive && account.Schedulable &&
			hasCanonicalCindyProviderIdentity(account) && ProviderIdentityCompatible(account, key.Group) {
			return key, nil
		}
	}
	return nil, newImageStudioError(404, "api_key_unavailable", "Image Studio API key is unavailable")
}

func (s *ImageStudioService) Create(
	ctx context.Context,
	userID int64,
	input ImageStudioCreateInput,
	reference, mask *ImageStudioUpload,
) (*ImageStudioJob, error) {
	input.Prompt = strings.TrimSpace(input.Prompt)
	plan, err := planImageStudio(ctx, input, reference != nil, mask != nil, true)
	if err != nil {
		return nil, err
	}
	input.Model, input.Mode, input.Size, input.Quality = plan.Model, ImageStudioMode(plan.Mode), plan.Size, plan.Quality
	capabilityValue, known := resolveKnownCindyCapability(input.Model)
	var capability *CindyCapability
	if known {
		capability = &capabilityValue
	}
	if capability == nil || !capability.PublicModel || capability.Kind != CindyModelKindImage {
		return nil, newImageStudioError(400, "model_unavailable", "Image Studio model is unavailable")
	}
	if _, err := s.eligibleAPIKey(ctx, userID, input.APIKeyID); err != nil {
		return nil, err
	}
	if s.repo == nil || s.store == nil {
		return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	if s.runtime != nil {
		if err := s.runtime.Start(context.Background()); err != nil {
			return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
		}
	}
	now := s.now()
	expiresAt := now.Add(ImageStudioFileRetention)
	saved := make([]ImageStudioInputArtifact, 0, 2)
	removeSaved := func() {
		for _, artifact := range saved {
			_ = s.store.Remove(artifact.StorageKey)
		}
	}
	if reference != nil {
		artifact, err := s.store.Save(ctx, userID, ImageStudioArtifactReference, reference.Data, reference.ContentType, expiresAt)
		if err != nil {
			return nil, err
		}
		saved = append(saved, artifact)
	}
	if mask != nil {
		artifact, err := s.store.Save(ctx, userID, ImageStudioArtifactMask, mask.Data, mask.ContentType, expiresAt)
		if err != nil {
			removeSaved()
			return nil, err
		}
		saved = append(saved, artifact)
	}
	job, err := s.repo.Create(ctx, ImageStudioCreateParams{
		UserID: userID, Input: input, RequestExpiresAt: expiresAt,
		RetainUntil: now.Add(ImageStudioMetadataRetention), InputArtifacts: saved,
	})
	if err != nil {
		removeSaved()
		return nil, err
	}
	return job, nil
}

func (s *ImageStudioService) Get(ctx context.Context, userID, jobID int64) (*ImageStudioJob, error) {
	return s.repo.Get(ctx, userID, jobID)
}

func (s *ImageStudioService) List(ctx context.Context, userID int64, limit, offset int) ([]ImageStudioJob, error) {
	return s.repo.List(ctx, userID, limit, offset)
}

func (s *ImageStudioService) ListItems(ctx context.Context, userID, jobID int64) ([]ImageStudioItem, error) {
	return s.repo.ListItems(ctx, userID, jobID)
}

func (s *ImageStudioService) ListArtifacts(ctx context.Context, userID, jobID int64) ([]ImageStudioArtifact, error) {
	return s.repo.ListOutputArtifacts(ctx, userID, jobID)
}

func (s *ImageStudioService) Cancel(ctx context.Context, userID, jobID int64) (*ImageStudioJob, error) {
	return s.repo.RequestCancel(ctx, userID, jobID)
}

func (s *ImageStudioService) Retry(ctx context.Context, userID, jobID int64) (*ImageStudioJob, error) {
	if err := EnsureImageStudioAvailable(ctx); err != nil {
		return nil, err
	}
	job, err := s.repo.Get(ctx, userID, jobID)
	if err != nil {
		return nil, err
	}
	if _, err = s.eligibleAPIKey(ctx, userID, job.APIKeyID); err != nil {
		return nil, err
	}
	if s.runtime != nil {
		if err := s.runtime.Start(context.Background()); err != nil {
			return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
		}
	}
	return s.repo.Retry(ctx, userID, jobID, s.now())
}

func (s *ImageStudioService) OpenArtifact(ctx context.Context, userID, jobID, artifactID int64) (*ImageStudioArtifactDownload, error) {
	artifact, err := s.repo.GetArtifact(ctx, userID, jobID, artifactID)
	if err != nil {
		return nil, err
	}
	if !artifact.ExpiresAt.After(s.now()) {
		return nil, ErrImageStudioNotFound
	}
	reader, err := s.store.Open(artifact.StorageKey)
	if err != nil {
		if errors.Is(err, ErrImageStudioNotFound) {
			return nil, ErrImageStudioNotFound
		}
		return nil, err
	}
	return &ImageStudioArtifactDownload{Artifact: artifact, Reader: reader}, nil
}
