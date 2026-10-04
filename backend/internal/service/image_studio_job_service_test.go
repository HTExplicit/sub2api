//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

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

func (f *imageStudioStoreFake) Save(_ context.Context, _ int64, kind ImageStudioArtifactKind, data []byte, contentType string) (ImageStudioInputArtifact, error) {
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

func canonicalImageStudioFixture() APIKey {
	group := &Group{ID: 31, Name: "Images", Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}
	return APIKey{ID: 41, UserID: 7, Key: "must-never-leave-server", Name: "Studio", Status: StatusActive, GroupID: &group.ID, Group: group}
}

func TestImageStudioEligibleKeysHidesUnavailableImageCandidatesAndCredentials(t *testing.T) {
	key := canonicalImageStudioFixture()
	legacyGroup := &Group{ID: 32, Name: "legacy", Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}
	legacyKey := APIKey{ID: 42, UserID: 7, Key: "legacy-secret", Name: "legacy", Status: StatusActive, GroupID: &legacyGroup.ID, Group: legacyGroup}
	studio := NewImageStudioService(
		&imageStudioRepoFake{},
		&imageStudioAPIKeyRepoFake{keys: []APIKey{key, legacyKey}},
		&imageStudioStoreFake{},
	)

	items, err := studio.EligibleKeys(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, items, "no Image Studio model source exists after the provider catalog removal")

	raw, err := json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "must-never-leave-server")
	require.NotContains(t, string(raw), `"key"`)
}
