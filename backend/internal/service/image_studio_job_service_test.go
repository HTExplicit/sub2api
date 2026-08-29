//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type imageStudioAPIKeyRepoFake struct {
	APIKeyRepository
	keys []APIKey
}

func (f *imageStudioAPIKeyRepoFake) ListAllByUserID(context.Context, int64, APIKeyListFilters) ([]APIKey, error) {
	return append([]APIKey(nil), f.keys...), nil
}

func (f *imageStudioAPIKeyRepoFake) GetByID(_ context.Context, id int64) (*APIKey, error) {
	for i := range f.keys {
		if f.keys[i].ID == id {
			copy := f.keys[i]
			return &copy, nil
		}
	}
	return nil, ErrAPIKeyNotFound
}

type imageStudioAccountReaderFake struct {
	AccountRepository
	accounts map[int64][]Account
}

func (f *imageStudioAccountReaderFake) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]Account, error) {
	return append([]Account(nil), f.accounts[groupID]...), nil
}

type imageStudioRepoFake struct {
	ImageStudioRepository
	created ImageStudioCreateParams
	job     *ImageStudioJob
}

func (f *imageStudioRepoFake) Create(_ context.Context, params ImageStudioCreateParams) (*ImageStudioJob, error) {
	f.created = params
	return f.job, nil
}

type imageStudioStoreFake struct {
	saved   []ImageStudioInputArtifact
	removed []string
}

func (f *imageStudioStoreFake) Save(_ context.Context, _ int64, kind ImageStudioArtifactKind, data []byte, contentType string, _ time.Time) (ImageStudioInputArtifact, error) {
	artifact := ImageStudioInputArtifact{Kind: kind, StorageKey: string(kind) + ".png", ContentType: contentType, ByteSize: int64(len(data))}
	f.saved = append(f.saved, artifact)
	return artifact, nil
}

func (f *imageStudioStoreFake) Open(string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("image")), nil
}

func (f *imageStudioStoreFake) Read(string) ([]byte, error) { return []byte("image"), nil }
func (f *imageStudioStoreFake) Remove(key string) error {
	f.removed = append(f.removed, key)
	return nil
}

func canonicalImageStudioFixture() (*Group, APIKey, Account) {
	group := &Group{
		ID: 31, Name: "Cindy Images", Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1, Status: StatusActive, AllowImageGeneration: true,
	}
	key := APIKey{ID: 41, UserID: 7, Key: "must-never-leave-server", Name: "Studio", Status: StatusActive, GroupID: &group.ID, Group: group}
	account := Account{
		ID: 51, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "upstream-secret", "base_url": "https://api.laxarouter.ai"},
	}
	return group, key, account
}

func TestImageStudioEligibleKeysHidesUnavailableCindyImageCandidatesAndCredentials(t *testing.T) {
	group, key, account := canonicalImageStudioFixture()
	legacyGroup := &Group{ID: 32, Name: "legacy", Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}
	legacyKey := APIKey{ID: 42, UserID: 7, Key: "legacy-secret", Name: "legacy", Status: StatusActive, GroupID: &legacyGroup.ID, Group: legacyGroup}
	studio := NewImageStudioService(
		&imageStudioRepoFake{},
		&imageStudioAPIKeyRepoFake{keys: []APIKey{key, legacyKey}},
		&imageStudioAccountReaderFake{accounts: map[int64][]Account{group.ID: {account}}},
		&imageStudioStoreFake{},
	)

	items, err := studio.EligibleKeys(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, key.ID, items[0].APIKey.ID)
	require.Empty(t, items[0].Capabilities)

	nativeCapabilities := make(map[string]CindyCapability)
	for _, capability := range CindyCapabilities() {
		nativeCapabilities[capability.PublicID] = capability
	}
	gptNative := nativeCapabilities[ImageStudioModelGPTImage2]
	require.False(t, gptNative.PublicModel)
	require.NotNil(t, gptNative.Controls)
	require.NotNil(t, gptNative.Controls.Generation)
	require.Equal(t, 1, gptNative.Controls.Generation.MaxOutputCount)
	geminiNative := nativeCapabilities[ImageStudioModelGeminiProImage]
	require.False(t, geminiNative.PublicModel)
	require.NotNil(t, geminiNative.Controls)
	require.NotNil(t, geminiNative.Controls.Generation)
	require.NotNil(t, geminiNative.Controls.Edit)
	require.Equal(t, 1, geminiNative.Controls.Generation.MaxOutputCount)
	require.Equal(t, 1, geminiNative.Controls.Edit.MaxOutputCount)

	raw, err := json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "must-never-leave-server")
	require.NotContains(t, string(raw), "upstream-secret")
	require.NotContains(t, string(raw), `"key"`)
}

func TestImageStudioCreatePersistsInputsAndFourFanoutItems(t *testing.T) {
	group, key, account := canonicalImageStudioFixture()
	repo := &imageStudioRepoFake{job: &ImageStudioJob{ID: 61, UserID: 7, APIKeyID: key.ID, Count: 4, Status: ImageStudioJobPending}}
	store := &imageStudioStoreFake{}
	studio := NewImageStudioService(
		repo,
		&imageStudioAPIKeyRepoFake{keys: []APIKey{key}},
		&imageStudioAccountReaderFake{accounts: map[int64][]Account{group.ID: {account}}},
		store,
	)
	studio.now = func() time.Time { return time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC) }

	job, err := studio.Create(context.Background(), 7, ImageStudioCreateInput{
		APIKeyID: key.ID, Mode: ImageStudioModeEdit, Model: ImageStudioModelGeminiProImage,
		Prompt: " replace the sky ", Count: 4,
	}, &ImageStudioUpload{Data: []byte("reference"), ContentType: "image/png"}, &ImageStudioUpload{Data: []byte("mask"), ContentType: "image/png"})

	require.NoError(t, err)
	require.Equal(t, int64(61), job.ID)
	require.Equal(t, "replace the sky", repo.created.Input.Prompt)
	require.Equal(t, "1024x1024", repo.created.Input.Size)
	require.Equal(t, "low", repo.created.Input.Quality)
	require.Equal(t, 4, repo.created.Input.Count)
	require.Len(t, repo.created.InputArtifacts, 2)
	require.Equal(t, ImageStudioFileRetention, repo.created.RequestExpiresAt.Sub(studio.now()))
	require.Equal(t, ImageStudioMetadataRetention, repo.created.RetainUntil.Sub(studio.now()))
}
