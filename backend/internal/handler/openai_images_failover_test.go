//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type openAIImagesFailoverAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r openAIImagesFailoverAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

func (r openAIImagesFailoverAccountRepo) ListByGroup(_ context.Context, _ int64) ([]service.Account, error) {
	return append([]service.Account(nil), r.accounts...), nil
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) accountsForPlatform(platform string) []service.Account {
	out := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform {
			out = append(out, account)
		}
	}
	return out
}

type openAIImagesFailoverHTTPUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

type cindyNativeImagesBudgetRepo struct {
	openAIImagesFailoverAccountRepo
	service.CindyBalanceAccountRepository
	mu        sync.Mutex
	markedIDs []int64
}

func (r *cindyNativeImagesBudgetRepo) MarkCindyBalanceInsufficient(_ context.Context, accountID int64, _ time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markedIDs = append(r.markedIDs, accountID)
	return true, nil
}

func (r *cindyNativeImagesBudgetRepo) marked() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.markedIDs...)
}

type cindyNativeImagesBudgetUpstream struct {
	service.HTTPUpstream
	mu               sync.Mutex
	imageAccountIDs  []int64
	probeAccountIDs  []int64
	exhaustedAccount int64
}

type cindyNativeImagesHTTP201Upstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *cindyNativeImagesBudgetUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	if req.URL.Path == "/v1/responses" {
		u.probeAccountIDs = append(u.probeAccountIDs, accountID)
	} else {
		u.imageAccountIDs = append(u.imageAccountIDs, accountID)
	}
	u.mu.Unlock()
	if req.URL.Path == "/v1/responses" {
		body := io.NopCloser(bytes.NewBufferString(
			`{"id":"resp_probe","object":"response","status":"completed","output":[{"type":"message"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
		))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       body,
		}, nil
	}
	if accountID == u.exhaustedAccount {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(bytes.NewBufferString(
				`{"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream budget detail"}}`,
			)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewBufferString(
			`{"created":1,"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}, nil
}

func (u *cindyNativeImagesBudgetUpstream) imageCalls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.imageAccountIDs...)
}

func (u *cindyNativeImagesBudgetUpstream) probeCalls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.probeAccountIDs...)
}

func (u *cindyNativeImagesHTTP201Upstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewBufferString(
			`{"type":"error","error":{"type":"budget_exceeded","code":"429","message":"ordinary 201 body"}}`,
		)),
	}, nil
}

func (u *cindyNativeImagesHTTP201Upstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (u *openAIImagesFailoverHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"req_img_failover"},
		},
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"image backend unavailable\"}}\n\n",
		)),
	}, nil
}

func (u *openAIImagesFailoverHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestOpenAIGatewayHandlerImages_ServerErrorFailsOverAndReturnsClearErrorWhenExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3130)
	accounts := []service.Account{
		{
			ID:          1,
			Name:        "image-account-1",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 0,
			Priority:    0,
			Credentials: map[string]any{"access_token": "token-1"},
		},
		{
			ID:          2,
			Name:        "image-account-2",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 0,
			Priority:    1,
			Credentials: map[string]any{"access_token": "token-2"},
		},
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: accounts}
	upstream := &openAIImagesFailoverHTTPUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		upstream,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	concurrencyService := service.NewConcurrencyService(nil)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		concurrencyService,
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)
	handler.maxAccountSwitches = 10

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","quality":"high","size":"1536x1024"}`)
	core, observedLogs := observer.New(zap.DebugLevel)
	requestCtx := logger.IntoContext(context.Background(), zap.New(core))
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			AllowImageGeneration: true,
		},
		User: &service.User{ID: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 0})

	handler.Images(c)

	accountSelectingLogs := observedLogs.FilterMessage("openai.images.account_selecting").All()
	require.NotEmpty(t, accountSelectingLogs)
	loggedFields := make(map[string]string)
	for _, field := range accountSelectingLogs[0].Context {
		loggedFields[field.Key] = field.String
	}
	require.Equal(t, "high", loggedFields["img_quality"])
	require.Equal(t, "1536x1024", loggedFields["img_size"])
	require.NotContains(t, loggedFields, "prompt")

	require.Equal(t, []int64{1, 1, 2, 2}, upstream.calls())
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream service temporarily unavailable", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())

	rawEvents, ok := c.Get(service.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*service.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 4)
	require.Equal(t, "failover", events[0].Kind)
	for _, event := range events[1:] {
		require.Equal(t, "failover", event.Kind)
	}
}

