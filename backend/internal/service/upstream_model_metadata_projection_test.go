package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaleUpstreamCapacityIsNotProjectedIntoCodexManifest(t *testing.T) {
	account := &Account{ID: 9202, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://old.example/v1"}}
	oldIdentity := UpstreamModelMetadataSourceIdentity(account)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Source: "upstream", SourceIdentity: oldIdentity,
		Models: map[string]UpstreamModelMetadata{"unknown-model": {
			ID: "unknown-model", ContextWindow: 900000, MaxContextWindow: 1000000,
			CapacitySource: ModelContextSourceUpstream,
		}},
	})
	account.Credentials["base_url"] = "https://new.example/v1"
	body, err := applySyncedAPIKeyCodexModelMetadata([]byte(`{"models":[{"slug":"unknown-model"}]}`), account, true)
	require.NoError(t, err)
	require.NotContains(t, strings.TrimSpace(string(body)), "context_window")
	require.NotContains(t, strings.TrimSpace(string(body)), "max_context_window")
}
