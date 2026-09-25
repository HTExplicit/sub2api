package handler

import (
	"context"
	"fmt"
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
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const wsResponseOwnerFixtureGroupID int64 = 61201

// Every upstream response is synthetic. Native modes use a loopback WS fake;
// HTTP bridge mode terminates in Do without opening an upstream connection.
type wsResponseOwnerUpstream struct {
	service.HTTPUpstream
	mu       sync.Mutex
	requests [][]byte
}

func (u *wsResponseOwnerUpstream) complete(body []byte) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, append([]byte(nil), body...))
	return fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_ws_owner_%d","object":"response","status":"completed","model":"gpt-5.2","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"local owner fixture"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, len(u.requests))
}

func (u *wsResponseOwnerUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	event := u.complete(body)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + event + "\n\n"))}, nil
}

func (u *wsResponseOwnerUpstream) DoWithTLS(request *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(request, proxy, accountID, concurrency)
}

func (u *wsResponseOwnerUpstream) snapshot() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([][]byte(nil), u.requests...)
}

type wsResponseOwnerFixture struct {
	handler  *OpenAIGatewayHandler
	upstream *wsResponseOwnerUpstream
	server   *httptest.Server
	finished chan struct{}
}

func newWSResponseOwnerFixture(t *testing.T, mode string) *wsResponseOwnerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &wsResponseOwnerFixture{upstream: &wsResponseOwnerUpstream{}, finished: make(chan struct{}, 8)}
	baseURL := "https://upstream.invalid"
	if mode != service.OpenAIWSIngressModeHTTPBridge {
		upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
			if err != nil {
				return
			}
			defer func() { _ = conn.CloseNow() }()
			for {
				_, body, readErr := conn.Read(r.Context())
				if readErr != nil {
					return
				}
				if err := conn.Write(r.Context(), coderws.MessageText, []byte(f.upstream.complete(body))); err != nil {
					return
				}
			}
		}))
		t.Cleanup(upstreamServer.Close)
		baseURL = upstreamServer.URL
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	repo := &httpResponseOwnerAccountRepo{&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{{
		ID: 61202, Name: "local-ws-owner-fixture", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{"api_key": "synthetic-key", "base_url": baseURL},
		Extra: map[string]any{
			"openai_passthrough":                            true,
			"openai_apikey_responses_websockets_v2_enabled": true,
			"openai_apikey_responses_websockets_v2_mode":    mode,
		},
	}}}}
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	})
	gateway := service.NewOpenAIGatewayService(
		repo, &openAIWSUsageHandlerUsageLogRepoStub{}, nil, nil, nil, nil, nil, cfg, nil, concurrency,
		service.NewBillingService(cfg, nil), nil, billingCache, f.upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	t.Cleanup(gateway.CloseOpenAIWSPool)
	f.handler = &OpenAIGatewayHandler{
		cfg: cfg, gatewayService: gateway, billingCacheService: billingCache,
		apiKeyService:     &service.APIKeyService{},
		concurrencyHelper: NewConcurrencyHelper(concurrency, SSEPingFormatNone, time.Second),
	}
	group := &service.Group{ID: wsResponseOwnerFixtureGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}
	wireOpenAIWSFixtureGroupReader(t, f.handler, cfg, group)
	router := gin.New()
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		// Fixture-only middleware identities are independent from all payload fields.
		userID, _ := strconv.ParseInt(c.Query("user"), 10, 64)
		keyID, _ := strconv.ParseInt(c.Query("key"), 10, 64)
		groupID := wsResponseOwnerFixtureGroupID
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
			ID: keyID, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
			User: &service.User{ID: userID, Status: service.StatusActive}, Group: group,
		})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 1})
		defer func() { f.finished <- struct{}{} }()
		f.handler.ResponsesWebSocket(c)
	})
	f.server = httptest.NewServer(router)
	t.Cleanup(f.server.Close)
	return f
}

