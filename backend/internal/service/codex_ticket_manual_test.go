package service

import (
	"context"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type ticketTokenFailureRepo struct {
	AccountRepository
	account      *Account
	statusWrites int
}

func (r *ticketTokenFailureRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *ticketTokenFailureRepo) SetError(context.Context, int64, string) error {
	r.statusWrites++
	return nil
}
func TestCodexTicketExpiredCredentialFailurePreservesAccountStatus(t *testing.T) {
	a := ticketTestAccount(41)
	a.Status, a.Schedulable, a.ErrorMessage = "disabled", false, "original diagnostic"
	a.Credentials["expires_at"] = time.Now().Add(-time.Minute).Format(time.RFC3339)
	delete(a.Credentials, "refresh_token")
	repo := &ticketTokenFailureRepo{account: a}
	provider := NewOpenAITokenProvider(repo, nil, nil)
	gateway := &OpenAIGatewayService{accountRepo: repo, openAITokenProvider: provider}
	_, err := gateway.ResolveExtensionIdentity(context.Background(), extensionv1.AccountQuery{AccountID: a.ID, PrepareCredentials: true})
	require.Error(t, err)
	require.Zero(t, repo.statusWrites)
	require.Equal(t, "disabled", a.Status)
	require.False(t, a.Schedulable)
	require.Equal(t, "original diagnostic", a.ErrorMessage)
	_, err = provider.GetAccessToken(context.Background(), a)
	require.Error(t, err)
	require.Equal(t, 1, repo.statusWrites, "ordinary inference still quarantines unusable credentials")
}
