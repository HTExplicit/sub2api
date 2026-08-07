//go:build unit

package handler

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type replayableEndpointFailoverUpstream struct {
	service.HTTPUpstream
	mu          sync.Mutex
	accountIDs  []int64
	firstError  error
	errorCalls  int
	firstStatus int
	successBody string
	successCode int
}

func (u *replayableEndpointFailoverUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	call := len(u.accountIDs)
	u.mu.Unlock()
	if u.firstError != nil {
		errorCalls := u.errorCalls
		if errorCalls <= 0 {
			errorCalls = 1
		}
		if call <= errorCalls {
			return nil, u.firstError
		}
	}
	if call == 1 {
		status := u.firstStatus
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary"}}`)),
		}, nil
	}
	code := u.successCode
	if code == 0 {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(u.successBody)),
	}, nil
}

func (u *replayableEndpointFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func newReplayableEndpointFailoverHandler(t *testing.T, upstream service.HTTPUpstream) (*OpenAIGatewayHandler, int64) {
	t.Helper()
	groupID := int64(44001)
	accounts := []service.Account{
		{
			ID:          1,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 0,
			Priority:    0,
			GroupIDs:    []int64{groupID},
			Credentials: map[string]any{
				"api_key":                      "sk-1",
				"base_url":                     "https://upstream-1.example/v1",
				"pool_mode":                    true,
				"pool_mode_retry_count":        10,
				"pool_mode_retry_status_codes": []any{float64(http.StatusServiceUnavailable)},
			},
		},
		{
			ID:          2,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 0,
			Priority:    1,
			GroupIDs:    []int64{groupID},
			Credentials: map[string]any{"api_key": "sk-2", "base_url": "https://upstream-2.example/v1"},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		openAIImagesFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	h := NewOpenAIGatewayHandler(
		gatewayService, service.NewConcurrencyService(nil), billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	h.maxAccountSwitches = 3
	return h, groupID
}

func newReplayableEndpointContext(t *testing.T, groupID int64, method, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      91,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                    groupID,
			Platform:              service.PlatformOpenAI,
			AllowImageGeneration:  true,
			AllowMessagesDispatch: true,
		},
		User: &service.User{ID: 92},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 92, Concurrency: 0})
	return c, recorder
}

func TestAlphaSearchRetriesPoolFailureOnExactSameAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{successCode: http.StatusBadRequest, successBody: `{"error":{"message":"terminal"}}`}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, _ := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/alpha/search", []byte(`{"model":"gpt-5.1","id":"search-1"}`))

	h.AlphaSearch(c)

	require.Equal(t, []int64{1, 1}, upstream.calls())
}

func TestEmbeddingsRetriesPoolFailureOnExactSameAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{successCode: http.StatusBadRequest, successBody: `{"error":{"message":"terminal"}}`}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, _ := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/embeddings", []byte(`{"model":"text-embedding-3-small","input":"hello"}`))

	h.Embeddings(c)

	require.Equal(t, []int64{1, 1}, upstream.calls())
}

func TestImagesRetriesTransientTransportOnExactSameAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{
		firstError:  errors.New("temporary upstream reset"),
		successBody: `{"data":[{"url":"https://images.example.test/result.png"}]}`,
	}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, recorder := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/images/generations", []byte(`{"model":"gpt-image-1","prompt":"hello"}`))

	h.Images(c)

	require.Equal(t, []int64{1, 1}, upstream.calls())
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestImagesRetriesTransientTransportAtMostOnceBeforeSwitching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{
		firstError:  errors.New("temporary upstream reset"),
		errorCalls:  2,
		successBody: `{"data":[{"url":"https://images.example.test/result.png"}]}`,
	}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, recorder := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/images/generations", []byte(`{"model":"gpt-image-1","prompt":"hello"}`))

	h.Images(c)

	require.Equal(t, []int64{1, 1, 2}, upstream.calls())
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestImagesAuthenticationAndRateLimitFailuresSwitchImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := &replayableEndpointFailoverUpstream{
				firstStatus: status,
				successBody: `{"data":[{"url":"https://images.example.test/result.png"}]}`,
			}
			h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
			c, recorder := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/images/generations", []byte(`{"model":"gpt-image-1","prompt":"hello"}`))

			h.Images(c)

			require.Equal(t, []int64{1, 2}, upstream.calls())
			require.Equal(t, http.StatusOK, recorder.Code)
		})
	}
}

func TestCountTokensRetriesPoolFailureOnExactSameAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{successBody: `{"input_tokens":5}`}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, recorder := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/messages/count_tokens", []byte(`{"model":"gpt-5.1","messages":[{"role":"user","content":"hello"}]}`))

	h.CountTokens(c)

	require.Equal(t, []int64{1, 1}, upstream.calls())
	require.Equal(t, http.StatusOK, recorder.Code)
}
