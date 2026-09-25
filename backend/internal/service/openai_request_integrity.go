package service

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
)

// Pre/post-transform request integrity check.
//
// Design attribution: the comparator shape (snapshot the client body, compare the
// final plaintext wire body after every service-owned rewrite, canonicalize the
// documented non-lossy Codex compatibility conversions on both sides, report
// field names only) follows the request-integrity guard of the third-party
// "sub2 新站" 0.2.4 distribution. The code here is written against sub2api's own
// compatibility helpers and shares no source with that package.
//
// Modes: off (default) does nothing; observe logs one warning per account per
// requestIntegrityLogInterval and flags the gin context, never rejecting or
// rewriting a request and never touching scheduling or cooldowns; enforce returns
// a validation error so the caller can reject (wired, nothing turns it on).
//
// The hooks compare the byte slices they already hold and never read req.Body.
// Identity/metadata fields (client_metadata, prompt_cache_key, headers, store,
// stream, include, service_tier) are outside the compared set.
//
// Intentional v1 exclusions: /responses/compact requests (opts.Compact skips the
// check); the native WS wire stage after parseClientPayload (verified full replay
// and inferred previous_response_id rewrite input on purpose); the
// chat-completions and /v1/messages bridges (openai_gateway_chat_completions.go,
// openai_gateway_messages.go); the other doOpenAICodexUpstream callers
// (openai_gateway_cc_pipeline.go, openai_alpha_search.go); and the admin
// account-test probes (account_test_service.go issues raw POSTs that bypass
// every transform, so there is nothing to compare).

type requestIntegrityMode string

const (
	requestIntegrityOff     requestIntegrityMode = "off"
	requestIntegrityObserve requestIntegrityMode = "observe"
	requestIntegrityEnforce requestIntegrityMode = "enforce"
)

const (
	// requestIntegrityModeExtraKey is the per-account override (off|observe|enforce).
	// It must stay in the scheduler-cache extra projection (filterSchedulerExtra)
	// or accounts selected through the cache lose the override at forward time.
	requestIntegrityModeExtraKey = "request_integrity_mode"

	requestIntegrityOriginalContextKey       = "openai_request_integrity_original"
	requestIntegrityForwardOptionsContextKey = "openai_request_integrity_forward_options"
	requestIntegrityDifferenceContextKey     = "openai_request_integrity_difference"
	requestIntegrityFieldsContextKey         = "openai_request_integrity_fields"

	requestIntegrityErrorReason = "OPENAI_REQUEST_INTEGRITY"
	// requestIntegrityMaxBodyBytes bounds the two extra decodes; larger bodies are
	// skipped with a debug log instead of being compared.
	requestIntegrityMaxBodyBytes = 4 << 20
	// requestIntegrityLogInterval rate-limits the observe warning per account.
	requestIntegrityLogInterval       = 5 * time.Minute
	requestIntegrityLogGateMaxEntries = 4096
)

func parseRequestIntegrityMode(raw string) (requestIntegrityMode, bool) {
	switch mode := requestIntegrityMode(strings.TrimSpace(raw)); mode {
	case requestIntegrityOff, requestIntegrityObserve, requestIntegrityEnforce:
		return mode, true
	}
	return "", false
}

// requestIntegrityModeExplicit recognizes only an explicitly written legal
// per-account override; a missing, empty, or invalid value is not explicit.
// Admin write paths do not validate the key in v1: an invalid value simply
// falls back to the global mode here.
func requestIntegrityModeExplicit(extra map[string]any) (requestIntegrityMode, bool) {
	if extra == nil {
		return "", false
	}
	raw, _ := extra[requestIntegrityModeExtraKey].(string)
	return parseRequestIntegrityMode(raw)
}

// requestIntegrityModeFor resolves the effective mode with a fixed precedence:
//  1. nil config or nil account -> off;
//  2. accounts that are not OpenAI OAuth-like (Platform openai with an oauth or
//     setup_token credential) are always off, whatever their extra says;
//  3. an explicit per-account extra request_integrity_mode wins;
//  4. otherwise gateway.openai_request_integrity_mode; its zero value "" and
//     any invalid value behave as off.
func requestIntegrityModeFor(cfg *config.Config, account *Account) requestIntegrityMode {
	if cfg == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return requestIntegrityOff
	}
	if mode, ok := requestIntegrityModeExplicit(account.Extra); ok {
		return mode
	}
	if mode, ok := parseRequestIntegrityMode(cfg.Gateway.OpenAIRequestIntegrityMode); ok {
		return mode
	}
	return requestIntegrityOff
}

// requestIntegrityMode is the nil-safe service accessor (test fixtures build
// &OpenAIGatewayService{} without a config and call Forward).
func (s *OpenAIGatewayService) requestIntegrityMode(account *Account) requestIntegrityMode {
	if s == nil {
		return requestIntegrityOff
	}
	return requestIntegrityModeFor(s.cfg, account)
}

type requestIntegritySnapshot struct {
	accountID int64
	path      string
	body      []byte
}

// stageRequestIntegrityOriginal snapshots the client body for the account that
// is about to be forwarded. It always resets the slot first (and any staged
// forward options), so a failover to another account can never be compared
// against a stale snapshot, and mode off leaves nothing behind.
func (s *OpenAIGatewayService) stageRequestIntegrityOriginal(c *gin.Context, account *Account, path string, body []byte) {
	if c == nil {
		return
	}
	c.Set(requestIntegrityOriginalContextKey, (*requestIntegritySnapshot)(nil))
	c.Set(requestIntegrityForwardOptionsContextKey, requestIntegrityOptions{})
	if account == nil || s.requestIntegrityMode(account) == requestIntegrityOff {
		return
	}
	c.Set(requestIntegrityOriginalContextKey, &requestIntegritySnapshot{
		accountID: account.ID,
		path:      path,
		body:      bytes.Clone(body),
	})
}

