package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptSeedBundleManifestMatchesPinnedDigest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "skill-bundles", BusinessSystemPromptSeedBundleID, BusinessSystemPromptBundleManifestName))
	require.NoError(t, err)
	require.Equal(t, BusinessSystemPromptSeedBundleManifestSHA256, hashBusinessSystemPromptBundleBytes(raw))
}

func TestNormalizeBusinessSystemPromptComposition(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		bundleID   string
		manifest   string
		wantMode   string
		wantBundle string
		wantHash   string
		wantErr    bool
	}{
		{name: "empty defaults to inline", wantMode: BusinessSystemPromptCompositionInline},
		{name: "remote skill keeps fixed bundle identity", mode: " remote_skill ", bundleID: " codexrip-reverse-skill ", wantMode: BusinessSystemPromptCompositionRemoteSkill, wantBundle: BusinessSystemPromptRemoteSkillBundleID},
		{name: "codex skill hybrid keeps fixed bundle identity", mode: " codex_skill_hybrid ", bundleID: " codexrip-reverse-skill ", wantMode: BusinessSystemPromptCompositionCodexSkillHybrid, wantBundle: BusinessSystemPromptRemoteSkillBundleID},
		{name: "codex skill hybrid requires bundle id", mode: "codex_skill_hybrid", wantErr: true},
		{name: "codex skill hybrid rejects another bundle id", mode: "codex_skill_hybrid", bundleID: "another-skill", wantErr: true},
		{name: "codex skill hybrid follows active registry", mode: "codex_skill_hybrid", bundleID: "codexrip-reverse-skill", manifest: strings.Repeat("a", 64), wantErr: true},
		{name: "remote skill requires bundle id", mode: "remote_skill", wantErr: true},
		{name: "remote skill rejects another bundle id", mode: "remote_skill", bundleID: "another-skill", wantErr: true},
		{name: "remote skill rejects version pin", mode: "remote_skill", bundleID: "codexrip-reverse-skill", manifest: strings.Repeat("a", 64), wantErr: true},
		{name: "offline normalizes digest", mode: " offline_bundle ", bundleID: " moxinggang-reverse-skill ", manifest: strings.ToUpper(BusinessSystemPromptSeedBundleManifestSHA256), wantMode: BusinessSystemPromptCompositionOfflineBundle, wantBundle: BusinessSystemPromptSeedBundleID, wantHash: BusinessSystemPromptSeedBundleManifestSHA256},
		{name: "inline rejects bundle reference", mode: "inline", bundleID: "bundle", manifest: strings.Repeat("a", 64), wantErr: true},
		{name: "offline requires bundle id", mode: "offline_bundle", manifest: strings.Repeat("a", 64), wantErr: true},
		{name: "offline requires sha256", mode: "offline_bundle", bundleID: "bundle", manifest: "not-a-digest", wantErr: true},
		{name: "unknown mode rejected", mode: "network", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBusinessSystemPromptComposition(tt.mode, tt.bundleID, tt.manifest)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMode, got.Mode)
			require.Equal(t, tt.wantBundle, got.BundleID)
			require.Equal(t, tt.wantHash, got.BundleManifestSHA256)
		})
	}
}
