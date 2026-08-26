//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type runtimeBreakerTestEntry struct {
	blockUntil time.Time
	reason     string
	owner      string
}

type runtimeBreakerTestCache struct {
	stubGatewayCache
	mu            sync.Mutex
	entries       map[string]runtimeBreakerTestEntry
	renewCalls    int
	renewFailures int
	releaseCalls  int
}

func runtimeBreakerTestKey(accountID int64, model string) string {
	return fmt.Sprintf("%d:%s", accountID, normalizeOpenAIAccountModelTransientModel(model))
}

func (c *runtimeBreakerTestCache) BlockOpenAIRuntimeBreaker(_ context.Context, accountID int64, model, reason string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]runtimeBreakerTestEntry)
	}
	key := runtimeBreakerTestKey(accountID, model)
	entry := c.entries[key]
	requestedUntil := time.Now().Add(ttl)
	if entry.blockUntil.Before(requestedUntil) {
		entry.blockUntil = requestedUntil
		entry.reason = reason
	}
	entry.owner = ""
	c.entries[key] = entry
	return nil
}

func (c *runtimeBreakerTestCache) AllowOpenAIRuntimeBreakerProbe(_ context.Context, accountID int64, model, owner string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[runtimeBreakerTestKey(accountID, model)]
	if !ok {
		return true, nil
	}
	if time.Now().Before(entry.blockUntil) {
		return false, nil
	}
	if entry.owner == "" {
		entry.owner = owner
		c.entries[runtimeBreakerTestKey(accountID, model)] = entry
		return true, nil
	}
	return entry.owner == owner, nil
}

func (c *runtimeBreakerTestCache) AllowOpenAIRuntimeBreakerProbes(_ context.Context, accountID int64, models []string, owner string, _ time.Duration) (bool, []string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for _, model := range models {
		entry, ok := c.entries[runtimeBreakerTestKey(accountID, model)]
		if !ok {
			continue
		}
		if now.Before(entry.blockUntil) || (entry.owner != "" && entry.owner != owner) {
			return false, nil, nil
		}
	}
	claimed := make([]string, 0, len(models))
	for _, model := range models {
		key := runtimeBreakerTestKey(accountID, model)
		entry, ok := c.entries[key]
		if !ok {
			continue
		}
		entry.owner = owner
		c.entries[key] = entry
		claimed = append(claimed, model)
	}
	return true, claimed, nil
}

func (c *runtimeBreakerTestCache) RenewOpenAIRuntimeBreakerProbes(_ context.Context, accountID int64, models []string, owner string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewCalls++
	if c.renewFailures > 0 {
		c.renewFailures--
		return false, nil
	}
	for _, model := range models {
		entry, ok := c.entries[runtimeBreakerTestKey(accountID, model)]
		if !ok || entry.owner != owner {
			return false, nil
		}
	}
	return true, nil
}

func TestOpenAIRuntimeBreaker_FailedPromotionDoesNotReturnSelection(t *testing.T) {
	cache := &runtimeBreakerTestCache{
		entries: map[string]runtimeBreakerTestEntry{
			runtimeBreakerTestKey(4718, ""):        {blockUntil: time.Now().Add(-time.Second)},
			runtimeBreakerTestKey(4718, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
		},
		renewFailures: 1,
	}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4718, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	released := false
	ctx := withOpenAIRuntimeBreakerProbeOwner(context.Background(), "promotion-owner")

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"))
	selection := attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{
		Account:     account,
		Acquired:    true,
		ReleaseFunc: func() { released = true },
	})

	require.Nil(t, selection)
	require.True(t, released, "failed lease promotion must release the acquired account slot")
	cache.mu.Lock()
	accountOwner := cache.entries[runtimeBreakerTestKey(account.ID, "")].owner
	modelOwner := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")].owner
	cache.mu.Unlock()
	require.Empty(t, accountOwner)
	require.Empty(t, modelOwner)
}

func (c *runtimeBreakerTestCache) ReleaseOpenAIRuntimeBreakerProbes(_ context.Context, accountID int64, models []string, owner string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releaseCalls++
	for _, model := range models {
		entry, ok := c.entries[runtimeBreakerTestKey(accountID, model)]
		if ok && entry.owner != "" && entry.owner != owner {
			return false, nil
		}
	}
	released := false
	for _, model := range models {
		key := runtimeBreakerTestKey(accountID, model)
		entry, ok := c.entries[key]
		if ok && entry.owner == owner {
			entry.owner = ""
			c.entries[key] = entry
			released = true
		}
	}
	return released, nil
}

func (c *runtimeBreakerTestCache) ClearOpenAIRuntimeBreaker(_ context.Context, accountID int64, model, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := runtimeBreakerTestKey(accountID, model)
	entry, ok := c.entries[key]
	if ok && !time.Now().Before(entry.blockUntil) && entry.owner == owner {
		delete(c.entries, key)
	}
	return nil
}

