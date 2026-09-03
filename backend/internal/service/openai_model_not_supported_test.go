//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const cindyModelNotSupportedBody = `{"error":{"code":400,"type":"model_not_supported","message":"Model 'gpt-5.6-luna' is temporarily not supported. Please choose another model from GET /v1/models."}}`

func TestOpenAIModelNotSupportedClassifierIsStrict(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"exact cindy response", http.StatusBadRequest, cindyModelNotSupportedBody, true},
		{"nested response error", http.StatusBadRequest, `{"response":{"error":{"code":"400","type":"model_not_supported","message":"model is not supported"}}}`, true},
		{"wrong status", http.StatusBadGateway, cindyModelNotSupportedBody, false},
		{"wrong code", http.StatusBadRequest, `{"error":{"code":401,"type":"model_not_supported","message":"model is not supported"}}`, false},
		{"wrong type", http.StatusBadRequest, `{"error":{"code":400,"type":"invalid_request_error","message":"model is not supported"}}`, false},
		{"unsupported parameter", http.StatusBadRequest, `{"error":{"code":400,"type":"model_not_supported","message":"parameter is unsupported"}}`, false},
		{"free form text", http.StatusBadRequest, `{"error":{"message":"model is not supported"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isOpenAIModelNotSupportedError(tt.status, "", []byte(tt.body)))
		})
	}
}

func TestOpenAIModelNotSupportedStreamAndWSSemantics(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"400","type":"model_not_supported","message":"Model 'gpt-5.6-luna' is temporarily not supported"}}}`)
	require.True(t, isOpenAIModelNotSupportedPayload(payload))
	require.Equal(t, http.StatusBadRequest, openAIStreamFailureStatus(payload, extractOpenAISSEErrorMessage(payload)))
	require.True(t, openAIStreamFailedEventShouldFailover(payload, extractOpenAISSEErrorMessage(payload)))
	require.True(t, openAIStreamErrorEventShouldFailover(payload, extractOpenAISSEErrorMessage(payload)))
	require.Equal(t, http.StatusBadRequest, openAIWSPayloadStatus(payload))
	require.Equal(t, "model_not_supported", func() string {
		reason, _ := classifyOpenAIWSErrorEventFromRaw("", "model_not_supported", "model is not supported")
		return reason
	}())
	require.Equal(t, http.StatusBadRequest, openAIWSErrorHTTPStatusFromRaw("400", "model_not_supported"))
}

func TestRateLimitService_HandleUpstreamError_CindyModelNotSupportedUsesModelCooldown(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{
		ID:          701,
		Platform:    PlatformCindy,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "test-key"},
	}

	handled := svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, nil, []byte(cindyModelNotSupportedBody), "gpt-5.6-luna")

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "openai/gpt-5.6-luna", repo.modelRateLimitCalls[0].scope)
	require.Equal(t, string(openAIModelNotSupportedReason), repo.modelRateLimitCalls[0].reason)
	require.WithinDuration(t, time.Now().Add(upstreamModelNotSupportedCooldown), repo.modelRateLimitCalls[0].resetAt, 5*time.Second)
}

func TestRateLimitService_HandleUpstreamError_ModelNotSupportedDoesNotPersistForNonCindy(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{
		ID:          702,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"base_url": "https://api.openai.com"},
	}

	require.False(t, svc.HandleUpstreamModelNotFound(context.Background(), account, "gpt-5.6-luna", http.StatusBadRequest, []byte(cindyModelNotSupportedBody)))
	require.Empty(t, repo.modelRateLimitCalls)
}
