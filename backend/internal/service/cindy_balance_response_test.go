//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCindyBalanceInsufficientResponse(t *testing.T) {
	cindy := newCindyRateLimitAccount(8501, true)
	nonCindy := &Account{
		ID:          8502,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com", "api_key": "test-key"},
	}

	tests := []struct {
		name    string
		account *Account
		status  int
		body    string
		want    bool
	}{
		{name: "exact structured 429", account: cindy, status: http.StatusTooManyRequests, body: exactCindyBudgetExceededBody, want: true},
		{name: "generic 402", account: cindy, status: http.StatusPaymentRequired, body: `{"error":{"type":"budget_exceeded","code":"429"}}`},
		{name: "missing code", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"budget_exceeded"}}`},
		{name: "numeric code", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"budget_exceeded","code":429}}`},
		{name: "wrong code", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"budget_exceeded","code":"402"}}`},
		{name: "wrong type", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error","code":"429"}}`},
		{name: "case changed type", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"BUDGET_EXCEEDED","code":"429"}}`},
		{name: "message only", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"message":"ExceededBudget: User=aigw:v1:cindy:fixture over budget. Spend=3.1, Budget=3.0"}}`},
		{name: "fields win without message", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"budget_exceeded","code":"429"}}`, want: true},
		{name: "ordinary rate limit", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error","message":"too many requests"}}`},
		{name: "malformed 429", account: cindy, status: http.StatusTooManyRequests, body: `ExceededBudget: user over budget`},
		{name: "non-Cindy 429", account: nonCindy, status: http.StatusTooManyRequests, body: exactCindyBudgetExceededBody},
		{name: "other Cindy status", account: cindy, status: http.StatusBadRequest, body: exactCindyBudgetExceededBody},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsCindyBalanceInsufficientResponse(tt.account, tt.status, []byte(tt.body)))
		})
	}
}

func TestClassifyCindyBalanceInsufficientTerminalEvents(t *testing.T) {
	cindy := newCindyRateLimitAccount(8511, true)
	nonCindy := &Account{
		ID:          8512,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com", "api_key": "test-key"},
	}
	tests := []struct {
		name    string
		account *Account
		status  int
		body    string
		want    CindyBalanceSignal
	}{
		{name: "http exact", account: cindy, status: http.StatusTooManyRequests, body: exactCindyBudgetExceededBody, want: CindyBalanceSignalHTTP429},
		{name: "responses failed exact", account: cindy, status: http.StatusOK, body: `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`, want: CindyBalanceSignalResponseFailed},
		{name: "websocket error exact", account: cindy, status: http.StatusOK, body: `{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`, want: CindyBalanceSignalErrorEvent},
		{name: "failed numeric code rejected", account: cindy, status: http.StatusOK, body: `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":429}}}`},
		{name: "failed top level error rejected", account: cindy, status: http.StatusOK, body: `{"type":"response.failed","error":{"type":"budget_exceeded","code":"429"}}`},
		{name: "wrong event rejected", account: cindy, status: http.StatusOK, body: `{"type":"response.completed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`},
		{name: "non cindy event rejected", account: nonCindy, status: http.StatusOK, body: `{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ClassifyCindyBalanceInsufficient(tt.account, tt.status, []byte(tt.body)))
		})
	}
}

func TestClassifyCindyBalanceInsufficientRejectsTerminalShapesOutsideHTTP200(t *testing.T) {
	cindy := newCindyRateLimitAccount(8513, true)
	terminalShapes := []string{
		`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`,
		`{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`,
	}
	for _, statusCode := range []int{
		http.StatusCreated,
		http.StatusBadRequest,
		http.StatusServiceUnavailable,
	} {
		for _, body := range terminalShapes {
			require.Equal(t, CindyBalanceSignalNone,
				ClassifyCindyBalanceInsufficient(cindy, statusCode, []byte(body)),
				"status=%d body=%s", statusCode, body)
		}
	}
	require.Equal(t, CindyBalanceSignalNone, ClassifyCindyBalanceInsufficient(
		cindy,
		http.StatusTooManyRequests,
		[]byte(`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`),
	))
	require.Equal(t, CindyBalanceSignalHTTP429, ClassifyCindyBalanceInsufficient(
		cindy,
		http.StatusTooManyRequests,
		[]byte(`{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`),
	), "HTTP 429 top-level exact error retains priority over the event-shape guard")
}

func TestIsAmbiguousCindyBalanceTerminalEvent(t *testing.T) {
	cindy := newCindyRateLimitAccount(8521, true)
	require.True(t, IsAmbiguousCindyBalanceTerminalEvent(cindy, []byte(`{"type":"response.failed","response":{"error":{"message":"request failed"}}}`)))
	require.True(t, IsAmbiguousCindyBalanceTerminalEvent(cindy, []byte(`{"type":"error","error":{"type":"budget_exceeded","message":"request failed"}}`)))
	require.False(t, IsAmbiguousCindyBalanceTerminalEvent(cindy, []byte(`{"type":"response.failed","response":{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}}`)))
	require.False(t, IsAmbiguousCindyBalanceTerminalEvent(cindy, []byte(`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`)))
}