func (c *runtimeBreakerTestCache) ClearAllOpenAIRuntimeBreakers(_ context.Context, accountID int64) error {
	c.mu.Lock()
	for key := range c.entries {
		if strings.HasPrefix(key, fmt.Sprintf("%d:", accountID)) {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	return nil
}

type pausedClearRuntimeBreakerTestCache struct {
	*runtimeBreakerTestCache
	started chan struct{}
	proceed chan struct{}
}

func (c *pausedClearRuntimeBreakerTestCache) ClearAllOpenAIRuntimeBreakers(ctx context.Context, accountID int64) error {
	close(c.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.proceed:
		return c.runtimeBreakerTestCache.ClearAllOpenAIRuntimeBreakers(ctx, accountID)
	}
}

func TestOpenAIRuntimeBreaker_ClearDoesNotDeleteConcurrentNewBlock(t *testing.T) {
	baseCache := &runtimeBreakerTestCache{}
	cache := &pausedClearRuntimeBreakerTestCache{
		runtimeBreakerTestCache: baseCache,
		started:                 make(chan struct{}),
		proceed:                 make(chan struct{}),
	}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4720, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "old")

	clearDone := make(chan struct{})
	go func() {
		svc.ClearAccountSchedulingBlock(account.ID)
		close(clearDone)
	}()
	select {
	case <-cache.started:
	case <-time.After(time.Second):
		close(cache.proceed)
		t.Fatal("clear did not reach the Redis boundary")
	}

	blockDone := make(chan struct{})
	go func() {
		svc.BlockAccountScheduling(account, time.Now().Add(2*time.Minute), "new")
		close(blockDone)
	}()
	select {
	case <-blockDone:
		close(cache.proceed)
		t.Fatal("new block must wait until clear has completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(cache.proceed)

	select {
	case <-clearDone:
	case <-time.After(time.Second):
		t.Fatal("clear did not finish")
	}
	select {
	case <-blockDone:
	case <-time.After(time.Second):
		t.Fatal("new block did not finish")
	}

	baseCache.mu.Lock()
	entry := baseCache.entries[runtimeBreakerTestKey(account.ID, "")]
	baseCache.mu.Unlock()
	require.Equal(t, "new", entry.reason)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBreaker_PersistsAcrossServiceInstancesAndClaimsHalfOpen(t *testing.T) {
	cache := &runtimeBreakerTestCache{}
	account := &Account{ID: 4705, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	first := &OpenAIGatewayService{cache: cache}
	first.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-5.4", &UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable,
	})

	cache.mu.Lock()
	entry := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")]
	cache.mu.Unlock()
	require.Equal(t, "retry_exhausted_transient", entry.reason)
	require.True(t, entry.blockUntil.After(time.Now()))

	second := &OpenAIGatewayService{cache: cache}
	require.True(t, second.isOpenAIAccountRequestRuntimeBlockedContext(
		withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-2"), account, "gpt-5.4",
	))

	cache.mu.Lock()
	entry = cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")]
	entry.blockUntil = time.Now().Add(-time.Second)
	cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")] = entry
	cache.mu.Unlock()

	owner1 := withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1")
	owner2 := withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-2")
	require.False(t, second.isOpenAIAccountRequestRuntimeBlockedContext(owner1, account, "gpt-5.4"))
	require.True(t, (&OpenAIGatewayService{cache: cache}).isOpenAIAccountRequestRuntimeBlockedContext(owner2, account, "gpt-5.4"))
	require.False(t, second.isOpenAIAccountRequestRuntimeBlockedContext(owner1, account, "gpt-5.4"))

	second.ReportOpenAIAccountScheduleResultForSelection(
		&AccountSelectionResult{runtimeBreakerProbeOwner: "stale-owner"},
		account.ID,
		"gpt-5.4",
		true,
		nil,
	)
	require.True(t, (&OpenAIGatewayService{cache: cache}).isOpenAIAccountRequestRuntimeBlockedContext(owner2, account, "gpt-5.4"))
	second.ReportOpenAIAccountScheduleResultForSelection(
		&AccountSelectionResult{runtimeBreakerProbeOwner: "owner-1"},
		account.ID,
		"gpt-5.4",
		true,
		nil,
	)
	owner3 := withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-3")
	require.False(t, (&OpenAIGatewayService{cache: cache}).isOpenAIAccountRequestRuntimeBlockedContext(owner3, account, "gpt-5.4"))
}

func TestOpenAIRuntimeBreaker_RechecksRedisAfterAnAllowedDecision(t *testing.T) {
	cache := &runtimeBreakerTestCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4708, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	ctx := withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1")

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"))
	require.NoError(t, cache.BlockOpenAIRuntimeBreaker(
		context.Background(),
		account.ID,
		"",
		"concurrent_failure",
		time.Minute,
	))

	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"),
		"an allowed decision must not hide a breaker opened later in the same request")
}

func TestOpenAIRuntimeBreaker_DoesNotPartiallyClaimAccountWhenModelScopeIsBlocked(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4709, ""): {
			blockUntil: time.Now().Add(-time.Second),
		},
		runtimeBreakerTestKey(4709, "gpt-5.4"): {
			blockUntil: time.Now().Add(time.Minute),
		},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4709, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(
		withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1"),
		account,
		"gpt-5.4",
	))

	cache.mu.Lock()
	accountScope := cache.entries[runtimeBreakerTestKey(account.ID, "")]
	cache.mu.Unlock()
	require.Empty(t, accountScope.owner,
		"denying the model scope must atomically roll back the earlier account claim")
}

