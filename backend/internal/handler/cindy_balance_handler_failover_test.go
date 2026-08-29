package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	cindyHandlerExhaustedAccountID = int64(57101)
	cindyHandlerHealthyAccountID   = int64(57102)
)

// cindyHandlerFailoverAccountRepo deliberately keeps the exhausted account in
// every fresh schedulable snapshot. Combined with the disabled runtime blocker
// in newCindyBalanceFailoverHandler, this makes the second selection exercise
// the handler's failedAccountIDs exclusion instead of relying on mutated repo
// state to pick the healthy account.
type cindyHandlerFailoverAccountRepo struct {
	service.AccountRepository

	mu            sync.Mutex
	accounts      []service.Account
	listSnapshots [][]int64
	markedIDs     []int64
}

type cindyHandlerConcurrencyCache struct {
	service.ConcurrencyCache
}

func (c *cindyHandlerConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

func (c *cindyHandlerConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}

func (c *cindyHandlerConcurrencyCache) GetAccountsLoadBatch(_ context.Context, accounts []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	result := make(map[int64]*service.AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		result[account.ID] = &service.AccountLoadInfo{AccountID: account.ID}
	}
	return result, nil
}

func (r *cindyHandlerFailoverAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]service.Account, 0, len(r.accounts))
	ids := make([]int64, 0, len(r.accounts))
	for i := range r.accounts {
		account := r.accounts[i]
		if account.Platform != platform || !account.IsSchedulable() {
			continue
		}
		result = append(result, account)
		ids = append(ids, account.ID)
	}
	r.listSnapshots = append(r.listSnapshots, ids)
	return result, nil
}

func (r *cindyHandlerFailoverAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *cindyHandlerFailoverAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *cindyHandlerFailoverAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, nil
}

func (r *cindyHandlerFailoverAccountRepo) MarkCindyBalanceInsufficient(_ context.Context, accountID int64, _ time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markedIDs = append(r.markedIDs, accountID)
	// Do not mutate r.accounts: the next repository snapshot must still contain
	// A so the handler-local failedAccountIDs map is what excludes it.
	return true, nil
}

func (r *cindyHandlerFailoverAccountRepo) ClearCindyBalanceInsufficient(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *cindyHandlerFailoverAccountRepo) PreviewCindyInsufficientDeletion(context.Context) (*service.CindyInsufficientDeletePreview, error) {
	return &service.CindyInsufficientDeletePreview{}, nil
}

func (r *cindyHandlerFailoverAccountRepo) DeleteCindyInsufficient(context.Context, int, string) (*service.CindyInsufficientDeleteResult, error) {
	return &service.CindyInsufficientDeleteResult{}, nil
}

func (r *cindyHandlerFailoverAccountRepo) snapshot() ([]int64, [][]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	marked := append([]int64(nil), r.markedIDs...)
	lists := make([][]int64, 0, len(r.listSnapshots))
	for _, ids := range r.listSnapshots {
		lists = append(lists, append([]int64(nil), ids...))
	}
	return marked, lists
}

type cindyHandlerFailoverUpstream struct {
	service.HTTPUpstream

	mu              sync.Mutex
	accountIDs      []int64
	requestModels   []string
	requestPaths    []string
	requestBodies   [][]byte
	exhaustedStatus int
	healthyOpaque   string
}

func (u *cindyHandlerFailoverUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	return u.respond(req, accountID)
}

func (u *cindyHandlerFailoverUpstream) DoWithTLS(req *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.respond(req, accountID)
}

