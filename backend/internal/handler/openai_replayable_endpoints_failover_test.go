//go:build unit

package handler

import (
	"bytes"
	"context"
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
	firstBody   string
	successBody string
	successCode int
	successType string
	paths       []string
}

func (u *replayableEndpointFailoverUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	if req != nil && req.URL != nil {
		u.paths = append(u.paths, req.URL.Path)
	}
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
		body := u.firstBody
		if body == "" {
			body = `{"error":{"message":"temporary"}}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	code := u.successCode
	if code == 0 {
		code = http.StatusOK
	}
	contentType := u.successType
	if contentType == "" {
		contentType = "application/json"
	}
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(u.successBody)),
	}, nil
}

func (u *replayableEndpointFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (u *replayableEndpointFailoverUpstream) requestPaths() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.paths...)
}

type replayableEndpointSettingRepo struct {
	values map[string]string
}

func (r replayableEndpointSettingRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	return nil, nil
}

func (r replayableEndpointSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}

func (r replayableEndpointSettingRepo) Set(_ context.Context, key, value string) error {
	return nil
}

func (r replayableEndpointSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r replayableEndpointSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	return nil
}

func (r replayableEndpointSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	return r.GetMultiple(context.Background(), []string{service.SettingKeyOpenAIAPIKeyAlphaSearchResponsesBridgeEnabled})
}

func (r replayableEndpointSettingRepo) Delete(_ context.Context, key string) error {
	return nil
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
			Extra: map[string]any{
				"openai_alpha_search_mode": service.OpenAIAlphaSearchModeResponsesWebSearch,
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
			Extra: map[string]any{
				"openai_alpha_search_mode": service.OpenAIAlphaSearchModeResponsesWebSearch,
			},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	settingService := service.NewSettingService(replayableEndpointSettingRepo{values: map[string]string{
		service.SettingKeyOpenAIAPIKeyAlphaSearchResponsesBridgeEnabled: "true",
	}}, cfg)
	gatewayService := service.NewOpenAIGatewayService(
		openAIImagesFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, settingService, nil,
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

func TestAlphaSearchLegacyBridgeSettingsDoNotSwitchOrdinaryAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{
		firstStatus: http.StatusBadRequest,
		firstBody:   `{"error":{"type":"invalid_request_error","message":"This upstream tool is unavailable"}}`,
	}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, recorder := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/alpha/search", []byte(
		`{"model":"gpt-5.1","commands":{"search_query":[{"q":"latest news"}]}}`,
	))

	h.AlphaSearch(c)

	require.Equal(t, []int64{1}, upstream.calls())
	require.Equal(t, []string{"/v1/alpha/search"}, upstream.requestPaths())
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.JSONEq(t, `{"error":{"type":"invalid_request_error","message":"This upstream tool is unavailable"}}`, recorder.Body.String())
	require.Equal(t, EndpointAlphaSearch, GetUpstreamEndpoint(c, service.PlatformOpenAI))
}

func TestEmbeddingsRetriesPoolFailureOnExactSameAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{successCode: http.StatusBadRequest, successBody: `{"error":{"message":"terminal"}}`}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, _ := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/embeddings", []byte(`{"model":"text-embedding-3-small","input":"hello"}`))

	h.Embeddings(c)

	require.Equal(t, []int64{1, 1}, upstream.calls())
}

func TestImagesTransportFailureSwitchesAccountWithoutImplicitReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{
		firstError:  errors.New("temporary upstream reset"),
		successBody: `{"data":[{"url":"https://images.example.test/result.png"}]}`,
	}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, recorder := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/images/generations", []byte(`{"model":"gpt-image-1","prompt":"hello"}`))

	h.Images(c)

	require.Equal(t, []int64{1, 2}, upstream.calls())
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestImagesRepeatedTransportFailuresDoNotReplaySameAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &replayableEndpointFailoverUpstream{
		firstError:  errors.New("temporary upstream reset"),
		errorCalls:  2,
		successBody: `{"data":[{"url":"https://images.example.test/result.png"}]}`,
	}
	h, groupID := newReplayableEndpointFailoverHandler(t, upstream)
	c, recorder := newReplayableEndpointContext(t, groupID, http.MethodPost, "/v1/images/generations", []byte(`{"model":"gpt-image-1","prompt":"hello"}`))

	h.Images(c)

	require.Equal(t, []int64{1, 2}, upstream.calls())
	require.Equal(t, http.StatusBadGateway, recorder.Code)
}

func TestImagesAuthenticationAndRateLimitFailuresSwitchAccounts(t *testing.T) {
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