func TestOpenAIStrictContinuation_RuntimeBlockStopsFallbackAndPreservesResponseBinding(t *testing.T) {
	ctx := context.Background()
	groupID := int64(47)
	bound := Account{
		ID:          4712,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}
	fallback := bound
	fallback.ID = 4713
	fallback.Priority = 100
	cache := &runtimeBreakerTestCache{}
	store := NewOpenAIWSStateStore(cache)
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{bound, fallback}},
		cache:              cache,
		cfg:                newOpenAIWSV2TestConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
		openaiWSStateStore: store,
	}
	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_runtime_open", bound.ID, time.Hour))
	svc.BlockAccountScheduling(&bound, time.Now().Add(time.Minute), "test_runtime_open")

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx,
		&groupID,
		"resp_runtime_open",
		"",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportResponsesWebsocketV2,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		true,
	)
	require.Nil(t, selection)
	var continuationErr *UpstreamFailoverError
	require.ErrorAs(t, err, &continuationErr)
	require.True(t, continuationErr.IsOpenAIContinuationStateUnavailable())
	require.False(t, continuationErr.ShouldRetryNextAccount())

	accountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_runtime_open")
	require.NoError(t, getErr)
	require.Equal(t, bound.ID, accountID, "runtime cooldown must preserve the strict continuation anchor")
}

func TestOpenAIStrictContinuation_MissingBindingDoesNotFallBackToAnotherAccount(t *testing.T) {
	ctx := context.Background()
	groupID := int64(4711)
	bound := Account{
		ID:          4716,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}
	fallback := bound
	fallback.ID = 4717
	fallback.Priority = 100
	cache := &runtimeBreakerTestCache{}
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{bound, fallback}},
		cache:              cache,
		cfg:                newOpenAIWSV2TestConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
		openaiWSStateStore: NewOpenAIWSStateStore(cache),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx,
		&groupID,
		"resp_missing_binding",
		"",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportResponsesWebsocketV2,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		true,
	)
	require.Nil(t, selection)
	var continuationErr *UpstreamFailoverError
	require.ErrorAs(t, err, &continuationErr)
	require.True(t, continuationErr.IsOpenAIContinuationStateUnavailable())
	require.False(t, continuationErr.ShouldRetryNextAccount())
}

func TestOpenAIStrictContinuation_APIKeyIgnoresRedisHalfOpenProbe(t *testing.T) {
	ctx := context.Background()
	groupID := int64(48)
	bound := Account{
		ID:          4714,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}
	fallback := bound
	fallback.ID = 4715
	fallback.Priority = 100
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(bound.ID, ""): {
			blockUntil: time.Now().Add(-time.Second),
			owner:      "other-request",
		},
	}}
	store := NewOpenAIWSStateStore(cache)
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{bound, fallback}},
		cache:              cache,
		cfg:                newOpenAIWSV2TestConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
		openaiWSStateStore: store,
	}
	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_probe_busy", bound.ID, time.Hour))
	require.NoError(t, cache.SetSessionAccountID(ctx, groupID, "strict-session", bound.ID, time.Hour))

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx,
		&groupID,
		"resp_probe_busy",
		"strict-session",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportResponsesWebsocketV2,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, bound.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	accountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_probe_busy")
	require.NoError(t, getErr)
	require.Equal(t, bound.ID, accountID, "API-key scheduling must preserve the strict anchor")
	stickyAccountID, stickyErr := cache.GetSessionAccountID(ctx, groupID, "strict-session")
	require.NoError(t, stickyErr)
	require.Equal(t, bound.ID, stickyAccountID, "API-key scheduling must preserve the session sticky binding")
}

func TestOpenAIRuntimeBreaker_OrdinarySessionStickyProbeBusyPreservesBinding(t *testing.T) {
	ctx := context.Background()
	groupID := int64(49)
	bound := Account{
		ID:          4718,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}
	fallback := bound
	fallback.ID = 4719
	fallback.Priority = 100
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(bound.ID, ""): {
			blockUntil: time.Now().Add(-time.Second),
			owner:      "other-request",
		},
	}}
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{bound, fallback}},
		cache:              cache,
		cfg:                newOpenAIWSV2TestConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
	}
	require.NoError(t, cache.SetSessionAccountID(ctx, groupID, "ordinary-session", bound.ID, time.Hour))

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"ordinary-session",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportResponsesWebsocketV2,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, bound.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	stickyAccountID, stickyErr := cache.GetSessionAccountID(ctx, groupID, "ordinary-session")
	require.NoError(t, stickyErr)
	require.Equal(t, bound.ID, stickyAccountID)
}

