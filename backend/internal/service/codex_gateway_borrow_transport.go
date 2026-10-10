package service

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// Trace evidence, rather than error text, decides whether another connection is
// safe. An uninstrumented transport or any written HTTP header forbids retry.
// This wrapper is used only by isolated source/target observations.
type codexBorrowConnectionTrace struct {
	wrote            atomic.Bool
	failedBeforeHTTP atomic.Bool
	phase            atomic.Int32
}

func (t *codexBorrowConnectionTrace) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		ConnectStart:         func(_, _ string) { t.phase.Store(1); t.failedBeforeHTTP.Store(false) },
		ConnectDone:          func(_, _ string, err error) { t.failedBeforeHTTP.Store(err != nil) },
		TLSHandshakeStart:    func() { t.phase.Store(2); t.failedBeforeHTTP.Store(false) },
		TLSHandshakeDone:     func(_ tls.ConnectionState, err error) { t.failedBeforeHTTP.Store(err != nil) },
		GotConn:              func(httptrace.GotConnInfo) { t.phase.Store(3); t.failedBeforeHTTP.Store(false) },
		WroteHeaders:         func() { t.wrote.Store(true); t.phase.Store(4) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { t.wrote.Store(true); t.phase.Store(4) },
		GotFirstResponseByte: func() { t.wrote.Store(true); t.phase.Store(5) },
	}
}

func (t *codexBorrowConnectionTrace) name() string {
	switch t.phase.Load() {
	case 1:
		return "connect"
	case 2:
		return "tls_handshake"
	case 3:
		return "connected"
	case 4:
		return "request_write"
	case 5:
		return "response_headers"
	default:
		return "unobserved"
	}
}

type codexBorrowTransportError struct {
	phase    string
	attempts int
	cause    error
}

func (e *codexBorrowTransportError) Error() string {
	return fmt.Sprintf("borrow transport %s after %d attempt(s): %s", e.phase, e.attempts, e.cause)
}
func (e *codexBorrowTransportError) Unwrap() error { return e.cause }

func (s *CodexGatewayBorrowService) sendBorrowObservationHTTP(req *http.Request, proxy string, account *Account, profile *tlsfingerprint.Profile) (*http.Response, string, int, error) {
	current := req
	for attempt := 1; ; attempt++ {
		trace := &codexBorrowConnectionTrace{}
		current = current.WithContext(httptrace.WithClientTrace(req.Context(), trace.trace()))
		resp, err := s.upstream.DoWithTLS(current, proxy, account.ID, account.Concurrency, profile)
		if err == nil {
			return resp, trace.name(), attempt, nil
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		retry := attempt == 1 && resp == nil && trace.failedBeforeHTTP.Load() && !trace.wrote.Load() && req.GetBody != nil && req.Context().Err() == nil
		if !retry {
			return nil, trace.name(), attempt, &codexBorrowTransportError{trace.name(), attempt, err}
		}
		body, bodyErr := req.GetBody()
		if bodyErr != nil {
			return nil, trace.name(), attempt, &codexBorrowTransportError{trace.name(), attempt, err}
		}
		// Preparation and diagnostic accounting includes every physical attempt.
		if budgetErr := consumeBorrowDiagnosticRequest(req.Context()); budgetErr != nil {
			_ = body.Close()
			return nil, trace.name(), attempt, budgetErr
		}
		current = req.Clone(req.Context())
		current.Body = body
	}
}
