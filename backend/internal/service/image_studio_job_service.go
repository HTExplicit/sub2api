package service

import (
	"context"
	"errors"
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
	APIKey ImageStudioEligibleAPIKey `json:"api_key"`
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

// EligibleKeys lists the caller's API keys that can run Image Studio. Its
// image models and groups came only from the removed Cindy catalog, so no key
// is eligible until a generic image model source is added.
func (s *ImageStudioService) EligibleKeys(ctx context.Context, userID int64) ([]ImageStudioEligibleKey, error) {
	if s == nil || s.apiKeys == nil || s.accounts == nil || userID <= 0 {
		return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	return []ImageStudioEligibleKey{}, nil
}

// eligibleAPIKey rejects every key for the same reason as EligibleKeys.
func (s *ImageStudioService) eligibleAPIKey(ctx context.Context, userID, apiKeyID int64) (*APIKey, error) {
	if s == nil || s.apiKeys == nil || s.accounts == nil {
		return nil, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
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
