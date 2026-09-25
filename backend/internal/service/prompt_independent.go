package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// IndependentPromptStore is the one-time upgrade and read-only compatibility
// boundary. It never installs seeds or follows a registry's active version.
type IndependentPromptStore interface {
	InitializeIndependentPrompts(context.Context, RemoteSkillRegistryFiles) (map[string][]byte, error)
	PromptVersionPreserveEcho(context.Context, int64) (bool, error)
	PromptRuleHistory(context.Context, string) ([]PromptHistoryVersion, error)
}

type PromptHistoryVersion struct {
	ID              int64     `json:"id"`
	Body            string    `json:"body"`
	CompositionMode string    `json:"composition_mode"`
	CreatedAt       time.Time `json:"created_at"`
	Restorable      bool      `json:"restorable"`
}

type FrozenPromptFiles struct {
	store  IndependentPromptStore
	source RemoteSkillRegistryFiles
	mu     sync.RWMutex
	files  map[string][]byte
}

func ProvideFrozenPromptFiles(store BusinessSystemPromptStore, files RemoteSkillRegistryFiles) *FrozenPromptFiles {
	independent, _ := store.(IndependentPromptStore)
	return &FrozenPromptFiles{store: independent, source: files}
}

func (f *FrozenPromptFiles) Initialize(ctx context.Context) error {
	if f == nil || f.store == nil {
		return ErrBusinessSystemPromptUnavailable
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files != nil {
		return nil
	}
	files, err := f.store.InitializeIndependentPrompts(ctx, f.source)
	if err != nil {
		return err
	}
	f.files = cloneRemoteSkillFiles(files)
	return nil
}

func (f *FrozenPromptFiles) LoadPublishedFile(ctx context.Context, name string) (RemoteSkillPublicFile, error) {
	if err := ctx.Err(); err != nil {
		return RemoteSkillPublicFile{}, err
	}
	normalized, err := normalizeBundleRelativePath(name)
	if err != nil || normalized != name || strings.ContainsAny(name, "?#") || strings.HasSuffix(name, "/") {
		return RemoteSkillPublicFile{}, ErrRemoteSkillPublicFileNotFound
	}
	if f == nil {
		return RemoteSkillPublicFile{}, ErrBusinessSystemPromptUnavailable
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.files == nil {
		return RemoteSkillPublicFile{}, ErrBusinessSystemPromptUnavailable
	}
	body, ok := f.files[name]
	if !ok {
		return RemoteSkillPublicFile{}, ErrRemoteSkillPublicFileNotFound
	}
	return RemoteSkillPublicFile{Body: bytes.Clone(body), ETag: `"` + hashBusinessSystemPromptBundleBytes(body) + `"`, ContentType: remoteSkillContentType(name)}, nil
}

// ValidateFrozenSkillPublication uses the same paired-generation validator as
// ActivePublication. Only the effective body and complete effective tree leave
// this boundary; there is deliberately no seed fallback or historical rollback.
func ValidateFrozenSkillPublication(revision int64, candidate RemoteSkillCandidate) (RemoteSkillPublication, error) {
	if err := validateStoredPairedRemoteSkillCandidate(candidate, true); err != nil {
		return RemoteSkillPublication{}, err
	}
	return remoteSkillPublicationFromCandidate(revision, candidate)
}

func (s *BusinessSystemPromptService) SetFrozenPromptFiles(files *FrozenPromptFiles) {
	s.frozenFiles = files
}

// Only an exact content restoration inherits compatibility. Typing into the
// restored draft makes it an ordinary private edit, just like any new version.
func PreserveRestoredPromptEcho(historicalBody, draftBody string, historicalEcho bool) bool {
	return historicalEcho && historicalBody == draftBody
}

func (s *BusinessSystemPromptService) PromptRuleHistory(ctx context.Context, ruleID string) ([]PromptHistoryVersion, error) {
	snapshot, ok := s.CurrentSnapshot()
	if !ok || snapshot.RulePolicy == nil {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	found := false
	for _, rule := range snapshot.RulePolicy.Rules {
		if rule.ID == ruleID {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrBusinessSystemPromptVersionNotFound
	}
	store, ok := s.store.(IndependentPromptStore)
	if !ok {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	return store.PromptRuleHistory(ctx, ruleID)
}

// Editing structured content may change text only. Restore is separately
// authorized against the current rule's history before this check is bypassed.
func ValidateStructuredPromptTextEdit(previous, next string) error {
	if err := ValidateClaudeOAuthSystemPromptBlocksConfig(next); err != nil {
		return fmt.Errorf("%w: invalid structured prompt", ErrBusinessSystemPromptInvalid)
	}
	var before, after any
	if json.Unmarshal([]byte(previous), &before) != nil || json.Unmarshal([]byte(next), &after) != nil {
		return ErrBusinessSystemPromptInvalid
	}
	stripText := func(value any) bool {
		var blocks []any
		switch document := value.(type) {
		case map[string]any:
			if _, ok := document["expansion_prompt"]; ok {
				document["expansion_prompt"] = ""
			}
			if document["blocks"] != nil {
				var ok bool
				blocks, ok = document["blocks"].([]any)
				if !ok {
					return false
				}
			}
		case []any:
			blocks = document
		default:
			return false
		}
		for _, raw := range blocks {
			if raw == nil {
				continue
			}
			block, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			if _, ok := block["text"].(string); block["text"] != nil && !ok {
				return false
			}
			block["text"] = ""
		}
		return true
	}
	if !stripText(before) || !stripText(after) || !reflect.DeepEqual(before, after) {
		return fmt.Errorf("%w: structured prompt metadata is immutable", ErrBusinessSystemPromptInvalid)
	}
	return nil
}
