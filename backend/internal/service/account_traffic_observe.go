package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// Observe-only per-account traffic telemetry for the OpenAI-family gateway.
//
// Scope: every business entry of OpenAIGatewayHandler that binds an account
// slot before forwarding — Responses HTTP, chat-completions compat, Messages
// compat, Images, Embeddings, alpha-search, and the Responses WebSocket ingress
// in all modes (ctx_pool / shared / dedicated / http_bridge / passthrough). Each
// forwarded attempt on an account is one "started" turn on that account and is
// finished exactly once with a single outcome bucket, so per protocol
// started == sum(outcomes) once every open turn has finished.
//
// Explicitly EXCLUDED (intentional scope exceptions, stated for reviewers):
//   - Grok media / audio / native web-search (grok_media.go, grok_audio.go,
//     gateway_web_search.go) — Grok-specific transports;
//   - Live sessions — they hold their own live lease (LiveConcurrencyCache) and
//     write their own usage rows;
//   - admin account test probes (account_test_service) — the reviewer contract
//     forbids observing through probes, and probe traffic is not client traffic;
//   - the Codex models manifest fetch (openai_codex_models_handler.go) — a
//     metadata lookup, not a model turn;
//   - the non-OpenAI (Anthropic-protocol) gateway handler (gateway_handler.go).
//
// This never admits, rejects, reschedules or cools anything down, and persists
// nothing but bounded counters: no tokens, cookies, seeds, bodies or upstream
// error text. Telemetry failures are debug-logged and never gate a request.

// AccountTrafficProtocol is the client-facing ingress protocol of a turn.
type AccountTrafficProtocol string

const (
	AccountTrafficProtocolHTTP AccountTrafficProtocol = "http"
	AccountTrafficProtocolWS   AccountTrafficProtocol = "ws"
)

// AccountTrafficProtocols lists every protocol label the cache may carry.
func AccountTrafficProtocols() []AccountTrafficProtocol {
	return []AccountTrafficProtocol{AccountTrafficProtocolHTTP, AccountTrafficProtocolWS}
}

// Valid reports whether p is one of the bounded protocol labels.
func (p AccountTrafficProtocol) Valid() bool {
	switch p {
	case AccountTrafficProtocolHTTP, AccountTrafficProtocolWS:
		return true
	default:
		return false
	}
}

// AccountTrafficOutcome is the single bucket a finished turn lands in.
type AccountTrafficOutcome string

const (
	AccountTrafficOutcomeCompleted2xx AccountTrafficOutcome = "completed_2xx"
	AccountTrafficOutcomeUpstream429  AccountTrafficOutcome = "upstream_429"
	AccountTrafficOutcomeUpstream5xx  AccountTrafficOutcome = "upstream_5xx"
	// AccountTrafficOutcomeCancelled covers client cancellation / disconnect and
	// the upstream WS terminals response.cancelled and response.incomplete
	// (max_output_tokens, content filter): the turn ended without a complete
	// answer, but not because the upstream failed.
	AccountTrafficOutcomeCancelled AccountTrafficOutcome = "cancelled"
	// AccountTrafficOutcomeFailedOther is the reconciliation bucket: non-429/5xx
	// failures, WS turns without a demonstrable terminal, and safety-net
	// finishes for turns that never reported.
	AccountTrafficOutcomeFailedOther AccountTrafficOutcome = "failed_other"
)

// Valid reports whether o is one of the bounded outcome labels (Redis field
// names are derived from it, so the set must stay closed).
func (o AccountTrafficOutcome) Valid() bool {
	switch o {
	case AccountTrafficOutcomeCompleted2xx, AccountTrafficOutcomeUpstream429, AccountTrafficOutcomeUpstream5xx,
		AccountTrafficOutcomeCancelled, AccountTrafficOutcomeFailedOther:
		return true
	default:
		return false
	}
}

// AccountTrafficObserveTTLSeconds is the Redis TTL applied to every telemetry
// key on every write (24h).
const AccountTrafficObserveTTLSeconds = 24 * 60 * 60

