package registry

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const (
	RemoteSkillUpstreamSourceID            = "moxinggang"
	RemoteSkillUpstreamRoot                = "https://moxinggang.com/skills/security-research/current"
	RemoteSkillPublicRoot                  = "https://codexrip.vip/skills/security-research/current"
	remoteSkillMaxFileCount                = 2000
	remoteSkillMaxTotalBytes               = 256 << 20
	businessSystemPromptBundleMaxFileBytes = 64 << 20
)

//go:embed seed/manifest.json all:seed/tree all:seed/pinned
var remoteSkillSeedFS embed.FS

//go:embed prompts/codexrip_reverse_skill_system_prompt.txt
var defaultRemotePromptRaw string

func DefaultPrompt() string { return strings.TrimSuffix(defaultRemotePromptRaw, "\n") }

var ErrBusinessSystemPromptBundleInvalid = errors.New("invalid skill bundle")

func normalizeBundleRelativePath(value string) (string, error) {
	return extensionv1.NormalizeDocumentPath(value)
}
func hashBusinessSystemPromptBundleBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validRemoteSkillSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}

func readRemoteSkillTreeFS(tree fs.FS, root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(tree, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(tree, name)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(strings.TrimPrefix(name, root), "/")] = raw
		return nil
	})
	return files, err
}

// SeedFS exposes read-only source bytes for independent contract fixtures.
func SeedFS() fs.FS { return remoteSkillSeedFS }
