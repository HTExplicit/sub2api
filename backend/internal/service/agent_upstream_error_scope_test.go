//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentReportedUpstream400IsNotClientValidation(t *testing.T) {
	s := &OpenAIGatewayService{}
	for _, tc := range []struct {
		name, body string
		retry      bool
	}{
		{"nested_upstream_failure", `{"error":{"code":"400","message":"400","param":"","type":"upstream_error"}}`, true},
		{"client_validation", `{"error":{"code":"invalid_request_error","message":"invalid input","type":"invalid_request_error"}}`, false},
		{"parameter_identified", `{"error":{"code":"400","param":"tools","type":"upstream_error"}}`, false},
		{"opaque_number_only", `{"error":{"code":"400","message":"400"}}`, false},
		{"continuation", `{"error":{"code":"invalid_encrypted_content","type":"upstream_error"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.retry, s.shouldFailoverOpenAIUpstreamResponse(400, "", []byte(tc.body)))
		})
	}
}

func TestAgentRequestBudgetRejectionDoesNotBecomeAccountAuthCooldown(t *testing.T) {
	a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"pool_mode": true, "base_url": "https://fixture.example"}}
	s := &OpenAIGatewayService{}
	body := []byte(`{"error":{"type":"balance_insufficient_error","code":"balance_insufficient","message":"fixture"}}`)
	failure := s.newOpenAIAccountFailoverError(a, http.StatusForbidden, nil, body, "", false, true)
	require.False(t, failure.RetryableOnSameAccount)
	require.True(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.ShouldReportAccountScheduleFailure())
	s.CooldownOpenAIRetryExhausted(context.Background(), a, "fixture", failure)
	require.False(t, s.isOpenAIAccountRuntimeBlocked(a))
}

func TestAgentRequestBudgetClassificationPrecedesHTTPFailover(t *testing.T) {
	a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://fixture.example"}}
	s := &OpenAIGatewayService{}
	body := []byte(`{"error":{"type":"balance_insufficient_error","code":"balance_insufficient"}}`)
	for _, status := range []int{http.StatusPaymentRequired, http.StatusForbidden} {
		require.True(t, s.shouldFailoverOpenAIUpstreamResponseForAccount(a, status, "", body), "status=%d", status)
		require.True(t, shouldFailoverOpenAIPassthroughResponse(a, status, body), "status=%d", status)
	}
	for _, body := range []string{
		`{"error":{"type":"balance_insufficient_error","code":"invalid_api_key"}}`,
		`{"error":{"type":"authentication_error","code":"balance_insufficient"}}`,
	} {
		require.False(t, isOpenAIRequestBudgetRejection(a, http.StatusForbidden, []byte(body)))
	}
}
