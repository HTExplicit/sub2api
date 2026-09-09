package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserPlatformQuotasCindyMigration(t *testing.T) {
	content, err := FS.ReadFile("237_user_platform_quotas_add_cindy.sql")
	require.NoError(t, err)
	// This migration is already recorded in downstream databases. The new
	// MiniMax compatibility belongs in the unpublished upstream 237 and 241,
	// not in a rewritten historical Cindy migration or a checksum exception.
	checksum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
	require.Equal(t, "0e61f662164c98f0710794bf27e4f116b2ab68edb8ca19425d2e080b865d8cf3", hex.EncodeToString(checksum[:]))

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'cindy'))")
}