func TestOpenAIRuntimeBreaker_FailedSelectionReleasesItsProbeClaims(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4710, ""): {
			blockUntil: time.Now().Add(-time.Second),
			owner:      "owner-1",
		},
		runtimeBreakerTestKey(4710, "gpt-5.4"): {
			blockUntil: time.Now().Add(-time.Second),
			owner:      "owner-1",
		},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	selection := &AccountSelectionResult{runtimeBreakerProbeOwner: "owner-1"}
	account := &Account{ID: 4710, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	svc.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, "gpt-5.4", false, nil)

	cache.mu.Lock()
	accountScope := cache.entries[runtimeBreakerTestKey(account.ID, "")]
	modelScope := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")]
	cache.mu.Unlock()
	require.Empty(t, accountScope.owner)
	require.Empty(t, modelScope.owner)
}

func TestOpenAIRuntimeBreaker_SelectedHalfOpenClaimStartsRenewableLease(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4711, ""): {
			blockUntil: time.Now().Add(-time.Second),
		},
		runtimeBreakerTestKey(4711, "gpt-5.4"): {
			blockUntil: time.Now().Add(-time.Second),
		},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4711, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	ctx, cancel := context.WithCancel(withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1"))
	defer cancel()

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"))
	selection := attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{
		Account:     account,
		Acquired:    true,
		ReleaseFunc: func() {},
	})
	require.NotNil(t, selection)
	defer selection.ReleaseFunc()

	cache.mu.Lock()
	renewCalls := cache.renewCalls
	cache.mu.Unlock()
	require.Equal(t, 1, renewCalls,
		"promoting a claimed account to the selected lease must renew Redis ownership immediately")
}

func TestOpenAIRuntimeBreaker_APIKeySameAccountRetryDoesNotUseRedisLease(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4712, ""): {
			blockUntil: time.Now().Add(-time.Second),
		},
		runtimeBreakerTestKey(4712, "gpt-5.4"): {
			blockUntil: time.Now().Add(-time.Second),
		},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:          4712,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
	ctx, cancel := context.WithCancel(withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1"))
	defer cancel()

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"))
	first := attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{
		Account:     account,
		Acquired:    true,
		ReleaseFunc: func() {},
	})
	require.Nil(t, first.runtimeBreakerProbeLease)
	require.Empty(t, first.runtimeBreakerProbeModels)
	svc.ReportOpenAIAccountScheduleResultForSelection(first, account.ID, "gpt-5.4", false, nil)

	retry, err := svc.ReacquireOpenAISameAccountSelection(context.Background(), first)
	require.NoError(t, err)
	require.NotNil(t, retry)
	require.Nil(t, retry.runtimeBreakerProbeLease)
	require.Empty(t, retry.runtimeBreakerProbeModels)
	cache.mu.Lock()
	accountOwner := cache.entries[runtimeBreakerTestKey(account.ID, "")].owner
	modelOwner := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")].owner
	cache.mu.Unlock()
	require.Empty(t, accountOwner)
	require.Empty(t, modelOwner)

	svc.ReportOpenAIAccountScheduleResultForSelection(retry, account.ID, "gpt-5.4", true, nil)
}

func TestOpenAIRuntimeBreaker_OAuthSameAccountRetryTransfersProbeUntilFinalFailure(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4719, ""):        {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4719, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:          4719,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
	ctx := withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1")

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"))
	first := attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{
		Account:     account,
		Acquired:    true,
		ReleaseFunc: func() {},
	})
	require.NotNil(t, first.runtimeBreakerProbeLease)
	first.ReleaseFunc()
	svc.ReportOpenAIAccountSameAccountRetry(first, account.ID, "gpt-5.4")

	cache.mu.Lock()
	require.Equal(t, "owner-1", cache.entries[runtimeBreakerTestKey(account.ID, "")].owner)
	require.Equal(t, "owner-1", cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")].owner)
	require.Zero(t, cache.releaseCalls)
	cache.mu.Unlock()

	retry, err := svc.ReacquireOpenAISameAccountSelection(context.Background(), first)
	require.NoError(t, err)
	require.NotNil(t, retry)
	require.NotNil(t, retry.runtimeBreakerProbeLease)
	retry.ReleaseFunc()
	svc.ReportOpenAIAccountScheduleResultForSelection(retry, account.ID, "gpt-5.4", false, nil)
	svc.ReleaseOpenAIRuntimeBreakerProbeForSelection(retry)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Empty(t, cache.entries[runtimeBreakerTestKey(account.ID, "")].owner)
	require.Empty(t, cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")].owner)
	require.Equal(t, 1, cache.releaseCalls, "final failure must release the transferred probe exactly once")
}

func TestOpenAIRuntimeBreaker_SuppressedRetryReleasesSelectionClaim(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4718, ""):        {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4718, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4718, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	ctx := withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1")

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"))
	selection := attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{
		Account:     account,
		Acquired:    true,
		ReleaseFunc: func() {},
	})
	require.NotNil(t, selection)
	require.NotNil(t, selection.runtimeBreakerProbeLease)

	svc.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
	svc.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
	cache.mu.Lock()
	accountOwner := cache.entries[runtimeBreakerTestKey(account.ID, "")].owner
	modelOwner := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")].owner
	releaseCalls := cache.releaseCalls
	cache.mu.Unlock()
	require.Empty(t, accountOwner)
	require.Empty(t, modelOwner)
	require.Equal(t, 1, releaseCalls, "selection probe release must be idempotent")
}

