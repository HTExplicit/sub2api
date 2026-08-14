//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const exactCindyBudgetExceededBody = `{"error":{"message":"ExceededBudget: User=aigw:v1:cindy:fixture-account over budget. Spend=3.0533505, Budget=3.0","type":"budget_exceeded","param":null,"code":"429"}}`

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
