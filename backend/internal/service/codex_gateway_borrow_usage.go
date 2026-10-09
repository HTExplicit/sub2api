package service

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/tidwall/gjson"
)

type CodexGatewayBorrowUsage struct {
	Dispatched       bool       `json:"dispatched"`
	FailureStage     string     `json:"failure_stage,omitempty"`
	ClientRequestID  string     `json:"client_request_id,omitempty"`
	GatewayRequestID string     `json:"gateway_request_id,omitempty"`
	AttemptCount     uint64     `json:"attempt_count"`
	BlockedCount     uint64     `json:"blocked_count"`
	AccountID        int64      `json:"account_id"`
	Model            string     `json:"model"`
	Transport        string     `json:"transport"`
	Origin           string     `json:"origin"`
	Applied          bool       `json:"applied"`
	Reason           string     `json:"reason"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	Outcome          string     `json:"outcome"`
	RequestID        string     `json:"request_id,omitempty"`
	ReportedModel    string     `json:"reported_model,omitempty"`
	Count            uint64     `json:"count"`
	AppliedCount     uint64     `json:"applied_count"`
}

type codexBorrowUsageKey struct {
	account                  int64
	model, transport, origin string
}

// Only configured pairs are retained, one last observation per protocol/purpose.
// Payloads, credentials and routing cookies are never copied into this index.
func (s *CodexGatewayBorrowService) beginUsage(ctx context.Context, accountID int64, model, transport string, applied bool) *codexBorrowUsageTracker {
	t := codexBorrowUsageFromContext(ctx)
	if t == nil || t.service != s || t.key.account != accountID || t.key.model != model || t.key.transport != transport || !t.pendingDispatch() {
		t = s.beginAttempt(ctx, accountID, model, transport)
	}
	t.dispatched(applied)
	return t
}

func (t *codexBorrowUsageTracker) pendingDispatch() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.finished && !t.row.Dispatched
}

type codexBorrowUsageContextKey struct{}

func codexBorrowUsageFromContext(ctx context.Context) *codexBorrowUsageTracker {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(codexBorrowUsageContextKey{}).(*codexBorrowUsageTracker)
	return t
}

func (s *CodexGatewayBorrowService) beginAttempt(ctx context.Context, accountID int64, model, transport string) *codexBorrowUsageTracker {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.config.TargetAccountIDs, accountID) || !slices.Contains(s.config.Models, model) {
		return nil
	}
	origin := "business"
	if IsPelicanGeneration(ctx) || borrowDiagnosticFromContext(ctx) != nil {
		origin = "diagnostic"
	}
	key := codexBorrowUsageKey{accountID, model, transport, origin}
	previous := s.usage[key]
	row := CodexGatewayBorrowUsage{AccountID: accountID, Model: model, Transport: transport, Origin: origin, StartedAt: time.Now(), Outcome: "preparing", Reason: "preparing", Count: previous.Count, AppliedCount: previous.AppliedCount, AttemptCount: previous.AttemptCount + 1, BlockedCount: previous.BlockedCount}
	row.ClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
	row.GatewayRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	if !s.config.Enabled {
		row.Reason = "disabled"
	}
	if s.usage == nil {
		s.usage = make(map[codexBorrowUsageKey]CodexGatewayBorrowUsage)
	}
	s.usage[key] = row
	return &codexBorrowUsageTracker{service: s, key: key, row: row, revision: s.revision}
}

// Counter changes apply even when a newer attempt owns the displayed row. A
// late finish may not overwrite that newer request or roll its counters back.
func (t *codexBorrowUsageTracker) publish(sent, applied, blocked uint64) {
	t.service.mu.Lock()
	defer t.service.mu.Unlock()
	current, ok := t.service.usage[t.key]
	if !ok || t.service.revision != t.revision {
		return
	}
	current.Count += sent
	current.AppliedCount += applied
	current.BlockedCount += blocked
	if current.AttemptCount == t.row.AttemptCount {
		t.row.Count, t.row.AppliedCount, t.row.BlockedCount = current.Count, current.AppliedCount, current.BlockedCount
		current = t.row
	}
	t.service.usage[t.key] = current
}

func (t *codexBorrowUsageTracker) dispatched(applied bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished || t.row.Dispatched {
		return
	}
	t.row.Dispatched, t.row.Applied, t.row.Outcome = true, applied, "sent"
	if t.row.Reason != "disabled" {
		t.row.Reason = "not_applied"
	}
	var appliedCount uint64
	if applied {
		t.row.Reason = "borrow_applied"
		appliedCount = 1
	}
	t.publish(1, appliedCount, 0)
}

func (t *codexBorrowUsageTracker) blocked(err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.finished || t.row.Dispatched {
		t.mu.Unlock()
		return
	}
	t.row.FailureStage, t.row.Reason = codexBorrowFailureStage(err)
	t.row.Outcome, t.terminal = "blocked", true
	// Mark finished while holding the tracker lock so repeated error owners
	// cannot count the same preparation rejection twice.
	t.finished = true
	now := time.Now()
	t.row.FinishedAt = &now
	t.publish(0, 0, 1)
	t.mu.Unlock()
}

type codexBorrowUsageTracker struct {
	mu       sync.Mutex
	terminal bool
	finished bool
	revision uint64
	service  *CodexGatewayBorrowService
	key      codexBorrowUsageKey
	row      CodexGatewayBorrowUsage
	once     sync.Once
}

func (t *codexBorrowUsageTracker) observe(message []byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished || t.terminal {
		return
	}
	typeName := gjson.GetBytes(message, "type").String()
	if id := gjson.GetBytes(message, "response.id").String(); id != "" {
		t.row.RequestID = id
	}
	if model := gjson.GetBytes(message, "response.model").String(); model != "" {
		t.row.ReportedModel = model
	}
	switch typeName {
	case "response.completed", "response.done":
		t.terminal = true
		if gjson.GetBytes(message, "response.status").String() == "completed" && !gjson.GetBytes(message, "response.error").IsObject() {
			t.row.Outcome = "completed"
		} else if gjson.GetBytes(message, "response.status").String() != "" || gjson.GetBytes(message, "response.error").IsObject() {
			t.row.Outcome = "upstream_error"
		} else {
			t.row.Outcome = "unobserved"
		}
	case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		t.terminal = true
		t.row.Outcome = "upstream_error"
	}
}

func (t *codexBorrowUsageTracker) finish(err error) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.finished {
			return
		}
		if !t.terminal {
			if err != nil {
				t.row.Outcome = "transport_error"
			} else {
				t.row.Outcome = "incomplete"
			}
		}
		now := time.Now()
		t.row.FinishedAt = &now
		t.finished = true
		t.publish(0, 0, 0)
	})
}

type codexBorrowUsageBody struct {
	mu sync.Mutex
	io.ReadCloser
	tracker *codexBorrowUsageTracker
	line    []byte
	err     error
}

func (b *codexBorrowUsageBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range p[:n] {
		if ch == '\n' {
			if strings.HasPrefix(string(b.line), "data:") {
				b.tracker.observe(b.line[5:])
			}
			b.line = b.line[:0]
		} else if len(b.line) < 1<<20 {
			b.line = append(b.line, ch)
		}
	}
	if err != nil {
		if len(b.line) > 5 && strings.HasPrefix(string(b.line), "data:") {
			b.tracker.observe(b.line[5:])
			b.line = nil
		}
		if err != io.EOF {
			b.err = err
		}
		b.tracker.finish(b.err)
	}
	return n, err
}

func (b *codexBorrowUsageBody) Close() error {
	err := b.ReadCloser.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = err
	}
	b.tracker.finish(b.err)
	return err
}

func (s *CodexGatewayBorrowService) trackHTTPResponse(t *codexBorrowUsageTracker, resp *http.Response, err error) (*http.Response, error) {
	if t == nil {
		return resp, err
	}
	if resp == nil {
		t.finish(err)
		return resp, err
	}
	t.mu.Lock()
	t.row.RequestID = resp.Header.Get("X-Request-ID")
	if resp.StatusCode >= 400 {
		t.row.Outcome = "upstream_error"
		t.terminal = true
	}
	t.mu.Unlock()
	if err != nil || resp.Body == nil {
		t.finish(err)
	} else {
		resp.Body = &codexBorrowUsageBody{ReadCloser: resp.Body, tracker: t}
	}
	return resp, err
}

func (t *codexBorrowUsageTracker) terminalObserved() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.terminal
}
