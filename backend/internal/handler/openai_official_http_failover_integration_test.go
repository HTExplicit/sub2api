//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIOfficialHTTPGroupID int64 = 45101

// Reuse the mutable account repository and sticky cache from the existing real
// handler failover tests. Only upstream I/O and persistence are in-memory: the
// handler, retry policy, selector, forwarding code and rate-limit service run.
type openAIOfficialHTTPAccountRepo struct {
	grokCredentialHandlerRepo
}

func (r *openAIOfficialHTTPAccountRepo) ListByGroup(context.Context, int64) ([]service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]service.Account(nil), r.accounts...), nil
}

func (r *openAIOfficialHTTPAccountRepo) makeUnavailable(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].Schedulable = false
		}
	}
}

type openAIOfficialHTTPStickyCache struct {
	grokCredentialHandlerGatewayCache
	lookupMu sync.Mutex
	lookups  []grokCredentialHandlerGatewayCacheKey
}

func (c *openAIOfficialHTTPStickyCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	c.lookupMu.Lock()
	c.lookups = append(c.lookups, grokCredentialHandlerGatewayCacheKey{groupID: groupID, sessionHash: sessionHash})
	c.lookupMu.Unlock()
	return c.grokCredentialHandlerGatewayCache.GetSessionAccountID(ctx, groupID, sessionHash)
}

func (c *openAIOfficialHTTPStickyCache) assertStableSession(t *testing.T) {
	t.Helper()
	c.lookupMu.Lock()
	defer c.lookupMu.Unlock()
	require.GreaterOrEqual(t, len(c.lookups), 2, "retry must re-enter session-aware selection")
	require.NotEmpty(t, c.lookups[0].sessionHash)
	for _, lookup := range c.lookups[1:] {
		require.Equal(t, c.lookups[0], lookup, "reselection must retain the original group and session")
	}
}

type openAIOfficialHTTPAttempt struct {
	accountID int64
	path      string
	body      []byte
}

type openAIOfficialHTTPUpstream struct {
	service.HTTPUpstream
	attempts []openAIOfficialHTTPAttempt
	reply    func(openAIOfficialHTTPAttempt, int) (*http.Response, error)
}

func (u *openAIOfficialHTTPUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	attempt := openAIOfficialHTTPAttempt{accountID: accountID, path: req.URL.Path, body: body}
	u.attempts = append(u.attempts, attempt)
	return u.reply(attempt, len(u.attempts))
}

func (u *openAIOfficialHTTPUpstream) accountIDs() []int64 {
	ids := make([]int64, len(u.attempts))
	for i := range u.attempts {
		ids[i] = u.attempts[i].accountID
	}
	return ids
}

func openAIOfficialHTTPResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func openAIOfficialHTTPUnavailableResponse() *http.Response {
	return openAIOfficialHTTPResponse(http.StatusServiceUnavailable, "application/json",
		`{"error":{"type":"server_error","message":"temporary upstream failure"}}`)
}