func TestCindyNativeImagesHTTP200BudgetJSONFailsOverBeforeWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3131)
	exhaustedAccountID := int64(11)
	accounts := []service.Account{
		{
			ID: exhaustedAccountID, Name: "cindy-image-exhausted", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 0, Credentials: map[string]any{"api_key": "sk-cindy-a", "base_url": "https://api.laxarouter.ai"},
		},
		{
			ID: 12, Name: "cindy-image-healthy", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 1, Credentials: map[string]any{"api_key": "sk-cindy-b", "base_url": "https://api.laxarouter.ai"},
		},
	}
	repo := &cindyNativeImagesBudgetRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}}
	upstream := &cindyNativeImagesBudgetUpstream{exhaustedAccount: exhaustedAccountID}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	rateLimitService := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	gatewayService := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, rateLimitService, nil,
		upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService, service.NewConcurrencyService(nil), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	handler.maxAccountSwitches = 2

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","n":1,"size":"1024x1024","quality":"low","response_format":"b64_json"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: 100, GroupID: &groupID, Status: service.StatusActive,
		User: &service.User{ID: 101, Status: service.StatusActive},
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive,
			StrictCindyKnown: true, StrictCindy: true, AllowImageGeneration: true, RateMultiplier: 1,
		},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 101})

	handler.Images(c)

	require.Equal(t, []int64{exhaustedAccountID, 12}, upstream.imageCalls())
	require.Empty(t, upstream.probeCalls(), "request-time exact signals must not start balance probes")
	require.Empty(t, repo.marked(), "one exact request signal must not permanently mark the account")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "aW1hZ2U=", gjson.GetBytes(recorder.Body.Bytes(), "data.0.b64_json").String())
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "sensitive upstream budget detail")
}

func TestCindyNativeImagesHTTP201BudgetEventShapeDoesNotMarkOrFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3132)
	firstAccountID := int64(21)
	accounts := []service.Account{
		{
			ID: firstAccountID, Name: "cindy-image-first", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 0, Credentials: map[string]any{"api_key": "sk-cindy-a", "base_url": "https://api.laxarouter.ai"},
		},
		{
			ID: 22, Name: "cindy-image-second", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 1, Credentials: map[string]any{"api_key": "sk-cindy-b", "base_url": "https://api.laxarouter.ai"},
		},
	}
	repo := &cindyNativeImagesBudgetRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}}
	upstream := &cindyNativeImagesHTTP201Upstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	rateLimitService := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	gatewayService := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, rateLimitService, nil,
		upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService, service.NewConcurrencyService(nil), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	handler.maxAccountSwitches = 2

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","n":1,"size":"1024x1024","quality":"low","response_format":"b64_json"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: 102, GroupID: &groupID, Status: service.StatusActive,
		User: &service.User{ID: 103, Status: service.StatusActive},
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive,
			StrictCindyKnown: true, StrictCindy: true, AllowImageGeneration: true, RateMultiplier: 1,
		},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 103})

	handler.Images(c)

	require.Equal(t, []int64{firstAccountID}, upstream.calls())
	require.Empty(t, repo.marked())
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.Equal(t, "error", gjson.GetBytes(recorder.Body.Bytes(), "type").String())
	require.Equal(t, "ordinary 201 body", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
}
