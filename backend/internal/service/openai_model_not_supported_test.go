//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
		{"unquoted model response", http.StatusBadRequest, `{"error":{"code":400,"type":"model_not_supported","message":"Model gpt-5.6-luna is not supported"}}`, true},
		{"nested response error", http.StatusBadRequest, `{"response":{"error":{"code":"400","type":"model_not_supported","message":"model is not supported"}}}`, true},
		{"wrong status", http.StatusBadGateway, cindyModelNotSupportedBody, false},
		{"missing code", http.StatusBadRequest, `{"error":{"type":"model_not_supported","message":"model is not supported"}}`, false},
		{"empty code", http.StatusBadRequest, `{"error":{"code":"","type":"model_not_supported","message":"model is not supported"}}`, false},
		{"wrong code", http.StatusBadRequest, `{"error":{"code":401,"type":"model_not_supported","message":"model is not supported"}}`, false},
		{"wrong type", http.StatusBadRequest, `{"error":{"code":400,"type":"invalid_request_error","message":"model is not supported"}}`, false},
		{"unsupported parameter", http.StatusBadRequest, `{"error":{"code":400,"type":"model_not_supported","message":"parameter is unsupported"}}`, false},
		{"unsupported model feature", http.StatusBadRequest, `{"error":{"code":400,"type":"model_not_supported","message":"model output format is unsupported"}}`, false},
		{"unsupported model parameter", http.StatusBadRequest, `{"error":{"code":400,"type":"model_not_supported","message":"unsupported model parameter"}}`, false},
		{"model unsupported parameter", http.StatusBadRequest, `{"error":{"code":400,"type":"model_not_supported","message":"model is unsupported parameter"}}`, false},
		{"free form text", http.StatusBadRequest, `{"error":{"message":"model is not supported"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isOpenAIModelNotSupportedError(tt.status, "", []byte(tt.body)))
		})
	}
}

func TestLegacyCindyModelCooldownUsesCanonicalKeyForReads(t *testing.T) {
	reset := time.Now().Add(time.Minute)
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"},
		Extra: map[string]any{modelRateLimitsKey: map[string]any{
			"openai/gpt-5.6-luna": map[string]any{"rate_limit_reset_at": reset.UTC().Format(time.RFC3339)},
		}},
	}
	require.False(t, account.IsSchedulableForModelWithContext(context.Background(), "gpt-5.6-luna"))
	require.True(t, account.IsSchedulableForModelWithContext(context.Background(), "gpt-5.6-sol"))
}

func TestLegacyCindyCompactCooldownUsesForwardedCompactTarget(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"base_url": "https://api.laxarouter.ai",
			"compact_model_mapping": map[string]any{
				"gpt-5.6-luna": "luna-compact-wire",
			},
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"luna-compact-wire": map[string]any{
					"rate_limit_reset_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
				},
			},
			"openai_passthrough": true,
		},
	}
	ctx := WithOpenAIForwardModel(context.Background(), "gpt-5.6-luna", true)
	require.False(t, account.IsSchedulableForModelWithContext(ctx, "gpt-5.6-luna"))
}

func TestLegacyCindyCompactMappingChecksCanonicalKey(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"base_url": "https://api.laxarouter.ai",
			"compact_model_mapping": map[string]any{
				"openai/gpt-5.6-luna": "luna-compact-wire",
			},
		},
		Extra: map[string]any{
			"openai_passthrough": true,
			modelRateLimitsKey: map[string]any{
				"luna-compact-wire": map[string]any{
					"rate_limit_reset_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
				},
			},
		},
	}
	ctx := WithOpenAIForwardModel(context.Background(), "gpt-5.6-luna", true)
	require.False(t, account.IsSchedulableForModelWithContext(ctx, "gpt-5.6-luna"))
	require.Equal(t, "luna-compact-wire", resolveOpenAIAccountUpstreamModelForRequest(account, "gpt-5.6-luna", true))
}

func TestLegacyCindyCompactCooldownWritesForwardedCompactTarget(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{
		ID: 704, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"base_url": "https://api.laxarouter.ai",
			"compact_model_mapping": map[string]any{
				"gpt-5.6-luna": "luna-compact-wire",
			},
		},
		Extra: map[string]any{"openai_passthrough": true},
	}
	ctx := WithOpenAIForwardModel(context.Background(), "gpt-5.6-luna", true)

	require.True(t, svc.HandleUpstreamModelNotFound(
		ctx, account, "luna-compact-wire", http.StatusBadRequest, []byte(cindyModelNotSupportedBody),
	))
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "luna-compact-wire", repo.modelRateLimitCalls[0].scope)
}

func TestOpenAIModelNotSupportedStreamAndWSSemantics(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"400","type":"model_not_supported","message":"Model 'gpt-5.6-luna' is temporarily not supported"}}}`)
	require.True(t, isOpenAIModelNotSupportedPayload(payload))
	require.Equal(t, http.StatusBadRequest, openAIStreamFailureStatus(payload, extractOpenAISSEErrorMessage(payload)))
	require.True(t, openAIStreamFailedEventShouldFailover(payload, extractOpenAISSEErrorMessage(payload)))
	require.True(t, openAIStreamErrorEventShouldFailover(payload, extractOpenAISSEErrorMessage(payload)))
	require.Equal(t, http.StatusBadRequest, openAIWSPayloadStatus(payload))
	require.Equal(t, http.StatusBadRequest, openAIWSErrorHTTPStatusFromRaw("400", "model_not_supported"))
}

