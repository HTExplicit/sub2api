package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildCindyDuplicateIdentityInventoryIsDeterministicAndRedacted(t *testing.T) {
	created := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	accounts := []Account{
		{ID: 9, Platform: PlatformCindy, Type: AccountTypeAPIKey, Status: StatusActive, CreatedAt: created.Add(2 * time.Hour), Credentials: map[string]any{"base_url": "https://API.LAXAROUTER.ai/", "api_key": "secret-key"}},
		{ID: 3, Platform: PlatformCindy, Type: AccountTypeAPIKey, Status: StatusActive, CreatedAt: created, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "secret-key"}},
		{ID: 5, Platform: PlatformCindy, Type: AccountTypeAPIKey, Status: StatusDisabled, CreatedAt: created.Add(-time.Hour), Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "secret-key"}},
	}
	result := BuildCindyDuplicateIdentityInventory(accounts)
	require.Len(t, result, 1)
	require.Equal(t, int64(3), result[0].ProposedOwnerID)
	require.Equal(t, []int64{5, 9}, result[0].OtherAccountIDs)
	require.Len(t, result[0].IdentityHash, 64)
	require.NotContains(t, result[0].IdentityHash, "secret-key")
}
