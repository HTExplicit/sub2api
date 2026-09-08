package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIMergeStreamModelNotFoundPrecedesOpaque400(t *testing.T) {
	request := []byte(`{"model":"missing-model","stream":true,"input":"hello"}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"type":"invalid_request_error","message":"unknown provider for model missing-model"}}`),
	}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	svc.accountRepo = &modelNotFoundManagedAccountRepo{}
	c := newOpenAIRejectedFieldTestContext(request)

	result, err := svc.Forward(context.Background(), c, newOpenAIRejectedFieldTestAccount(), request)

	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Nil(t, result)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.IsOpenAIRequestRejected())
	require.Len(t, upstream.bodies, 1)
	require.False(t, c.Writer.Written(), "missing-model failover must precede a generic streaming 400 terminal")
}

func TestOpenAIMergeRequestFailuresPrecedeModelAvailability(t *testing.T) {
	svc := &OpenAIGatewayService{accountRepo: &modelNotFoundManagedAccountRepo{}}
	account := newOpenAIRejectedFieldTestAccount()
	for _, tc := range []struct {
		name string
		body string
	}{
		{"safety", `{"error":{"code":"cyber_policy","message":"model not found"}}`},
		{"previous state", `{"error":{"code":"previous_response_not_found","message":"model not found"}}`},
		{"reasoning state", `{"error":{"code":"invalid_encrypted_content","message":"model not found"}}`},
		{"context limit", `{"error":{"code":"context_length_exceeded","message":"model not found"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(account, http.StatusBadRequest, "model not found", []byte(tc.body)))
			require.False(t, svc.shouldFailoverOpenAIUpstreamResponseForAccount(account, http.StatusBadRequest, "model not found", []byte(tc.body)))
		})
	}
}

func TestOpenAIMergeSafetyTerminalSkipsAccountSideEffects(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadGateway} {
			t.Run(fmt.Sprintf("%s/%d", accountType, status), func(t *testing.T) {
				repo := &openAIAuthPolicyAccountRepo{}
				rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
				svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rateLimits}
				rateLimits.SetAccountRuntimeBlocker(svc)
				account := &Account{
					ID: 5109, Platform: PlatformOpenAI, Type: accountType, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"refresh_token": "fixture", "pool_mode": true},
				}
				body := []byte(`{"error":{"code":"cyber_policy","message":"blocked"}}`)
				payload := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"cyber_policy","message":"blocked"}}}`)

				require.True(t, isOpenAIRequestScopedSafetyRejection(body))
				require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "blocked", body))
				require.False(t, shouldFailoverOpenAIPassthroughResponse(account, status, body))
				require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, body, "model"))
				_, disabled := svc.handleOpenAIStreamTerminalAccountSideEffectsWithContext(context.Background(), account, payload, "blocked", nil, "model")
				require.False(t, disabled)
				require.Nil(t, svc.nonStreamingTerminalFailureFailover(nil, nil, account, true, "response.failed", payload, "blocked", "model"))
				require.Zero(t, repo.setErrorCalls)
				require.Zero(t, repo.tempCalls)
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			})
		}
	}
}

func TestOpenAIMergeSafetyPredicateDoesNotScanEchoedText(t *testing.T) {
	require.False(t, isOpenAIRequestScopedSafetyRejection([]byte(`{"error":{"code":"model_not_found","message":"model unavailable"},"echo":{"error":{"code":"cyber_policy"}}}`)))
	require.False(t, isOpenAIRequestScopedSafetyRejection([]byte(`{"error":{"message":"cyber_policy is an example"}}`)))
	require.True(t, isOpenAIRequestScopedSafetyRejection([]byte(`{"type":"response.failed","response":{"error":{"code":"cyber_policy"}}}`)))
}

func TestOpenAIMergePassthroughSafetyTerminalMarksAuthoritativeUsage(t *testing.T) {
	for _, eventType := range []string{"response.failed", "response.done"} {
		t.Run(eventType, func(t *testing.T) {
			svc := newOpenAIRefusalRecoveryPipelineService(t, true, true)
			c, recorder := newOpenAIRefusalRecoveryTestContext()
			payload := fmt.Sprintf(`{"type":%q,"response":{"id":"resp_safety_usage","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":17,"output_tokens":3,"total_tokens":20}}}`, eventType)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("event: " + eventType + "\ndata: " + payload + "\n\n")),
			}

			result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c,
				&Account{ID: 5110, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, time.Now(), "", "")

			var failover *UpstreamFailoverError
			require.Error(t, err)
			require.False(t, errors.As(err, &failover))
			require.NotNil(t, result)
			require.Equal(t, 17, result.usage.InputTokens)
			require.Equal(t, 3, result.usage.OutputTokens)
			mark := GetOpsCyberPolicy(c)
			require.NotNil(t, mark)
			require.Equal(t, 17, mark.UpstreamInTok, "billing reads the safety mark, not only the forward result")
			require.Equal(t, 3, mark.UpstreamOutTok)
			require.Contains(t, recorder.Body.String(), "cyber_policy")
			require.NotContains(t, recorder.Body.String(), "response.completed")
		})
	}
}