func (u *cindyHandlerFailoverUpstream) respond(req *http.Request, accountID int64) (*http.Response, error) {
	payload, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.requestModels = append(u.requestModels, gjson.GetBytes(payload, "model").String())
	u.requestPaths = append(u.requestPaths, req.URL.Path)
	u.requestBodies = append(u.requestBodies, append([]byte(nil), payload...))
	u.mu.Unlock()

	isMessages := strings.HasSuffix(req.URL.Path, "/v1/messages")
	if accountID == cindyHandlerExhaustedAccountID {
		body := strings.Join([]string{
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_budget_a","status":"failed","error":{"type":"budget_exceeded","code":"429","message":"balance-secret-A-responses"}}}`,
			"",
		}, "\n")
		if isMessages {
			body = strings.Join([]string{
				"event: error",
				`data: {"type":"error","error":{"type":"budget_exceeded","code":"429","message":"balance-secret-A-messages"}}`,
				"",
			}, "\n")
		}
		resp := cindyHandlerSSEResponse(body)
		if u.exhaustedStatus != 0 {
			resp.StatusCode = u.exhaustedStatus
		}
		return resp, nil
	}

	if isMessages {
		return cindyHandlerSSEResponse(strings.Join([]string{
			"event: message_start",
			`data: {"type":"message_start","message":{"id":"msg_healthy_b","type":"message","role":"assistant","model":"google/gemini-3.6-flash","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
			"",
			"event: content_block_delta",
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"messages-recovered-B"}}`,
			"",
			"event: message_delta",
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			"",
			"event: message_stop",
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")), nil
	}

	healthyOutput := `[]`
	if u.healthyOpaque != "" {
		healthyOutput = `[{"type":"reasoning","id":"rs_bound","encrypted_content":` + strconv.Quote(u.healthyOpaque) + `,"phase":"analysis"}]`
	}
	return cindyHandlerSSEResponse(strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_healthy_b","status":"in_progress"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","response_id":"resp_healthy_b","delta":"responses-recovered-B"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_healthy_b","object":"response","model":"openai/gpt-5.6-luna","status":"completed","output":` + healthyOutput + `,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		"",
	}, "\n")), nil
}

func cindyHandlerSSEResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func (u *cindyHandlerFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (u *cindyHandlerFailoverUpstream) models() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.requestModels...)
}

func (u *cindyHandlerFailoverUpstream) paths() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.requestPaths...)
}

func (u *cindyHandlerFailoverUpstream) bodies() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	result := make([][]byte, len(u.requestBodies))
	for i := range u.requestBodies {
		result[i] = append([]byte(nil), u.requestBodies[i]...)
	}
	return result
}

func newCindyBalanceFailoverHandler(t *testing.T) (*OpenAIGatewayHandler, *cindyHandlerFailoverAccountRepo, *cindyHandlerFailoverUpstream, int64) {
	return newCindyBalanceFailoverHandlerWithCache(t, nil)
}