func TestOpenAIWSPrewarmResponseFailedPreservesModelNotSupported(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.PrewarmGenerateEnabled = true
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 1
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 1
	repo := &modelNotFoundAccountRepoStub{}
	account := &Account{
		ID: 705, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"},
	}
	capture := &openAIWSCaptureConn{events: [][]byte{[]byte(
		`{"type":"response.failed","response":{"error":{"code":400,"type":"model_not_supported","message":"model is temporarily not supported"}}}`,
	)}}
	conn := newOpenAIWSConn("prewarm_model_not_supported", account.ID, capture, nil)
	lease := &openAIWSConnLease{accountID: account.ID, conn: conn}
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		rateLimitService: &RateLimitService{accountRepo: repo},
	}

	err := svc.performOpenAIWSGeneratePrewarm(
		context.Background(),
		lease,
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
		map[string]any{"type": "response.create", "model": "openai/gpt-5.6-luna"},
		"",
		map[string]any{"model": "openai/gpt-5.6-luna"},
		account,
		nil,
		0,
	)

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.IsOpenAIModelNotSupported())
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "openai/gpt-5.6-luna", repo.modelRateLimitCalls[0].scope)
}

func TestOpenAIModelNotSupportedFailoverSuppressesAccountHealthPenalty(t *testing.T) {
	err := newOpenAIModelNotSupportedFailoverError(nil, []byte(cindyModelNotSupportedBody))
	require.True(t, err.SuppressAccountHealthPenalty)
	require.False(t, err.ShouldReportAccountScheduleFailure())
}

func TestOpenAIModelNotSupportedIsCindyOnlyForHTTPFailover(t *testing.T) {
	svc := &OpenAIGatewayService{}
	ordinary := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com"},
	}
	legacyLaxa := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"},
	}

	require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(http.StatusBadRequest, "", []byte(cindyModelNotSupportedBody)))
	require.False(t, svc.shouldFailoverOpenAIUpstreamResponseForAccount(ordinary, http.StatusBadRequest, "", []byte(cindyModelNotSupportedBody)))
	require.True(t, svc.shouldFailoverOpenAIUpstreamResponseForAccount(legacyLaxa, http.StatusBadRequest, "", []byte(cindyModelNotSupportedBody)))
}

func TestOpenAIModelNotSupportedIsCindyOnlyForStreamFailover(t *testing.T) {
	payload := []byte(cindyModelNotSupportedBody)
	message := extractOpenAISSEErrorMessage(payload)
	ordinary := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}
	legacyLaxa := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	require.False(t, openAIStreamFailedEventShouldFailoverForAccount(ordinary, payload, message))
	require.False(t, openAIStreamErrorEventShouldFailoverForAccount(ordinary, payload, message))
	require.True(t, openAIStreamFailedEventShouldFailoverForAccount(legacyLaxa, payload, message))
	require.True(t, openAIStreamErrorEventShouldFailoverForAccount(legacyLaxa, payload, message))
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

func TestRateLimitService_ModelNotSupportedCooldownIgnoresCustomErrorCodeFilter(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{
		ID: 703, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"base_url":                   "https://api.laxarouter.ai",
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(500)},
		},
	}
	require.True(t, svc.HandleUpstreamModelNotFound(context.Background(), account, "gpt-5.6-luna", http.StatusBadRequest, []byte(cindyModelNotSupportedBody)))
	require.Len(t, repo.modelRateLimitCalls, 1)
}
