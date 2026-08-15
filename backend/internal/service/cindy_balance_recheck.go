package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
)

const (
	cindyBalanceRecheckConcurrency     = 2
	cindyBalanceRecheckBatchLimit      = 10
	cindyBalanceRecheckSuccessCooldown = 24 * time.Hour
	cindyBalanceRecheckBreakerCooldown = 5 * time.Minute
	cindyBalanceRecheckTimeout         = 20 * time.Second
	cindyBalanceRecheckMaxBodyBytes    = 64 << 10
	cindyBalanceRecheckModel           = "openai/gpt-5.6-luna"
)

var cindyBalanceRecheckBackoffs = [...]time.Duration{
	15 * time.Minute,
	time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

type cindyBalanceRecheckOutcome uint8

const (
	cindyBalanceRecheckOther cindyBalanceRecheckOutcome = iota
	cindyBalanceRecheckSuccess
	cindyBalanceRecheckExhausted
	cindyBalanceRecheckNetworkFailure
	cindyBalanceRecheckServerFailure
)

type cindyBalanceRecheckState struct {
	inFlight    bool
	failures    int
	nextAllowed time.Time
}

// cindyBalanceRecheckCoordinator bounds paid follow-up probes process-wide per
// gateway instance. It provides per-account singleflight, a ten-item pending
// batch, two workers, cooldown/backoff, and a global network/5xx breaker.
type cindyBalanceRecheckCoordinator struct {
	mu          sync.Mutex
	states      map[int64]*cindyBalanceRecheckState
	semaphore   chan struct{}
	pending     atomic.Int32
	breakerUnix atomic.Int64
	now         func() time.Time
	probe       func(context.Context, *Account) cindyBalanceRecheckOutcome
}

func newCindyBalanceRecheckCoordinator(probe func(context.Context, *Account) cindyBalanceRecheckOutcome) *cindyBalanceRecheckCoordinator {
	return &cindyBalanceRecheckCoordinator{
		states:    make(map[int64]*cindyBalanceRecheckState),
		semaphore: make(chan struct{}, cindyBalanceRecheckConcurrency),
		now:       time.Now,
		probe:     probe,
	}
}

func (c *cindyBalanceRecheckCoordinator) schedule(account *Account) bool {
	if c == nil || c.probe == nil || account == nil || account.ID <= 0 ||
		!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return false
	}
	now := c.now()
	if until := c.breakerUnix.Load(); until > now.UnixNano() {
		return false
	}

	c.mu.Lock()
	state := c.states[account.ID]
	if state == nil {
		state = &cindyBalanceRecheckState{}
		c.states[account.ID] = state
	}
	if state.inFlight || now.Before(state.nextAllowed) {
		c.mu.Unlock()
		return false
	}
	if c.pending.Load() >= cindyBalanceRecheckBatchLimit {
		c.mu.Unlock()
		return false
	}
	state.inFlight = true
	c.pending.Add(1)
	c.mu.Unlock()

	accountCopy := *account
	go c.run(&accountCopy)
	return true
}

func (c *cindyBalanceRecheckCoordinator) run(account *Account) {
	defer c.pending.Add(-1)
	c.semaphore <- struct{}{}
	defer func() { <-c.semaphore }()

	now := c.now()
	outcome := cindyBalanceRecheckOther
	if c.breakerUnix.Load() <= now.UnixNano() {
		ctx, cancel := context.WithTimeout(context.Background(), cindyBalanceRecheckTimeout)
		outcome = c.probe(ctx, account)
		cancel()
	}

	completedAt := c.now()
	c.mu.Lock()
	state := c.states[account.ID]
	if state == nil {
		state = &cindyBalanceRecheckState{}
		c.states[account.ID] = state
	}
	state.inFlight = false
	switch outcome {
	case cindyBalanceRecheckSuccess, cindyBalanceRecheckExhausted:
		state.failures = 0
		state.nextAllowed = completedAt.Add(cindyBalanceRecheckSuccessCooldown)
	default:
		state.failures++
		index := state.failures - 1
		if index >= len(cindyBalanceRecheckBackoffs) {
			index = len(cindyBalanceRecheckBackoffs) - 1
		}
		state.nextAllowed = completedAt.Add(cindyBalanceRecheckBackoffs[index])
	}
	c.mu.Unlock()

	if outcome == cindyBalanceRecheckNetworkFailure || outcome == cindyBalanceRecheckServerFailure {
		c.breakerUnix.Store(completedAt.Add(cindyBalanceRecheckBreakerCooldown).UnixNano())
	}
}

