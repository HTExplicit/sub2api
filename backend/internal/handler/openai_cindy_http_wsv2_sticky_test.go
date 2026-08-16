package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cindyStickyTestRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r *cindyStickyTestRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	accounts := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform && account.IsSchedulable() {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (r *cindyStickyTestRepo) GetByID(_ context.Context, accountID int64) (*service.Account, error) {
	for _, account := range r.accounts {
		if account.ID == accountID {
			copy := account
			return &copy, nil
		}
	}
	return nil, nil
}

type cindyStickyTestCacheKey struct {
	groupID     int64
	sessionHash string
}

type cindyStickyTestCache struct {
	service.GatewayCache
	mu       sync.Mutex
	sessions map[cindyStickyTestCacheKey]int64
}

func (c *cindyStickyTestCache) GetSessionAccountID(_ context.Context, groupID int64, sessionHash string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[cindyStickyTestCacheKey{groupID: groupID, sessionHash: sessionHash}], nil
}

func (c *cindyStickyTestCache) SetSessionAccountID(_ context.Context, groupID int64, sessionHash string, accountID int64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[cindyStickyTestCacheKey{groupID: groupID, sessionHash: sessionHash}] = accountID
	return nil
}

func (c *cindyStickyTestCache) RefreshSessionTTL(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}

func (c *cindyStickyTestCache) DeleteSessionAccountIDIfMatches(_ context.Context, groupID int64, sessionHash string, expectedAccountID int64) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cindyStickyTestCacheKey{groupID: groupID, sessionHash: sessionHash}
	if c.sessions[key] != expectedAccountID {
		return false, nil
	}
	delete(c.sessions, key)
	return true, nil
}

func (c *cindyStickyTestCache) accountID(sessionHash string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[cindyStickyTestCacheKey{sessionHash: "openai:" + sessionHash}]
}

func newCindyStickyTestGateway(repo service.AccountRepository, cache service.GatewayCache) *service.OpenAIGatewayService {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	return service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
}

func cindyStickyTestAccount(accountID int64) service.Account {
	return service.Account{
		ID:          accountID,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://api.laxarouter.ai",
		},
	}
}

func cindyStickyTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestCindyHTTPToWSV2Handshake502SwitchClearsStickyForReconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accountA := cindyStickyTestAccount(8101)
	accountB := cindyStickyTestAccount(8102)
	repo := &cindyStickyTestRepo{accounts: []service.Account{accountA, accountB}}
	cache := &cindyStickyTestCache{sessions: make(map[cindyStickyTestCacheKey]int64)}
	gateway := newCindyStickyTestGateway(repo, cache)
	handler := &OpenAIGatewayHandler{gatewayService: gateway}
	ctx := context.Background()
	sessionHash := "cindy-first-turn-session"

	require.NoError(t, gateway.BindStickySession(ctx, nil, sessionHash, accountA.ID))
	selectedA, err := gateway.SelectAccountForModelWithExclusions(ctx, nil, sessionHash, "gpt-5.6-luna", nil)
	require.NoError(t, err)
	require.Equal(t, accountA.ID, selectedA.ID)

	handler.clearCindyHTTPToWSV2StickyBeforeAccountSwitch(
		cindyStickyTestContext(), nil, sessionHash, &accountA, &service.UpstreamFailoverError{
			StatusCode:               http.StatusBadGateway,
			Scope:                    service.GatewayFailureScopeAccount,
			CindyHTTPToWSV2FirstTurn: true,
		}, nil,
	)
	require.Zero(t, cache.accountID(sessionHash))

	selectedB, err := gateway.SelectAccountForModelWithExclusions(
		ctx, nil, sessionHash, "gpt-5.6-luna", map[int64]struct{}{accountA.ID: {}},
	)
	require.NoError(t, err)
	require.Equal(t, accountB.ID, selectedB.ID)
	require.Equal(t, accountB.ID, cache.accountID(sessionHash))

	reconnected, err := gateway.SelectAccountForModelWithExclusions(ctx, nil, sessionHash, "gpt-5.6-luna", nil)
	require.NoError(t, err)
	require.Equal(t, accountB.ID, reconnected.ID, "the next client reconnect must not return to failed account A")
}

func TestCindyHTTPToWSV2StickyClearPreservesConcurrentRebind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accountA := cindyStickyTestAccount(8201)
	accountB := cindyStickyTestAccount(8202)
	cache := &cindyStickyTestCache{sessions: make(map[cindyStickyTestCacheKey]int64)}
	gateway := newCindyStickyTestGateway(&cindyStickyTestRepo{accounts: []service.Account{accountA, accountB}}, cache)
	handler := &OpenAIGatewayHandler{gatewayService: gateway}
	sessionHash := "concurrent-rebind-session"

	require.NoError(t, gateway.BindStickySession(context.Background(), nil, sessionHash, accountA.ID))
	require.NoError(t, gateway.BindStickySession(context.Background(), nil, sessionHash, accountB.ID))
	handler.clearCindyHTTPToWSV2StickyBeforeAccountSwitch(
		cindyStickyTestContext(), nil, sessionHash, &accountA, &service.UpstreamFailoverError{
			StatusCode:               http.StatusBadGateway,
			Scope:                    service.GatewayFailureScopeAccount,
			CindyHTTPToWSV2FirstTurn: true,
		}, nil,
	)

	require.Equal(t, accountB.ID, cache.accountID(sessionHash))
}

func TestCindyHTTPToWSV2StickyClearRequiresFirstTurnWithoutSemanticOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accountA := cindyStickyTestAccount(8301)
	cache := &cindyStickyTestCache{sessions: make(map[cindyStickyTestCacheKey]int64)}
	gateway := newCindyStickyTestGateway(&cindyStickyTestRepo{accounts: []service.Account{accountA}}, cache)
	handler := &OpenAIGatewayHandler{gatewayService: gateway}
	sessionHash := "semantic-output-session"
	require.NoError(t, gateway.BindStickySession(context.Background(), nil, sessionHash, accountA.ID))
	c := cindyStickyTestContext()
	_, err := c.Writer.Write([]byte("data: semantic-output\n\n"))
	require.NoError(t, err)

	handler.clearCindyHTTPToWSV2StickyBeforeAccountSwitch(
		c, nil, sessionHash, &accountA, &service.UpstreamFailoverError{
			StatusCode:               http.StatusBadGateway,
			Scope:                    service.GatewayFailureScopeAccount,
			CindyHTTPToWSV2FirstTurn: true,
		}, nil,
	)

	require.Equal(t, accountA.ID, cache.accountID(sessionHash))
}