func newCindyBalanceFailoverHandlerWithCache(t *testing.T, cache service.GatewayCache) (*OpenAIGatewayHandler, *cindyHandlerFailoverAccountRepo, *cindyHandlerFailoverUpstream, int64) {
	t.Helper()
	groupID := int64(57100)
	accounts := []service.Account{
		{
			ID: cindyHandlerExhaustedAccountID, Name: "cindy-A", Platform: service.PlatformCindy,
			WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 1, Concurrency: 0,
			Credentials: map[string]any{"api_key": "sk-a", "base_url": "https://api.laxarouter.ai"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
		{
			ID: cindyHandlerHealthyAccountID, Name: "cindy-B", Platform: service.PlatformCindy,
			WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 2, Concurrency: 0,
			Credentials: map[string]any{"api_key": "sk-b", "base_url": "https://api.laxarouter.ai"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
	}
	repo := &cindyHandlerFailoverAccountRepo{accounts: accounts}
	upstream := &cindyHandlerFailoverUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1

	billingService := service.NewBillingService(cfg, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	concurrencyService := service.NewConcurrencyService(&cindyHandlerConcurrencyCache{})
	rateLimitService := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	openAIGateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, cache, cfg, nil, concurrencyService,
		billingService, rateLimitService, billingCache, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	nativeGateway := service.NewGatewayService(
		repo, nil, nil, nil, nil, nil, nil, nil, cfg, nil, concurrencyService,
		billingService, rateLimitService, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	// Isolate handler exclusion: persistence succeeds, but neither the repository
	// snapshot nor a process-local runtime block hides A from the second query.
	rateLimitService.SetAccountRuntimeBlocker(nil)

	h := NewOpenAIGatewayHandler(
		openAIGateway, concurrencyService, billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	h.SetNativeAnthropicGatewayService(nativeGateway)
	return h, repo, upstream, groupID
}

func newFirstClassCindyContinuationHandler(t *testing.T, cache service.GatewayCache) (*OpenAIGatewayHandler, *cindyHandlerFailoverAccountRepo, *cindyHandlerFailoverUpstream, int64) {
	h, repo, upstream, groupID := newCindyBalanceFailoverHandlerWithCache(t, cache)
	h.cfg.Gateway.OpenAIWS.Enabled = true
	h.cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	h.cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	h.cfg.Gateway.OpenAIWS.CindyHTTPToWSV2Enabled = true
	repo.mu.Lock()
	for i := range repo.accounts {
		repo.accounts[i].Platform = service.PlatformCindy
		repo.accounts[i].WirePlatform = service.WirePlatformOpenAI
		repo.accounts[i].ProviderProfile = service.ProviderProfileCindyLaxaV1
		repo.accounts[i].Concurrency = 1
		repo.accounts[i].GroupIDs = []int64{groupID}
		repo.accounts[i].Extra["openai_responses_supported"] = true
	}
	repo.mu.Unlock()
	return h, repo, upstream, groupID
}

func newFirstClassCindyContinuationContext(t *testing.T, groupID int64, body string) (*gin.Context, *httptest.ResponseRecorder) {
	c, recorder := newStrictCindyHandlerContext(t, groupID, "/v1/responses", body)
	c.Request.Header.Set("User-Agent", "compatible-client/1.0")
	raw, exists := c.Get(string(middleware.ContextKeyAPIKey))
	require.True(t, exists)
	apiKey, ok := raw.(*service.APIKey)
	require.True(t, ok)
	apiKey.Group.Platform = service.PlatformCindy
	apiKey.Group.WirePlatform = service.WirePlatformOpenAI
	apiKey.Group.ProviderProfile = service.ProviderProfileCindyLaxaV1
	return c, recorder
}

func newStrictCindyHandlerContext(t *testing.T, groupID int64, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	apiKey := &service.APIKey{
		ID: 57103, GroupID: &groupID, Status: service.StatusActive,
		User: &service.User{ID: 57104, Status: service.StatusActive},
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI,
			ProviderProfile: service.ProviderProfileCindyLaxaV1, Status: service.StatusActive,
			StrictCindyKnown: true, StrictCindy: true, RateMultiplier: 1,
		},
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 0})
	return c, recorder
}

func requireCindyHandlerFailover(t *testing.T, repo *cindyHandlerFailoverAccountRepo, upstream *cindyHandlerFailoverUpstream) {
	t.Helper()
	require.Equal(t, []int64{cindyHandlerExhaustedAccountID, cindyHandlerHealthyAccountID}, upstream.calls())
	require.Never(t, func() bool {
		return len(upstream.calls()) > 2
	}, 100*time.Millisecond, 5*time.Millisecond, "request-level failover must not start a background Luna/Terra probe")
	marked, listSnapshots := repo.snapshot()
	require.Empty(t, marked, "one request-level signal must not create a durable balance marker")
	require.GreaterOrEqual(t, len(listSnapshots), 2, "failover must perform a fresh account selection")
	for _, ids := range listSnapshots {
		require.Equal(t, []int64{cindyHandlerExhaustedAccountID, cindyHandlerHealthyAccountID}, ids,
			"repo deliberately keeps A eligible; handler failedAccountIDs must exclude it")
	}
}

func TestStrictCindyResponsesHandlerFailsOverHTTP200BudgetSSEBeforeFirstByte(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo, upstream, groupID := newCindyBalanceFailoverHandler(t)
	c, recorder := newStrictCindyHandlerContext(t, groupID, "/v1/responses",
		`{"model":"gpt-5.6-luna","input":"hi","stream":true}`)

	h.Responses(c)

	requireCindyHandlerFailover(t, repo, upstream)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "responses-recovered-B")
	require.Contains(t, recorder.Body.String(), `"type":"response.completed"`)
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "balance-secret-A-responses")
	require.NotContains(t, recorder.Body.String(), "resp_budget_a")
}

func TestFirstClassCindyResponsesForwardsUnanchoredCodexHistoryToStickyAccount(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "item reference", body: `{"model":"gpt-5.6-luna","prompt_cache_key":"legacy-reference-session","store":true,"input":[{"type":"item_reference","id":"fc_1"}],"stream":false}`},
		{name: "reasoning id", body: `{"model":"gpt-5.6-luna","prompt_cache_key":"legacy-reference-session","input":[{"type":"reasoning","id":"rs_1"}],"stream":false}`},
		{name: "referenced tool output", body: `{"model":"gpt-5.6-luna","prompt_cache_key":"legacy-reference-session","store":false,"input":[{"type":"item_reference","id":"call_1"},{"type":"function_call_output","call_id":"call_1","output":"ok"}],"stream":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			cache := &cindyLegacySessionCache{accountID: cindyHandlerHealthyAccountID}
			h, _, upstream, groupID := newFirstClassCindyContinuationHandler(t, cache)
			c, recorder := newFirstClassCindyContinuationContext(t, groupID, test.body)

			h.Responses(c)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, []int64{cindyHandlerHealthyAccountID}, upstream.calls())
			bodies := upstream.bodies()
			require.Len(t, bodies, 1)
			require.JSONEq(t, gjson.Get(test.body, "input").Raw, gjson.GetBytes(bodies[0], "input").Raw)
			require.Equal(t, gjson.Get(test.body, "store").Exists(), gjson.GetBytes(bodies[0], "store").Exists())
			require.Equal(t, gjson.Get(test.body, "store").Raw, gjson.GetBytes(bodies[0], "store").Raw)
		})
	}
}

func TestFirstClassCindyResponsesReferenceHistoryRequiresSessionBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, upstream, groupID := newFirstClassCindyContinuationHandler(t, nil)
	c, recorder := newFirstClassCindyContinuationContext(t, groupID,
		`{"model":"gpt-5.6-luna","prompt_cache_key":"missing-reference-session","input":[{"type":"reasoning","id":"rs_1"}],"stream":false}`)

	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, gjson.Get(recorder.Body.String(), "error.code").String())
	require.Empty(t, upstream.calls(), "reference history must not load-balance without its original session binding")
}

func TestFirstClassCindyResponsesReferenceHistoryDistinguishesSessionStoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &cindyOpaqueStoreErrorCache{err: errors.New("redis unavailable")}
	h, _, upstream, groupID := newFirstClassCindyContinuationHandler(t, cache)
	c, recorder := newFirstClassCindyContinuationContext(t, groupID,
		`{"model":"gpt-5.6-luna","prompt_cache_key":"unavailable-reference-session","input":[{"type":"reasoning","id":"rs_1"}],"stream":false}`)

	h.Responses(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, gjson.Get(recorder.Body.String(), "error.code").String())
	require.Empty(t, upstream.calls())
}

func TestFirstClassCindyResponsesExistingAnchorUsesBoundAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, upstream, groupID := newFirstClassCindyContinuationHandler(t, nil)
	first, firstRecorder := newFirstClassCindyContinuationContext(t, groupID,
		`{"model":"gpt-5.6-luna","input":"first","stream":true}`)
	h.Responses(first)
	require.Equal(t, http.StatusOK, firstRecorder.Code, firstRecorder.Body.String())
	require.Equal(t, []int64{cindyHandlerExhaustedAccountID, cindyHandlerHealthyAccountID}, upstream.calls())

	second, secondRecorder := newFirstClassCindyContinuationContext(t, groupID,
		`{"model":"gpt-5.6-luna","store":true,"previous_response_id":"resp_healthy_b","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}],"stream":true}`)
	h.Responses(second)

	require.Equal(t, http.StatusOK, secondRecorder.Code, secondRecorder.Body.String())
	require.Equal(t, []int64{cindyHandlerExhaustedAccountID, cindyHandlerHealthyAccountID, cindyHandlerHealthyAccountID}, upstream.calls())
	bodies := upstream.bodies()
	require.Equal(t, "resp_healthy_b", gjson.GetBytes(bodies[2], "previous_response_id").String())
	require.True(t, gjson.GetBytes(bodies[2], "store").Bool(), "anchored legacy conversations must preserve the client's store mode")
}

func TestFirstClassCindyResponsesFullReplayForcesStoreFalseAndPreservesIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, upstream, groupID := newFirstClassCindyContinuationHandler(t, nil)
	body := `{"model":"gpt-5.6-luna","store":true,"input":[{"type":"message","id":"foreign-message-id","role":"user","content":"hi","phase":"analysis"},{"type":"function_call","id":"foreign-tool-id","call_id":"foreign-call-id","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"foreign-call-id","output":"ok"}],"stream":true}`
	c, recorder := newFirstClassCindyContinuationContext(t, groupID, body)

	h.Responses(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	requestBodies := upstream.bodies()
	require.Len(t, requestBodies, 2)
	for _, requestBody := range requestBodies {
		require.False(t, gjson.GetBytes(requestBody, "store").Bool())
		require.Equal(t, "foreign-message-id", gjson.GetBytes(requestBody, "input.0.id").String())
		require.Equal(t, "analysis", gjson.GetBytes(requestBody, "input.0.phase").String())
		require.Equal(t, "foreign-tool-id", gjson.GetBytes(requestBody, "input.1.id").String())
		require.Equal(t, "foreign-call-id", gjson.GetBytes(requestBody, "input.1.call_id").String())
	}
}

func TestFirstClassCindyOpaqueFullUsesStableOriginalAccountBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, upstream, groupID := newFirstClassCindyContinuationHandler(t, nil)
	upstream.healthyOpaque = "cipher-stable"
	first, firstRecorder := newFirstClassCindyContinuationContext(t, groupID,
		`{"model":"gpt-5.6-luna","input":"establish opaque state","stream":true}`)
	h.Responses(first)
	require.Equal(t, http.StatusOK, firstRecorder.Code)
	require.Equal(t, []int64{cindyHandlerExhaustedAccountID, cindyHandlerHealthyAccountID}, upstream.calls())

	secondBody := `{"model":"gpt-5.6-luna","store":false,"input":[{"type":"reasoning","id":"rs_bound","encrypted_content":"cipher-stable","phase":"analysis"},{"type":"message","role":"user","content":"different current user content"}],"stream":false}`
	classification, err := service.ClassifyCindyContinuation([]byte(secondBody), service.CindyContinuationProof{})
	require.NoError(t, err)
	lookup := h.gatewayService.LookupCindyOpaqueContinuationBinding(context.Background(), groupID, classification.OpaqueBindingIDs)
	require.Equal(t, service.OpenAIContinuationBindingHit, lookup.State)
	require.Equal(t, cindyHandlerHealthyAccountID, lookup.AccountID)
	second, secondRecorder := newFirstClassCindyContinuationContext(t, groupID, secondBody)
	h.Responses(second)

	require.Equal(t, http.StatusOK, secondRecorder.Code, secondRecorder.Body.String())
	require.Equal(t, []int64{cindyHandlerExhaustedAccountID, cindyHandlerHealthyAccountID, cindyHandlerHealthyAccountID}, upstream.calls())
	requestBodies := upstream.bodies()
	require.Equal(t, "cipher-stable", gjson.GetBytes(requestBodies[2], "input.0.encrypted_content").String())
	require.Equal(t, "different current user content", gjson.GetBytes(requestBodies[2], "input.1.content").String())
}

func TestFirstClassCindyOpaqueFullBindingMissReplaysThroughOrdinaryFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo, upstream, groupID := newFirstClassCindyContinuationHandler(t, nil)
	body := `{"model":"gpt-5.6-luna","store":false,"input":[{"type":"reasoning","id":"rs_foreign","encrypted_content":"cipher-missing","phase":"analysis"},{"type":"function_call","id":"fc_foreign","call_id":"call_foreign","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_foreign","output":"ok"},{"type":"message","role":"user","content":"next"}],"stream":false}`
	classification, err := service.ClassifyCindyContinuation([]byte(body), service.CindyContinuationProof{})
	require.NoError(t, err)
	require.Equal(t, service.CindyContinuationOpaqueFull, classification.Mode)
	require.False(t, classification.HasExternalReference)
	c, recorder := newFirstClassCindyContinuationContext(t, groupID, body)

	h.Responses(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	requireCindyHandlerFailover(t, repo, upstream)
	requestBodies := upstream.bodies()
	require.Len(t, requestBodies, 2)
	for _, requestBody := range requestBodies {
		require.JSONEq(t, gjson.Get(body, "input").Raw, gjson.GetBytes(requestBody, "input").Raw)
		require.False(t, gjson.GetBytes(requestBody, "store").Bool())
		require.False(t, gjson.GetBytes(requestBody, "previous_response_id").Exists())
	}
}

func TestFirstClassCindyOpaqueFullBindingMissUsesOrdinarySessionAffinity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &cindyLegacySessionCache{accountID: cindyHandlerHealthyAccountID}
	h, repo, upstream, groupID := newFirstClassCindyContinuationHandler(t, cache)
	boundAccount, err := repo.GetByID(context.Background(), cindyHandlerHealthyAccountID)
	require.NoError(t, err)
	require.True(t, service.IsCindyAPIKeyAccount(boundAccount.Platform, boundAccount.Type, boundAccount.Credentials))
	require.True(t, boundAccount.IsModelSupported("gpt-5.6-luna"))
	require.True(t, boundAccount.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityChatCompletions))
	secondBody := `{"model":"gpt-5.6-luna","prompt_cache_key":"legacy-opaque-session","store":false,"input":[{"type":"reasoning","id":"rs_legacy","encrypted_content":"legacy-cipher","phase":"analysis"},{"type":"message","role":"user","content":"continue"}],"stream":true}`
	second, secondRecorder := newFirstClassCindyContinuationContext(t, groupID, secondBody)
	h.Responses(second)

	require.Equal(t, http.StatusOK, secondRecorder.Code, secondRecorder.Body.String())
	require.Equal(t, []int64{cindyHandlerHealthyAccountID}, upstream.calls())
	bodies := upstream.bodies()
	require.Equal(t, "legacy-cipher", gjson.GetBytes(bodies[0], "input.0.encrypted_content").String())
	classification, err := service.ClassifyCindyContinuation([]byte(secondBody), service.CindyContinuationProof{})
	require.NoError(t, err)
	lookup := h.gatewayService.LookupCindyOpaqueContinuationBinding(context.Background(), groupID, classification.OpaqueBindingIDs)
	require.Equal(t, service.OpenAIContinuationBindingMiss, lookup.State)
}

type cindyOpaqueStoreErrorCache struct {
	service.GatewayCache
	err error
}

func (c *cindyOpaqueStoreErrorCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, c.err
}

type cindyLegacySessionCache struct {
	service.GatewayCache
	accountID int64
}

func (c *cindyLegacySessionCache) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	if strings.HasPrefix(key, "openai:response:") {
		return 0, service.ErrStickySessionNotFound
	}
	return c.accountID, nil
}