// accountTrafficObserveRedisTimeout bounds each telemetry round-trip so a slow
// Redis can never hold a request or a slot release hostage.
const accountTrafficObserveRedisTimeout = 2 * time.Second

// AccountTrafficObserveState is the per-protocol counter snapshot of one account.
type AccountTrafficObserveState struct {
	Started      int64 `json:"started"`
	Completed2xx int64 `json:"completed_2xx"`
	Upstream429  int64 `json:"upstream_429"`
	Upstream5xx  int64 `json:"upstream_5xx"`
	Cancelled    int64 `json:"cancelled"`
	FailedOther  int64 `json:"failed_other"`
	// PeakInFlight is the highest slot count observed at any Begin, read from the
	// existing concurrency:account:{id} / concurrency:live:account:{id} ZSETs. It
	// includes scheduler-acquired slots and Live leases of the same account and is
	// only meaningful for accounts with Concurrency > 0: unlimited accounts never
	// write slot members, so it reads 0 for them regardless of load.
	PeakInFlight int `json:"peak_in_flight"`
	// RequestsLast60s is the rolling count of turns started in the last 60s.
	RequestsLast60s int `json:"requests_last_60s"`
	// ObservedSinceMs is when the current 24h window first saw this protocol.
	ObservedSinceMs int64 `json:"observed_since_ms"`
}

// AccountTrafficObserveCache is the Redis-backed counter store (repository).
type AccountTrafficObserveCache interface {
	Begin(ctx context.Context, accountID int64, protocol AccountTrafficProtocol) error
	Finish(ctx context.Context, accountID int64, protocol AccountTrafficProtocol, outcome AccountTrafficOutcome) error
	Snapshot(ctx context.Context, accountID int64) (map[AccountTrafficProtocol]AccountTrafficObserveState, error)
}

// ErrAccountTrafficTelemetryUnavailable is returned by Snapshot when telemetry
// is disabled or not wired; callers must treat it as "no data", never as 5xx.
var ErrAccountTrafficTelemetryUnavailable = errors.New("account traffic telemetry is unavailable")

// AccountTrafficObserver hands out turns to the gateway seams. A nil observer,
// a disabled one, or one without a cache is a no-op everywhere.
type AccountTrafficObserver struct {
	cache    AccountTrafficObserveCache
	disabled bool
}

// NewAccountTrafficObserver builds the observer; gateway.account_traffic_telemetry_disabled
// is the kill switch (zero value = enabled). A nil cfg means enabled.
func NewAccountTrafficObserver(cache AccountTrafficObserveCache, cfg *config.Config) *AccountTrafficObserver {
	return &AccountTrafficObserver{
		cache:    cache,
		disabled: cfg != nil && cfg.Gateway.AccountTrafficTelemetryDisabled,
	}
}

// Enabled reports whether Begin will record anything.
func (o *AccountTrafficObserver) Enabled() bool {
	return o != nil && !o.disabled && o.cache != nil
}

// Begin records one started turn for accountID/protocol and samples in-flight
// slots. It never returns an error: on failure it debug-logs and returns nil,
// whose Finish is a no-op, so started and outcomes stay reconciled.
func (o *AccountTrafficObserver) Begin(ctx context.Context, accountID int64, protocol AccountTrafficProtocol) *AccountTrafficTurn {
	if !o.Enabled() || !protocol.Valid() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	beginCtx, cancel := context.WithTimeout(ctx, accountTrafficObserveRedisTimeout)
	defer cancel()
	if err := o.cache.Begin(beginCtx, accountID, protocol); err != nil {
		logger.L().Debug("account_traffic_observe.begin_failed",
			zap.Int64("account_id", accountID),
			zap.String("protocol", string(protocol)),
			zap.Error(err),
		)
		return nil
	}
	return &AccountTrafficTurn{cache: o.cache, accountID: accountID, protocol: protocol}
}