func (s *OpenAIGatewayService) scheduleAmbiguousCindyBalanceRecheck(account *Account) {
	if !CindyBalanceDetectionFeatureEnabled() || s == nil || account == nil || s.httpUpstream == nil {
		return
	}
	s.cindyBalanceRecheckOnce.Do(func() {
		s.cindyBalanceRecheck = newCindyBalanceRecheckCoordinator(s.probeCindyBalance)
	})
	if s.cindyBalanceRecheck != nil && s.cindyBalanceRecheck.schedule(account) {
		slog.Info("cindy_balance_recheck_scheduled", "account_id", account.ID)
	}
}

func (s *RateLimitService) scheduleAmbiguousCindyBalanceRecheck(account *Account) {
	if !CindyBalanceDetectionFeatureEnabled() || s == nil || s.runtimeBlocker == nil {
		return
	}
	scheduler, ok := s.runtimeBlocker.(interface {
		scheduleAmbiguousCindyBalanceRecheck(*Account)
	})
	if ok {
		scheduler.scheduleAmbiguousCindyBalanceRecheck(account)
	}
}

func (s *OpenAIGatewayService) probeCindyBalance(ctx context.Context, account *Account) cindyBalanceRecheckOutcome {
	if !CindyBalanceDetectionFeatureEnabled() || s == nil || account == nil || s.httpUpstream == nil {
		return cindyBalanceRecheckOther
	}
	body, _ := json.Marshal(map[string]any{
		"model":             cindyBalanceRecheckModel,
		"input":             "Reply OK.",
		"max_output_tokens": 1,
		"stream":            false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildOpenAIResponsesURL(account.GetOpenAIBaseURL()), bytes.NewReader(body))
	if err != nil {
		return cindyBalanceRecheckOther
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Authorization", "Bearer "+account.GetOpenAIApiKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return cindyBalanceRecheckNetworkFailure
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, cindyBalanceRecheckMaxBodyBytes))
	if err != nil {
		return cindyBalanceRecheckNetworkFailure
	}
	if ClassifyCindyBalanceInsufficient(account, resp.StatusCode, responseBody) != CindyBalanceSignalNone {
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, responseBody)
		return cindyBalanceRecheckExhausted
	}
	inBandExhausted := false
	if resp.StatusCode == http.StatusOK {
		forEachOpenAISSEDataPayload(string(responseBody), func(payload []byte) {
			if inBandExhausted ||
				ClassifyCindyBalanceInsufficient(account, http.StatusOK, payload) == CindyBalanceSignalNone {
				return
			}
			s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusOK, resp.Header, payload)
			inBandExhausted = true
		})
	}
	if inBandExhausted {
		return cindyBalanceRecheckExhausted
	}
	if resp.StatusCode == http.StatusOK {
		if terminalType, terminalPayload, ok := extractOpenAISSETerminalEvent(string(responseBody)); ok {
			if ClassifyCindyBalanceInsufficient(account, http.StatusOK, terminalPayload) != CindyBalanceSignalNone {
				s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusOK, resp.Header, terminalPayload)
				return cindyBalanceRecheckExhausted
			}
			if terminalType == "response.failed" {
				return cindyBalanceRecheckOther
			}
		}
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return cindyBalanceRecheckServerFailure
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		if isValidCindyBalanceRecheckSuccess(resp.Header, responseBody) {
			return cindyBalanceRecheckSuccess
		}
	}
	return cindyBalanceRecheckOther
}

func isValidCindyBalanceRecheckSuccess(headers http.Header, body []byte) bool {
	contentType := ""
	if headers != nil {
		contentType = strings.ToLower(strings.TrimSpace(headers.Get("Content-Type")))
	}
	looksLikeSSE := strings.Contains(contentType, "text/event-stream") ||
		strings.Contains(string(body), "data:")
	if looksLikeSSE {
		terminalType, terminalPayload, ok := extractOpenAISSETerminalEvent(string(body))
		if !ok || terminalType != "response.completed" || !gjson.ValidBytes(terminalPayload) {
			return false
		}
		return isValidCindyBalanceCompletedResponse(gjson.GetBytes(terminalPayload, "response"))
	}
	if !gjson.ValidBytes(body) {
		return false
	}
	return isValidCindyBalanceCompletedResponse(gjson.ParseBytes(body))
}

func isValidCindyBalanceCompletedResponse(response gjson.Result) bool {
	if !response.Exists() || !response.IsObject() ||
		strings.TrimSpace(response.Get("id").String()) == "" ||
		strings.TrimSpace(response.Get("object").String()) != "response" ||
		strings.TrimSpace(response.Get("status").String()) != "completed" {
		return false
	}
	output := response.Get("output")
	if !output.IsArray() || len(output.Array()) == 0 {
		return false
	}
	usage := response.Get("usage")
	return usage.IsObject() &&
		usage.Get("input_tokens").Type == gjson.Number &&
		usage.Get("output_tokens").Type == gjson.Number
}
