package service

import (
	"context"
	"fmt"
	"sort"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type remoteSkillManifest = extensionv1.SkillManifest
type remoteSkillManifestEntry = extensionv1.SkillManifestEntry

func loadRemoteSkillManifest() (remoteSkillManifest, error) {
	return loadRemoteSkillManifestContext(context.Background())
}
func loadRemoteSkillManifestContext(ctx context.Context) (remoteSkillManifest, error) {
	var manifest remoteSkillManifest
	err := invokePromptManagement(ctx, "skills.seed.index", struct{}{}, &manifest)
	if err != nil {
		return manifest, err
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > remoteSkillMaxFileCount || manifest.ExpectedFileCount != len(manifest.Files) {
		return manifest, ErrBusinessSystemPromptBundleInvalid
	}
	names := map[string]bool{}
	var total int64
	for _, entry := range manifest.Files {
		normalized, pathErr := normalizeBundleRelativePath(entry.Path)
		if pathErr != nil || normalized != entry.Path || names[portableRemoteSkillPathKey(entry.Path)] || entry.ByteLength <= 0 || entry.ByteLength > businessSystemPromptBundleMaxFileBytes || !validRemoteSkillSHA256(entry.SHA256) {
			return manifest, ErrBusinessSystemPromptBundleInvalid
		}
		names[portableRemoteSkillPathKey(entry.Path)] = true
		total += int64(entry.ByteLength)
		if total > remoteSkillMaxTotalBytes {
			return manifest, ErrBusinessSystemPromptBundleInvalid
		}
	}
	return manifest, nil
}
func validateRemoteSkillManifest(manifest remoteSkillManifest) error {
	return invokePromptManagement(context.Background(), "skills.manifest.validate", manifest, nil)
}
func loadRemoteSkillSeedFiles() (remoteSkillManifest, map[string][]byte, error) {
	return loadRemoteSkillSeedFilesContext(context.Background())
}
func loadRemoteSkillSeedFilesContext(ctx context.Context) (remoteSkillManifest, map[string][]byte, error) {
	manifest, err := loadRemoteSkillManifestContext(ctx)
	if err != nil {
		return manifest, nil, err
	}
	files := make(map[string][]byte, len(manifest.Files))
	for _, entry := range manifest.Files {
		body, err := readRemoteSkillSeedFile(ctx, entry)
		if err != nil {
			return manifest, nil, err
		}
		files[entry.Path] = body
	}
	return manifest, files, nil
}
func readRemoteSkillSeedFile(ctx context.Context, entry remoteSkillManifestEntry) ([]byte, error) {
	if entry.ByteLength <= 0 || entry.ByteLength > businessSystemPromptBundleMaxFileBytes {
		return nil, ErrBusinessSystemPromptBundleInvalid
	}
	body := make([]byte, 0, entry.ByteLength)
	for len(body) < entry.ByteLength {
		var chunk extensionv1.SkillSeedChunk
		err := invokePromptManagement(ctx, "skills.seed.chunk", extensionv1.SkillSeedChunkRequest{Path: entry.Path, Offset: len(body), Limit: 256 << 10}, &chunk)
		if err != nil {
			return nil, err
		}
		if chunk.TotalBytes != entry.ByteLength || len(chunk.Data) == 0 || len(chunk.Data) > 256<<10 || len(chunk.Data) > entry.ByteLength-len(body) {
			return nil, ErrBusinessSystemPromptBundleInvalid
		}
		body = append(body, chunk.Data...)
	}
	if !remoteSkillManifestEntryMatches(entry, body) {
		return nil, fmt.Errorf("%w: seed content mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return body, nil
}
func remoteSkillManifestEntryMatches(entry remoteSkillManifestEntry, body []byte) bool {
	return len(body) == entry.ByteLength && len(body) > 0 && utf8.Valid(body) && hashBusinessSystemPromptBundleBytes(body) == entry.SHA256
}
func loadRemoteSkillPinnedAsset(entry remoteSkillManifestEntry) ([]byte, error) {
	if entry.SourceKind != "pinned" {
		return nil, ErrBusinessSystemPromptBundleInvalid
	}
	return readRemoteSkillSeedFile(context.Background(), entry)
}
func sortRemoteSkillPaths(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		left, right := portableRemoteSkillPathKey(paths[i]), portableRemoteSkillPathKey(paths[j])
		if left == right {
			return paths[i] < paths[j]
		}
		return left < right
	})
}
