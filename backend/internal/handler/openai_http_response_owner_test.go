package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// This fake terminates the real HTTP forwarding path without opening a socket.
// Response IDs are read by the production passthrough response handler and then
// registered through bindHTTPResponseAccount, never by a test-only wrapper.
type httpResponseOwnerUpstream struct {
	service.HTTPUpstream
	mu       sync.Mutex
	requests [][]byte
}

func (u *httpResponseOwnerUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.requests = append(u.requests, body)
	sequence := len(u.requests)
	u.mu.Unlock()
	response := fmt.Sprintf(`{"id":"resp_owner_%d","object":"response","model":"gpt-5.2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"local fixture"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, sequence)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func (u *httpResponseOwnerUpstream) DoWithTLS(request *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(request, proxy, accountID, concurrency)
}

func (u *httpResponseOwnerUpstream) calls() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.requests)
}

type httpResponseOwnerAccountRepo struct {
	*openAIWSFailoverHandlerAccountRepoStub
}

func (r *httpResponseOwnerAccountRepo) ListModelAvailabilityCandidates(ctx context.Context, _ *int64, platforms []string, _ bool) ([]service.Account, error) {
	var accounts []service.Account
	for _, platform := range platforms {
		matching, err := r.ListSchedulableByPlatform(ctx, platform)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, matching...)
	}
	return accounts, nil
}

func newHTTPResponseOwnerHandler(t *testing.T, cache service.GatewayCache) (*OpenAIGatewayHandler, *httpResponseOwnerUpstream) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	repo := &httpResponseOwnerAccountRepo{&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{{
		ID: 61101, Name: "local-owner-fixture", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://upstream.invalid"},
		Extra:       map[string]any{"openai_passthrough": true},
	}}}}
	upstream := &httpResponseOwnerUpstream{}
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	concurrency := service.NewConcurrencyService(nil)
	gateway := service.NewOpenAIGatewayService(
		repo, &openAIWSUsageHandlerUsageLogRepoStub{}, nil, nil, nil, nil, cache, cfg, nil, concurrency,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	return NewOpenAIGatewayHandler(gateway, concurrency, billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg), upstream
}

func runHTTPResponseOwnerRequest(h *OpenAIGatewayHandler, groupID, userID, apiKeyID int64, anchor string) *httptest.ResponseRecorder {
	body := `{"model":"gpt-5.2","input":"hello","stream":false,"user_id":101,"api_key_id":1001,"metadata":{"user_id":101,"api_key_id":1001}}`
	if anchor != "" {
		body = fmt.Sprintf(`{"model":"gpt-5.2","input":"hello","stream":false,"previous_response_id":%q,"user_id":101,"api_key_id":1001,"metadata":{"user_id":101,"api_key_id":1001}}`, anchor)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	// Only middleware-authenticated context is authority. Body fields above
	// deliberately name another tenant in some cases and must have no effect.
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: apiKeyID, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
		User:  &service.User{ID: userID, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 0})
	h.Responses(c)
	return recorder
}

func TestOpenAIResponsesHTTPOwner_RejectsForeignBeforeUpstream(t *testing.T) {
	h, upstream := newHTTPResponseOwnerHandler(t, nil)
	const groupID int64 = 61102
	created := runHTTPResponseOwnerRequest(h, groupID, 101, 1001, "")
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	require.Equal(t, 1, upstream.calls())
	responseID := gjson.GetBytes(created.Body.Bytes(), "id").String()
	require.Equal(t, "resp_owner_1", responseID)

	response := runHTTPResponseOwnerRequest(h, groupID, 202, 2002, responseID)
	require.Equal(t, 1, upstream.calls(), "foreign continuation must add no fake upstream IO; response status=%d", response.Code)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Equal(t, "invalid_request_error", gjson.GetBytes(response.Body.Bytes(), "error.type").String())
	require.Equal(t, "previous_response_id is not available for this user", gjson.GetBytes(response.Body.Bytes(), "error.message").String())
}

func TestOpenAIResponsesHTTPOwner_RegistersAndContinuesSameUser(t *testing.T) {
	h, upstream := newHTTPResponseOwnerHandler(t, nil)
	const groupID int64 = 61103
	first := runHTTPResponseOwnerRequest(h, groupID, 202, 2002, "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Equal(t, 1, upstream.calls())
	firstID := gjson.GetBytes(first.Body.Bytes(), "id").String()
	require.Equal(t, "resp_owner_1", firstID)
	owned, err := h.gatewayService.ValidateOpenAIHTTPResponseOwner(context.Background(), groupID, firstID, 202, 2002)
	require.NoError(t, err)
	require.True(t, owned, "the real successful response path must record the authenticated owner")
	spoofed, err := h.gatewayService.ValidateOpenAIHTTPResponseOwner(context.Background(), groupID, firstID, 101, 1001)
	require.NoError(t, err)
	require.False(t, spoofed, "body user_id/api_key_id must not own the new response")

	// The owner is the downstream user, not one API key. This second real
	// handler call proves authorization, forwarding and the next registration.
	next := runHTTPResponseOwnerRequest(h, groupID, 202, 2003, firstID)
	require.Equal(t, http.StatusOK, next.Code, next.Body.String())
	require.Equal(t, 2, upstream.calls())
	nextID := gjson.GetBytes(next.Body.Bytes(), "id").String()
	require.Equal(t, "resp_owner_2", nextID)
	owned, err = h.gatewayService.ValidateOpenAIHTTPResponseOwner(context.Background(), groupID, nextID, 202, 2003)
	require.NoError(t, err)
	require.True(t, owned)
	foreign := runHTTPResponseOwnerRequest(h, groupID, 303, 3003, nextID)
	require.Equal(t, http.StatusBadRequest, foreign.Code, foreign.Body.String())
	require.Equal(t, 2, upstream.calls(), "a newly registered response is protected on the next HTTP request")
}

type httpResponseOwnerLookupFailure struct {
	service.GatewayCache
	reads int
}

func (c *httpResponseOwnerLookupFailure) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	c.reads++
	return 0, errors.New("synthetic owner store unavailable")
}

func TestOpenAIResponsesHTTPOwner_UnknownGroupAndStoreFailureStayClosed(t *testing.T) {
	for _, name := range []string{"unknown_owner", "different_group", "owner_store_error"} {
		t.Run(name, func(t *testing.T) {
			var cache service.GatewayCache
			var failedCache *httpResponseOwnerLookupFailure
			if name == "owner_store_error" {
				failedCache = &httpResponseOwnerLookupFailure{}
				cache = failedCache
			}
			h, upstream := newHTTPResponseOwnerHandler(t, cache)
			const groupID int64 = 61104
			if name == "different_group" {
				require.NoError(t, h.gatewayService.BindOpenAIHTTPResponseOwner(context.Background(), groupID+1, "resp_scoped_owner", 202, 2002))
			}
			response := runHTTPResponseOwnerRequest(h, groupID, 202, 2002, "resp_scoped_owner")
			require.Zero(t, upstream.calls())
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Equal(t, "invalid_request_error", gjson.GetBytes(response.Body.Bytes(), "error.type").String())
			require.Equal(t, "previous_response_id is not available for this user", gjson.GetBytes(response.Body.Bytes(), "error.message").String())
			require.NotContains(t, response.Body.String(), "synthetic owner store unavailable")
			if failedCache != nil {
				require.Equal(t, 1, failedCache.reads, "failure must stop after the owner lookup, before routing cache reads")
			}
		})
	}
}