func TestOpenAIRuntimeBreaker_SelectedAccountReleasesOtherCandidateClaims(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4713, ""):        {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4713, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4714, ""):        {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4714, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	first := &Account{ID: 4713, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	selected := &Account{ID: 4714, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	ctx, cancel := context.WithCancel(withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1"))
	defer cancel()

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, first, "gpt-5.4"))
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, selected, "gpt-5.4"))
	selection := attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{
		Account:     selected,
		Acquired:    true,
		ReleaseFunc: func() {},
	})
	require.NotNil(t, selection)
	defer selection.ReleaseFunc()

	cache.mu.Lock()
	firstAccountOwner := cache.entries[runtimeBreakerTestKey(first.ID, "")].owner
	firstModelOwner := cache.entries[runtimeBreakerTestKey(first.ID, "gpt-5.4")].owner
	selectedAccountOwner := cache.entries[runtimeBreakerTestKey(selected.ID, "")].owner
	selectedModelOwner := cache.entries[runtimeBreakerTestKey(selected.ID, "gpt-5.4")].owner
	cache.mu.Unlock()
	require.Empty(t, firstAccountOwner)
	require.Empty(t, firstModelOwner)
	require.Equal(t, "owner-1", selectedAccountOwner)
	require.Equal(t, "owner-1", selectedModelOwner)
}

func TestOpenAIRuntimeBreaker_NoSelectionReleasesAllCandidateClaims(t *testing.T) {
	groupID := int64(4715)
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4715, ""):        {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4715, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
	}}
	account := Account{
		ID:          4715,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		GroupIDs:    []int64{groupID},
	}
	svc := &OpenAIGatewayService{
		accountRepo:      schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:            cache,
		cfg:              &config.Config{},
		rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("true"),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapability("unsupported-for-test"),
		false,
		false,
		false,
	)
	require.Error(t, err)
	require.Nil(t, selection)

	cache.mu.Lock()
	accountOwner := cache.entries[runtimeBreakerTestKey(account.ID, "")].owner
	modelOwner := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")].owner
	cache.mu.Unlock()
	require.Empty(t, accountOwner)
	require.Empty(t, modelOwner)
}

func TestOpenAIRuntimeBreaker_SchedulerSelectionOwnsRenewableLease(t *testing.T) {
	groupID := int64(4716)
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4716, ""):        {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4716, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
	}}
	account := Account{
		ID:          4716,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		GroupIDs:    []int64{groupID},
	}
	svc := &OpenAIGatewayService{
		accountRepo:      schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:            cache,
		cfg:              &config.Config{},
		rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("true"),
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil,
		OpenAIUpstreamTransportAny,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.runtimeBreakerProbeLease)
	require.ElementsMatch(t, []string{"", "gpt-5.4"}, selection.runtimeBreakerProbeModels)
	cache.mu.Lock()
	renewCalls := cache.renewCalls
	accountOwner := cache.entries[runtimeBreakerTestKey(account.ID, "")].owner
	modelOwner := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")].owner
	cache.mu.Unlock()
	require.Equal(t, 1, renewCalls)
	require.NotEmpty(t, accountOwner)
	require.Equal(t, accountOwner, modelOwner)

	svc.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, "gpt-5.4", true, nil)
}

func TestOpenAIRuntimeBreaker_SuccessClearsSelectionClaimScopesWhenModelChanges(t *testing.T) {
	cache := &runtimeBreakerTestCache{entries: map[string]runtimeBreakerTestEntry{
		runtimeBreakerTestKey(4717, ""):        {blockUntil: time.Now().Add(-time.Second)},
		runtimeBreakerTestKey(4717, "gpt-5.4"): {blockUntil: time.Now().Add(-time.Second)},
	}}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4717, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	ctx, cancel := context.WithCancel(withOpenAIRuntimeBreakerProbeOwner(context.Background(), "owner-1"))
	defer cancel()

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, account, "gpt-5.4"))
	selection := attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{
		Account:     account,
		Acquired:    true,
		ReleaseFunc: func() {},
	})
	require.NotNil(t, selection.runtimeBreakerProbeLease)

	svc.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, "gpt-5.5", true, nil)
	cache.mu.Lock()
	_, accountExists := cache.entries[runtimeBreakerTestKey(account.ID, "")]
	_, originalModelExists := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")]
	cache.mu.Unlock()
	require.False(t, accountExists)
	require.False(t, originalModelExists, "success must close the exact model scope claimed during scheduling")
}

func TestOpenAIRuntimeBreaker_LateSuccessDoesNotClearActiveCooldown(t *testing.T) {
	cache := &runtimeBreakerTestCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4706, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "concurrent_failure")
	svc.ReportOpenAIAccountScheduleResult(account.ID, "gpt-5.4", true, nil)

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	cache.mu.Lock()
	_, exists := cache.entries[runtimeBreakerTestKey(account.ID, "")]
	cache.mu.Unlock()
	require.False(t, exists, "API-key cooldown must remain process-local")
}

