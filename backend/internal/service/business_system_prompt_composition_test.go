package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
		{name: "codex skill hybrid keeps fixed bundle identity", mode: " codex_skill_hybrid ", bundleID: " codexrip-reverse-skill ", wantMode: BusinessSystemPromptCompositionCodexSkillHybrid, wantBundle: BusinessSystemPromptRemoteSkillBundleID},
		{name: "codex skill hybrid requires bundle id", mode: "codex_skill_hybrid", wantErr: true},
		{name: "codex skill hybrid rejects another bundle id", mode: "codex_skill_hybrid", bundleID: "another-skill", wantErr: true},
		{name: "codex skill hybrid follows active registry", mode: "codex_skill_hybrid", bundleID: "codexrip-reverse-skill", manifest: strings.Repeat("a", 64), wantErr: true},
		{name: "remote skill removed", mode: "remote_skill", bundleID: "codexrip-reverse-skill", wantErr: true},
		{name: "offline bundle removed", mode: "offline_bundle", bundleID: "moxinggang-reverse-skill", manifest: strings.Repeat("a", 64), wantErr: true},
		{name: "inline rejects bundle reference", mode: "inline", bundleID: "bundle", manifest: strings.Repeat("a", 64), wantErr: true},
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