// Snapshot returns the per-protocol counters of one account.
func (o *AccountTrafficObserver) Snapshot(ctx context.Context, accountID int64) (map[AccountTrafficProtocol]AccountTrafficObserveState, error) {
	if !o.Enabled() {
		return nil, ErrAccountTrafficTelemetryUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, accountTrafficObserveRedisTimeout)
	defer cancel()
	return o.cache.Snapshot(snapshotCtx, accountID)
}

// AccountTrafficTurn is one started turn awaiting its single Finish.
type AccountTrafficTurn struct {
	cache     AccountTrafficObserveCache
	accountID int64
	protocol  AccountTrafficProtocol
	once      sync.Once
}

// Finish classifies the turn and increments exactly one outcome bucket. It is
// idempotent (later calls are ignored) and nil-safe. clientCancelled is the
// client-side signal (request context / connection lifecycle already ended); a
// nil result with a nil err is the safety-net reconciliation and lands in
// failed_other unless clientCancelled.
func (t *AccountTrafficTurn) Finish(result *OpenAIForwardResult, err error, clientCancelled bool) {
	if t == nil || t.cache == nil {
		return
	}
	t.once.Do(func() {
		outcome := classifyAccountTrafficOutcome(result, err, clientCancelled)
		// Detached like the ConcurrencyService ReleaseFunc: the request context
		// is usually already cancelled when a cancelled turn finishes.
		finishCtx, cancel := context.WithTimeout(context.Background(), accountTrafficObserveRedisTimeout)
		defer cancel()
		if ferr := t.cache.Finish(finishCtx, t.accountID, t.protocol, outcome); ferr != nil {
			logger.L().Debug("account_traffic_observe.finish_failed",
				zap.Int64("account_id", t.accountID),
				zap.String("protocol", string(t.protocol)),
				zap.String("outcome", string(outcome)),
				zap.Error(ferr),
			)
		}
	})
}

// classifyAccountTrafficOutcome is the pure bucket classifier, in priority order:
//  1. client cancellation (flag, result.ClientDisconnect) or the WS terminals
//     response.cancelled / response.incomplete → cancelled;
//  2. err carrying an *UpstreamFailoverError → 429 / 5xx / failed_other by its
//     StatusCode; any other err → failed_other;
//  3. no err: a nil result (safety net) → failed_other; a non-WS result →
//     completed_2xx; a WS result by terminal: completed/done → completed_2xx,
//     response.failed → by UpstreamTerminalStatus, "" or unknown → failed_other
//     (an empty WS terminal, e.g. the passthrough zero-turn fallback, is not a
//     demonstrated success even though SucceededForScheduling tolerates it).
func classifyAccountTrafficOutcome(result *OpenAIForwardResult, err error, clientCancelled bool) AccountTrafficOutcome {
	if clientCancelled || (result != nil && result.ClientDisconnect) {
		return AccountTrafficOutcomeCancelled
	}
	if result != nil && result.OpenAIWSMode {
		switch result.UpstreamTerminalEvent {
		case "response.cancelled", "response.incomplete":
			return AccountTrafficOutcomeCancelled
		}
	}
	if err != nil {
		var failoverErr *UpstreamFailoverError
		if errors.As(err, &failoverErr) && failoverErr != nil {
			return accountTrafficOutcomeForStatus(failoverErr.StatusCode)
		}
		return AccountTrafficOutcomeFailedOther
	}
	if result == nil {
		return AccountTrafficOutcomeFailedOther
	}
	if !result.OpenAIWSMode {
		return AccountTrafficOutcomeCompleted2xx
	}
	switch result.UpstreamTerminalEvent {
	case "response.completed", "response.done":
		return AccountTrafficOutcomeCompleted2xx
	case "response.failed":
		return accountTrafficOutcomeForStatus(result.UpstreamTerminalStatus)
	default:
		return AccountTrafficOutcomeFailedOther
	}
}

func accountTrafficOutcomeForStatus(status int) AccountTrafficOutcome {
	switch {
	case status == http.StatusTooManyRequests:
		return AccountTrafficOutcomeUpstream429
	case status >= 500 && status < 600:
		return AccountTrafficOutcomeUpstream5xx
	default:
		return AccountTrafficOutcomeFailedOther
	}
}
