package handler

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const managementIsolationRequestCount = 32

type managementIsolationUpstream struct {
	release <-chan struct{}
	entered chan string
	blocked atomic.Int32

	mu    sync.Mutex
	calls map[string]int
}

func (u *managementIsolationUpstream) Do(
	req *http.Request,
	_ string,
	_ int64,
	_ int,
) (*http.Response, error) {
	payload, _ := io.ReadAll(req.Body)
	kind := gjson.GetBytes(payload, "input").String()
	u.mu.Lock()
	u.calls[kind]++
	u.mu.Unlock()

	u.blocked.Add(1)
	defer u.blocked.Add(-1)
	u.entered <- kind
	select {
	case <-u.release:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}

	switch kind {
	case "timeout":
		return nil, context.DeadlineExceeded
	case "429":
		return managementIsolationResponse(http.StatusTooManyRequests, "application/json", `{"error":{"message":"rate limited"}}`), nil
	case "503":
		return managementIsolationResponse(http.StatusServiceUnavailable, "application/json", `{"error":{"message":"capacity unavailable"}}`), nil
	case "stream":
		return managementIsolationResponse(http.StatusOK, "text/event-stream", "event: response.created\n"+
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_isolation\",\"status\":\"in_progress\"}}\n\n"+
			"event: error\n"+
			"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"stream_transport_error\",\"message\":\"stream failed\"}}\n\n"), nil
	default:
		return managementIsolationResponse(http.StatusBadGateway, "application/json", `{"error":{"message":"bad gateway"}}`), nil
	}
}

func (u *managementIsolationUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *managementIsolationUpstream) callSnapshot() map[string]int {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]int, len(u.calls))
	for kind, count := range u.calls {
		out[kind] = count
	}
	return out
}

func managementIsolationResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

type databaseBackedIsolationAccountRepo struct {
	*openAIWSFailoverHandlerAccountRepoStub
	db *sql.DB
}

func (r *databaseBackedIsolationAccountRepo) probe(ctx context.Context) error {
	var one int
	return r.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func (r *databaseBackedIsolationAccountRepo) ListSchedulableByPlatform(
	ctx context.Context,
	platform string,
) ([]service.Account, error) {
	if err := r.probe(ctx); err != nil {
		return nil, err
	}
	return r.openAIWSFailoverHandlerAccountRepoStub.ListSchedulableByPlatform(ctx, platform)
}

func (r *databaseBackedIsolationAccountRepo) ListSchedulableByGroupIDAndPlatform(
	ctx context.Context,
	_ int64,
	platform string,
) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *databaseBackedIsolationAccountRepo) ListSchedulableUngroupedByPlatform(
	ctx context.Context,
	platform string,
) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func newManagementIsolationHandler(
	t *testing.T,
	upstream service.HTTPUpstream,
	repo service.AccountRepository,
) (*OpenAIGatewayHandler, int64) {
	t.Helper()
	groupID := int64(4301)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	gatewaySvc := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCacheSvc, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := NewOpenAIGatewayHandler(
		gatewaySvc, service.NewConcurrencyService(nil), billingCacheSvc,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	return handler, groupID
}

func TestOpenAIBlockedUpstreamsDoNotSerializeManagementPlane(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	mock.MatchExpectationsInOrder(false)
	for range 256 {
		mock.ExpectQuery(`^SELECT 1$`).WillReturnRows(sqlmock.NewRows([]string{"one"}).AddRow(1))
	}

	accounts := []service.Account{
		{
			ID: 9920, Name: "isolation-pool", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 1,
			Credentials: map[string]any{
				"api_key": "test-pool", "base_url": "https://upstream.invalid",
				"pool_mode": true, "pool_mode_retry_count": float64(1),
				"pool_mode_retry_status_codes": []any{float64(http.StatusServiceUnavailable)},
			},
			Extra: map[string]any{"openai_passthrough": true},
		},
		{
			ID: 9921, Name: "isolation-fallback", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 2,
			Credentials: map[string]any{"api_key": "test-fallback", "base_url": "https://upstream.invalid"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
	}
	repo := &databaseBackedIsolationAccountRepo{
		openAIWSFailoverHandlerAccountRepoStub: &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts},
		db:                                     db,
	}
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	upstream := &managementIsolationUpstream{
		release: release,
		entered: make(chan string, managementIsolationRequestCount*3),
		calls:   make(map[string]int),
	}
	handler, groupID := newManagementIsolationHandler(t, upstream, repo)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if c.Request.URL.Path == "/openai/v1/responses" {
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 1901, GroupID: &groupID,
				User:  &service.User{ID: 1801, Status: service.StatusActive},
				Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1801})
		}
		c.Next()
	})
	router.POST("/openai/v1/responses", handler.Responses)
	managementRoute := func(c *gin.Context) {
		var one int
		if queryErr := db.QueryRowContext(c.Request.Context(), "SELECT 1").Scan(&one); queryErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": one == 1})
	}
	for _, path := range []string{
		"/health",
		"/api/v1/admin/accounts",
		"/api/v1/admin/accounts/facets",
		"/api/v1/admin/accounts/folders",
	} {
		router.GET(path, managementRoute)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	transport := &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64, MaxConnsPerHost: 64}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}

	kinds := []string{"timeout", "502", "503", "429", "stream"}
	errCh := make(chan error, managementIsolationRequestCount)
	var requests sync.WaitGroup
	for index := 0; index < managementIsolationRequestCount; index++ {
		kind := kinds[index%len(kinds)]
		stream := kind == "stream"
		body := []byte(`{"model":"gpt-5.2","input":"` + kind + `","stream":` + map[bool]string{true: "true", false: "false"}[stream] + `}`)
		requests.Add(1)
		go func() {
			defer requests.Done()
			resp, requestErr := client.Post(server.URL+"/openai/v1/responses", "application/json", bytes.NewReader(body))
			if requestErr == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				requestErr = resp.Body.Close()
			}
			errCh <- requestErr
		}()
	}

	for index := 0; index < managementIsolationRequestCount; index++ {
		select {
		case <-upstream.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("gateway requests did not reach the blocked upstream concurrently")
		}
	}
	require.Equal(t, int32(managementIsolationRequestCount), upstream.blocked.Load())
	require.Zero(t, db.Stats().InUse, "gateway requests must release database connections before waiting on upstream")
	require.LessOrEqual(t, db.Stats().OpenConnections, 4)

	for _, path := range []string{
		"/health",
		"/api/v1/admin/accounts",
		"/api/v1/admin/accounts/facets",
		"/api/v1/admin/accounts/folders",
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		require.NoError(t, requestErr)
		resp, requestErr := client.Do(req)
		cancel()
		require.NoError(t, requestErr, "%s waited for blocked upstream requests", path)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	}

	close(release)
	released = true
	done := make(chan struct{})
	go func() {
		requests.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("gateway requests did not terminate after the simulated upstream was released")
	}
	close(errCh)
	for requestErr := range errCh {
		require.NoError(t, requestErr)
	}
	require.Zero(t, upstream.blocked.Load(), "no simulated upstream request may leak")
	calls := upstream.callSnapshot()
	totalCalls := 0
	for _, kind := range kinds {
		require.Positive(t, calls[kind], "missing simulated %s outcome", kind)
		totalCalls += calls[kind]
	}
	require.LessOrEqual(t, totalCalls, managementIsolationRequestCount*3, "retries and account switches must remain bounded")
}
