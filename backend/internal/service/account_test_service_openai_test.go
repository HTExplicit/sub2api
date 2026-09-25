//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// --- shared test helpers ---

type queuedHTTPUpstream struct {
	responses []*http.Response
	requests  []*http.Request
	tlsFlags  []bool
	leases    []string
}

// OAuth Codex account tests share business traffic's entry points: Do, or the
// verified connection lease for a qualified route. API-key probes use DoWithTLS.
func (u *queuedHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.serve(req, nil)
}

func (u *queuedHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.serve(req, profile)
}

func (u *queuedHTTPUpstream) DoWithCodexConnectionLease(req *http.Request, _ string, _ int64, _ string, leaseID string, _ time.Time, profile *tlsfingerprint.Profile) (*http.Response, string, error) {
	u.leases = append(u.leases, leaseID)
	resp, err := u.serve(req, profile)
	return resp, leaseID, err
}

func (u *queuedHTTPUpstream) serve(req *http.Request, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	u.tlsFlags = append(u.tlsFlags, profile != nil)
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// --- test functions ---

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/test", nil)
	return c, rec
}

type openAIAccountTestRepo struct {
	mockAccountRepoForGemini
	updatedExtra       map[string]any
	persistedExtra     map[int64]map[string]any
	extraWriteID       int64
	extraWriteErr      error
	bulkUpdatedIDs     []int64
	bulkUpdatedPayload AccountBulkUpdate
	rateLimitedID      int64
	rateLimitedAt      *time.Time
	clearedErrorID     int64
	setErrorID         int64
	setErrorMsg        string
}

type cindyAccountTestRepo struct {
	openAIAccountTestRepo
	markCalls int
	markedIDs []int64
}

func (r *cindyAccountTestRepo) MarkCindyBalanceInsufficient(_ context.Context, accountID int64, _ time.Time) (bool, error) {
	r.markCalls++
	r.markedIDs = append(r.markedIDs, accountID)
	return r.markCalls == 1, nil
}

func (r *cindyAccountTestRepo) ClearCindyBalanceInsufficient(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *cindyAccountTestRepo) PreviewCindyInsufficientDeletion(context.Context) (*CindyInsufficientDeletePreview, error) {
	return &CindyInsufficientDeletePreview{}, nil
}

func (r *cindyAccountTestRepo) DeleteCindyInsufficient(context.Context, int, string) (*CindyInsufficientDeleteResult, error) {
	return &CindyInsufficientDeleteResult{}, nil
}

func (r *openAIAccountTestRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.extraWriteID = id
	if r.extraWriteErr != nil {
		return r.extraWriteErr
	}
	r.updatedExtra = updates
	if r.persistedExtra == nil {
		r.persistedExtra = make(map[int64]map[string]any)
	}
	if r.persistedExtra[id] == nil {
		r.persistedExtra[id] = make(map[string]any)
	}
	for key, value := range updates {
		r.persistedExtra[id][key] = value
	}
	return nil
}

func (r *openAIAccountTestRepo) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.bulkUpdatedIDs = append([]int64(nil), ids...)
	r.bulkUpdatedPayload = updates
	return int64(len(ids)), nil
}

func (r *openAIAccountTestRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.rateLimitedAt = &resetAt
	return nil
}

func (r *openAIAccountTestRepo) ClearError(_ context.Context, id int64) error {
	r.clearedErrorID = id
	return nil
}

func (r *openAIAccountTestRepo) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	return nil
}

func requireAccountTestQuotaState(t *testing.T, repo *openAIAccountTestRepo, account *Account) *UpstreamQuotaState {
	t.Helper()
	require.Equal(t, account.ID, repo.extraWriteID)
	require.Len(t, repo.persistedExtra, 1, "quota writes must stay on the observed account")
	require.Contains(t, repo.persistedExtra, account.ID)
	now := time.Now()
	state := account.QuotaState(now)
	require.NotNil(t, state)
	require.True(t, state.Blocked)
	persisted := *account
	persisted.Extra = repo.persistedExtra[account.ID]
	require.Equal(t, state, persisted.QuotaState(now))
	require.Zero(t, repo.rateLimitedID, "hard quota must not create an independent ordinary cooldown")
	require.Nil(t, repo.rateLimitedAt)
	require.Zero(t, repo.clearedErrorID, "quota evidence does not resolve another account error")
	return state
}

