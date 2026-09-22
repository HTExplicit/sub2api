package service

import (
	"context"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func validateCurrentRemoteSkillTree(files map[string][]byte) error {
	return validateRemoteSkillTreeFacts(files, true)
}
func validateGenericRemoteSkillTree(files map[string][]byte) error {
	return validateRemoteSkillTreeFacts(files, false)
}
func validateRemoteSkillTreeFacts(files map[string][]byte, current bool) error {
	entries := make([]extensionv1.SkillTreeEntry, 0, len(files))
	for name, body := range files {
		entries = append(entries, extensionv1.SkillTreeEntry{Path: name, Bytes: len(body), SHA256: hashBusinessSystemPromptBundleBytes(body), UTF8: utf8.Valid(body)})
	}
	// The plugin validates its exact release source and its complete link graph.
	// Matching these host-computed hashes proves the supplied tree has the same
	// bytes without copying a multi-megabyte corpus into a single RPC message.
	return invokePromptManagement(context.Background(), "skills.tree.check", extensionv1.SkillTreeCheck{Entries: entries, Current: current}, nil)
}
func portableRemoteSkillPathKey(name string) string { return extensionv1.FoldDocumentPath(name) }
