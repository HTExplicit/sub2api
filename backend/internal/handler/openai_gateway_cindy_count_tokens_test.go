//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const cindyCatalogRollbackCountTokensHelperEnv = "SUB2API_CINDY_CATALOG_ROLLBACK_COUNT_TOKENS_HELPER"

type cindyCountTokensAccountRepo struct {
	service.AccountRepository

	mu             sync.Mutex
	accounts       []service.Account
	selectionCalls int
}

func (r *cindyCountTokensAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selectionCalls++
	out := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform && account.IsSchedulable() {
			out = append(out, account)
		}
	}
	return out, nil
}

func (r *cindyCountTokensAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *cindyCountTokensAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *cindyCountTokensAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

type cindyCountTokensUpstreamCall struct {
	accountID     int64
	method        string
	path          string
	rawQuery      string
	authorization string
	xAPIKey       string
	body          []byte
}

type cindyCountTokensUpstream struct {
	service.HTTPUpstream

	mu    sync.Mutex
	calls []cindyCountTokensUpstreamCall
}

func (u *cindyCountTokensUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	return u.respond(req, accountID)
}

func (u *cindyCountTokensUpstream) DoWithTLS(req *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.respond(req, accountID)
}

func (u *cindyCountTokensUpstream) respond(req *http.Request, accountID int64) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.calls = append(u.calls, cindyCountTokensUpstreamCall{
		accountID:     accountID,
		method:        req.Method,
		path:          req.URL.Path,
		rawQuery:      req.URL.RawQuery,
		authorization: req.Header.Get("Authorization"),
		xAPIKey:       req.Header.Get("x-api-key"),
		body:          append([]byte(nil), body...),
	})
	u.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"input_tokens":17}`)),
	}, nil
}

func (u *cindyCountTokensUpstream) snapshots() []cindyCountTokensUpstreamCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]cindyCountTokensUpstreamCall, len(u.calls))
	copy(out, u.calls)
	return out
}

func newCindyCountTokensHandler(
	t *testing.T,
	accounts []service.Account,
	upstream service.HTTPUpstream,
) (*OpenAIGatewayHandler, *cindyCountTokensAccountRepo, int64) {
	t.Helper()
	groupID := int64(55100)
	repo := &cindyCountTokensAccountRepo{accounts: accounts}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 3
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1

	concurrencyService := service.NewConcurrencyService(nil)
	billingService := service.NewBillingService(cfg, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	rateLimitService := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	openAIGateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrencyService,
		billingService, rateLimitService, billingCache, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	nativeGateway := service.NewGatewayService(
		repo, nil, nil, nil, nil, nil, nil, nil, cfg, nil, concurrencyService,
		billingService, rateLimitService, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(
		openAIGateway, concurrencyService, billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	h.SetNativeAnthropicGatewayService(nativeGateway)
	return h, repo, groupID
}

func newCindyCountTokensContext(t *testing.T, groupID int64, strict bool, allowMessages bool, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	group := &service.Group{
		ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive,
		AllowMessagesDispatch: allowMessages, RateMultiplier: 1,
		StrictCindyKnown: true, StrictCindy: strict,
	}
	if strict {
		group.Platform = service.PlatformCindy
		group.WirePlatform = service.WirePlatformOpenAI
		group.ProviderProfile = service.ProviderProfileCindyLaxaV1
	}
	apiKey := &service.APIKey{
		ID: 55110, GroupID: &groupID, Status: service.StatusActive,
		User:  &service.User{ID: 55111, Status: service.StatusActive},
		Group: group,
	}
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.User.ID, Concurrency: 0})
	return c, recorder
}

func cindyCountTokensAccount(id int64, priority int) service.Account {
	return service.Account{
		ID: id, Name: "cindy-count-tokens", Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Concurrency: 0, Priority: priority,
		Credentials: map[string]any{
			"api_key":  "cindy-upstream-secret",
			"base_url": "https://api.laxarouter.ai",
		},
	}
}

func TestCountTokensFirstClassCindyReturnsFixedNotFoundBeforeRequestProcessing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "valid public model", body: `{"model":"claude-opus-5","messages":[]}`},
		{name: "malformed body", body: `{"model":`},
		{name: "empty body", body: ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &cindyCountTokensUpstream{}
			h, repo, groupID := newCindyCountTokensHandler(t, []service.Account{cindyCountTokensAccount(55101, 0)}, upstream)
			h.billingCacheService = nil
			h.nativeAnthropicGatewayService = nil
			c, recorder := newCindyCountTokensContext(t, groupID, true, false, tt.body)

			h.CountTokens(c)

			require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
			require.Equal(t, "not_found_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
			require.Equal(t, "count_tokens endpoint is not supported by upstream", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
			require.Empty(t, upstream.snapshots(), "Cindy count_tokens must never reach an upstream account")
			require.Zero(t, repo.selectionCalls, "Cindy count_tokens must return before account selection")
		})
	}
}

func TestCountTokensOrdinaryOpenAIStillUsesResponsesInputTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ordinary := service.Account{
		ID: 55201, Name: "ordinary-openai", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "ordinary-secret", "base_url": "https://ordinary.example"},
	}
	upstream := &cindyCountTokensUpstream{}
	h, _, groupID := newCindyCountTokensHandler(t, []service.Account{ordinary}, upstream)
	c, recorder := newCindyCountTokensContext(t, groupID, false, true,
		`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}]}`,
	)

	h.CountTokens(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	calls := upstream.snapshots()
	require.Len(t, calls, 1)
	require.Equal(t, "/v1/responses/input_tokens", calls[0].path)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(calls[0].body, "model").String())
	require.True(t, gjson.GetBytes(calls[0].body, "input").Exists())
	require.False(t, gjson.GetBytes(calls[0].body, "messages").Exists())
}

func TestCindyCatalogRollbackKeepsFirstClassCountTokensNotFound(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCindyCatalogRollbackCountTokensBridgeHelper$")
	cmd.Env = append(withoutCindyCatalogHandlerEnv(os.Environ()),
		service.CindyCapabilityCatalogEnabledEnv+"=false",
		cindyCatalogRollbackCountTokensHelperEnv+"=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated count_tokens catalog rollback test failed: %v\n%s", err, output)
	}
}

func TestCindyCatalogRollbackCountTokensBridgeHelper(t *testing.T) {
	if os.Getenv(cindyCatalogRollbackCountTokensHelperEnv) == "" {
		t.Skip("subprocess helper")
	}
	if service.CindyCapabilityCatalogFeatureEnabled() {
		t.Fatal("count_tokens rollback helper started with the catalog enabled")
	}

	gin.SetMode(gin.TestMode)
	accountID := int64(55401)
	upstream := &cindyCountTokensUpstream{}
	h, _, groupID := newCindyCountTokensHandler(t, []service.Account{
		cindyCountTokensAccount(accountID, 0),
	}, upstream)
	c, recorder := newCindyCountTokensContext(t, groupID, true, true,
		`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}]}`,
	)

	h.CountTokens(c)

	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	require.Equal(t, "not_found_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Equal(t, "count_tokens endpoint is not supported by upstream", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
	require.Empty(t, upstream.snapshots())
}