func TestOpenAIRuntimeBreaker_LateSuccessDoesNotClearActiveModelCooldown(t *testing.T) {
	cache := &runtimeBreakerTestCache{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 4707, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-5.4", &UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable,
	})
	svc.ReportOpenAIAccountScheduleResult(account.ID, "gpt-5.4", true, nil)

	require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.4"))
	cache.mu.Lock()
	_, exists := cache.entries[runtimeBreakerTestKey(account.ID, "gpt-5.4")]
	cache.mu.Unlock()
	require.False(t, exists, "API-key model cooldown must remain process-local")
}

func TestOpenAI429LegacySideEffects_DoNotPersistOrDuplicateRuntimeCooldown(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKeyAccount := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	shouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, nil)
	apiKeyShouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), apiKeyAccount, http.StatusTooManyRequests, http.Header{}, nil)

	require.False(t, shouldDisable)
	require.False(t, apiKeyShouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(apiKeyAccount))
}

func TestFirstClassCindyAccountUsesOpenAIRuntimeBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := newFirstClassCindyRateLimitAccount(4403, false)

	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "cindy_health_quarantine")

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

// TestOpenAI429FastPath_SkipsSparkShadow 外审第8轮 P1:spark 影子被选中后若 /responses 返回 429,
// 不得按 global x-codex-* 信号写内存运行时熔断(否则 spark 被冷却到 global reset、单影子场景无可用账号)。
func TestOpenAI429FastPath_SkipsSparkShadow(t *testing.T) {
	svc := &OpenAIGatewayService{}
	parentID := int64(800)
	shadow := &Account{
		ID:              801,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
	}
	normal := &Account{ID: 802, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "18000")
	headers.Set("x-codex-primary-window-minutes", "300")

	svc.markOpenAIOAuth429RateLimited(context.Background(), shadow, headers, nil)
	svc.markOpenAIOAuth429RateLimited(context.Background(), normal, headers, nil)

	require.False(t, svc.isOpenAIAccountRuntimeBlocked(shadow), "spark shadow must not be runtime-blocked by /responses global 429")
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(normal), "normal OpenAI OAuth account should still be runtime-blocked")
}

func TestOpenAIRuntimeBlock_AppliesToOpenAIAPIKeyWhenRateLimitServiceStopsScheduling(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 44, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.BlockAccountScheduling(account, time.Time{}, "custom_error_code")

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlock_DoesNotApplyToOtherPlatforms(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 45, Platform: PlatformGemini, Type: AccountTypeOAuth}

	svc.BlockAccountScheduling(account, time.Time{}, "custom_error_code")

	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlocker_IgnoresNonOpenAIFromRateLimitService(t *testing.T) {
	gateway := &OpenAIGatewayService{}
	repo := &rateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := &Account{ID: 45, Platform: PlatformGemini, Type: AccountTypeOAuth}

	shouldDisable := rateLimitService.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, []byte("forbidden"))

	require.True(t, shouldDisable)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

// 自 #4547（issue 4527 第4点）起，临时不可调度规则命中已知模型时按模型隔离：
// 只封 (账号, 模型) 对，不再账号级一刀切；未知模型仍走账号级兜底
// （见 TestOpenAITempUnschedulable_UnknownModelKeepsAccountRuntimeBlock）。
// 池模式规则仍然生效（issue 4470）：停止同账号重试并对命中模型设临时封锁。
func TestOpenAIPoolModeTempRule_StopsSameAccountRetryAndIsolatesBlockToModel(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{
		cfg:              &config.Config{},
		rateLimitService: rateLimitService,
	}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := &Account{
		ID:          46,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusServiceUnavailable)},
			"temp_unschedulable_enabled":   true,
			"temp_unschedulable_rules": []any{
				map[string]any{
					"error_code":       float64(http.StatusServiceUnavailable),
					"keywords":         []any{"unavailable"},
					"duration_minutes": float64(30),
				},
			},
		},
	}
	body := []byte(`{"error":{"message":"Service temporarily unavailable"}}`)
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
	}

	failoverErr := gateway.failoverOpenAIUpstreamHTTPError(
		context.Background(),
		nil,
		account,
		resp,
		body,
		"gpt-5.4",
	)

	require.NotNil(t, failoverErr)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Zero(t, repo.tempCalls)
	require.Equal(t, 0, repo.setErrCalls)
	require.Equal(t, StatusActive, account.Status)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.modelRateLimitCalls[0].scope)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.5"))
}

func TestOpenAIPoolModeRetryable5xx_DoesNotCreateModelTransientBlock(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	account := &Account{
		ID:       47,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(524)},
		},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gateway.handleOpenAIAccountUpstreamError(
			context.Background(),
			account,
			524,
			http.Header{},
			[]byte(`{"error":{"message":"upstream timeout"}}`),
			"gpt-5.4",
		)
		require.False(t, shouldDisable)
	}

	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.4"))
}