func TestAccountTestService_CindyBudget429DoesNotPersistWithoutConfirmation(t *testing.T) {
	const responseBody = exactCindyBudgetExceededBody

	tests := []struct {
		name string
		run  func(*AccountTestService, *gin.Context, *Account) error
	}{
		{
			name: "responses",
			run: func(svc *AccountTestService, c *gin.Context, account *Account) error {
				return svc.testOpenAIAccountConnection(c, account, "gpt-5.6-sol", "hi", AccountTestModeDefault)
			},
		},
		{
			name: "chat completions",
			run: func(svc *AccountTestService, c *gin.Context, account *Account) error {
				return svc.testOpenAIChatCompletionsConnection(c, account, "gpt-5.6-sol", "hi", "https://api.laxarouter.ai", "test-key")
			},
		},
		{
			name: "compact",
			run: func(svc *AccountTestService, c *gin.Context, account *Account) error {
				return svc.testOpenAICompactConnection(c, account, "gpt-5.6-sol")
			},
		},
		{
			name: "images",
			run: func(svc *AccountTestService, c *gin.Context, account *Account) error {
				return svc.testOpenAIImageAPIKey(c, c.Request.Context(), account, "gpt-image-2", "test")
			},
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, recorder := newTestContext()
			repo := &cindyAccountTestRepo{}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusTooManyRequests, responseBody)}}
			svc := &AccountTestService{
				accountRepo:  repo,
				httpUpstream: upstream,
				cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
			}
			account := &Account{
				ID:              int64(8600 + index),
				Platform:        PlatformCindy,
				WirePlatform:    WirePlatformOpenAI,
				ProviderProfile: ProviderProfileCindyLaxaV1,
				Type:            AccountTypeAPIKey,
				Status:          StatusActive,
				Schedulable:     true,
				Concurrency:     1,
				Credentials:     cindyCredentials(),
				Extra:           map[string]any{"use_responses_api": true},
			}

			err := tt.run(svc, c, account)

			require.Error(t, err)
			require.Zero(t, repo.markCalls)
			require.Empty(t, repo.markedIDs)
			require.Nil(t, account.CindyBalanceInsufficientAt)
			require.Contains(t, recorder.Body.String(), "returned 429")
		})
	}
}

func TestAccountTestService_CindyBudget429DoesNotStartBackgroundProbe(t *testing.T) {
	c, _ := newTestContext()
	repo := &cindyRateLimitAccountRepoStub{}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newJSONResponse(http.StatusTooManyRequests, exactCindyBudgetExceededBody),
	}}
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
	rateLimitService := NewRateLimitService(repo, nil, cfg, nil, nil)
	gateway := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: upstream, rateLimitService: rateLimitService,
	}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	svc := &AccountTestService{
		accountRepo: repo, httpUpstream: upstream, openAIGatewayService: gateway, cfg: cfg,
	}
	account := &Account{
		ID: 8660, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: cindyCredentials(), Extra: map[string]any{"use_responses_api": true},
	}

	err := svc.testOpenAIAccountConnection(c, account, "gpt-5.6-luna", "hi", AccountTestModeDefault)

	require.Error(t, err)
	require.Len(t, upstream.bodies, 1, "a manual connection test must issue only its requested call")
	require.Equal(t, "openai/gpt-5.6-luna", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Zero(t, repo.markCalls)
	require.Nil(t, account.CindyBalanceInsufficientAt)
}