func openAIOfficialHTTPSuccess(attempt openAIOfficialHTTPAttempt) *http.Response {
	if strings.HasSuffix(attempt.path, "/chat/completions") {
		return openAIOfficialHTTPResponse(http.StatusOK, "application/json",
			`{"id":"chatcmpl_official_http_healthy","object":"chat.completion","model":"gpt-5.1","choices":[{"index":0,"message":{"role":"assistant","content":"healthy answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}
	body := `{"id":"resp_official_http_healthy","object":"response","model":"gpt-5.1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"healthy answer"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	if gjson.GetBytes(attempt.body, "stream").Bool() {
		return openAIOfficialHTTPResponse(http.StatusOK, "text/event-stream",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":"+body+"}\n\n")
	}
	return openAIOfficialHTTPResponse(http.StatusOK, "application/json", body)
}

type openAIOfficialHTTPEndpoint struct {
	name        string
	chat        bool
	rawChat     bool
	passthrough bool
}

func openAIOfficialHTTPPrimaryEndpoints() []openAIOfficialHTTPEndpoint {
	return []openAIOfficialHTTPEndpoint{
		{name: "responses"},
		{name: "chat_completions", chat: true, rawChat: true},
	}
}

func (e openAIOfficialHTTPEndpoint) requestBody() []byte {
	if e.chat {
		return []byte(`{"model":"gpt-5.1","messages":[{"role":"user","content":"keep this request"}],"stream":false,"metadata":{"fixture":"retain-me"}}`)
	}
	return []byte(`{"model":"gpt-5.1","input":"keep this request","stream":false,"metadata":{"fixture":"retain-me"}}`)
}

func (e openAIOfficialHTTPEndpoint) context(t *testing.T, ctx context.Context, stream bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	path := "/v1/responses"
	if e.chat {
		path = "/v1/chat/completions"
	}
	body := e.requestBody()
	if stream {
		body = []byte(strings.Replace(string(body), `"stream":false`, `"stream":true`, 1))
	}
	c, recorder := newReplayableEndpointContext(t, openAIOfficialHTTPGroupID, http.MethodPost, path, body)
	if ctx != nil {
		c.Request = c.Request.WithContext(ctx)
	}
	c.Request.Header.Set("session_id", "official-http-stable-session")
	return c, recorder
}

func (e openAIOfficialHTTPEndpoint) serve(h *OpenAIGatewayHandler, c *gin.Context) {
	if e.chat {
		h.ChatCompletions(c)
		return
	}
	h.Responses(c)
}

func newOpenAIOfficialHTTPHandler(t *testing.T, endpoint openAIOfficialHTTPEndpoint, upstream *openAIOfficialHTTPUpstream, retryCount *int, includeFallback bool) (*OpenAIGatewayHandler, *openAIOfficialHTTPAccountRepo, *openAIOfficialHTTPStickyCache) {
	t.Helper()
	accounts := []service.Account{
		{
			ID: 45111, Name: "official-http-a", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: 0, GroupIDs: []int64{openAIOfficialHTTPGroupID},
			Credentials: map[string]any{
				"api_key": "fixture-key-a", "base_url": "https://upstream-a.example.test",
				"pool_mode": true, "pool_mode_retry_status_codes": []any{float64(http.StatusServiceUnavailable)},
			},
		},
	}
	if retryCount != nil {
		accounts[0].Credentials["pool_mode_retry_count"] = *retryCount
	}
	if includeFallback {
		accounts = append(accounts, service.Account{
			ID: 45112, Name: "official-http-b", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: 1, GroupIDs: []int64{openAIOfficialHTTPGroupID},
			Credentials: map[string]any{"api_key": "fixture-key-b", "base_url": "https://upstream-b.example.test"},
		})
	}
	for i := range accounts {
		accounts[i].Extra = map[string]any{"openai_passthrough": endpoint.passthrough}
		if endpoint.rawChat {
			accounts[i].Extra["openai_responses_supported"] = false
		}
	}
	repo := &openAIOfficialHTTPAccountRepo{grokCredentialHandlerRepo: grokCredentialHandlerRepo{accounts: accounts}}
	cache := &openAIOfficialHTTPStickyCache{grokCredentialHandlerGatewayCache: grokCredentialHandlerGatewayCache{
		sessions: make(map[grokCredentialHandlerGatewayCacheKey]int64),
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 1
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, cache, cfg, nil, nil,
		service.NewBillingService(cfg, nil), service.NewRateLimitService(repo, nil, cfg, nil, nil), billing, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	return h, repo, cache
}

func TestOpenAIOfficialHTTPRetryReselectsAvailableAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoints := append(openAIOfficialHTTPPrimaryEndpoints(),
		openAIOfficialHTTPEndpoint{name: "responses_passthrough", passthrough: true},
		openAIOfficialHTTPEndpoint{name: "responses_via_chat", rawChat: true},
		openAIOfficialHTTPEndpoint{name: "chat_via_responses", chat: true},
	)
	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			upstream := &openAIOfficialHTTPUpstream{}
			h, repo, cache := newOpenAIOfficialHTTPHandler(t, endpoint, upstream, nil, true)
			upstream.reply = func(attempt openAIOfficialHTTPAttempt, _ int) (*http.Response, error) {
				if attempt.accountID == 45111 {
					repo.makeUnavailable(attempt.accountID)
					return openAIOfficialHTTPUnavailableResponse(), nil
				}
				return openAIOfficialHTTPSuccess(attempt), nil
			}
			c, recorder := endpoint.context(t, nil, false)

			endpoint.serve(h, c)

			require.Equal(t, []int64{45111, 45112}, upstream.accountIDs(), "an approved retry must reselect after A becomes unavailable")
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), "healthy answer")
			require.GreaterOrEqual(t, repo.selectorCalls(), 2, "must use the normal selector, not exact-account reacquisition")
			require.JSONEq(t, string(upstream.attempts[0].body), string(upstream.attempts[1].body), "account change must not mutate the request")
			require.Equal(t, "gpt-5.1", gjson.GetBytes(upstream.attempts[1].body, "model").String())
			require.Contains(t, string(upstream.attempts[1].body), "keep this request")
			require.Equal(t, "official-http-stable-session", c.GetHeader("session_id"))
			cache.assertStableSession(t)
		})
	}
}

