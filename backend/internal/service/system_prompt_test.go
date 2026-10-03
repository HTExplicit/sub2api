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

// Besides the binding endpoint only account creation stores a binding, and only
// for an account that can take a prompt: data import and duplicate create from
// a stored extra. The key-level writers of extra store none on any platform.
func TestSystemPromptBindingHasNoOtherWriter(t *testing.T) {
	binding := map[string]any{"mode": SystemPromptModeOff}
	given := func() map[string]any {
		return map[string]any{AccountExtraSystemPromptKey: binding, "ordinary_setting": true}
	}
	created := func(platform string) map[string]any {
		account, err := buildAccountForCreate(&CreateAccountInput{
			Name: "imported", Platform: platform, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "key"},
		}, given())
		require.NoError(t, err)
		return account.Extra
	}
	require.Equal(t, given(), created(PlatformAnthropic))
	require.Equal(t, map[string]any{"ordinary_setting": true}, created(PlatformTypeSafe))

	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}}
	svc := &adminServiceImpl{accountRepo: repo}
	require.NoError(t, svc.UpdateAccountExtra(context.Background(), 1, given()))
	require.Equal(t, []map[string]any{{"ordinary_setting": true}}, repo.updates[1])
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: given()})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)
	require.Equal(t, map[string]any{"ordinary_setting": true}, repo.bulkUpdates[0].Extra)
}