func TestAccountTestService_OrdinaryCindy429DoesNotMarkBalanceInsufficient(t *testing.T) {
	c, _ := newTestContext()
	repo := &cindyAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(
		http.StatusTooManyRequests,
		`{"error":{"type":"rate_limit_error","message":"too many requests"}}`,
	)}}
	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:              8699,
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Concurrency:     1,
		Credentials:     cindyCredentials(),
		Extra:           map[string]any{"use_responses_api": true},
	}

	err := svc.testOpenAIAccountConnection(c, account, "gpt-5.6-sol", "hi", AccountTestModeDefault)

	require.Error(t, err)
	require.Zero(t, repo.markCalls)
	require.Nil(t, account.CindyBalanceInsufficientAt)
}

func TestAccountTestService_CindyEmptyModelUsesLuna(t *testing.T) {
	c, _ := newTestContext()
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(
		http.StatusOK,
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
	)}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:              8700,
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Concurrency:     1,
		Credentials:     cindyCredentials(),
		Extra:           map[string]any{"use_responses_api": true},
	}

	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "", "hi", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	requestBody, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-5.6-luna", gjson.GetBytes(requestBody, "model").String())
}

func TestAccountTestService_NonCindyEmptyModelKeepsOpenAIDefault(t *testing.T) {
	c, _ := newTestContext()
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(
		http.StatusOK,
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
	)}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          8701,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.openai.com"},
		Extra:       map[string]any{"use_responses_api": true},
	}

	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "", "hi", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	requestBody, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, openai.DefaultTestModel, gjson.GetBytes(requestBody, "model").String())
}

func TestAccountTestService_OpenAISuccessPersistsSnapshotFromHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()
	ctx.Request = ctx.Request.WithContext(withCodexTransportFixture(ctx.Request.Context(), true))

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"OK"}

data: {"type":"response.completed"}

`))
	resp.Header.Set("x-codex-primary-used-percent", "88")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "42")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
		cfg:          &config.Config{Gateway: config.GatewayConfig{OpenAICodexRequestZstd: true}},
	}
	account := &Account{
		ID:          89,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.requests[0].Context()))
	// 普通 OAuth 连接测试与真实转发同一 zstd 压缩边界：线上副本带 Content-Encoding: zstd，解压后语义不变。
	require.Equal(t, "zstd", upstream.requests[0].Header.Get("Content-Encoding"))
	compressedProbe, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(zstdDecodeForTest(t, compressedProbe), "model").String())
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 42.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, 88.0, repo.updatedExtra["codex_7d_used_percent"])
	require.Contains(t, recorder.Body.String(), `"zstd_applied":true`)
	require.Contains(t, recorder.Body.String(), "test_complete")
}

func TestAccountTestService_OpenAIOAuthTestNormalizesGPT56Alias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"OK"}

data: {"type":"response.completed"}

`))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{httpUpstream: upstream}
	account := &Account{
		ID:          90,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.6", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)

	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(body, "model").String())
}

func TestAccountTestService_OpenAIShadowUsesParentCredentialsAndShadowModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"OK"}

data: {"type":"response.completed"}

`))

	parentID := int64(100)
	parent := &Account{
		ID:       parentID,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":       "parent-token",
			"chatgpt_account_id": "org-parent",
		},
	}
	shadow := &Account{
		ID:              200,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Concurrency:     2,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.3-codex-spark": "gpt-5.3-codex-spark",
			},
		},
	}

	repo := &openAIAccountTestRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{
				parentID: parent,
				200:      shadow,
			},
		},
	}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}

	err := svc.TestAccountConnection(ctx, shadow.ID, "gpt-5.3-codex-spark", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, "Bearer parent-token", req.Header.Get("Authorization"))
	require.Equal(t, "org-parent", req.Header.Get("chatgpt-account-id"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.3-codex-spark", gjson.GetBytes(body, "model").String())
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.Contains(t, recorder.Body.String(), `"shadow_parent_id":100`)
	require.NotContains(t, recorder.Body.String(), "parent-token")
}

func TestAccountTestService_OpenAIStreamEOFBeforeCompletedFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"hi"}

`))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{httpUpstream: upstream}
	account := &Account{
		ID:          90,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAccountTestIncomplete)
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_DeepSeekCustomBaseURLUsesV1ResponsesPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"OK"}

data: {"type":"response.completed"}

