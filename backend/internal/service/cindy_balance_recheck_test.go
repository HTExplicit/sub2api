//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProbeCindyBalanceRecognizesHTTPAndInBandExhaustion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "http_429", status: http.StatusTooManyRequests, body: exactCindyBudgetExceededBody},
		{name: "http_200_sse", status: http.StatusOK, body: "event: response.failed\n" +
			`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}` + "\n\n"},
		{name: "http_200_sse_error", status: http.StatusOK, body: "event: error\n" +
			`data: {"type":"error","error":{"type":"budget_exceeded","code":"429"}}` + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, nil, nil, nil)
			gateway := &OpenAIGatewayService{
				rateLimitService: rateLimitService,
				httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: tc.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tc.body)),
				}},
			}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(8551, true)

			require.Equal(t, cindyBalanceRecheckExhausted, gateway.probeCindyBalance(context.Background(), account))
			require.Equal(t, 1, repo.markCalls)
			require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestProbeCindyBalanceRejectsTerminalShapesOutsideHTTP200(t *testing.T) {
	body := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}` + "\n\n"
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, nil, nil, nil)
	gateway := &OpenAIGatewayService{
		rateLimitService: rateLimitService,
		httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}},
	}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8554, true)

	require.Equal(t, cindyBalanceRecheckOther, gateway.probeCindyBalance(context.Background(), account))
	require.Equal(t, 0, repo.markCalls)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestProbeCindyBalanceRequiresProtocolValidCompletedResponse(t *testing.T) {
	validResponse := `{"id":"resp_probe","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		want        cindyBalanceRecheckOutcome
	}{
		{name: "valid_json", contentType: "application/json", body: validResponse, want: cindyBalanceRecheckSuccess},
		{
			name:        "valid_sse",
			contentType: "text/event-stream",
			body: "event: response.completed\n" +
				`data: {"type":"response.completed","response":` + validResponse + `}` + "\n\n",
			want: cindyBalanceRecheckSuccess,
		},
		{name: "empty", contentType: "application/json", body: "", want: cindyBalanceRecheckOther},
		{name: "html", contentType: "text/html", body: "<html>ok</html>", want: cindyBalanceRecheckOther},
		{name: "empty_object", contentType: "application/json", body: `{}`, want: cindyBalanceRecheckOther},
		{name: "truncated", contentType: "application/json", body: `{"status":"completed"`, want: cindyBalanceRecheckOther},
		{
			name:        "completed_without_usage",
			contentType: "application/json",
			body:        `{"id":"resp_probe","object":"response","status":"completed","output":[{"type":"message"}]}`,
			want:        cindyBalanceRecheckOther,
		},
		{
			name:        "sse_without_completed_terminal",
			contentType: "text/event-stream",
			body:        "event: response.created\n" + `data: {"type":"response.created","response":{"id":"resp_probe"}}` + "\n\n",
			want:        cindyBalanceRecheckOther,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &OpenAIGatewayService{
				httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{tc.contentType}},
					Body:       io.NopCloser(strings.NewReader(tc.body)),
				}},
			}

			require.Equal(t, tc.want, gateway.probeCindyBalance(
				context.Background(), newCindyRateLimitAccount(8552, true),
			))
		})
	}
}

func TestCindyBalanceRecheckMalformedHTTP200UsesFailureBackoff(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	gateway := &OpenAIGatewayService{
		httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}},
	}
	coordinator := newCindyBalanceRecheckCoordinator(gateway.probeCindyBalance)
	coordinator.now = func() time.Time { return now }
	account := newCindyRateLimitAccount(8553, true)

	require.True(t, coordinator.schedule(account))
	require.Eventually(t, func() bool { return coordinator.pending.Load() == 0 }, time.Second, time.Millisecond)
	coordinator.mu.Lock()
	nextAllowed := coordinator.states[account.ID].nextAllowed
	failures := coordinator.states[account.ID].failures
	coordinator.mu.Unlock()
	require.Equal(t, 1, failures)
	require.Equal(t, now.Add(cindyBalanceRecheckBackoffs[0]), nextAllowed)
	require.False(t, coordinator.schedule(account), "malformed 2xx must not receive the 24h success cooldown")
}

func TestCindyBalanceRecheckCoordinatorSingleflightAndSuccessCooldown(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	var calls atomic.Int32
	coordinator := newCindyBalanceRecheckCoordinator(func(context.Context, *Account) cindyBalanceRecheckOutcome {
		calls.Add(1)
		<-release
		return cindyBalanceRecheckSuccess
	})
	coordinator.now = func() time.Time { return now }
	account := newCindyRateLimitAccount(8601, true)

	require.True(t, coordinator.schedule(account))
	require.False(t, coordinator.schedule(account), "same account must be singleflight")
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
	close(release)
	require.Eventually(t, func() bool { return coordinator.pending.Load() == 0 }, time.Second, time.Millisecond)
	require.False(t, coordinator.schedule(account), "success must cool rechecks for 24h")
	now = now.Add(cindyBalanceRecheckSuccessCooldown + time.Second)
	require.True(t, coordinator.schedule(account))
}