func (f *wsResponseOwnerFixture) connect(t *testing.T, userID, keyID int64) *coderws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("ws%s/openai/v1/responses?user=%d&key=%d", strings.TrimPrefix(f.server.URL, "http"), userID, keyID)
	conn, _, err := coderws.Dial(ctx, endpoint, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func wsResponseOwnerSend(t *testing.T, conn *coderws.Conn, anchor string) {
	t.Helper()
	body := `{"type":"response.create","model":"gpt-5.2","input":"local fixture","stream":true,"store":true,"user_id":101,"api_key_id":1001}`
	if anchor != "" {
		body = fmt.Sprintf(`{"type":"response.create","model":"gpt-5.2","input":"local fixture","stream":true,"store":true,"previous_response_id":%q,"user_id":101,"api_key_id":1001}`, anchor)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(body)))
}

func wsResponseOwnerReadCompleted(t *testing.T, conn *coderws.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, event, err := conn.Read(ctx)
		require.NoError(t, err)
		if gjson.GetBytes(event, "type").String() == "response.completed" {
			return gjson.GetBytes(event, "response.id").String()
		}
	}
}

func (f *wsResponseOwnerFixture) close(t *testing.T, conn *coderws.Conn) {
	t.Helper()
	_ = conn.Close(coderws.StatusNormalClosure, "fixture done")
	select {
	case <-f.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("owner fixture handler did not finish")
	}
}

func wsResponseOwnerAssertRejected(t *testing.T, conn *coderws.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, event, err := conn.Read(ctx)
	require.Error(t, err, "foreign anchor reached a synthetic upstream and produced %s", event)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	require.Equal(t, "previous_response_id is not available for this user", closeErr.Reason)
}

func TestOpenAIResponsesWSOwner_RejectsForeignFirstAndSubsequentFrames(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		for _, subsequent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/subsequent=%t", mode, subsequent), func(t *testing.T) {
				f := newWSResponseOwnerFixture(t, mode)
				owner := f.connect(t, 101, 1001)
				wsResponseOwnerSend(t, owner, "")
				privateID := wsResponseOwnerReadCompleted(t, owner)
				require.Equal(t, "resp_ws_owner_1", privateID)
				f.close(t, owner)

				other := f.connect(t, 202, 2002)
				if subsequent {
					wsResponseOwnerSend(t, other, "")
					require.Equal(t, "resp_ws_owner_2", wsResponseOwnerReadCompleted(t, other))
				}
				before := len(f.upstream.snapshot())
				wsResponseOwnerSend(t, other, privateID)
				wsResponseOwnerAssertRejected(t, other)
				require.Len(t, f.upstream.snapshot(), before, "foreign continuation must produce no additional upstream IO")
				owned, err := f.handler.gatewayService.ValidateOpenAIHTTPResponseOwner(context.Background(), wsResponseOwnerFixtureGroupID, privateID, 101, 1001)
				require.NoError(t, err)
				require.True(t, owned, "a rejected frame must preserve the original response owner")
				foreignOwned, err := f.handler.gatewayService.ValidateOpenAIHTTPResponseOwner(context.Background(), wsResponseOwnerFixtureGroupID, privateID, 202, 2002)
				require.NoError(t, err)
				require.False(t, foreignOwned, "a rejected anchor must not be rebound to the caller")
			})
		}
	}
}

func TestOpenAIResponsesWSOwner_RegistersAndContinuesSameUser(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			f := newWSResponseOwnerFixture(t, mode)
			first := f.connect(t, 202, 2002)
			wsResponseOwnerSend(t, first, "")
			firstID := wsResponseOwnerReadCompleted(t, first)
			f.close(t, first)
			owned, err := f.handler.gatewayService.ValidateOpenAIHTTPResponseOwner(context.Background(), wsResponseOwnerFixtureGroupID, firstID, 202, 2002)
			require.NoError(t, err)
			require.True(t, owned, "real WS completion must register authenticated user, not body user_id")

			// Existing HTTP contract permits another key of the same user.
			next := f.connect(t, 202, 2003)
			wsResponseOwnerSend(t, next, firstID)
			nextID := wsResponseOwnerReadCompleted(t, next)
			wsResponseOwnerSend(t, next, nextID)
			require.Equal(t, "resp_ws_owner_3", wsResponseOwnerReadCompleted(t, next))
			f.close(t, next)
			requests := f.upstream.snapshot()
			require.Len(t, requests, 3)
			require.Equal(t, firstID, gjson.GetBytes(requests[1], "previous_response_id").String())
			require.Equal(t, nextID, gjson.GetBytes(requests[2], "previous_response_id").String())
		})
	}
}
