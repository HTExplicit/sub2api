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
)

// A single exact budget_exceeded event proves only that one request was
// rejected. Cindy's public protocol does not define that event as a permanent,
// account-wide balance state, so durable exclusion requires the same signal on
// two independently verified control models.
var cindyBalanceRecheckModels = [...]string{
	"openai/gpt-5.6-luna",
	"openai/gpt-5.6-terra",
}

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
	inFlight                bool
	inFlightExact           bool
	inFlightFingerprint     string
	exactPending            *Account
	exactPendingFingerprint string
	failures                int
	nextAllowed             time.Time
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
	return c.scheduleWithPriority(account, false)
}

func (c *cindyBalanceRecheckCoordinator) scheduleExact(account *Account) bool {
	return c.scheduleWithPriority(account, true)
}

func (c *cindyBalanceRecheckCoordinator) scheduleWithPriority(account *Account, exact bool) bool {
	if c == nil || c.probe == nil || account == nil || account.ID <= 0 ||
		!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return false
	}
	now := c.now()
	if until := c.breakerUnix.Load(); until > now.UnixNano() {
		return false
	}

	accountCopy, ok := cloneCindyBalanceProbeAccount(account)
	if !ok {
		return false
	}
	fingerprint, err := CindyAccountIdentityFingerprint(
		accountCopy.Platform,
		accountCopy.Type,
		accountCopy.Credentials,
	)
	if err != nil {
		return false
	}

	c.mu.Lock()
	state := c.states[account.ID]
	if state == nil {
		state = &cindyBalanceRecheckState{}
		c.states[account.ID] = state
	}
	if state.inFlight {
		if !exact || (state.inFlightExact && state.inFlightFingerprint == fingerprint) {
			c.mu.Unlock()
			return false
		}
		if state.exactPending != nil && state.exactPendingFingerprint == fingerprint {
			c.mu.Unlock()
			return false
		}
		// Keep only the latest credential generation. A same-generation exact
		// event coalesces, while a rotation must not be lost behind an old probe.
		state.exactPending = accountCopy
		state.exactPendingFingerprint = fingerprint
		c.mu.Unlock()
		return true
	}
	if !exact && now.Before(state.nextAllowed) {
		c.mu.Unlock()
		return false
	}
	if c.pending.Load() >= cindyBalanceRecheckBatchLimit {
		c.mu.Unlock()
		return false
	}
	state.inFlight = true
	state.inFlightExact = exact
	state.inFlightFingerprint = fingerprint
	c.pending.Add(1)
	c.mu.Unlock()

	go c.run(accountCopy)
	return true
}

func cloneCindyBalanceProbeAccount(account *Account) (*Account, bool) {
	if account == nil {
		return nil, false
	}
	credentialsJSON, err := json.Marshal(account.Credentials)
	if err != nil {
		return nil, false
	}
	credentials := make(map[string]any)
	if err := json.Unmarshal(credentialsJSON, &credentials); err != nil {
		return nil, false
	}
	accountCopy := *account
	accountCopy.Credentials = credentials
	return &accountCopy, true
}

func (c *cindyBalanceRecheckCoordinator) run(account *Account) {
	defer c.pending.Add(-1)
	c.semaphore <- struct{}{}
	defer func() { <-c.semaphore }()
	for {
		now := c.now()
		outcome := cindyBalanceRecheckOther
		if c.breakerUnix.Load() <= now.UnixNano() {
			ctx, cancel := context.WithTimeout(context.Background(), cindyBalanceRecheckTimeout)
			outcome = c.probe(ctx, account)
			cancel()
		}

		completedAt := c.now()
		if outcome == cindyBalanceRecheckNetworkFailure || outcome == cindyBalanceRecheckServerFailure {
			c.breakerUnix.Store(completedAt.Add(cindyBalanceRecheckBreakerCooldown).UnixNano())
		}

		c.mu.Lock()
		state := c.states[account.ID]
		if state == nil {
			state = &cindyBalanceRecheckState{}
			c.states[account.ID] = state
		}
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

		next := state.exactPending
		nextFingerprint := state.exactPendingFingerprint
		completedFingerprint := state.inFlightFingerprint
		if next != nil &&
			(outcome != cindyBalanceRecheckExhausted || nextFingerprint != completedFingerprint) &&
			c.breakerUnix.Load() <= completedAt.UnixNano() {
			state.exactPending = nil
			state.exactPendingFingerprint = ""
			state.inFlightExact = true
			state.inFlightFingerprint = nextFingerprint
			c.mu.Unlock()
			account = next
			continue
		}
		state.exactPending = nil
		state.exactPendingFingerprint = ""
		state.inFlight = false
		state.inFlightExact = false
		state.inFlightFingerprint = ""
		c.mu.Unlock()
		return
	}
}

