//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type alwaysBusyAccountConcurrencyCache struct {
	fakeConcurrencyCache
}

type countTokensReacquireBusyCache struct {
	fakeConcurrencyCache
	mu                sync.Mutex
	acquiresByAccount map[int64]int
}

func (c *countTokensReacquireBusyCache) AcquireAccountSlot(_ context.Context, accountID int64, _ int, _ string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.acquiresByAccount == nil {
		c.acquiresByAccount = make(map[int64]int)
	}
	c.acquiresByAccount[accountID]++
	return c.acquiresByAccount[accountID] == 1, nil
}

type countTokensExhaustedUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *countTokensExhaustedUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	call := len(u.accountIDs)
	u.mu.Unlock()

	status := http.StatusOK
	body := `{"input_tokens":5}`
	if call == 1 {
		status = http.StatusUnauthorized
		body = `{"error":{"message":"expired credential"}}`
	} else if call == 2 {
		status = http.StatusServiceUnavailable
		body = `{"error":{"message":"temporary upstream failure"}}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (u *countTokensExhaustedUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func (c *alwaysBusyAccountConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return false, nil
}

func TestAcquireResponsesAccountSlotForSameAccountRetryTimeoutKeepsRequestReplayable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &alwaysBusyAccountConcurrencyCache{}
	h := &OpenAIGatewayHandler{
		gatewayService:    &service.OpenAIGatewayService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, time.Millisecond),
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	streamStarted := false
	selection := &service.AccountSelectionResult{
		Account: &service.Account{
			ID:          42001,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 1,
		},
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      42001,
			MaxConcurrency: 1,
			Timeout:        20 * time.Millisecond,
			MaxWaiting:     1,
		},
	}

	release, result := h.acquireResponsesAccountSlotForSameAccountRetry(
		c, nil, "", selection, true, &streamStarted, zap.NewNop(),
	)

	require.Nil(t, release)
	require.Equal(t, openAISlotAcquireFailed, result)
	require.False(t, streamStarted, "retry reacquisition must not commit a streaming ping")
	require.Zero(t, w.Body.Len(), "retry reacquisition failure must stay replayable for account failover")
	require.False(t, w.Flushed)
}

func TestCountTokensSameAccountReacquireExhaustionWritesOneErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(44002)
	accounts := []service.Account{
		{
			ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0,
			GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "sk-1", "base_url": "https://upstream-1.example/v1"},
		},
		{
			ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
			GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "sk-2", "base_url": "https://upstream-2.example/v1"},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.Scheduling.FallbackWaitTimeout = 5 * time.Millisecond
	cfg.Gateway.Scheduling.FallbackMaxWaiting = 1
	upstream := &countTokensExhaustedUpstream{}
	concurrencyService := service.NewConcurrencyService(&countTokensReacquireBusyCache{})
	gatewayService := service.NewOpenAIGatewayService(
		openAIImagesFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, concurrencyService, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	h := NewOpenAIGatewayHandler(
		gatewayService,
		concurrencyService,
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	h.maxAccountSwitches = 1
	c, recorder := newReplayableEndpointContext(
		t, groupID, http.MethodPost, "/v1/messages/count_tokens",
		[]byte(`{"model":"gpt-5.1","messages":[{"role":"user","content":"hello"}]}`),
	)

	h.CountTokens(c)

	require.Equal(t, []int64{1, 2}, upstream.calls())
	decoder := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes()))
	var envelope map[string]any
	require.NoError(t, decoder.Decode(&envelope))
	require.ErrorIs(t, decoder.Decode(&map[string]any{}), io.EOF, "handler must write exactly one JSON envelope")
}