func requestIntegritySnapshotFromContext(c *gin.Context, account *Account) *requestIntegritySnapshot {
	if c == nil || account == nil {
		return nil
	}
	value, ok := c.Get(requestIntegrityOriginalContextKey)
	if !ok {
		return nil
	}
	snapshot, _ := value.(*requestIntegritySnapshot)
	if snapshot == nil || snapshot.accountID != account.ID {
		return nil
	}
	return snapshot
}

// requestIntegrityStaged lets hot paths skip building the final byte slice
// (for example marshalling a WS payload) when nothing will be compared.
func (s *OpenAIGatewayService) requestIntegrityStaged(c *gin.Context, account *Account) bool {
	return s != nil && requestIntegritySnapshotFromContext(c, account) != nil
}

// stageRequestIntegrityForwardOptions publishes the transform predicates Forward
// derived for this attempt so forwardOpenAIPassthrough and the WS forwarder read
// the same values instead of re-deriving them.
func stageRequestIntegrityForwardOptions(c *gin.Context, opts requestIntegrityOptions) {
	if c != nil {
		c.Set(requestIntegrityForwardOptionsContextKey, opts)
	}
}

func requestIntegrityForwardOptionsFromContext(c *gin.Context) requestIntegrityOptions {
	if c == nil {
		return requestIntegrityOptions{}
	}
	value, ok := c.Get(requestIntegrityForwardOptionsContextKey)
	if !ok {
		return requestIntegrityOptions{}
	}
	opts, _ := value.(requestIntegrityOptions)
	return opts
}

// requestIntegrityFrameSnapshot copies a WebSocket frame for a later per-frame
// comparison; it returns nil when the check is off so hot paths pay nothing.
func (s *OpenAIGatewayService) requestIntegrityFrameSnapshot(account *Account, frame []byte) []byte {
	if s.requestIntegrityMode(account) == requestIntegrityOff {
		return nil
	}
	return bytes.Clone(frame)
}

// checkStagedRequestIntegrity compares the final plaintext body against the
// snapshot staged for this account; a missing or foreign snapshot is a no-op.
func (s *OpenAIGatewayService) checkStagedRequestIntegrity(c *gin.Context, account *Account, stage string, finalBody []byte, opts requestIntegrityOptions) error {
	snapshot := requestIntegritySnapshotFromContext(c, account)
	if snapshot == nil {
		return nil
	}
	return s.checkRequestIntegrity(c, account, snapshot.path, stage, snapshot.body, finalBody, opts)
}

// checkRequestIntegrity is the per-frame variant: original is the frame the
// service received, final is the frame about to be written upstream. A nil
// original means no snapshot was taken (mode was off at that time).
func (s *OpenAIGatewayService) checkRequestIntegrity(c *gin.Context, account *Account, path, stage string, original, final []byte, opts requestIntegrityOptions) error {
	mode := s.requestIntegrityMode(account)
	if mode == requestIntegrityOff || original == nil || opts.Compact {
		return nil
	}
	if bytes.Equal(original, final) {
		return nil
	}
	if len(original) > requestIntegrityMaxBodyBytes || len(final) > requestIntegrityMaxBodyBytes {
		slog.Debug("openai_request_integrity_skipped",
			"account_id", account.ID, "path", path, "stage", stage, "reason", "body_too_large")
		return nil
	}
	field, err := compareRequestIntegrity(account, opts, original, undoSystemPromptForIntegrity(c, final))
	if err != nil {
		// One side is not a JSON object; report the shape, never the bytes.
		field = "json"
	}
	if field == "" {
		return nil
	}
	fields := []string{field}
	if c != nil {
		c.Set(requestIntegrityDifferenceContextKey, true)
		c.Set(requestIntegrityFieldsContextKey, fields)
	}
	if requestIntegrityShouldLog(account.ID, time.Now()) {
		// Field names only: request bodies, credentials and conversation content
		// must never reach the log.
		slog.Warn("openai_request_integrity_difference",
			"account_id", account.ID, "path", path, "stage", stage, "mode", string(mode), "fields", fields)
	}
	if mode == requestIntegrityEnforce {
		return infraerrors.BadRequest(requestIntegrityErrorReason, field)
	}
	return nil
}

var requestIntegrityLogGate = struct {
	mu   sync.Mutex
	last map[int64]time.Time
}{last: make(map[int64]time.Time)}

// requestIntegrityShouldLog allows one warning per account per interval. The
// map is bounded: when it fills up, expired entries are evicted first and the
// whole map is dropped as a last resort.
func requestIntegrityShouldLog(accountID int64, now time.Time) bool {
	gate := &requestIntegrityLogGate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if last, ok := gate.last[accountID]; ok && now.Sub(last) < requestIntegrityLogInterval {
		return false
	}
	if len(gate.last) >= requestIntegrityLogGateMaxEntries {
		for id, last := range gate.last {
			if now.Sub(last) >= requestIntegrityLogInterval {
				delete(gate.last, id)
			}
		}
		if len(gate.last) >= requestIntegrityLogGateMaxEntries {
			gate.last = make(map[int64]time.Time)
		}
	}
	gate.last[accountID] = now
	return true
}
