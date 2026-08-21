package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var imageStudioStorageKeyPattern = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|webp)$`)

type ImageStudioArtifactStore struct {
	root string
	repo ImageStudioRepository
	mu   sync.Mutex
}

func NewImageStudioArtifactStore(root string, repo ImageStudioRepository) *ImageStudioArtifactStore {
	root = strings.TrimSpace(root)
	if root == "" {
		root = filepath.Join("data", "image-studio")
	}
	return &ImageStudioArtifactStore{root: filepath.Clean(root), repo: repo}
}

func (s *ImageStudioArtifactStore) Save(
	ctx context.Context,
	userID int64,
	kind ImageStudioArtifactKind,
	data []byte,
	contentType string,
	expiresAt time.Time,
) (ImageStudioInputArtifact, error) {
	contentType, extension, err := validateImageStudioImage(data, contentType)
	if err != nil {
		return ImageStudioInputArtifact{}, err
	}
	if kind != ImageStudioArtifactReference && kind != ImageStudioArtifactMask && kind != ImageStudioArtifactOutput {
		return ImageStudioInputArtifact{}, newImageStudioError(400, "invalid_artifact", "Image artifact type is not supported")
	}
	if userID <= 0 || s == nil || s.repo == nil {
		return ImageStudioInputArtifact{}, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	usage, err := s.repo.StorageUsage(ctx, userID, time.Now())
	if err != nil {
		return ImageStudioInputArtifact{}, fmt.Errorf("load image studio storage usage: %w", err)
	}
	byteSize := int64(len(data))
	if byteSize > ImageStudioGlobalBytes-usage.Global || byteSize > ImageStudioUserBytes-usage.User {
		return ImageStudioInputArtifact{}, newImageStudioError(409, "storage_quota_exceeded", "Image Studio storage quota is full")
	}
	if err = os.MkdirAll(s.root, 0o700); err != nil {
		return ImageStudioInputArtifact{}, fmt.Errorf("create image studio storage: %w", err)
	}

	storageKey, err := newImageStudioStorageKey(extension)
	if err != nil {
		return ImageStudioInputArtifact{}, fmt.Errorf("create image studio storage key: %w", err)
	}
	path, err := s.path(storageKey)
	if err != nil {
		return ImageStudioInputArtifact{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ImageStudioInputArtifact{}, fmt.Errorf("create image studio artifact: %w", err)
	}
	written, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || written != len(data) || closeErr != nil {
		_ = os.Remove(path)
		if writeErr != nil {
			return ImageStudioInputArtifact{}, fmt.Errorf("write image studio artifact: %w", writeErr)
		}
		if closeErr != nil {
			return ImageStudioInputArtifact{}, fmt.Errorf("close image studio artifact: %w", closeErr)
		}
		return ImageStudioInputArtifact{}, io.ErrShortWrite
	}
	return ImageStudioInputArtifact{
		Kind: kind, StorageKey: storageKey, ContentType: contentType, ByteSize: byteSize,
	}, nil
}

func (s *ImageStudioArtifactStore) Open(storageKey string) (io.ReadCloser, error) {
	path, err := s.path(storageKey)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrImageStudioNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open image studio artifact: %w", err)
	}
	return file, nil
}

func (s *ImageStudioArtifactStore) Read(storageKey string) ([]byte, error) {
	reader, err := s.Open(storageKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, ImageStudioMaxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image studio artifact: %w", err)
	}
	if len(data) > ImageStudioMaxImageBytes {
		return nil, newImageStudioError(400, "invalid_image", "Image exceeds the 20 MB limit")
	}
	return data, nil
}

func (s *ImageStudioArtifactStore) Remove(storageKey string) error {
	path, err := s.path(storageKey)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove image studio artifact: %w", err)
	}
	return nil
}

func (s *ImageStudioArtifactStore) path(storageKey string) (string, error) {
	if s == nil || !imageStudioStorageKeyPattern.MatchString(storageKey) {
		return "", newImageStudioError(400, "invalid_artifact", "Image artifact is invalid")
	}
	return filepath.Join(s.root, storageKey), nil
}

func newImageStudioStorageKey(extension string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random) + "." + extension, nil
}

func validateImageStudioImage(data []byte, declared string) (contentType, extension string, err error) {
	if len(data) == 0 || len(data) > ImageStudioMaxImageBytes {
		return "", "", newImageStudioError(400, "invalid_image", "Image is empty or exceeds the 20 MB limit")
	}
	detected := ""
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		detected = "image/png"
		extension = "png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		detected = "image/jpeg"
		extension = "jpg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		detected = "image/webp"
		extension = "webp"
	default:
		return "", "", newImageStudioError(400, "invalid_image", "Image format is not supported")
	}
	declared = strings.ToLower(strings.TrimSpace(strings.SplitN(declared, ";", 2)[0]))
	if declared != "" && declared != detected {
		return "", "", newImageStudioError(400, "invalid_image", "Image content type does not match its contents")
	}
	return detected, extension, nil
}