func (c *cindyLegacySessionCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (c *cindyLegacySessionCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *cindyLegacySessionCache) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (c *cindyLegacySessionCache) DeleteSessionAccountIDIfMatches(context.Context, int64, string, int64) (bool, error) {
	return false, nil
}

func TestFirstClassCindyOpaqueFullStoreErrorFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &cindyOpaqueStoreErrorCache{err: errors.New("redis unavailable")}
	h, _, upstream, groupID := newFirstClassCindyContinuationHandler(t, cache)
	body := `{"model":"gpt-5.6-luna","store":false,"input":[{"type":"reasoning","id":"rs_foreign","encrypted_content":"cipher-error","phase":"analysis"},{"type":"message","role":"user","content":"next"}],"stream":false}`
	c, recorder := newFirstClassCindyContinuationContext(t, groupID, body)

	h.Responses(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
	require.Empty(t, upstream.calls())
}

func TestStrictCindyNativeMessagesHandlerFailsOverHTTP200BudgetSSEBeforeFirstEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo, upstream, groupID := newCindyBalanceFailoverHandler(t)
	c, recorder := newStrictCindyHandlerContext(t, groupID, "/v1/messages",
		`{"model":"gemini-3.6-flash","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":true}`)

	h.Messages(c)

	requireCindyHandlerFailover(t, repo, upstream)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "messages-recovered-B")
	require.Contains(t, recorder.Body.String(), "event: message_stop")
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "balance-secret-A-messages")
	require.NotContains(t, recorder.Body.String(), "event: error")
}

func TestStrictCindyHTTP201TerminalShapesDoNotMarkBalance(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "responses",
			path: "/v1/responses",
			body: `{"model":"gpt-5.6-luna","input":"hi","stream":true}`,
		},
		{
			name: "native_messages",
			path: "/v1/messages",
			body: `{"model":"gemini-3.6-flash","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			h, repo, upstream, groupID := newCindyBalanceFailoverHandler(t)
			upstream.exhaustedStatus = http.StatusCreated
			c, _ := newStrictCindyHandlerContext(t, groupID, tc.path, tc.body)

			if tc.path == "/v1/messages" {
				h.Messages(c)
			} else {
				h.Responses(c)
			}

			marked, _ := repo.snapshot()
			require.Empty(t, marked, "non-200 terminal shapes must never persist a balance marker")
			require.NotEmpty(t, upstream.calls(), "the request must reach the first account")
		})
	}
}
