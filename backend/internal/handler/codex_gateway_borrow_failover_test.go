package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexGatewayBorrowFailoverSkipsSameAccountRetry(t *testing.T) {
	account := &service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
	failure := service.NewCodexGatewayBorrowRequestFailure(errors.New("source proxy connection refused"))
	require.Zero(t, openAISameAccountRetryLimit(account, failure, true))
	require.True(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.ShouldReportAccountScheduleFailure())
	state := newOpenAIFailoverRetryState()
	action := state.HandleHTTP(context.Background(), &service.OpenAIGatewayService{}, account, "gpt-6-astra", failure, true, 0, "borrow-test")
	require.Equal(t, openAIFailoverRetrySwitchAccount, action)
	require.Zero(t, state.sameAccountRetryCount[account.ID])
	// The new reason is narrow: ordinary transport and upstream failures retain
	// their existing bounded retry on OAuth accounts.
	require.Equal(t, 1, openAISameAccountRetryLimit(account, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAITransientTransportFailureReason}, true))
	require.Equal(t, 1, openAISameAccountRetryLimit(account, &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}, true))
}
