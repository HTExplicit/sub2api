package service

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

type CodexGatewayBorrowUsage struct {
	AccountID     int64      `json:"account_id"`
	Model         string     `json:"model"`
	Transport     string     `json:"transport"`
	Origin        string     `json:"origin"`
	Applied       bool       `json:"applied"`
	Reason        string     `json:"reason"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Outcome       string     `json:"outcome"`
	RequestID     string     `json:"request_id,omitempty"`
	ReportedModel string     `json:"reported_model,omitempty"`
	Count         uint64     `json:"count"`
	AppliedCount  uint64     `json:"applied_count"`
}

type codexBorrowUsageKey struct {
	account                  int64
	model, transport, origin string
}

// Only configured pairs are retained, one last observation per protocol/purpose.
// Payloads, credentials and routing cookies are never copied into this index.
func (s *CodexGatewayBorrowService) beginUsage(ctx context.Context, accountID int64, model, transport string, applied bool) *codexBorrowUsageTracker {
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
	row := CodexGatewayBorrowUsage{AccountID: accountID, Model: model, Transport: transport, Origin: origin, Applied: applied, StartedAt: time.Now(), Outcome: "sent", Count: previous.Count + 1, AppliedCount: previous.AppliedCount}
	row.Reason = "not_applied"
	if applied {
		row.Reason = "borrow_applied"
		row.AppliedCount++
	} else if !s.config.Enabled {
		row.Reason = "disabled"
	}
	if s.usage == nil {
		s.usage = make(map[codexBorrowUsageKey]CodexGatewayBorrowUsage)
	}
	s.usage[key] = row
	return &codexBorrowUsageTracker{service: s, key: key, row: row, revision: s.revision}
}

type codexBorrowUsageTracker struct {
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
	typeName := gjson.GetBytes(message, "type").String()
	if id := gjson.GetBytes(message, "response.id").String(); id != "" {
		t.row.RequestID = id
	}
	if model := gjson.GetBytes(message, "response.model").String(); model != "" {
		t.row.ReportedModel = model
	}
	switch typeName {
	case "response.completed", "response.done":
		if gjson.GetBytes(message, "response.status").String() == "completed" && !gjson.GetBytes(message, "response.error").IsObject() {
			t.row.Outcome = "completed"
		} else if gjson.GetBytes(message, "response.status").String() != "" || gjson.GetBytes(message, "response.error").IsObject() {
			t.row.Outcome = "upstream_error"
		} else {
			t.row.Outcome = "unobserved"
		}
	case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		t.row.Outcome = "upstream_error"
	}
}

func (t *codexBorrowUsageTracker) finish(err error) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if err != nil && t.row.Outcome != "completed" {
			t.row.Outcome = "transport_error"
		} else if t.row.Outcome == "sent" {
			t.row.Outcome = "incomplete"
		}
		now := time.Now()
		t.row.FinishedAt = &now
		t.service.mu.Lock()
		defer t.service.mu.Unlock()
		// An older completion must not overwrite a newer attempt or cleared config.
		if current, ok := t.service.usage[t.key]; ok && t.service.revision == t.revision && current.Count == t.row.Count {
			t.service.usage[t.key] = t.row
		}
	})
}

type codexBorrowUsageBody struct {
	io.ReadCloser
	tracker *codexBorrowUsageTracker
	line    []byte
	err     error
}

func (b *codexBorrowUsageBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
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
	t.row.RequestID = resp.Header.Get("X-Request-ID")
	if resp.StatusCode >= 400 {
		t.row.Outcome = "upstream_error"
	}
	if err != nil || resp.Body == nil {
		t.finish(err)
	} else {
		resp.Body = &codexBorrowUsageBody{ReadCloser: resp.Body, tracker: t}
	}
	return resp, err
}