func TestCooldownOpenAIRetryExhausted_AccountAndModelScopes(t *testing.T) {
	t.Run("API key 429 uses account cooldown", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		account := &Account{ID: 4701, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

		svc.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-5.4", &UpstreamFailoverError{
			StatusCode:      http.StatusTooManyRequests,
			ResponseHeaders: http.Header{"Retry-After": []string{"45"}},
		})

		require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	})

	t.Run("503 uses immediate model cooldown", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		account := &Account{ID: 4702, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

		svc.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-5.4", &UpstreamFailoverError{
			StatusCode: http.StatusServiceUnavailable,
		})

		require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.4"))
		require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.5"))
	})

	t.Run("503 honors Retry-After for model cooldown", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		account := &Account{ID: 4706, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		now := time.Now()

		svc.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-5.4", &UpstreamFailoverError{
			StatusCode:      http.StatusServiceUnavailable,
			ResponseHeaders: http.Header{"Retry-After": []string{"45"}},
		})

		state := svc.getOpenAIAccountModelTransientState()
		state.mu.Lock()
		entry := state.entries[openAIAccountModelKey{AccountID: account.ID, Model: "gpt-5.4"}]
		state.mu.Unlock()
		require.Greater(t, entry.blockUntil.Sub(now), 35*time.Second)
	})

	t.Run("request scoped failure does not cool account", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		account := &Account{ID: 4703, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

		svc.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-5.4", &UpstreamFailoverError{
			StatusCode:             http.StatusServiceUnavailable,
			RequestScopedTransient: true,
			Scope:                  GatewayFailureScopeRequest,
		})

		require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.4"))
	})

	t.Run("Cindy terminal request failure does not cool account", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		account := cindyHTTPToWSV2TestAccount()
		account.ID = 4707

		svc.CooldownOpenAIRetryExhausted(context.Background(), account, account.GetMappedModel("gpt-5.6-luna"), &UpstreamFailoverError{
			StatusCode:               http.StatusBadGateway,
			Scope:                    GatewayFailureScopeRequest,
			Reason:                   openAICindyHTTPToWSV2TerminalReason,
			CindyHTTPToWSV2FirstTurn: true,
		})

		require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna"))
	})

	t.Run("Cindy handshake 502 cools only failed account model", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		account := cindyHTTPToWSV2TestAccount()
		account.ID = 4708

		svc.CooldownOpenAIRetryExhausted(context.Background(), account, account.GetMappedModel("gpt-5.6-luna"), &UpstreamFailoverError{
			StatusCode:               http.StatusBadGateway,
			Scope:                    GatewayFailureScopeAccount,
			Reason:                   openAICindyHTTPToWSV2FailoverReason,
			CindyHTTPToWSV2FirstTurn: true,
		})

		require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna"))
		require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-sol"))
	})
}

func TestCooldownOpenAIRetryExhausted_DoesNotShortenExistingBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 4704, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	wantMinimum := time.Now().Add(30 * time.Minute)
	svc.BlockAccountScheduling(account, time.Now().Add(time.Hour), "existing")

	svc.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-5.4", &UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"1"}},
	})

	value, ok := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	until, ok := value.(time.Time)
	require.True(t, ok)
	require.True(t, until.After(wantMinimum))
}

func TestOpenAIPoolModeNonRetryable5xx_StillCreatesModelTransientBlock(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	account := &Account{
		ID:       48,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{float64(http.StatusGatewayTimeout)},
		},
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gateway.handleOpenAIAccountUpstreamError(
			context.Background(),
			account,
			http.StatusServiceUnavailable,
			http.Header{},
			[]byte(`{"error":{"message":"upstream unavailable"}}`),
			"gpt-5.4",
		)
		require.False(t, shouldDisable)
	}

	require.True(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.4"))
}