`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          91,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"base_url":     "https://relay.example.com/v1",
			"api_protocol": APIProtocolResponses,
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://relay.example.com/v1/responses", upstream.requests[0].URL.String())
}

func TestAccountTestService_DeepSeekResponsesRoutesToOpenAIProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"OK"}

data: {"type":"response.completed"}

`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          93,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"base_url":     "https://relay.example.com/v1",
			"api_protocol": APIProtocolResponses,
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}
	repo := &openAIAccountTestRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{93: account},
		},
	}
	svc.accountRepo = repo

	err := svc.TestAccountConnection(ctx, account.ID, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://relay.example.com/v1/responses", upstream.requests[0].URL.String())
}

func TestAccountTestService_DeepSeekDefaultBaseURLUsesNativeResponsesPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"OK"}

data: {"type":"response.completed"}

`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          92,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"api_protocol": APIProtocolResponses,
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://api.deepseek.com/responses", upstream.requests[0].URL.String())
}

func TestAccountTestService_OpenAI429PersistsSnapshotAndQuotaState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_at":1777283883}}`)
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "100")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          88,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusError,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	state := requireAccountTestQuotaState(t, repo, account)
	require.Equal(t, 100.0, repo.persistedExtra[account.ID]["codex_5h_used_percent"])
	require.Len(t, state.Windows, 2)
	require.NotNil(t, state.Until)
	require.WithinDuration(t, time.Now().Add(7*24*time.Hour), *state.Until, 2*time.Second)
	require.Equal(t, StatusError, account.Status)
	require.Empty(t, account.ErrorMessage)
	require.Nil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAI429BodyOnlyPersistsQuotaStateAndPreservesStaleError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_at":"1777283883"}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	priorLimitedAt := time.Now().Add(-time.Minute)
	priorResetAt := time.Now().Add(20 * time.Minute)
	priorTemporaryUntil := time.Now().Add(30 * time.Minute)
	account := &Account{
		ID:            77,
		Platform:      PlatformOpenAI,
		Type:          AccountTypeOAuth,
		Status:        StatusError,
		ErrorMessage:  "Access forbidden (403): account may be suspended or lack permissions",
		Concurrency:   1,
		Credentials:   map[string]any{"access_token": "test-token"},
		RateLimitedAt: &priorLimitedAt, RateLimitResetAt: &priorResetAt,
		TempUnschedulableUntil: &priorTemporaryUntil, TempUnschedulableReason: "operator hold",
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	state := requireAccountTestQuotaState(t, repo, account)
	require.Nil(t, state.Until, "the expired body timestamp is not a new reset deadline")
	require.Equal(t, StatusError, account.Status)
	require.Equal(t, "Access forbidden (403): account may be suspended or lack permissions", account.ErrorMessage)
	require.Equal(t, &priorLimitedAt, account.RateLimitedAt)
	require.Equal(t, &priorResetAt, account.RateLimitResetAt)
	require.Equal(t, &priorTemporaryUntil, account.TempUnschedulableUntil)
	require.Equal(t, "operator hold", account.TempUnschedulableReason)
	require.False(t, account.Schedulable)
}

func TestAccountTestService_OpenAI429SyncsObservedPlanType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","plan_type":"free","resets_at":1777283883}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          81,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token", "plan_type": "plus"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, []int64{account.ID}, repo.bulkUpdatedIDs)
	require.Equal(t, "free", repo.bulkUpdatedPayload.Credentials["plan_type"])
	require.Equal(t, "free", account.Credentials["plan_type"])
	state := requireAccountTestQuotaState(t, repo, account)
	require.Nil(t, state.Until)
	require.Nil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAI429ActiveAccountDoesNotClearError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_in_seconds":3600}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          78,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	state := requireAccountTestQuotaState(t, repo, account)
	require.NotNil(t, state.Until)
	require.WithinDuration(t, time.Now().Add(time.Hour), *state.Until, 2*time.Second)
	require.Equal(t, StatusActive, account.Status)
	require.Nil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAITransient429UsesShortCooldownDespiteQuotaObservationHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	resp.Header.Set("x-codex-primary-used-percent", "22")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-primary-window-minutes", "300")
	resp.Header.Set("x-codex-secondary-used-percent", "73")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-secondary-window-minutes", "10080")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID: 780, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Concurrency: 1, Credentials: map[string]any{"access_token": "test-token"},
	}

	before := time.Now()
	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, account.ID, repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.WithinDuration(t, before.Add(openAIOAuth429FallbackCooldown), *repo.rateLimitedAt, 2*time.Second)
	require.Less(t, repo.rateLimitedAt.Sub(before), time.Minute)
	require.NotContains(t, repo.persistedExtra[account.ID], "openai_quota_exhausted")
	state := account.QuotaState(time.Now())
	require.NotNil(t, state)
	require.False(t, state.Blocked)
}