func TestOpenAIOfficialHTTPRetryBudgetSurvivesReselection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	zero := 0
	for _, endpoint := range openAIOfficialHTTPPrimaryEndpoints() {
		for _, budget := range []struct {
			name       string
			retryCount *int
			want       []int64
		}{
			{name: "default_three", want: []int64{45111, 45111, 45111, 45111, 45112}},
			{name: "explicit_zero", retryCount: &zero, want: []int64{45111, 45112}},
		} {
			t.Run(endpoint.name+"/"+budget.name, func(t *testing.T) {
				upstream := &openAIOfficialHTTPUpstream{reply: func(attempt openAIOfficialHTTPAttempt, _ int) (*http.Response, error) {
					if attempt.accountID == 45111 {
						return openAIOfficialHTTPUnavailableResponse(), nil
					}
					return openAIOfficialHTTPSuccess(attempt), nil
				}}
				h, repo, _ := newOpenAIOfficialHTTPHandler(t, endpoint, upstream, budget.retryCount, true)
				c, recorder := endpoint.context(t, nil, false)

				endpoint.serve(h, c)

				require.Equal(t, budget.want, upstream.accountIDs(), "ordinary selector re-entry must not reset the account's request-local retry budget")
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.GreaterOrEqual(t, repo.selectorCalls(), len(budget.want))
			})
		}
	}
}

func TestOpenAIOfficialHTTPNativeResponsesTransientProcessingRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := openAIOfficialHTTPEndpoint{name: "responses"}
	upstream := &openAIOfficialHTTPUpstream{reply: func(attempt openAIOfficialHTTPAttempt, call int) (*http.Response, error) {
		if call == 1 {
			return openAIOfficialHTTPResponse(http.StatusBadRequest, "application/json",
				`{"error":{"type":"server_error","message":"An error occurred while processing your request. You can retry your request."}}`), nil
		}
		return openAIOfficialHTTPSuccess(attempt), nil
	}}
	h, repo, _ := newOpenAIOfficialHTTPHandler(t, endpoint, upstream, nil, true)
	c, recorder := endpoint.context(t, nil, false)

	endpoint.serve(h, c)

	// The native Responses service recognizes this exact transient signal even
	// though 400 is not in the account's pool_mode_retry_status_codes. The
	// official passthrough route intentionally has a different retry predicate.
	require.Equal(t, []int64{45111, 45111}, upstream.accountIDs())
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.GreaterOrEqual(t, repo.selectorCalls(), 2)
}