func TestOpenAINonPoolAPIKey5xx_StillCreatesModelTransientBlock(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	account := &Account{
		ID:       49,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}

	for i := 0; i < 2; i++ {
		shouldDisable := gateway.handleOpenAIAccountUpstreamError(
			context.Background(),
			account,
			http.StatusGatewayTimeout,
			http.Header{},
			[]byte(`{"error":{"message":"upstream timeout"}}`),
			"gpt-5.4",
		)
		require.False(t, shouldDisable)
	}

	require.True(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.4"))
}

func TestOpenAIModelNotFound_DoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &OpenAIGatewayService{
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := openAIModelNotFoundTempAccount()

	shouldDisable := svc.handleOpenAIAccountUpstreamError(
		context.Background(),
		account,
		http.StatusNotFound,
		http.Header{},
		[]byte(`{"error":{"code":"model_not_found","message":"model not found"}}`),
		"gpt-5.4",
	)

	require.True(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
}

func TestOpenAIModelTempUnschedulable_DoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &OpenAIGatewayService{
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := openAIModelNotFoundTempAccount()

	shouldDisable := svc.handleOpenAIAccountUpstreamError(
		context.Background(),
		account,
		http.StatusNotFound,
		http.Header{},
		[]byte(`{"error":{"message":"endpoint not found"}}`),
		"gpt-5.4",
	)

	require.True(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.modelRateLimitCalls[0].scope)
}

func TestOpenAIModelTempUnschedulable_WriteFailureDoesNotRuntimeBlockWholeAccount(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{modelRateLimitErr: errors.New("write failed")}
	svc := &OpenAIGatewayService{
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := openAIModelNotFoundTempAccount()

	shouldDisable := svc.handleOpenAIAccountUpstreamError(
		context.Background(),
		account,
		http.StatusNotFound,
		http.Header{},
		[]byte(`{"error":{"message":"endpoint not found"}}`),
		"gpt-5.4",
	)

	require.True(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
}

func TestOpenAIAuthAndQuotaErrorsSkipLegacySchedulingState(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
			account := openAIModelNotFoundTempAccount()
			account.Type = AccountTypeOAuth

			shouldDisable := svc.handleOpenAIAccountUpstreamError(
				context.Background(), account, statusCode, http.Header{},
				[]byte(`{"error":{"message":"credential or quota failure"}}`), "gpt-5.4",
			)

			require.False(t, shouldDisable)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			require.Zero(t, repo.tempCalls)
			require.Empty(t, repo.modelRateLimitCalls)
		})
	}
}

func TestOpenAITempUnschedulable_UnknownModelKeepsAccountRuntimeBlock(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &OpenAIGatewayService{
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := openAIModelNotFoundTempAccount()

	shouldDisable := svc.handleOpenAIAccountUpstreamError(
		context.Background(),
		account,
		http.StatusNotFound,
		http.Header{},
		[]byte(`{"error":{"message":"endpoint not found"}}`),
	)

	require.True(t, shouldDisable)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 1, repo.tempCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestOpenAIRuntimeBlock_DoesNotShortenExistingBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 46, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	longUntil := time.Now().Add(10 * time.Minute)

	svc.BlockAccountScheduling(account, longUntil, "oauth_401")
	svc.BlockAccountScheduling(account, time.Time{}, "upstream_disable")

	value, ok := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	actualUntil, ok := value.(time.Time)
	require.True(t, ok)
	require.WithinDuration(t, longUntil, actualUntil, time.Second)
}

func TestOpenAIRuntimeBlock_ClearAccountSchedulingBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 47, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "429")
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))

	svc.ClearAccountSchedulingBlock(account.ID)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestCindyBannedRuntimeBlockIsIndefiniteAndGenerationScopedAcrossABA(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := newCindyRateLimitAccount(99101, false)
	account.CindyCredentialGeneration = 4
	fingerprint, err := AccountCredentialFingerprint(
		ProviderProfileCindyLaxaV1, AccountTypeAPIKey, "https://api.laxarouter.ai", account.GetCredential("api_key"),
	)
	require.NoError(t, err)
	episode := CindyHealthEpisode{
		AccountID: account.ID, Generation: 4, EpisodeID: "episode-generation-4", Fingerprint: fingerprint,
	}

	require.True(t, svc.BlockCindyHealthEpisode(account, episode, "cindy_banned"))
	stored, ok := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	require.True(t, stored.(time.Time).IsZero(), "banned runtime block must be indefinite")
	require.True(t, svc.isOpenAIAccountRuntimeBlockedContext(context.Background(), account))
	svc.BlockAccountScheduling(account, time.Now().Add(2*time.Minute), "cindy_health_quarantine")

	currentABA := *account
	currentABA.CindyCredentialGeneration = 6
	require.False(t, svc.isOpenAIAccountRuntimeBlockedContext(context.Background(), &currentABA),
		"generation 4 evidence must not block generation 6 even when the credential fingerprint repeats")
}

func TestShouldStopOpenAIOAuth429Failover_OnlyDuringStorm(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKeyAccount := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	var state OpenAIOAuth429FailoverState

	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))

	for i := 0; i < openAIOAuth429StormThreshold; i++ {
		svc.recordOpenAIOAuth429()
	}

	require.True(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(apiKeyAccount, http.StatusTooManyRequests, 1, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusInternalServerError, 1, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 0, &state))
}

func TestShouldStopOpenAIOAuth429Failover_TracksOneGrokFollowupAttempt(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 44, Platform: PlatformGrok, Type: AccountTypeOAuth}
	apiKeyAccount := &Account{ID: 45, Platform: PlatformGrok, Type: AccountTypeAPIKey}

	t.Run("429 then 500 stops after one followup", func(t *testing.T) {
		var state OpenAIOAuth429FailoverState
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))
		require.True(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusInternalServerError, 2, &state))
	})

	t.Run("500 then 429 still allows one followup", func(t *testing.T) {
		var state OpenAIOAuth429FailoverState
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusInternalServerError, 1, &state))
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 2, &state))
		require.True(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusBadGateway, 3, &state))
	})

	t.Run("OAuth 429 then API-key failure consumes the same followup", func(t *testing.T) {
		var state OpenAIOAuth429FailoverState
		require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1, &state))
		require.True(t, svc.ShouldStopOpenAIOAuth429Failover(apiKeyAccount, http.StatusInternalServerError, 2, &state))
	})

	var state OpenAIOAuth429FailoverState
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 0, &state))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(apiKeyAccount, http.StatusTooManyRequests, 2, &state))
}
