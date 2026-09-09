//go:build unit

package schema_test

import (
	"testing"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/ent/userplatformquota"
	"github.com/stretchr/testify/require"
)

func TestUserPlatformQuotaValidatorAllowsCanonicalCindy(t *testing.T) {
	require.NotNil(t, userplatformquota.PlatformValidator)
	require.NoError(t, userplatformquota.PlatformValidator("cindy"))
	require.Error(t, userplatformquota.PlatformValidator("unknown"))
}

func TestUserPlatformQuotaValidatorAllowsMiniMaxAndAllExistingPlatforms(t *testing.T) {
	// runtime wires this validator directly from the schema; exercise the
	// generated entrypoint without regenerating unrelated ent code.
	require.NotNil(t, userplatformquota.PlatformValidator)
	for _, platform := range []string{
		"anthropic", "openai", "gemini", "antigravity", "grok",
		"kimi", "zhipu", "deepseek", "minimax", "cindy",
	} {
		require.NoError(t, userplatformquota.PlatformValidator(platform), platform)
	}
	for _, platform := range []string{"", "MiniMax", "Cindy", "unknown"} {
		require.Error(t, userplatformquota.PlatformValidator(platform), platform)
	}
}
