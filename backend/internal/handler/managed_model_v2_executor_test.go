//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const managedExecutorPublicModel = "claude-fable-5.1"

// This transport never opens a socket. Recording the actual outbound request
// exercises the production converters rather than a fake Forward method.
type managedExecutorCall struct {
	accountID int64
	path      string
	model     string
	body      []byte
}

type managedExecutorUpstream struct {
	service.HTTPUpstream
	mu      sync.Mutex
	calls   []managedExecutorCall
	respond func(managedExecutorCall, int) *http.Response
}

func (u *managedExecutorUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	call := managedExecutorCall{accountID: accountID, path: req.URL.Path, model: gjson.GetBytes(body, "model").String(), body: body}
	u.mu.Lock()
	u.calls = append(u.calls, call)
	n := len(u.calls)
	u.mu.Unlock()
	return u.respond(call, n), nil
}

func (u *managedExecutorUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func (u *managedExecutorUpstream) snapshot() []managedExecutorCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]managedExecutorCall(nil), u.calls...)
}

func managedExecutorResponse(status int, contentType, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {"managed-offline-request"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func managedExecutorUnavailable() *http.Response {
	return managedExecutorResponse(http.StatusServiceUnavailable, "application/json", `{"error":{"type":"overloaded_error","message":"offline route unavailable"}}`)
}

func managedExecutorSuccess(call managedExecutorCall, _ int) *http.Response {
	if strings.HasSuffix(call.path, "/chat/completions") {
		return managedExecutorResponse(http.StatusOK, "application/json", `{"id":"chat_offline","object":"chat.completion","model":"claude-fable-5.1","choices":[{"index":0,"message":{"role":"assistant","content":"managed recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}
	return managedExecutorResponse(http.StatusOK, "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_managed_offline\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"claude-fable-5.1\",\"output\":[{\"id\":\"msg_offline\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"managed recovered\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n")
}

type managedExecutorConcurrency struct {
	fakeConcurrencyCache
	userAcquire atomic.Int32
	userRelease atomic.Int32
}

func (c *managedExecutorConcurrency) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	c.userAcquire.Add(1)
	return true, nil
}

func (c *managedExecutorConcurrency) ReleaseUserSlot(context.Context, int64, string) error {
	c.userRelease.Add(1)
	return nil
}

type managedExecutorBillingCache struct {
	service.BillingCache
	balanceReads atomic.Int32
	quotaMu      sync.Mutex
	quotaReads   []string
}

func (c *managedExecutorBillingCache) GetUserBalance(context.Context, int64) (float64, error) {
	c.balanceReads.Add(1)
	return 100, nil
}

func (c *managedExecutorBillingCache) DeductUserBalance(context.Context, int64, float64) error {
	return nil
}

func (c *managedExecutorBillingCache) GetUserPlatformQuotaCache(_ context.Context, _ int64, platform string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	c.quotaMu.Lock()
	c.quotaReads = append(c.quotaReads, platform)
	c.quotaMu.Unlock()
	now := time.Now()
	return &service.UserPlatformQuotaCacheEntry{SchemaVersion: service.UserPlatformQuotaCacheSchemaV1,
		DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now}, true, nil
}

// The cache always returns a current unlimited entry; the non-nil repository
// enables the real eligibility/post-billing quota paths without external I/O.
type managedExecutorQuotaRepo struct {
	service.UserPlatformQuotaRepository
}

type managedExecutorUsageRepo struct {
	service.UsageLogRepository
	logs []*service.UsageLog
}

func (r *managedExecutorUsageRepo) Create(_ context.Context, entry *service.UsageLog) (bool, error) {
	copy := *entry
	r.logs = append(r.logs, &copy)
	return true, nil
}

type managedExecutorBillingRepo struct {
	service.UsageBillingRepository
	commands []*service.UsageBillingCommand
}

type managedExecutorAccountRepo struct {
	*cindyHandlerFailoverAccountRepo
}

func (r *managedExecutorAccountRepo) GetByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		for i := range r.accounts {
			if r.accounts[i].ID == id {
				copy := r.accounts[i]
				result = append(result, &copy)
				break
			}
		}
	}
	return result, nil
}

func (r *managedExecutorBillingRepo) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	copy := *command
	r.commands = append(r.commands, &copy)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type managedExecutorFixture struct {
	router      *gin.Engine
	group       *service.Group
	repo        *managedExecutorAccountRepo
	upstream    *managedExecutorUpstream
	concurrency *managedExecutorConcurrency
	balance     *managedExecutorBillingCache
	billing     *managedExecutorBillingRepo
	usage       *managedExecutorUsageRepo
	nextCalls   int
}

type managedExecutorBranch struct {
	account int
	wire    string
	target  string
}

func newManagedExecutorFixture(t *testing.T, platforms []string, branches []managedExecutorBranch, caches ...service.GatewayCache) *managedExecutorFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	groupID := int64(78300)
	endpoints := []string{service.CompositeRouteEndpointResponses, service.CompositeRouteEndpointMessages, service.CompositeRouteEndpointChatCompletions}
	group := &service.Group{ID: groupID, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1, ManagedModelRoutes: service.ManagedModelRoutesConfig{Version: 2, Enabled: true}}
	accounts := make([]service.Account, len(platforms))
	for i, platform := range platforms {
		accounts[i] = service.Account{ID: groupID + int64(i) + 1, Name: "offline account", Platform: platform, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: i + 1, Concurrency: 1, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"api_key": "synthetic-not-a-real-key", "base_url": "https://upstream.example.test/v1", "model_mapping": map[string]any{"private": "private-route-kept"}}}
		if platform == service.PlatformOpenAI {
			accounts[i].Extra = map[string]any{"openai_responses_mode": "force_responses", "openai_responses_supported": true}
		} else {
			accounts[i].Credentials["base_url"] = "https://upstream.example.test"
		}
	}
	route := service.ManagedModelRoute{PublicModel: managedExecutorPublicModel, Endpoints: endpoints}
	for _, spec := range branches {
		account := &accounts[spec.account]
		selector := service.ManagedModelBranchSelector(groupID, managedExecutorPublicModel, account.Platform, spec.wire, spec.target)
		if spec.wire == "" {
			selector = service.ManagedModelSelector(groupID, managedExecutorPublicModel)
		}
		account.Credentials["model_mapping"].(map[string]any)[selector] = spec.target
		route.Branches = append(route.Branches, service.ManagedModelRouteBranch{Selector: selector, TargetPlatform: account.Platform, UpstreamProtocol: spec.wire, Endpoints: endpoints,
			Accounts: []service.ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: spec.target, AccountFingerprint: service.ManagedModelAccountFingerprint(account), Endpoints: endpoints}}})
	}
	group.ManagedModelRoutes.Routes = []service.ManagedModelRoute{route}
	f := &managedExecutorFixture{group: group, repo: &managedExecutorAccountRepo{cindyHandlerFailoverAccountRepo: &cindyHandlerFailoverAccountRepo{accounts: accounts}}, upstream: &managedExecutorUpstream{respond: managedExecutorSuccess},
		concurrency: &managedExecutorConcurrency{}, balance: &managedExecutorBillingCache{}, billing: &managedExecutorBillingRepo{}, usage: &managedExecutorUsageRepo{}}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	quotaRepo := &managedExecutorQuotaRepo{}
	billingCache := service.NewBillingCacheService(f.balance, nil, nil, nil, nil, nil, cfg, quotaRepo)
	t.Cleanup(billingCache.Stop)
	concurrency := service.NewConcurrencyService(f.concurrency)
	billing := service.NewBillingService(cfg, nil)
	var pricingResolver *service.ModelPricingResolver
	for _, platform := range platforms {
		if !service.IsCNProvider(platform) {
			continue
		}
		// Production publication requires an explicit price. A CN wire carrying
		// this fixture's Claude-branded public name must not borrow the default
		// Claude price card: the existing billing guard correctly refuses that.
		input, output := 0.000001, 0.000002
		group.ModelPricing = []service.ChannelModelPricing{{Models: []string{managedExecutorPublicModel}, BillingMode: service.BillingModeToken, InputPrice: &input, OutputPrice: &output}}
		pricingResolver = service.NewModelPricingResolver(nil, billing)
		break
	}
	deferred := &service.DeferredService{}
	var cache service.GatewayCache
	if len(caches) > 0 {
		cache = caches[0]
	}
	openAI := service.NewOpenAIGatewayService(f.repo, f.usage, f.billing, nil, nil, nil, cache, cfg, nil, concurrency, billing, nil, billingCache, f.upstream, deferred, nil, nil, pricingResolver, nil, nil, nil, quotaRepo)
	native := service.NewGatewayService(f.repo, &fakeGroupRepo{group: group}, f.usage, f.billing, nil, nil, nil, cache, cfg, nil, concurrency, billing, nil, billingCache, nil, f.upstream, deferred, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, quotaRepo)
	apiKeys := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg)
	openAIHandler := NewOpenAIGatewayHandler(openAI, concurrency, billingCache, apiKeys, nil, nil, nil, nil, cfg)
	nativeHandler := NewGatewayHandler(native, openAI, nil, nil, nil, concurrency, billingCache, nil, apiKeys, nil, nil, nil, nil, cfg, nil)
	apiKey := &service.APIKey{ID: 78399, UserID: 78398, GroupID: &groupID, Status: service.StatusActive, User: &service.User{ID: 78398, Status: service.StatusActive, Balance: 100}, Group: group}
	f.router = gin.New()
	f.router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 2})
		c.Next()
	})
	// Compilation/source selection has independent service tests. This guard
	// still performs the real body/public-name preparation and allowlist check.
	f.router.Use(managedModelRouteGuard(&managedModelWSFixture{group: group}, 1<<20), middleware.GroupModelAllowlist(1<<20), nativeHandler.ManagedModelV2(openAIHandler))
	for _, path := range []string{"/v1/responses", "/v1/responses/input_tokens", "/v1/messages", "/v1/chat/completions"} {
		f.router.POST(path, func(c *gin.Context) { f.nextCalls++; c.Status(http.StatusTeapot) })
	}
	return f
}

func (f *managedExecutorFixture) request(path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}

func (f *managedExecutorFixture) requireOnce(t *testing.T, accountID int64) {
	t.Helper()
	require.EqualValues(t, 1, f.concurrency.userAcquire.Load(), "cross-branch forwarding must reuse the user admission")
	require.EqualValues(t, 1, f.concurrency.userRelease.Load())
	require.EqualValues(t, 1, f.balance.balanceReads.Load(), "billing eligibility must not run once per branch")
	require.Zero(t, f.nextCalls, "a managed request must never re-enter the next HTTP handler")
	require.Len(t, f.billing.commands, 1, "only the winning forward is billed")
	require.Equal(t, accountID, f.billing.commands[0].AccountID)
	require.Len(t, f.usage.logs, 1)
	require.Equal(t, accountID, f.usage.logs[0].AccountID)
	assert.Equal(t, managedExecutorPublicModel, f.usage.logs[0].RequestedModel, "usage display must retain the public request model")
	assert.Equal(t, managedExecutorPublicModel, f.usage.logs[0].Model, "a selector cannot become the usage model")
	assert.Equal(t, managedExecutorPublicModel, f.billing.commands[0].Model, "a selector cannot become the billed model")
	f.balance.quotaMu.Lock()
	quotaReads := append([]string(nil), f.balance.quotaReads...)
	f.balance.quotaMu.Unlock()
	require.Equal(t, []string{service.PlatformAnthropic, service.PlatformAnthropic}, quotaReads,
		"preflight and post-billing must consume the public group's quota, not the winning OpenAI branch's quota")
}

func TestManagedModelV2ExecutorCrossPlatformFailoverAdmitsAndBillsOnce(t *testing.T) {
	f := newManagedExecutorFixture(t, []string{service.PlatformAnthropic, service.PlatformOpenAI}, []managedExecutorBranch{
		{account: 0, wire: "messages", target: "claude-fable-5.1"}, {account: 1, wire: "chat_completions", target: "provider/Fable-5.1-CC"},
	})
	before, err := json.Marshal(f.repo.accounts[1])
	require.NoError(t, err)
	f.upstream.respond = func(call managedExecutorCall, n int) *http.Response {
		if n == 1 {
			return managedExecutorUnavailable()
		}
		return managedExecutorSuccess(call, n)
	}
	response := f.request("/v1/responses", `{"model":"claude-fable-5.1","input":"hello","stream":false}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "managed recovered")
	assert.Equal(t, managedExecutorPublicModel, gjson.GetBytes(response.Body.Bytes(), "model").String())
	assert.NotContains(t, response.Body.String(), "s2pub-", "internal branch selectors must not escape in any response field")
	calls := f.upstream.snapshot()
	require.Len(t, calls, 2)
	require.Equal(t, []string{"/v1/messages", "/v1/chat/completions"}, []string{calls[0].path, calls[1].path}, "the proven Chat wire overrides the account's default Responses wire")
	require.Equal(t, "provider/Fable-5.1-CC", calls[1].model)
	require.NotEqual(t, calls[0].accountID, calls[1].accountID)
	f.requireOnce(t, calls[1].accountID)
	after, err := json.Marshal(f.repo.accounts[1])
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "the forced wire must only change an attempt-local account clone")
}

func TestManagedModelV2ExecutorSameAccountAlternateTargetsRemainReachable(t *testing.T) {
	f := newManagedExecutorFixture(t, []string{service.PlatformOpenAI}, []managedExecutorBranch{
		{account: 0, wire: "chat_completions", target: "provider/fable-5.1"}, {account: 0, wire: "chat_completions", target: "provider/fable-5.1-CC"},
	})
	f.upstream.respond = func(call managedExecutorCall, n int) *http.Response {
		if n == 1 {
			return managedExecutorUnavailable()
		}
		return managedExecutorSuccess(call, n)
	}
	response := f.request("/v1/messages", `{"model":"claude-fable-5.1","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, managedExecutorPublicModel, gjson.GetBytes(response.Body.Bytes(), "model").String())
	assert.NotContains(t, response.Body.String(), "s2pub-", "internal branch selectors must not escape in any response field")
	calls := f.upstream.snapshot()
	require.Len(t, calls, 2, "a failure excludes the account/target branch, not the whole account")
	require.Equal(t, calls[0].accountID, calls[1].accountID)
	models := []string{calls[0].model, calls[1].model}
	sort.Strings(models)
	require.Equal(t, []string{"provider/fable-5.1", "provider/fable-5.1-CC"}, models)
	f.requireOnce(t, calls[1].accountID)
}

func TestManagedModelV2ExecutorDoesNotReplayDeliveredContent(t *testing.T) {
	f := newManagedExecutorFixture(t, []string{service.PlatformOpenAI, service.PlatformOpenAI}, []managedExecutorBranch{
		{account: 0, wire: "responses", target: "claude-fable-5.1"}, {account: 1, wire: "responses", target: "alternate/fable-5.1"},
	})
	f.upstream.respond = func(_ managedExecutorCall, _ int) *http.Response {
		return managedExecutorResponse(http.StatusOK, "text/event-stream", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"status\":\"in_progress\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"response_id\":\"resp_partial\",\"delta\":\"already delivered\"}\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_partial\",\"status\":\"failed\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"offline stream interrupted\"}}}\n\n")
	}
	response := f.request("/v1/responses", `{"model":"claude-fable-5.1","input":"hello","stream":true}`)
	require.Contains(t, response.Body.String(), "already delivered")
	require.Len(t, f.upstream.snapshot(), 1, "semantic output forbids switching to a second verified branch")
	require.Zero(t, f.nextCalls)
	require.EqualValues(t, 1, f.concurrency.userAcquire.Load())
}

func TestManagedModelV2ExecutorPinnedContinuationCannotFailOver(t *testing.T) {
	f := newManagedExecutorFixture(t, []string{service.PlatformOpenAI, service.PlatformOpenAI}, []managedExecutorBranch{
		{account: 0, wire: "responses", target: "claude-fable-5.1"}, {account: 1, wire: "responses", target: "alternate/fable-5.1"},
	})
	first := f.request("/v1/responses", `{"model":"claude-fable-5.1","input":"hello","stream":false}`)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Equal(t, "resp_managed_offline", gjson.GetBytes(first.Body.Bytes(), "id").String())
	f.upstream.respond = func(managedExecutorCall, int) *http.Response { return managedExecutorUnavailable() }
	second := f.request("/v1/responses", `{"model":"claude-fable-5.1","previous_response_id":"resp_managed_offline","input":"continue","stream":false}`)
	require.GreaterOrEqual(t, second.Code, 400, second.Body.String())
	calls := f.upstream.snapshot()
	require.Len(t, calls, 2, "the continuation makes one attempt, not a fresh cross-account selection")
	require.Equal(t, calls[0].accountID, calls[1].accountID)
	require.Equal(t, "resp_managed_offline", gjson.GetBytes(calls[1].body, "previous_response_id").String())
	require.Len(t, f.billing.commands, 1, "the failed continuation must not bill another successful call")
	require.Zero(t, f.nextCalls)
}

func TestManagedModelV2ExecutorAffinitySurvivesHandlerRebuildWithoutBranchFailover(t *testing.T) {
	for _, tc := range []struct {
		name      string
		platforms []string
		branches  []managedExecutorBranch
	}{
		{
			name:      "other account",
			platforms: []string{service.PlatformOpenAI, service.PlatformOpenAI},
			branches: []managedExecutorBranch{
				{account: 0, wire: "responses", target: "claude-fable-5.1"},
				{account: 1, wire: "responses", target: "alternate/fable-5.1"},
			},
		},
		{
			name:      "same account other target",
			platforms: []string{service.PlatformOpenAI},
			branches: []managedExecutorBranch{
				{account: 0, wire: "responses", target: "claude-fable-5.1"},
				{account: 0, wire: "responses", target: "alternate/fable-5.1"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache, _ := managedModelV2AffinityRedisFixture(t)
			gatewayCache := cache.(service.GatewayCache)
			first := newManagedExecutorFixture(t, tc.platforms, tc.branches, gatewayCache)
			first.upstream.respond = func(call managedExecutorCall, n int) *http.Response {
				if n == 1 {
					return managedExecutorUnavailable()
				}
				return managedExecutorSuccess(call, n)
			}
			response := first.request("/v1/responses", `{"model":"claude-fable-5.1","input":"hello","stream":false}`)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			calls := first.upstream.snapshot()
			require.Len(t, calls, 2)
			success := calls[1]

			// Construct fresh services, handler middleware and local stores. Only
			// the production GatewayCache is shared with the first deployment.
			rebuilt := newManagedExecutorFixture(t, tc.platforms, tc.branches, gatewayCache)
			rebuilt.upstream.respond = func(managedExecutorCall, int) *http.Response { return managedExecutorUnavailable() }
			continued := rebuilt.request("/v1/responses", `{"model":"claude-fable-5.1","previous_response_id":"resp_managed_offline","input":"continue","stream":false}`)
			require.GreaterOrEqual(t, continued.Code, 400, continued.Body.String())
			continuedCalls := rebuilt.upstream.snapshot()
			require.Len(t, continuedCalls, 1, "a restored continuation must try its retained branch, not fail closed or fail over")
			require.Equal(t, success.accountID, continuedCalls[0].accountID)
			require.Equal(t, success.model, continuedCalls[0].model, "the same account's alternate target is a different continuation branch")
			require.Equal(t, "resp_managed_offline", gjson.GetBytes(continuedCalls[0].body, "previous_response_id").String())
			require.Empty(t, rebuilt.billing.commands, "a failed restored continuation does not bill a new success")
			require.Zero(t, rebuilt.nextCalls)
		})
	}
}

func TestManagedModelV2ExecutorLegacyContinuationMigratesThroughProductionLookup(t *testing.T) {
	cache, _ := managedModelV2AffinityRedisFixture(t)
	gatewayCache := cache.(service.GatewayCache)
	const groupID, accountID, userID, apiKeyID = int64(78300), int64(78302), int64(78398), int64(78399)
	legacy := service.NewOpenAIWSStateStore(gatewayCache)
	require.NoError(t, legacy.BindResponseAccount(context.Background(), groupID, "resp_legacy_executor", accountID, time.Hour))
	require.NoError(t, legacy.BindHTTPResponseOwner(context.Background(), groupID, "resp_legacy_executor", userID, apiKeyID, time.Hour))
	f := newManagedExecutorFixture(t, []string{service.PlatformOpenAI, service.PlatformOpenAI}, []managedExecutorBranch{
		{account: 0, wire: "responses", target: "first-priority/fable-5.1"},
		{account: 1, wire: "", target: "legacy/fable-5.1"},
		{account: 1, wire: "responses", target: "new-alternate/fable-5.1"},
	}, gatewayCache)
	f.group.Platform = service.PlatformComposite
	f.group.ManagedModelRoutes.Routes[0].QuotaPlatform = service.PlatformAnthropic
	response := f.request("/v1/responses", `{"model":"claude-fable-5.1","previous_response_id":"resp_legacy_executor","input":"continue","stream":false}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	calls := f.upstream.snapshot()
	require.Len(t, calls, 1, "the handler must connect its legacy lookup rather than reject the missing v2 pin")
	require.Equal(t, accountID, calls[0].accountID)
	require.Equal(t, "legacy/fable-5.1", calls[0].model, "an account-only legacy binding can use only its retained original branch")
	f.requireOnce(t, accountID)

	// The production migration must persist the old reference, not merely
	// select the old account for this one request in process memory.
	pin, err := newManagedModelV2AffinityStore(cache).Resolve(apiKeyID, groupID, managedExecutorPublicModel, []byte(`{"previous_response_id":"resp_legacy_executor"}`))
	require.NoError(t, err)
	require.Equal(t, &managedModelV2Pin{AccountID: accountID, BranchSelector: service.ManagedModelSelector(groupID, managedExecutorPublicModel)}, pin)
}

type managedExecutorAffinityCache struct {
	service.GatewayCache
	service.ManagedModelAffinityCache
}

func TestManagedModelV2ExecutorAffinityPersistenceFailureIsObservable(t *testing.T) {
	cache, _ := managedModelV2AffinityRedisFixture(t)
	fault := &managedModelV2AffinityFaultCache{ManagedModelAffinityCache: cache, bindError: errors.New("private-cache-write-error")}
	f := newManagedExecutorFixture(t, []string{service.PlatformOpenAI}, []managedExecutorBranch{
		{account: 0, wire: "responses", target: "claude-fable-5.1"},
	}, &managedExecutorAffinityCache{GatewayCache: cache.(service.GatewayCache), ManagedModelAffinityCache: fault})
	core, logs := observer.New(zap.WarnLevel)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-fable-5.1","input":"private-user-prompt","stream":false}`))
	request = request.WithContext(logger.IntoContext(request.Context(), zap.New(core)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "managed recovered", "cache write failure must not replace already delivered success")
	require.Positive(t, fault.bindCalls)
	entries := logs.FilterMessage("managed_model_v2.affinity_persistence_failed").All()
	require.Len(t, entries, 1, "the writer's persistence failure flag must be consumed by the production handler")
	require.Equal(t, int64(78399), entries[0].ContextMap()["api_key_id"])
	require.Equal(t, int64(78300), entries[0].ContextMap()["group_id"])
	serialized, err := json.Marshal(entries)
	require.NoError(t, err)
	for _, private := range []string{"private-cache-write-error", "private-user-prompt", "resp_managed_offline"} {
		require.NotContains(t, string(serialized), private)
	}
	f.requireOnce(t, f.repo.accounts[0].ID)
}

func TestManagedModelV2ExecutorRejectsInputTokensBeforeAdmission(t *testing.T) {
	f := newManagedExecutorFixture(t, []string{service.PlatformOpenAI}, []managedExecutorBranch{{account: 0, wire: "responses", target: "claude-fable-5.1"}})
	response := f.request("/v1/responses/input_tokens", `{"model":"claude-fable-5.1","input":"hello"}`)
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Empty(t, f.upstream.snapshot(), "generative proof does not establish input-token counting support")
	require.Zero(t, f.concurrency.userAcquire.Load())
	require.Zero(t, f.balance.balanceReads.Load())
	require.Zero(t, f.nextCalls)
}
