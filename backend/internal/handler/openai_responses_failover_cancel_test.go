//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// openAIResponsesFailoverCancelUpstream 固定返回 HTTP 520，可在首次上游调用时
// 触发回调（用于模拟“上游在途期间客户端断开”）。
type openAIResponsesFailoverCancelUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
	onFirstDo  func()
}

func (u *openAIResponsesFailoverCancelUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	first := len(u.accountIDs) == 1
	u.mu.Unlock()
	if first && u.onFirstDo != nil {
		u.onFirstDo()
	}
	return &http.Response{
		StatusCode: 520,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(bytes.NewBufferString("<html>520: unknown error</html>")),
	}, nil
}

func (u *openAIResponsesFailoverCancelUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

// openAIResponsesContinuationStateUpstream simulates a compatibility upstream
// that wraps a request-history rejection in a generic 502. The handler must not
// reinterpret this as an account failure and select account 2.
type openAIResponsesContinuationStateUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *openAIResponsesContinuationStateUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewBufferString(
			`{"error":{"code":"previous_response_not_found","message":"previous response not found"}}`,
		)),
	}, nil
}

func (u *openAIResponsesContinuationStateUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

// openAIResponsesGenericBadRequestUpstream models a compatibility upstream
// that produces a non-semantic 400 before it writes any stream bytes.  This is
// deliberately not recognized as a continuation-state error: the handler must
// still uphold the client-requested Responses streaming contract.
type openAIResponsesGenericBadRequestUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

type openAIResponsesModelNotSupportedUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
	requests   [][]byte
}

type openAIResponsesCooledAccountRepo struct {
	openAIImagesFailoverAccountRepo
}

// The common failover repository stub embeds AccountRepository for methods
// unrelated to these tests. Implement the persistent model-availability query
// explicitly so continuation classification can inspect accounts even when a
// transient model cooldown is present.
func (r openAIImagesFailoverAccountRepo) ListModelAvailabilityCandidates(
	_ context.Context,
	_ *int64,
	platforms []string,
	_ bool,
) ([]service.Account, error) {
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	result := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if _, ok := allowed[account.Platform]; !ok || !account.IsActive() || !account.Schedulable {
			continue
		}
		result = append(result, account)
	}
	return result, nil
}

func (r openAIResponsesCooledAccountRepo) ListByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIResponsesCooledAccountRepo) ListByGroup(_ context.Context, _ int64) ([]service.Account, error) {
	return append([]service.Account(nil), r.accounts...), nil
}

// The shared failover repository stub embeds the production interface for
// unrelated methods. Provide the persistent-eligibility query explicitly so
// the legacy cooldown diagnosis can inspect accounts whose transient model
// limits would otherwise be filtered by ListSchedulable*.
func (r openAIResponsesCooledAccountRepo) ListModelAvailabilityCandidates(
	_ context.Context,
	_ *int64,
	platforms []string,
	_ bool,
) ([]service.Account, error) {
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	result := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if _, ok := allowed[account.Platform]; !ok || !account.IsActive() || !account.Schedulable {
			continue
		}
		result = append(result, account)
	}
	return result, nil
}

func (u *openAIResponsesModelNotSupportedUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	requestBody, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.requests = append(u.requests, requestBody)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewBufferString(
			`{"error":{"code":400,"type":"model_not_supported","message":"model is not supported"}}`,
		)),
	}, nil
}

func (u *openAIResponsesModelNotSupportedUpstream) bodies() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	result := make([][]byte, len(u.requests))
	for i := range u.requests {
		result[i] = append([]byte(nil), u.requests[i]...)
	}
	return result
}

func (u *openAIResponsesModelNotSupportedUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (u *openAIResponsesGenericBadRequestUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"must-not-leak"}}`)),
	}, nil
}

func (u *openAIResponsesGenericBadRequestUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func newOpenAIResponsesFailoverTestHandler(t *testing.T, upstream service.HTTPUpstream) *OpenAIGatewayHandler {
	t.Helper()
	accounts := []service.Account{
		{
			ID:          1,
			Name:        "responses-account-1",
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
			Name:        "responses-account-2",
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
	return handler
}

func newOpenAIResponsesFailoverTestContext(t *testing.T, ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	return newOpenAIResponsesFailoverTestContextWithStream(t, ctx, false)
}

func newOpenAIResponsesFailoverTestContextWithStream(t *testing.T, ctx context.Context, stream bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	body := []byte(`{"model":"gpt-5.1","stream":false,"input":"hello"}`)
	if stream {
		body = []byte(`{"model":"gpt-5.1","stream":true,"input":"hello"}`)
	}
	return newOpenAIResponsesFailoverTestContextWithBody(t, ctx, body)
}

func newOpenAIResponsesFailoverTestContextWithBody(t *testing.T, ctx context.Context, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	groupID := int64(3131)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformOpenAI,
		},
		User: &service.User{ID: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 0})
	return c, rec
}

// TestOpenAIGatewayHandlerResponses_FailoverAbortsWhenClientDisconnected 复现
// #4257：客户端在上游请求在途期间断开，上游随后返回可 failover 的 520。
// 期望：不再用已取消的 context 重新选号（不触达账号 2）、不把取消误报成
// 502 账号耗尽、请求按 499 归类。
func TestOpenAIGatewayHandlerResponses_FailoverAbortsWhenClientDisconnected(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &openAIResponsesFailoverCancelUpstream{onFirstDo: cancel}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	c, rec := newOpenAIResponsesFailoverTestContext(t, ctx)

	handler.Responses(c)

	require.Equal(t, []int64{1}, upstream.calls(), "客户端断开后不应再切换到账号 2")
	require.Equal(t, statusClientClosedRequest, c.Writer.Status(), "应按 499 归类")
	require.Zero(t, rec.Body.Len(), "不应写入 502 错误响应体")

	_, hasFinalUpstreamErr := c.Get(service.OpsUpstreamStatusCodeKey)
	require.False(t, hasFinalUpstreamErr, "不应记录 failover 耗尽的上游错误终态")

	// 真实发生过的 520 应保留 failover 事件（service 层在返回 failover 错误前记录）
	rawEvents, ok := c.Get(service.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*service.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, 520, events[0].UpstreamStatusCode)
}

// TestOpenAIGatewayHandlerResponses_FailoverContinuesForConnectedClient 回归
// 守卫：客户端在线时，每个 OAuth 账号对 520 只做一次同账号传输重试，
// 再切换到账号 2；两个账号均耗尽后返回 502。
func TestOpenAIGatewayHandlerResponses_FailoverContinuesForConnectedClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &openAIResponsesFailoverCancelUpstream{}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	c, rec := newOpenAIResponsesFailoverTestContext(t, nil)

	handler.Responses(c)

	require.Equal(t, []int64{1, 1, 2, 2}, upstream.calls(), "在线客户端应按上限重试并正常切换账号")
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}

func TestOpenAIGatewayHandlerResponses_ContinuationStateStopsBeforeSecondAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &openAIResponsesContinuationStateUpstream{}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	c, rec := newOpenAIResponsesFailoverTestContext(t, nil)

	handler.Responses(c)

	require.Equal(t, []int64{1}, upstream.calls(), "a request-scoped continuation failure must not select account 2")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Equal(t, service.OpenAIContinuationStateUnavailableClientMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())

	rawEvents, ok := c.Get(service.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*service.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "continuation_state", events[0].Kind)
}

// Regression: a stream:true Responses request can encounter stale continuation
// state before Forward has written any upstream bytes.  It must still receive a
// syntactically valid response.failed terminal event, not an HTTP 400 JSON body
// that strict Codex clients report as a transport/system error.
func TestOpenAIGatewayHandlerResponses_StreamingContinuationStateEmitsTerminalSSEBeforeFirstByte(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &openAIResponsesContinuationStateUpstream{}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	c, rec := newOpenAIResponsesFailoverTestContextWithStream(t, nil, true)

	handler.Responses(c)

	require.Equal(t, []int64{1}, upstream.calls(), "a request-scoped continuation failure must not select account 2")
	require.Equal(t, http.StatusOK, rec.Code, "streaming terminal must be delivered in-band as SSE")
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed\n"), "emit exactly one terminal event")
	require.NotContains(t, rec.Body.String(), "event: response.completed\n")

	_, errObj := parseResponsesFailedSSE(t, rec.Body.String())
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, errObj["code"])
	require.Equal(t, service.OpenAIContinuationStateUnavailableClientMessage, errObj["message"])
	require.NotContains(t, rec.Body.String(), "previous response not found")
}

// A generic upstream 400 must use the same terminal framing.  It does not get
// reclassified as continuation state, but a stream:true client still cannot
// consume a preflight JSON error as a completed Responses stream.
func TestOpenAIGatewayHandlerResponses_StreamingGenericPreflightErrorEmitsTerminalSSEBeforeFirstByte(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &openAIResponsesGenericBadRequestUpstream{}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	c, rec := newOpenAIResponsesFailoverTestContextWithStream(t, nil, true)

	handler.Responses(c)

	require.Equal(t, []int64{1}, upstream.calls())
	require.Equal(t, http.StatusOK, rec.Code, "streaming terminal must be delivered in-band as SSE")
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed\n"), "emit exactly one terminal event")
	require.NotContains(t, rec.Body.String(), "event: response.completed\n")
	require.NotContains(t, rec.Body.String(), "must-not-leak")

	resp, errObj := parseResponsesFailedSSE(t, rec.Body.String())
	require.Equal(t, "failed", resp["status"])
	require.NotEmpty(t, errObj["code"])
}

// A compatibility proxy may erase a continuation-state code completely.  The
// encrypted reasoning + function_call_output combination is not safely
// replayable, so the generic 400 must become the same request-scoped terminal
// rather than affecting the selected account or emitting a JSON body.
func TestOpenAIGatewayHandlerResponses_StreamingOpaqueContinuationToolChainStopsWithoutFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &openAIResponsesGenericBadRequestUpstream{}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	body := []byte(`{"model":"gpt-5.1","stream":true,"input":[{"type":"reasoning","encrypted_content":"opaque-test-carrier"},{"type":"function_call","call_id":"call_opaque","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_opaque","output":"{}"}]}`)
	c, rec := newOpenAIResponsesFailoverTestContextWithBody(t, nil, body)

	handler.Responses(c)

	require.Equal(t, []int64{1}, upstream.calls(), "opaque encrypted tool continuations must not select account 2")
	require.Equal(t, http.StatusOK, rec.Code, "streaming terminal must be delivered in-band as SSE")
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed\n"), "emit exactly one terminal event")
	require.NotContains(t, rec.Body.String(), "must-not-leak")

	_, errObj := parseResponsesFailedSSE(t, rec.Body.String())
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, errObj["code"])
	require.Equal(t, service.OpenAIContinuationStateUnavailableClientMessage, errObj["message"])

	rawEvents, ok := c.Get(service.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*service.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "continuation_state", events[0].Kind)
}

func TestLegacyLaxaContinuationModelNotSupportedDoesNotReplayAcrossAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &openAIResponsesModelNotSupportedUpstream{}
	accounts := []service.Account{
		{
			ID: 1, Name: "legacy-laxa-a", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 0,
			Credentials: map[string]any{"api_key": "test-a", "base_url": "https://api.laxarouter.ai"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
		{
			ID: 2, Name: "legacy-laxa-b", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 0, Priority: 1,
			Credentials: map[string]any{"api_key": "test-b", "base_url": "https://api.laxarouter.ai"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	repo := openAIResponsesCooledAccountRepo{
		openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts},
	}
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil,
		cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	handler.maxAccountSwitches = 10

	body := []byte(`{"model":"gpt-5.6-luna","stream":false,"store":true,"input":[{"type":"reasoning","encrypted_content":"opaque-state"}]}`)
	c, rec := newOpenAIResponsesFailoverTestContextWithBody(t, nil, body)
	handler.Responses(c)

	require.Equal(t, []int64{1}, upstream.calls(), "opaque legacy Laxa continuation must not replay to account 2")
	require.False(t, gjson.GetBytes(upstream.bodies()[0], "store").Bool(), "legacy Laxa opaque replay must be normalized to store=false")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, service.OpenAIModelNotSupportedCode, gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, service.OpenAIModelNotSupportedCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
}

func TestLegacyLaxaPreCooledPoolReturnsModelNotSupportedWithoutUpstreamReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetAt := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	accounts := make([]service.Account, 0, 2)
	for id := int64(1); id <= 2; id++ {
		accounts = append(accounts, service.Account{
			ID: id, Name: "legacy-laxa", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 0,
			Credentials: map[string]any{"api_key": "test", "base_url": "https://api.laxarouter.ai"},
			Extra: map[string]any{
				"openai_passthrough": true,
				"model_rate_limits": map[string]any{
					"openai/gpt-5.6-luna": map[string]any{
						"rate_limit_reset_at": resetAt,
						"reason":              "upstream_400_model_not_supported",
					},
				},
			},
		})
	}
	upstream := &openAIResponsesModelNotSupportedUpstream{}
	repo := openAIResponsesCooledAccountRepo{
		openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	body := []byte(`{"model":"gpt-5.6-luna","stream":false,"input":"hello"}`)
	c, rec := newOpenAIResponsesFailoverTestContextWithBody(t, nil, body)

	handler.Responses(c)

	require.Empty(t, upstream.calls())
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, service.OpenAIModelNotSupportedCode, gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, service.OpenAIModelNotSupportedCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
}

func TestMixedOpenAIAndLaxaReferenceOnlyRequestKeepsOrdinaryScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := []service.Account{
		{
			ID: 1, Name: "ordinary-openai", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 0,
			Credentials: map[string]any{"api_key": "ordinary", "base_url": "https://api.openai.com"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
		{
			ID: 2, Name: "legacy-laxa", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 0, Priority: 1,
			Credentials: map[string]any{"api_key": "laxa", "base_url": "https://api.laxarouter.ai"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
	}
	repo := openAIResponsesCooledAccountRepo{
		openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts},
	}
	upstream := &openAIHTTPPassthroughFailoverUpstream{succeedOnCall: 1}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	body := []byte(`{"model":"gpt-5.6-luna","stream":false,"input":[{"type":"reasoning","id":"rs_external"}]}`)
	c, rec := newOpenAIResponsesFailoverTestContextWithBody(t, nil, body)

	handler.Responses(c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []int64{1}, upstream.calls(), "a mixed group must not force an ordinary reference onto the Laxa session selector")
}
