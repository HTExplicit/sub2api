package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAISatelliteCCSafetyRejectionPrecedesTemporaryErrorPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway} {
		repo := &openAIAuthPolicyAccountRepo{}
		svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: NewRateLimitService(repo, nil, nil, nil, nil)}
		account := &Account{ID: 731, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules":   []any{map[string]any{"error_code": float64(status), "keywords": []any{"policy refusal"}, "duration_minutes": float64(1)}},
		}}
		body := []byte(`{"error":{"code":"cyber_policy","message":"policy refusal"}}`)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
		failover := svc.failoverOpenAIUpstreamHTTPError(context.Background(), c, account, resp, body, "gpt-5.6-sol")
		require.Nil(t, failover)
		require.Zero(t, repo.tempCalls)
		require.Zero(t, repo.setErrorCalls)
		require.NotNil(t, GetOpsCyberPolicy(c))
		require.False(t, c.Writer.Written(), "the endpoint-specific error writer owns the terminal response")
	}
}

func TestOpenAISatelliteAlphaSafetyRejectionNeverUsesFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name                  string
		status                int
		contentType, response string
		cindy                 bool
		wantStatus            int
	}{
		{"missing endpoint wrapper", http.StatusNotFound, "application/json", `{"error":{"code":"cyber_policy","type":"invalid_request_error","message":"unsupported tool policy refusal"}}`, false, http.StatusNotFound},
		{"server error wrapper", http.StatusBadGateway, "application/json", `{"response":{"error":{"code":"cyber_policy","message":"policy refusal"}}}`, false, http.StatusBadGateway},
		{"Cindy tool capability wrapper", http.StatusBadRequest, "application/json", `{"error":{"code":"cyber_policy","type":"invalid_request_error","message":"unsupported tool policy refusal"}}`, true, http.StatusBadRequest},
		{"Cindy failed SSE", http.StatusOK, "text/event-stream", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"policy refusal\"}}}\n\n", true, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"id":"search","model":"gpt-5.6-luna","commands":{"search_query":[{"q":"test"}]}}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", bytes.NewReader(body))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: tt.status, Header: http.Header{"Content-Type": []string{tt.contentType}}, Body: io.NopCloser(strings.NewReader(tt.response))}}
			repo := &openAIAuthPolicyAccountRepo{}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, accountRepo: repo}
			account := &Account{ID: 732, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://compat.example"}}
			if tt.cindy {
				account = firstClassCindyAlphaSearchAccount(732)
			}
			result, err := svc.ForwardAlphaSearch(context.Background(), c, account, body)
			require.Nil(t, result, "a refused search is not billable")
			require.ErrorIs(t, err, errOpenAICyberPolicyForwarded)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.Len(t, upstream.requests, 1)
			require.Equal(t, tt.wantStatus, recorder.Code)
			require.Contains(t, recorder.Body.String(), "cyber_policy")
			require.NotNil(t, GetOpsCyberPolicy(c))
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.setErrorCalls)
		})
	}
}

func TestOpenAISatelliteImagesSafetyRejectionCannotSecondaryFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &openAIAuthPolicyAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: NewRateLimitService(repo, nil, nil, nil, nil)}
	account := &Account{ID: 733, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"temp_unschedulable_enabled": true,
		"temp_unschedulable_rules":   []any{map[string]any{"error_code": float64(http.StatusBadGateway), "keywords": []any{"policy refusal"}, "duration_minutes": float64(1)}},
	}}
	body := []byte(`{"error":{"code":"cyber_policy","message":"policy refusal"}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
	result, err := svc.handleOpenAIImagesErrorResponse(context.Background(), resp, c, account, "gpt-image-2")
	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	var terminal *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &terminal)
	require.Equal(t, "cyber_policy", terminal.Code)
	require.NotNil(t, GetOpsCyberPolicy(c))
	require.Zero(t, repo.tempCalls)
	require.Zero(t, repo.setErrorCalls)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
}

func TestOpenAISatelliteLiveSafetyRejectionRemainsTerminal(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 734, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	body := []byte(`{"error":{"code":"cyber_policy","message":"policy refusal"}}`)
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway} {
		require.False(t, svc.shouldFailoverLiveCreateError(account, &UpstreamFailoverError{StatusCode: status, ResponseBody: body}))
	}
}
