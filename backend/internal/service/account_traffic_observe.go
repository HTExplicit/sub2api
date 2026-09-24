package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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
type AccountTrafficObserveState = extensionv1.AccountTrafficCounters

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
	cache AccountTrafficObserveCache
}

// Whether turns are recorded follows the admin_observability_config setting,
// whose default is the legacy gateway flag.
func NewAccountTrafficObserver(cache AccountTrafficObserveCache, _ *config.Config) *AccountTrafficObserver {
	return &AccountTrafficObserver{
		cache: cache,
	}
}

// Enabled reports whether telemetry is switched on, for management displays.
// Begin and Snapshot additionally require a concrete account identity.
func (o *AccountTrafficObserver) Enabled() bool {
	return o != nil && o.cache != nil && currentAdminObservabilityConfig().TelemetryEnabled
}

// Begin records one started turn for account/protocol and samples in-flight
// slots. It never returns an error: on failure it debug-logs and returns nil,
// whose Finish is a no-op, so started and outcomes stay reconciled.
func (o *AccountTrafficObserver) Begin(ctx context.Context, account *Account, protocol AccountTrafficProtocol) *AccountTrafficTurn {
	if o == nil || o.cache == nil || !protocol.Valid() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	policy, available := currentAccountTrafficObservationPolicy(account)
	if !available || !policy.Enabled {
		return nil
	}
	accountID := account.ID
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
	return &AccountTrafficTurn{cache: o.cache, accountID: accountID, protocol: protocol, classification: policy.Classification}
}

// Snapshot returns the per-protocol counters of one account.
func (o *AccountTrafficObserver) Snapshot(ctx context.Context, account *Account) (map[AccountTrafficProtocol]AccountTrafficObserveState, error) {
	if o == nil || o.cache == nil {
		return nil, ErrAccountTrafficTelemetryUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	policy, available := currentAccountTrafficObservationPolicy(account)
	if !available || !policy.Enabled {
		return nil, ErrAccountTrafficTelemetryUnavailable
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, accountTrafficObserveRedisTimeout)
	defer cancel()
	return o.cache.Snapshot(snapshotCtx, account.ID)
}

// AccountTrafficTurn is one started turn awaiting its single Finish.
type AccountTrafficTurn struct {
	classification extensionv1.DecisionTable
	cache          AccountTrafficObserveCache
	accountID      int64
	protocol       AccountTrafficProtocol
	once           sync.Once
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
		outcome := evaluateAccountTrafficOutcome(t.classification, result, err, clientCancelled)
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

// Compatibility facade for callers without a begun turn. Started observations
// instead evaluate the rule table captured before incrementing.
func classifyAccountTrafficOutcome(result *OpenAIForwardResult, err error, clientCancelled bool) AccountTrafficOutcome {
	return evaluateAccountTrafficOutcome(accountTrafficOutcomeRules, result, err, clientCancelled)
}

func evaluateAccountTrafficOutcome(table extensionv1.DecisionTable, result *OpenAIForwardResult, err error, clientCancelled bool) AccountTrafficOutcome {
	facts := map[string]string{"client_cancelled": strconv.FormatBool(clientCancelled || (result != nil && result.ClientDisconnect)), "has_result": strconv.FormatBool(result != nil), "has_error": strconv.FormatBool(err != nil), "ws": "false", "terminal": "", "terminal_status": "0", "error_status": "0"}
	if result != nil {
		facts["ws"] = strconv.FormatBool(result.OpenAIWSMode)
		facts["terminal"], facts["terminal_status"] = result.UpstreamTerminalEvent, strconv.Itoa(result.UpstreamTerminalStatus)
	}
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) && failover != nil {
		facts["error_status"] = strconv.Itoa(failover.StatusCode)
	}
	value, evalErr := table.Evaluate(facts)
	if evalErr != nil || !AccountTrafficOutcome(value).Valid() {
		return AccountTrafficOutcomeFailedOther
	}
	return AccountTrafficOutcome(value)
}
