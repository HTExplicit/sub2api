package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/tidwall/gjson"
)

const claudeSignatureFingerprintLimit = 16

// ClaudeSignatureValueFingerprint identifies a signature without retaining the
// signature itself or any message content. Length is the decoded UTF-8 byte size.
type ClaudeSignatureValueFingerprint struct {
	Path   string `json:"path"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

type ClaudeSignatureSnapshot struct {
	TotalCount   int                               `json:"total_count"`
	OmittedCount int                               `json:"omitted_count,omitempty"`
	Signatures   []ClaudeSignatureValueFingerprint `json:"signatures,omitempty"`
}

// ClaudeSignatureRecoveryDiagnostic is attached to the rejection that triggered
// recovery. It does not create a synthetic upstream attempt or change health.
type ClaudeSignatureRecoveryDiagnostic struct {
	Source         string                  `json:"source"`
	Outcome        string                  `json:"outcome"`
	Reason         string                  `json:"reason,omitempty"`
	Attempts       int                     `json:"attempts"`
	ElapsedMs      int64                   `json:"elapsed_ms"`
	MessageStarted bool                    `json:"message_started"`
	Inbound        ClaudeSignatureSnapshot `json:"inbound"`
	FirstWire      ClaudeSignatureSnapshot `json:"first_wire"`
}

type claudeSignatureRecoveryState struct {
	used          bool
	inbound       []byte
	firstWire     []byte
	firstWireSeen bool
	now           func() time.Time
}

func newClaudeSignatureRecoveryState(inbound []byte) *claudeSignatureRecoveryState {
	return &claudeSignatureRecoveryState{inbound: inbound, now: time.Now}
}

func claudeSignatureSnapshot(body []byte) ClaudeSignatureSnapshot {
	var snapshot ClaudeSignatureSnapshot
	for mi, message := range gjson.GetBytes(body, "messages").Array() {
		for bi, block := range message.Get("content").Array() {
			if block.Get("type").String() != "thinking" {
				continue
			}
			signature := block.Get("signature")
			if signature.Type != gjson.String {
				continue
			}
			snapshot.TotalCount++
			if len(snapshot.Signatures) >= claudeSignatureFingerprintLimit {
				continue
			}
			value := signature.String()
			hash := sha256.Sum256([]byte(value))
			snapshot.Signatures = append(snapshot.Signatures, ClaudeSignatureValueFingerprint{
				Path:   fmt.Sprintf("/messages/%d/content/%d/signature", mi, bi),
				Length: len(value), SHA256: hex.EncodeToString(hash[:]),
			})
		}
	}
	snapshot.OmittedCount = snapshot.TotalCount - len(snapshot.Signatures)
	return snapshot
}

func (s *claudeSignatureRecoveryState) observeFirstWire(body []byte) {
	if !s.firstWireSeen {
		s.firstWireSeen = true
		s.firstWire = body
	}
}

func (s *claudeSignatureRecoveryState) diagnostic(source string, messageStarted bool) *ClaudeSignatureRecoveryDiagnostic {
	return &ClaudeSignatureRecoveryDiagnostic{
		Source: source, Outcome: "skipped", MessageStarted: messageStarted,
		Inbound: claudeSignatureSnapshot(s.inbound), FirstWire: claudeSignatureSnapshot(s.firstWire),
	}
}

func (s *GatewayService) claudeSignatureRecoveryEnabled(ctx context.Context, account *Account, model string) bool {
	if account == nil || account.Platform != PlatformAnthropic || account.IsBedrock() || account.IsAnthropicAPIKeyPassthroughEnabled() || s.settingService == nil || !ShouldRectifyThinkingSignatureError(model) {
		return false
	}
	settings, err := s.settingService.GetRectifierSettings(ctx)
	if err != nil {
		// Match the existing account-type policy on a settings read failure.
		return account.Type != AccountTypeAPIKey
	}
	if !settings.Enabled {
		return false
	}
	if account.Type == AccountTypeAPIKey {
		return settings.APIKeySignatureEnabled
	}
	return settings.ThinkingSignatureEnabled
}

func (s *GatewayService) claudeSignatureErrorCandidate(ctx context.Context, account *Account, body []byte) bool {
	if s.isThinkingBlockSignatureError(body) {
		return true
	}
	return s.settingService != nil && s.isSignatureErrorPattern(ctx, account, body)
}

// run provides one first-stage repair shared by HTTP and SSE rejection paths.
// Its decision budget begins at the rejection, not before the first upstream
// request. Normal transport/stream timeouts remain in force after sending.
func (s *claudeSignatureRecoveryState) run(
	ctx context.Context, diagnostic *ClaudeSignatureRecoveryDiagnostic, baseBody []byte, model string,
	build func([]byte) (*http.Request, []byte, error), send func(*http.Request) (*http.Response, error),
) (resp *http.Response, wireBody []byte, err error) {
	started := s.now()
	defer func() { diagnostic.ElapsedMs = s.now().Sub(started).Milliseconds() }()
	if ctx.Err() != nil {
		diagnostic.Reason = "context_canceled"
		return nil, nil, nil
	}
	if diagnostic.MessageStarted {
		diagnostic.Reason = "output_started"
		return nil, nil, nil
	}
	if s.used {
		diagnostic.Reason = "attempt_limit"
		return nil, nil, nil
	}
	filteredBody := FilterThinkingBlocksForRetry(baseBody, model)
	// The existing HTTP two-stage fallback can need a no-op first stage
	// before a tool-specific rejection authorizes its second-stage filter.
	// New SSE recovery must not replay an unchanged body.
	if bytes.Equal(filteredBody, baseBody) && diagnostic.Source == "sse_error" {
		diagnostic.Reason = "body_unchanged"
		return nil, nil, nil
	}
	s.used = true
	req, wireBody, err := build(filteredBody)
	if err != nil {
		diagnostic.Outcome, diagnostic.Reason = "build_failed", "build_failed"
		return nil, nil, err
	}
	if ctx.Err() != nil {
		diagnostic.Reason = "context_canceled"
		return nil, nil, nil
	}
	if s.now().Sub(started) >= maxRetryElapsed {
		diagnostic.Reason = "budget_exhausted"
		return nil, nil, nil
	}
	diagnostic.Attempts++
	diagnostic.Outcome, diagnostic.Reason = "attempted", ""
	resp, err = send(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		diagnostic.Outcome, diagnostic.Reason = "transport_failed", "transport_failed"
		return nil, nil, err
	}
	if resp == nil || resp.Body == nil {
		diagnostic.Outcome, diagnostic.Reason = "transport_failed", "empty_response"
		return nil, nil, fmt.Errorf("signature recovery: empty upstream response")
	}
	if resp.StatusCode < 400 {
		diagnostic.Outcome = "accepted"
	} else {
		diagnostic.Outcome = "upstream_rejected"
	}
	return resp, wireBody, nil
}

var claudeSignaturePathPattern = regexp.MustCompile(`^/messages/[0-9]{1,10}/content/[0-9]{1,10}/signature$`)
var claudeSignatureHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sanitizeClaudeSignatureRecoveryDiagnostic makes a bounded, detached copy for
// asynchronous persistence. No caller-supplied strings other than a signature
// JSON pointer and a digest can enter these diagnostics.
func sanitizeClaudeSignatureRecoveryDiagnostic(in *ClaudeSignatureRecoveryDiagnostic) *ClaudeSignatureRecoveryDiagnostic {
	if in == nil {
		return nil
	}
	out := *in
	out.Source = claudeSignatureEnum(in.Source, "http_400", "sse_error")
	out.Outcome = claudeSignatureEnum(in.Outcome, "skipped", "attempted", "accepted", "upstream_rejected", "build_failed", "transport_failed", "response_read_failed")
	out.Reason = claudeSignatureEnum(in.Reason, "disabled", "passthrough", "output_started", "attempt_limit", "budget_exhausted", "body_unchanged", "context_canceled", "build_failed", "transport_failed", "empty_response", "response_read_failed", "stream_incomplete")
	out.Attempts = min(max(in.Attempts, 0), 2)
	if out.ElapsedMs < 0 {
		out.ElapsedMs = 0
	} else if limit := int64(24 * time.Hour / time.Millisecond); out.ElapsedMs > limit {
		out.ElapsedMs = limit
	}
	out.Inbound = sanitizeClaudeSignatureSnapshot(in.Inbound)
	out.FirstWire = sanitizeClaudeSignatureSnapshot(in.FirstWire)
	return &out
}

func claudeSignatureEnum(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return ""
}

func sanitizeClaudeSignatureSnapshot(in ClaudeSignatureSnapshot) ClaudeSignatureSnapshot {
	out := ClaudeSignatureSnapshot{TotalCount: min(max(in.TotalCount, 0), 1_000_000)}
	for _, signature := range in.Signatures {
		if len(out.Signatures) == claudeSignatureFingerprintLimit {
			break
		}
		if !claudeSignaturePathPattern.MatchString(signature.Path) || !claudeSignatureHashPattern.MatchString(signature.SHA256) {
			continue
		}
		signature.Length = min(max(signature.Length, 0), 100*1024*1024)
		out.Signatures = append(out.Signatures, signature)
	}
	out.OmittedCount = max(out.TotalCount-len(out.Signatures), 0)
	return out
}