func TestAccountTestService_OpenAIHardQuotaWithoutResetPreservesUnknownDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached"}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:           79,
		Platform:     PlatformOpenAI,
		Type:         AccountTypeOAuth,
		Status:       StatusDisabled,
		ErrorMessage: "operator disabled",
		Concurrency:  1,
		Credentials:  map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	state := requireAccountTestQuotaState(t, repo, account)
	require.Nil(t, state.Until)
	require.Nil(t, account.RateLimitResetAt)
	require.Equal(t, StatusDisabled, account.Status)
	require.Equal(t, "operator disabled", account.ErrorMessage)
	require.False(t, account.Schedulable)
}

func TestAccountTestService_OpenAI429PersistenceFailurePreservesState(t *testing.T) {
	ctx, recorder := newTestContext()
	repo := &openAIAccountTestRepo{extraWriteErr: fmt.Errorf("synthetic persistence failure")}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached"}}`),
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID: 785, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusError, ErrorMessage: "operator review required", Schedulable: false,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, account.ID, repo.extraWriteID)
	require.Empty(t, repo.persistedExtra)
	require.Zero(t, repo.rateLimitedID)
	require.Zero(t, repo.clearedErrorID)
	require.Equal(t, StatusError, account.Status)
	require.Equal(t, "operator review required", account.ErrorMessage)
	require.False(t, account.Schedulable)
	state := account.QuotaState(time.Now())
	require.NotNil(t, state)
	require.True(t, state.Blocked, "failed persistence must not discard the observed in-memory restriction")
	require.Nil(t, state.Until)
	require.NotContains(t, recorder.Body.String(), "synthetic persistence failure")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_OpenAI429KeepsNonOAuthScope(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		knownTime bool
	}{
		{"known reset keeps ordinary cooldown", `{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}`, true},
		{"unknown reset does not fabricate a deadline", `{"error":{"type":"usage_limit_reached"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &openAIAccountTestRepo{}
			svc := &AccountTestService{accountRepo: repo}
			account := &Account{ID: 786, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
			svc.reconcileOpenAI429State(t.Context(), account, http.Header{}, []byte(tc.body))
			require.Empty(t, repo.persistedExtra)
			require.NotContains(t, account.Extra, "openai_quota_exhausted")
			require.Nil(t, account.QuotaState(time.Now()))
			require.Zero(t, repo.clearedErrorID)
			if tc.knownTime {
				require.Equal(t, account.ID, repo.rateLimitedID)
				require.NotNil(t, repo.rateLimitedAt)
				require.WithinDuration(t, time.Now().Add(time.Hour), *repo.rateLimitedAt, 2*time.Second)
			} else {
				require.Zero(t, repo.rateLimitedID)
				require.Nil(t, account.RateLimitResetAt)
			}
		})
	}
}

func TestAccountTestService_OpenAI429SparkIgnoresGlobalQuota(t *testing.T) {
	for _, mode := range []string{AccountTestModeDefault, AccountTestModeCompact} {
		t.Run(mode, func(t *testing.T) {
			ctx, _ := newTestContext()
			parentID := int64(800)
			parent := &Account{
				ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				Credentials: map[string]any{"access_token": "parent-token", "plan_type": "plus"},
				Extra:       map[string]any{"parent_marker": "keep"},
			}
			observed := time.Now().UTC().Format(time.RFC3339Nano)
			shadow := &Account{
				ID: 801, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark, Schedulable: true,
				Extra: map[string]any{
					"codex_primary_used_percent": 25.0, "codex_primary_window_minutes": 300,
					"codex_primary_observed_at": observed,
				},
			}
			repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
				accountsByID: map[int64]*Account{parentID: parent, shadow.ID: shadow},
			}}
			resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","plan_type":"free"}}`)
			resp.Header.Set("x-codex-primary-used-percent", "100")
			resp.Header.Set("x-codex-primary-window-minutes", "10080")
			resp.Header.Set("x-codex-primary-reset-after-seconds", "3600")
			upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}

			err := svc.testOpenAIAccountConnection(ctx, shadow, "gpt-5.3-codex-spark", "", mode)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1)
			require.Empty(t, repo.bulkUpdatedIDs)
			require.Zero(t, repo.rateLimitedID)
			require.Zero(t, repo.clearedErrorID)
			require.NotContains(t, repo.persistedExtra, parentID)
			require.NotContains(t, repo.persistedExtra[shadow.ID], "codex_primary_used_percent")
			require.NotContains(t, repo.persistedExtra[shadow.ID], "openai_quota_exhausted")
			require.Equal(t, map[string]any{"parent_marker": "keep"}, parent.Extra)
			require.Equal(t, "plus", parent.Credentials["plan_type"])
			require.Equal(t, 25.0, shadow.Extra["codex_primary_used_percent"])
			require.Equal(t, observed, shadow.Extra["codex_primary_observed_at"])
			require.NotContains(t, shadow.Extra, "openai_quota_exhausted")
			require.False(t, shadow.QuotaState(time.Now()).Blocked)
			require.True(t, shadow.Schedulable)
		})
	}
}

