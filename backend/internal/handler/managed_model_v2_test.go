package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestManagedModelV2MiddlewarePreservesUnmanagedAndLegacyHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		request *service.ManagedModelRequest
	}{
		{"private", nil},
		{"v1 publication", &service.ManagedModelRequest{Version: 1}},
		{"retained legacy publication", &service.ManagedModelRequest{Version: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				if tc.request != nil {
					c.Request = c.Request.WithContext(service.WithManagedModelRequest(c.Request.Context(), tc.request))
				}
			})
			engine.Use((&GatewayHandler{}).ManagedModelV2(nil))
			called := 0
			engine.POST("/v1/responses", func(c *gin.Context) { called++; c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Equal(t, http.StatusNoContent, w.Code)
			require.Equal(t, 1, called)
		})
	}
}

func TestManagedModelV2MetadataCannotBorrowGenerativeEvidence(t *testing.T) {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(service.WithManagedModelRequest(c.Request.Context(), &service.ManagedModelRequest{
			Version: 2, Endpoint: service.CompositeRouteEndpointCountTokens,
			Route: service.ManagedModelRoute{Branches: []service.ManagedModelRouteBranch{{Selector: "internal"}}},
		}))
	})
	engine.Use((&GatewayHandler{}).ManagedModelV2(nil))
	called := false
	engine.POST("/v1/messages/count_tokens", func(c *gin.Context) { called = true })
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.False(t, called)
}

func TestManagedModelV2MayReplayOnlyBeforeOutputAndWithoutPinnedState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		pin       *managedModelV2Pin
		committed bool
		canceled  bool
		stop      bool
		heartbeat bool
		want      bool
	}{
		{name: "fresh temporary failure", want: true},
		{name: "content prevents cross branch", body: `data: {"type":"content_block_delta"}`},
		{name: "explicit semantic commit", committed: true},
		{name: "opaque continuation pinned", pin: &managedModelV2Pin{AccountID: 1, BranchSelector: "a"}},
		{name: "request canceled", canceled: true},
		{name: "request rejection", stop: true},
		{name: "only an identified heartbeat", body: ": ping\n\n", heartbeat: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			before := c.Writer.Size()
			if tc.body != "" {
				n, err := c.Writer.WriteString(tc.body)
				require.NoError(t, err)
				if tc.heartbeat {
					recordGatewayStreamHeartbeat(c, n)
				}
			}
			if tc.committed {
				service.MarkResponseCommitted(c)
			}
			if tc.canceled {
				cancel()
			}
			err := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, SafeToFailoverAfterWrite: tc.heartbeat}
			if tc.stop {
				err.NextAccountAction = service.NextAccountStop
			}
			require.Equal(t, tc.want, managedModelV2MayReplay(c, before, tc.pin, err))
		})
	}
}

func TestManagedModelV2RetryBudgetDoesNotGrowWithBranches(t *testing.T) {
	state := newManagedModelV2RetryState(1)
	account := &service.Account{ID: 7, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": false}}
	first := service.ManagedModelCandidate{Account: account, Branch: service.ManagedModelRouteBranch{Selector: "a", TargetPlatform: service.PlatformOpenAI}}
	second := service.ManagedModelCandidate{Account: account, Branch: service.ManagedModelRouteBranch{Selector: "b", TargetPlatform: service.PlatformAnthropic}}
	err := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	require.Equal(t, openAIFailoverRetrySwitchAccount, state.next(context.Background(), first, err))
	require.Equal(t, openAIFailoverRetryStop, state.next(context.Background(), second, err))
	require.NotEqual(t, first.Key(), second.Key(), "a failed real target must not erase another target on the same account")
}

func TestManagedModelV2SameAccountRetryKeepsExactBranchAndHonorsPolicy(t *testing.T) {
	state := newManagedModelV2RetryState(1)
	account := &service.Account{ID: 7, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true, "pool_mode_retry_count": float64(1), "pool_mode_retry_status_codes": []any{float64(http.StatusServiceUnavailable)}}}
	candidate := service.ManagedModelCandidate{Account: account, Branch: service.ManagedModelRouteBranch{Selector: "a", TargetPlatform: service.PlatformOpenAI}}
	err := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true, SameAccountRetryDelay: time.Nanosecond}
	require.Equal(t, openAIFailoverRetrySameAccount, state.next(context.Background(), candidate, err))
	require.Equal(t, openAIFailoverRetrySwitchAccount, state.next(context.Background(), candidate, err))
	require.Equal(t, openAIFailoverRetryStop, state.next(context.Background(), candidate, err))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Equal(t, openAIFailoverRetryCanceled, state.next(ctx, candidate, err))
}

func TestManagedModelV2HeaderRestoreDoesNotLeakFailedBranch(t *testing.T) {
	original := http.Header{"X-Request-Id": {"request"}}
	current := http.Header{"Content-Encoding": {"gzip"}, "X-Upstream": {"discard"}, "X-Request-Id": {"stale"}}
	restoreManagedModelV2Headers(current, original)
	require.Equal(t, original, current)
	current["X-Request-Id"][0] = "changed"
	require.Equal(t, "request", original.Get("X-Request-Id"))
}

func TestManagedModelV2RoutingFieldsRejectParserDisagreement(t *testing.T) {
	for _, body := range []string{
		`{"model":"public","model":"private"}`,
		`{"model":"public","Model":"private"}`,
		`{"model":"public","stream":false,"stream":true}`,
		`{"model":"public","Stream":true}`,
		`{"model":"private","stream":false}`,
		`[{"model":"public"}]`,
	} {
		require.False(t, managedModelV2RoutingFieldsValid([]byte(body), "public"), body)
	}
	require.True(t, managedModelV2RoutingFieldsValid([]byte(`{"model":"public","stream":false,"input":"hello"}`), "public"))
}