func (s *OpenAIGatewayService) scheduleAmbiguousCindyBalanceRecheck(account *Account) {
	s.scheduleCindyBalanceRecheck(account, false)
}

func (s *OpenAIGatewayService) scheduleExactCindyBalanceConfirmation(account *Account) {
	s.scheduleCindyBalanceRecheck(account, true)
}

func (s *OpenAIGatewayService) scheduleCindyBalanceRecheck(account *Account, exact bool) {
	if !CindyBalanceDetectionFeatureEnabled() || s == nil || account == nil || s.httpUpstream == nil {
		return
	}
	s.cindyBalanceRecheckOnce.Do(func() {
		s.cindyBalanceRecheck = newCindyBalanceRecheckCoordinator(s.probeCindyBalance)
	})
	scheduled := false
	if s.cindyBalanceRecheck != nil {
		if exact {
			scheduled = s.cindyBalanceRecheck.scheduleExact(account)
		} else {
			scheduled = s.cindyBalanceRecheck.schedule(account)
		}
	}
	if scheduled {
		slog.Info("cindy_balance_recheck_scheduled", "exact_priority", exact)
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

func (s *RateLimitService) scheduleExactCindyBalanceConfirmation(account *Account) {
	if !CindyBalanceDetectionFeatureEnabled() || s == nil || s.runtimeBlocker == nil {
		return
	}
	scheduler, ok := s.runtimeBlocker.(interface {
		scheduleExactCindyBalanceConfirmation(*Account)
	})
	if ok {
		scheduler.scheduleExactCindyBalanceConfirmation(account)
	}
}

func (s *OpenAIGatewayService) probeCindyBalance(ctx context.Context, account *Account) cindyBalanceRecheckOutcome {
	if account == nil {
		return cindyBalanceRecheckOther
	}
	for _, model := range cindyBalanceRecheckModels {
		outcome := s.probeCindyBalanceModel(ctx, account, model)
		if outcome != cindyBalanceRecheckExhausted {
			return outcome
		}
	}
	if s == nil || s.rateLimitService == nil {
		return cindyBalanceRecheckOther
	}
	s.rateLimitService.handleCindyBalanceInsufficient(ctx, account)
	return cindyBalanceRecheckExhausted
}

func (s *OpenAIGatewayService) probeCindyBalanceModel(ctx context.Context, account *Account, model string) cindyBalanceRecheckOutcome {
	if !CindyBalanceDetectionFeatureEnabled() || s == nil || account == nil || s.httpUpstream == nil {
		return cindyBalanceRecheckOther
	}
	body, _ := json.Marshal(map[string]any{
		"model":             model,
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
	if resp == nil || resp.Body == nil {
		return cindyBalanceRecheckNetworkFailure
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, cindyBalanceRecheckMaxBodyBytes))
	if err != nil {
		return cindyBalanceRecheckNetworkFailure
	}
	if ClassifyCindyBalanceInsufficient(account, resp.StatusCode, responseBody) != CindyBalanceSignalNone {
		return cindyBalanceRecheckExhausted
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	looksLikeSSE := strings.Contains(contentType, "text/event-stream") || strings.Contains(string(responseBody), "data:")
	if resp.StatusCode == http.StatusOK && looksLikeSSE {
		return classifyCindyBalanceRecheckSSE(account, responseBody)
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

func classifyCindyBalanceRecheckSSE(account *Account, body []byte) cindyBalanceRecheckOutcome {
	terminalCount := 0
	exhaustedCount := 0
	completedCount := 0
	forEachOpenAISSEDataPayload(string(body), func(payload []byte) {
		if !gjson.ValidBytes(payload) {
			return
		}
		eventType := gjson.GetBytes(payload, "type")
		if eventType.Type != gjson.String {
			return
		}
		switch eventType.Str {
		case "response.failed", "error":
			terminalCount++
			if ClassifyCindyBalanceInsufficient(account, http.StatusOK, payload) != CindyBalanceSignalNone {
				exhaustedCount++
			}
		case "response.completed":
			terminalCount++
			if isValidCindyBalanceCompletedResponse(gjson.GetBytes(payload, "response")) {
				completedCount++
			}
		}
	})
	if terminalCount != 1 {
		return cindyBalanceRecheckOther
	}
	if exhaustedCount == 1 {
		return cindyBalanceRecheckExhausted
	}
	if completedCount == 1 {
		return cindyBalanceRecheckSuccess
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
