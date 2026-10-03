//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemPromptSetBindingsSkipsAccountsWithoutInsertionPoint(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{ID: 2, Platform: PlatformTypeSafe, Type: AccountTypeAPIKey},
		{ID: 3, Platform: PlatformAntigravity, Type: AccountTypeAPIKey},
	}}
	svc := NewSystemPromptService(nil, repo, nil)
	off := SystemPromptBinding{Mode: SystemPromptModeOff}

	updated, err := svc.SetBindings(context.Background(), []int64{1, 2, 3, 2}, off)
	require.NoError(t, err)
	require.Equal(t, int64(2), updated)
	require.Equal(t, []int64{1, 2, 3}, repo.getByIDsIDs)
	require.Equal(t, []int64{1, 3}, repo.bulkUpdateIDs)
	require.Equal(t, map[string]any{AccountExtraSystemPromptKey: off}, repo.lastBulkUpdate.Extra)

	// A selection that holds only such accounts writes nothing.
	repo.getByIDsAccounts = repo.getByIDsAccounts[1:2]
	updated, err = svc.SetBindings(context.Background(), []int64{2}, off)
	require.NoError(t, err)
	require.Zero(t, updated)
	require.Empty(t, repo.bulkUpdateIDs)
}
