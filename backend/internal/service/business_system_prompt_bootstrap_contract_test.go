package service

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptBootstrapsAreContentAddressedAndPinnedByPrompt(t *testing.T) {
	root := filepath.Join("..", "..", "..", "deploy", "skill-registry", "bootstrap")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	labels := map[string]string{
		"bootstrap-reverse-skill.ps1": "POWERSHELL_BOOTSTRAP_SHA256",
		"bootstrap-reverse-skill.py":  "PYTHON_BOOTSTRAP_SHA256",
	}
	seen := map[string]bool{}
	contentAddressed := 0
	for _, entry := range entries {
		require.True(t, entry.IsDir())
		files, err := os.ReadDir(filepath.Join(root, entry.Name()))
		require.NoError(t, err)
		if len(files) == 0 {
			continue
		}
		contentAddressed++
		require.Regexp(t, `^[0-9a-f]{64}$`, entry.Name())
		require.Len(t, files, 1)
		name := files[0].Name()
		label, ok := labels[name]
		require.True(t, ok)
		raw, err := os.ReadFile(filepath.Join(root, entry.Name(), name))
		require.NoError(t, err)
		digest := sha256.Sum256(raw)
		hash := hex.EncodeToString(digest[:])
		require.Equal(t, entry.Name(), hash)
		require.Contains(t, embeddedBusinessSystemPrompt, "https://codexrip.vip/skills/bootstrap/"+hash+"/"+name)
		require.Contains(t, embeddedBusinessSystemPrompt, label+" = "+hash)
		seen[filepath.Ext(name)] = true
	}
	require.Equal(t, 2, contentAddressed)
	require.True(t, seen[".ps1"])
	require.True(t, seen[".py"])
}
