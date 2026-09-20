package service

import (
	"context"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type extensionAccountInventory struct {
	AccountRepository
	statuses []string
}

func (r *extensionAccountInventory) ListAllWithFilters(_ context.Context, platform, accountType, status, _ string, _ int64, _ string) ([]Account, error) {
	r.statuses = append(r.statuses, status)
	return []Account{
		{ID: 1, Platform: platform, Type: accountType, Status: StatusActive},
		{ID: 2, Platform: platform, Type: accountType, Status: StatusDisabled},
		{ID: 3, Platform: platform, Type: accountType, Status: StatusError},
	}, nil
}

func TestExtensionDirectoryHonorsInactiveAccountQuery(t *testing.T) {
	repo := &extensionAccountInventory{}
	directory := &OpenAIGatewayService{accountRepo: repo}
	query := extensionv1.AccountQuery{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, IncludeInactive: true}
	accounts, err := directory.ListExtensionAccounts(context.Background(), query)
	require.NoError(t, err)
	require.Len(t, accounts, 3)
	require.Equal(t, "", repo.statuses[0], "inactive inclusion must reach the storage query")
	query.IncludeInactive = false
	accounts, err = directory.ListExtensionAccounts(context.Background(), query)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, StatusActive, repo.statuses[1])
}