func TestAccountTestService_OpenAI401SetsPermanentErrorOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusUnauthorized, `{"error":"bad token"}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          80,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, account.ID, repo.setErrorID)
	require.Contains(t, repo.setErrorMsg, "Authentication failed (401)")
	require.Zero(t, repo.rateLimitedID)
	require.Zero(t, repo.clearedErrorID)
	require.Nil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAIAPIKeyResponsesUsesCodexProbeHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          95,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example/v1",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, "https://compat-upstream.example/v1/responses", req.URL.String())
	requireOpenAICodexProbeHeaders(t, req.Header)
}

func TestAccountTestService_OpenAIAPIKeyResponsesUnsupportedUsesChatCompletionsPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          91,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example/v1",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "hello", "")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	body := recorder.Body.String()
	require.Contains(t, body, "pong")
	require.Contains(t, body, "已通过 /v1/chat/completions 验证")
	require.Contains(t, body, `"success":true`)
	require.NotContains(t, body, "当前测试接口仅支持 Responses API 路径")
}

func TestAccountTestService_OpenAIChatCompletionsPathReturns4xx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstream := &httpUpstreamRecorder{resp: newJSONResponse(http.StatusBadRequest, `{"error":{"message":"bad request"}}`)}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          92,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Chat Completions API (/v1/chat/completions) returned 400")
	require.Contains(t, recorder.Body.String(), `Chat Completions API (/v1/chat/completions) returned 400: {\"error\":{\"message\":\"bad request\"}}`, "the upstream body is shown verbatim")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_OpenAIChatCompletionsPathTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstream := &httpUpstreamRecorder{err: context.DeadlineExceeded}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          93,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Chat Completions API (/v1/chat/completions) request failed")
	require.Contains(t, err.Error(), context.DeadlineExceeded.Error())
	require.Contains(t, recorder.Body.String(), "/v1/chat/completions")
	require.Contains(t, recorder.Body.String(), context.DeadlineExceeded.Error())
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_OpenAIChatCompletionsPathRejectsNonJSONStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: not-json\n\n")),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          94,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.ErrorIs(t, err, ErrAccountTestProtocol)
	require.Contains(t, recorder.Body.String(), "invalid SSE data: not-json")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}
