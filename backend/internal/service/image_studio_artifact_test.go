//go:build unit

package service

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type imageStudioUsageRepoStub struct {
	ImageStudioRepository
	usage ImageStudioStorageUsage
}

func (s *imageStudioUsageRepoStub) StorageUsage(context.Context, int64, time.Time) (ImageStudioStorageUsage, error) {
	return s.usage, nil
}

func TestImageStudioArtifactStoreWritesOpaqueValidatedFile(t *testing.T) {
	store := NewImageStudioArtifactStore(t.TempDir(), &imageStudioUsageRepoStub{})
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}

	artifact, err := store.Save(context.Background(), 7, ImageStudioArtifactReference, png, "image/png", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NotContains(t, artifact.StorageKey, "/")
	require.NotContains(t, artifact.StorageKey, `\`)
	require.Equal(t, int64(len(png)), artifact.ByteSize)

	reader, err := store.Open(artifact.StorageKey)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, png, got)
}

func TestImageStudioArtifactStoreRejectsQuotaBeforeWriting(t *testing.T) {
	store := NewImageStudioArtifactStore(t.TempDir(), &imageStudioUsageRepoStub{usage: ImageStudioStorageUsage{
		Global: ImageStudioGlobalBytes - 1,
		User:   ImageStudioUserBytes - 1,
	}})
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}

	_, err := store.Save(context.Background(), 7, ImageStudioArtifactOutput, png, "image/png", time.Now().Add(time.Hour))
	var studioErr *ImageStudioError
	require.ErrorAs(t, err, &studioErr)
	require.Equal(t, "storage_quota_exceeded", studioErr.Code)
}

func TestImageStudioArtifactStoreRejectsMismatchedContentType(t *testing.T) {
	store := NewImageStudioArtifactStore(t.TempDir(), &imageStudioUsageRepoStub{})
	jpeg := []byte{0xff, 0xd8, 0xff, 0xdb, 1, 2, 3}

	_, err := store.Save(context.Background(), 7, ImageStudioArtifactOutput, jpeg, "image/png", time.Now().Add(time.Hour))
	var studioErr *ImageStudioError
	require.ErrorAs(t, err, &studioErr)
	require.Equal(t, "invalid_image", studioErr.Code)
}
