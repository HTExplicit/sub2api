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
		{name: "Cindy 402 always matches", account: cindy, status: http.StatusPaymentRequired, body: "not-json", want: true},
		{name: "structured 429", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"budget_exceeded","message":"budget used"}}`, want: true},
		{name: "structured 429 ignores case", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"BuDgEt_ExCeEdEd"}}`, want: true},
		{name: "message fallback", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"message":"ExceededBudget: User=aigw:v1:cindy over budget. Spend=3.1, Budget=3.0"}}`, want: true},
		{name: "message fallback normalizes case and whitespace", account: cindy, status: http.StatusTooManyRequests, body: "{\"error\":{\"message\":\"EXCEEDED BUDGET: user\\nOVER   BUDGET\"}}", want: true},
		{name: "ordinary rate limit", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error","message":"too many requests"}}`},
		{name: "explicit non-budget type disables message fallback", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error","message":"ExceededBudget: user over budget"}}`},
		{name: "only one fallback marker", account: cindy, status: http.StatusTooManyRequests, body: `{"error":{"message":"ExceededBudget"}}`},
		{name: "malformed 429", account: cindy, status: http.StatusTooManyRequests, body: `ExceededBudget: user over budget`},
		{name: "non-Cindy 429", account: nonCindy, status: http.StatusTooManyRequests, body: `{"error":{"type":"budget_exceeded"}}`},
		{name: "other Cindy status", account: cindy, status: http.StatusBadRequest, body: `{"error":{"type":"budget_exceeded"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsCindyBalanceInsufficientResponse(tt.account, tt.status, []byte(tt.body)))
		})
	}
}
