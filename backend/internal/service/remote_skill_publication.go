package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var ErrRemoteSkillPublicFileNotFound = errors.New("remote skill public file not found")

type RemoteSkillPublication struct {
	Revision              int64
	CandidateID           int64
	EffectiveTreeSHA256   string
	EffectivePromptSHA256 string
	EffectivePromptBody   string
	RawPromptBody         string
	Files                 map[string][]byte
	RawFiles              map[string][]byte
	Version               RemoteSkillBundleVersion
	Prompt                RemoteSkillPromptVersion
	FileChanges           []RemoteSkillFileChange
}

type RemoteSkillPublicFile struct {
	Revision              int64
	CandidateID           int64
	EffectiveTreeSHA256   string
	EffectivePromptSHA256 string
	Body                  []byte
	ETag                  string
	ContentType           string
}

func (s *RemoteSkillRegistryService) installPublication(publication RemoteSkillPublication) error {
	if publication.Revision < 1 || publication.CandidateID < 1 ||
		!validRemoteSkillSHA256(publication.EffectiveTreeSHA256) ||
		!validRemoteSkillSHA256(publication.EffectivePromptSHA256) ||
		hashBusinessSystemPromptBundleBytes([]byte(publication.EffectivePromptBody)) != publication.EffectivePromptSHA256 ||
		len(publication.Files) == 0 {
		return fmt.Errorf("%w: invalid paired publication", ErrBusinessSystemPromptBundleInvalid)
	}
	for name, body := range publication.Files {
		normalized, err := normalizeBundleRelativePath(name)
		if err != nil || normalized != name || len(body) == 0 {
			return fmt.Errorf("%w: invalid published file", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	cloned := cloneRemoteSkillPublication(publication)
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if current := s.publication.Load(); current != nil && current.Revision > publication.Revision {
		return ErrBusinessSystemPromptRevisionConflict
	}
	s.publication.Store(&cloned)
	return nil
}

func (s *RemoteSkillRegistryService) ActivePublication(ctx context.Context) (RemoteSkillPublication, error) {
	if err := ctx.Err(); err != nil {
		return RemoteSkillPublication{}, err
	}
	if s == nil {
		return RemoteSkillPublication{}, fmt.Errorf("%w: registry unavailable", ErrBusinessSystemPromptBundleUnavailable)
	}
	current := s.publication.Load()
	if current == nil {
		return RemoteSkillPublication{}, fmt.Errorf("%w: no active paired publication", ErrBusinessSystemPromptBundleUnavailable)
	}
	return cloneRemoteSkillPublication(*current), nil
}

func (s *RemoteSkillRegistryService) LoadPublishedFile(ctx context.Context, name string) (RemoteSkillPublicFile, error) {
	if err := ctx.Err(); err != nil {
		return RemoteSkillPublicFile{}, err
	}
	if s == nil {
		return RemoteSkillPublicFile{}, ErrRemoteSkillPublicFileNotFound
	}
	normalized, err := normalizeBundleRelativePath(name)
	if err != nil || normalized != name || strings.HasSuffix(name, "/") || strings.ContainsAny(name, "?#") {
		return RemoteSkillPublicFile{}, ErrRemoteSkillPublicFileNotFound
	}
	current := s.publication.Load()
	if current == nil {
		return RemoteSkillPublicFile{}, ErrRemoteSkillPublicFileNotFound
	}
	body, ok := current.Files[name]
	if !ok {
		return RemoteSkillPublicFile{}, ErrRemoteSkillPublicFileNotFound
	}
	return RemoteSkillPublicFile{
		Revision:              current.Revision,
		CandidateID:           current.CandidateID,
		EffectiveTreeSHA256:   current.EffectiveTreeSHA256,
		EffectivePromptSHA256: current.EffectivePromptSHA256,
		Body:                  bytes.Clone(body),
		ETag:                  `"` + hashBusinessSystemPromptBundleBytes(body) + `"`,
		ContentType:           remoteSkillContentType(name),
	}, nil
}

func cloneRemoteSkillPublication(publication RemoteSkillPublication) RemoteSkillPublication {
	cloned := publication
	cloned.Files = make(map[string][]byte, len(publication.Files))
	for name, body := range publication.Files {
		cloned.Files[name] = bytes.Clone(body)
	}
	cloned.RawFiles = make(map[string][]byte, len(publication.RawFiles))
	for name, body := range publication.RawFiles {
		cloned.RawFiles[name] = bytes.Clone(body)
	}
	cloned.FileChanges = append([]RemoteSkillFileChange(nil), publication.FileChanges...)
	return cloned
}

func remoteSkillPublicationFromCandidate(revision int64, candidate RemoteSkillCandidate) (RemoteSkillPublication, error) {
	if revision < 1 || candidate.Version.ID < 1 || candidate.Prompt.ID < 1 ||
		candidate.Version.PromptVersionID != candidate.Prompt.ID ||
		candidate.Version.UpstreamSourceID != RemoteSkillUpstreamSourceID ||
		candidate.Version.UpstreamRoot != RemoteSkillUpstreamRoot ||
		candidate.Version.PublicRoot != RemoteSkillPublicRoot ||
		candidate.Prompt.RawSHA256 != hashBusinessSystemPromptBundleBytes([]byte(candidate.Prompt.RawBody)) ||
		candidate.Prompt.EffectiveSHA256 != hashBusinessSystemPromptBundleBytes([]byte(candidate.Prompt.EffectiveBody)) {
		return RemoteSkillPublication{}, fmt.Errorf("%w: paired publication metadata mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	if remoteSkillFileTreeSHA256(candidate.RawFiles) != candidate.Version.RawTreeSHA256 ||
		remoteSkillFileTreeSHA256(candidate.EffectiveFiles) != candidate.Version.EffectiveTreeSHA256 ||
		len(candidate.EffectiveFiles) != candidate.Version.FileCount {
		return RemoteSkillPublication{}, fmt.Errorf("%w: paired publication tree mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return RemoteSkillPublication{
		Revision:              revision,
		CandidateID:           candidate.Version.ID,
		EffectiveTreeSHA256:   candidate.Version.EffectiveTreeSHA256,
		EffectivePromptSHA256: candidate.Prompt.EffectiveSHA256,
		EffectivePromptBody:   candidate.Prompt.EffectiveBody,
		RawPromptBody:         candidate.Prompt.RawBody,
		Files:                 candidate.EffectiveFiles,
		RawFiles:              candidate.RawFiles,
		Version:               candidate.Version,
		Prompt:                candidate.Prompt,
		FileChanges:           candidate.FileChanges,
	}, nil
}

func remoteSkillCandidateFromPublication(publication RemoteSkillPublication) *RemoteSkillCandidate {
	return &RemoteSkillCandidate{
		Version:        publication.Version,
		Prompt:         publication.Prompt,
		RawFiles:       cloneRemoteSkillFiles(publication.RawFiles),
		EffectiveFiles: cloneRemoteSkillFiles(publication.Files),
		FileChanges:    append([]RemoteSkillFileChange(nil), publication.FileChanges...),
	}
}

func remoteSkillFileTreeSHA256(files map[string][]byte) string {
	hashes := make(map[string]string, len(files))
	for name, body := range files {
		hashes[name] = hashBusinessSystemPromptBundleBytes(body)
	}
	return hashRemoteSkillFileSet(hashes)
}

func cloneRemoteSkillFiles(files map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(files))
	for name, body := range files {
		cloned[name] = bytes.Clone(body)
	}
	return cloned
}

func validRemoteSkillSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func remoteSkillContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".yaml", ".yml":
		return "application/yaml; charset=utf-8"
	case ".py":
		return "text/x-python; charset=utf-8"
	case ".sh":
		return "text/x-shellscript; charset=utf-8"
	case ".ps1", ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
