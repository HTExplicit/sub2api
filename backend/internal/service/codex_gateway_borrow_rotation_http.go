package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// Business failures only affect future selection. A dispatched generation is
// never replayed by node selection, including an incomplete or ambiguous stream.
func (b *codexBorrowNodeBinding) rejectBusiness(reason string, explicit bool) {
	if b == nil {
		return
	}
	s := b.owner
	s.mu.Lock()
	current := s.revision == b.revision
	cfg := s.config
	s.mu.Unlock()
	if !current {
		return
	}
	seconds := 60
	if explicit {
		seconds = cfg.NodeCooldownSeconds
		if seconds == 0 {
			seconds = 3600
		}
	}
	host := ""
	if explicit {
		host = b.gateway
	}
	now := time.Now()
	until := now.Add(time.Duration(seconds) * time.Second)
	cache := s.rotation.cache(b.model)
	cache.reject(b.accountID, b.node.identity, host, until, now)
	until = cache.blockedUntil(b.accountID, b.node.identity, b.gateway, now)
	key := codexGatewayBorrowTargetKey{b.accountID, b.model}
	b.child.mu.Lock()
	check := b.child.targets[key]
	check.result.Success = false
	check.result.Reason = reason
	check.result.RetryOnNextRequest = !explicit
	check.retryAfter = until
	check.acquisition.Phase = "failed"
	check.acquisition.Reason = reason
	check.acquisition.RetryAfter = &until
	b.child.targets[key] = check
	prefix := fmt.Sprintf("%d:%s:", b.accountID, b.model)
	for k := range b.child.qualifications {
		if strings.HasPrefix(k, prefix) {
			delete(b.child.qualifications, k)
		}
	}
	b.child.mu.Unlock()
	s.rotation.mu.Lock()
	if s.rotation.active[key] == b.node.identity {
		delete(s.rotation.active, key)
	}
	s.rotation.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision == b.revision && s.targets[key].result.NodeIdentity == b.node.identity {
		check.result.ProxyID = b.node.id
		check.result.NodeIdentity = b.node.identity
		check.result.Gateway = b.gateway
		s.targets[key] = check
		delete(s.rotationCandidates, key)
	}
}

func trackCodexBorrowNodeHTTP(req *http.Request, resp *http.Response, err error) (*http.Response, error) {
	b := borrowNodeBinding(req)
	if b == nil {
		return resp, err
	}
	if req.Context().Err() != nil {
		return resp, err
	}
	if err != nil || resp == nil {
		b.rejectBusiness("business_transport_failed", false)
		return resp, err
	}
	old, next := req.Header.Get("X-Codex-Turn-State"), resp.Header.Get("X-Codex-Turn-State")
	explicitRejected := false
	if req.URL.Path == "/backend-api/codex/responses" && old != "" && next != "" && old != next {
		b.rejectBusiness("business_state_changed", true)
		explicitRejected = true
	}
	if sent, e := req.Cookie("__oailb"); e == nil {
		for _, got := range resp.Cookies() {
			if got.Name == "__oailb" && (got.Value != sent.Value || got.MaxAge < 0 || (!got.Expires.IsZero() && !time.Now().Before(got.Expires))) {
				b.rejectBusiness("business_route_changed", true)
				explicitRejected = true
				break
			}
		}
	}
	if resp.StatusCode != http.StatusOK || resp.Body == nil {
		if !explicitRejected {
			b.rejectBusiness("business_http_failed", false)
		}
		return resp, err
	}
	// Compact/lite JSON has its own terminal and STATE protocol. Never turn
	// the absence of SSE framing on that path into a stream failure.
	if req.URL.Path == "/backend-api/codex/responses" && (resp.Header.Get("Content-Type") == "" || strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")) {
		resp.Body = &codexBorrowNodeBody{ReadCloser: resp.Body, binding: b, ctx: req.Context(), rejected: explicitRejected}
	}
	return resp, err
}

type codexBorrowNodeBody struct {
	io.ReadCloser
	binding           *codexBorrowNodeBinding
	ctx               context.Context
	mu                sync.Mutex
	line              []byte
	payload           []byte
	completed, failed bool
	rejected          bool
	once              sync.Once
}

func (b *codexBorrowNodeBody) observe() {
	line := strings.TrimSpace(string(b.line))
	b.line = nil
	if line != "" {
		if strings.HasPrefix(line, "data:") && len(b.payload) < 1<<20 {
			b.payload = append(b.payload, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
			b.payload = append(b.payload, '\n')
		}
		return
	}
	payload := strings.TrimSpace(string(b.payload))
	b.payload = nil
	if payload == "" || payload == "[DONE]" {
		return
	}
	if !gjson.Valid(payload) {
		b.failed = true
		return
	}
	kind := gjson.Get(payload, "type").String()
	switch kind {
	case "response.completed", "response.done":
		b.completed = gjson.Get(payload, "response.status").String() == "completed" && !gjson.Get(payload, "response.error").IsObject()
	case "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
		b.failed = true
	}
}

func (b *codexBorrowNodeBody) finish() {
	b.once.Do(func() {
		if !b.rejected && b.ctx.Err() == nil && (!b.completed || b.failed) {
			b.binding.rejectBusiness("business_stream_incomplete", false)
		}
	})
}

func (b *codexBorrowNodeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range p[:n] {
		if ch == '\n' {
			b.observe()
		} else if len(b.line) < 1<<20 {
			b.line = append(b.line, ch)
		}
	}
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *codexBorrowNodeBody) Close() error {
	err := b.ReadCloser.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finish()
	return err
}