func TestCindyBalanceRecheckCoordinatorBoundsConcurrencyAndBatch(t *testing.T) {
	release := make(chan struct{}, cindyBalanceRecheckBatchLimit)
	var current atomic.Int32
	var maximum atomic.Int32
	coordinator := newCindyBalanceRecheckCoordinator(func(context.Context, *Account) cindyBalanceRecheckOutcome {
		active := current.Add(1)
		for {
			seen := maximum.Load()
			if active <= seen || maximum.CompareAndSwap(seen, active) {
				break
			}
		}
		<-release
		current.Add(-1)
		return cindyBalanceRecheckSuccess
	})

	for i := 0; i < cindyBalanceRecheckBatchLimit; i++ {
		require.True(t, coordinator.schedule(newCindyRateLimitAccount(int64(8700+i), true)))
	}
	require.False(t, coordinator.schedule(newCindyRateLimitAccount(8800, true)), "eleventh pending probe must be rejected")
	require.Eventually(t, func() bool { return maximum.Load() == cindyBalanceRecheckConcurrency }, time.Second, time.Millisecond)
	require.LessOrEqual(t, maximum.Load(), int32(cindyBalanceRecheckConcurrency))
	for i := 0; i < cindyBalanceRecheckBatchLimit; i++ {
		release <- struct{}{}
	}
	require.Eventually(t, func() bool { return coordinator.pending.Load() == 0 }, time.Second, time.Millisecond)
}

func TestCindyBalanceRecheckCoordinatorBackoffAndGlobalBreaker(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	coordinator := newCindyBalanceRecheckCoordinator(func(context.Context, *Account) cindyBalanceRecheckOutcome {
		calls.Add(1)
		return cindyBalanceRecheckNetworkFailure
	})
	coordinator.now = func() time.Time { return now }
	first := newCindyRateLimitAccount(8801, true)
	second := newCindyRateLimitAccount(8802, true)

	require.True(t, coordinator.schedule(first))
	require.Eventually(t, func() bool { return coordinator.pending.Load() == 0 }, time.Second, time.Millisecond)
	require.False(t, coordinator.schedule(second), "network failure must open the global breaker")
	now = now.Add(cindyBalanceRecheckBreakerCooldown + time.Second)
	require.False(t, coordinator.schedule(first), "failed account must keep its 15m backoff")
	require.True(t, coordinator.schedule(second), "another account may probe after breaker recovery")
}

func TestCindyBalanceRecheckCoordinatorUsesFullCappedBackoffSequence(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	coordinator := newCindyBalanceRecheckCoordinator(func(context.Context, *Account) cindyBalanceRecheckOutcome {
		return cindyBalanceRecheckOther
	})
	coordinator.now = func() time.Time { return now }
	account := newCindyRateLimitAccount(8891, true)

	for index, backoff := range append(cindyBalanceRecheckBackoffs[:], cindyBalanceRecheckBackoffs[len(cindyBalanceRecheckBackoffs)-1]) {
		require.True(t, coordinator.schedule(account), "probe %d should be admitted", index+1)
		require.Eventually(t, func() bool { return coordinator.pending.Load() == 0 }, time.Second, time.Millisecond)
		coordinator.mu.Lock()
		nextAllowed := coordinator.states[account.ID].nextAllowed
		coordinator.mu.Unlock()
		require.Equal(t, now.Add(backoff), nextAllowed)
		require.False(t, coordinator.schedule(account), "probe must stay blocked during backoff %s", backoff)
		now = nextAllowed.Add(time.Nanosecond)
	}
}

func TestCindyBalanceRecheckCoordinatorServerFailureOpensGlobalBreaker(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	coordinator := newCindyBalanceRecheckCoordinator(func(context.Context, *Account) cindyBalanceRecheckOutcome {
		return cindyBalanceRecheckServerFailure
	})
	coordinator.now = func() time.Time { return now }

	require.True(t, coordinator.schedule(newCindyRateLimitAccount(8892, true)))
	require.Eventually(t, func() bool { return coordinator.pending.Load() == 0 }, time.Second, time.Millisecond)
	require.False(t, coordinator.schedule(newCindyRateLimitAccount(8893, true)), "5xx probe failures must open the global breaker")
}