func TestOpenAIOfficialHTTPTransportFailureDoesNotReplaySameAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range openAIOfficialHTTPPrimaryEndpoints() {
		for _, fallback := range []bool{true, false} {
			name := "no_candidate"
			if fallback {
				name = "healthy_candidate"
			}
			t.Run(endpoint.name+"/"+name, func(t *testing.T) {
				upstream := &openAIOfficialHTTPUpstream{reply: func(attempt openAIOfficialHTTPAttempt, _ int) (*http.Response, error) {
					if attempt.accountID == 45111 {
						return nil, errors.New("http2: client connection lost")
					}
					return openAIOfficialHTTPSuccess(attempt), nil
				}}
				h, repo, _ := newOpenAIOfficialHTTPHandler(t, endpoint, upstream, nil, fallback)
				c, recorder := endpoint.context(t, nil, false)

				endpoint.serve(h, c)

				wantIDs := []int64{45111}
				wantStatus := http.StatusBadGateway
				if fallback {
					wantIDs = append(wantIDs, 45112)
					wantStatus = http.StatusOK
				}
				require.Equal(t, wantIDs, upstream.accountIDs(), "H2 transport failure must make one attempt per account")
				require.Equal(t, wantStatus, recorder.Code, recorder.Body.String())
				require.Empty(t, repo.setTempIDs, "transient transport errors must not persist an account cooldown")
				require.Empty(t, repo.errorIDs())
			})
		}
	}
}

func TestOpenAIOfficialHTTPClientCancellationStopsReselection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range openAIOfficialHTTPPrimaryEndpoints() {
		t.Run(endpoint.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := &openAIOfficialHTTPUpstream{reply: func(openAIOfficialHTTPAttempt, int) (*http.Response, error) {
				cancel()
				return openAIOfficialHTTPUnavailableResponse(), nil
			}}
			h, repo, _ := newOpenAIOfficialHTTPHandler(t, endpoint, upstream, nil, true)
			c, recorder := endpoint.context(t, ctx, false)

			endpoint.serve(h, c)

			require.Equal(t, []int64{45111}, upstream.accountIDs())
			require.Equal(t, 1, repo.selectorCalls())
			require.Equal(t, statusClientClosedRequest, c.Writer.Status())
			require.Empty(t, recorder.Body.String(), "a disconnected client must not receive an account-exhausted envelope")
		})
	}
}

func TestOpenAIOfficialHTTPSemanticOutputStopsReselection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range openAIOfficialHTTPPrimaryEndpoints() {
		t.Run(endpoint.name, func(t *testing.T) {
			upstream := &openAIOfficialHTTPUpstream{reply: func(attempt openAIOfficialHTTPAttempt, _ int) (*http.Response, error) {
				body := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_partial\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial answer\"}\n\n"
				if strings.HasSuffix(attempt.path, "/chat/completions") {
					body = "data: {\"id\":\"chatcmpl_partial\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial answer\"},\"finish_reason\":null}]}\n\n"
				}
				// EOF without a complete terminal must remain a failure, but the
				// semantic delta makes replay onto another account unsafe.
				return openAIOfficialHTTPResponse(http.StatusOK, "text/event-stream", body), nil
			}}
			h, repo, _ := newOpenAIOfficialHTTPHandler(t, endpoint, upstream, nil, true)
			c, recorder := endpoint.context(t, nil, true)

			endpoint.serve(h, c)

			require.Equal(t, []int64{45111}, upstream.accountIDs())
			require.Equal(t, 1, repo.selectorCalls())
			require.Contains(t, recorder.Body.String(), "partial answer")
			require.NotContains(t, recorder.Body.String(), `"type":"response.completed"`, "a missing terminal must not be rewritten into success")
			require.Contains(t, recorder.Body.String(), "error", "the partial stream must keep an explicit failure outcome")
		})
	}
}
