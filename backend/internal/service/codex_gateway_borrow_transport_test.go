package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestCodexGatewayBorrowTransportRetryRequiresPreHTTPProof(t *testing.T) {
	for _, kind := range []string{"tls", "connect", "unobserved", "headers_written", "response_received", "budget_exhausted", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var count atomic.Int32
			count.Store(1)
			limit := int32(2)
			if kind == "budget_exhausted" {
				limit = 1
			}
			ctx = context.WithValue(ctx, codexBorrowDiagnosticContextKey{}, &codexBorrowDiagnostic{requests: &count, limit: limit})
			calls := 0
			service := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				require.Equal(t, "identical payload", string(borrowCoreBody(t, req)))
				if calls == 2 {
					return borrowCoreResponse("gpt-6-astra", "OK", ""), nil
				}
				trace := httptrace.ContextClientTrace(req.Context())
				switch kind {
				case "unobserved":
				case "connect":
					trace.ConnectStart("tcp", "synthetic")
					trace.ConnectDone("tcp", "synthetic", errors.New("connect failed"))
				default:
					trace.TLSHandshakeStart()
					trace.TLSHandshakeDone(tls.ConnectionState{}, errors.New("handshake failed"))
					if kind == "headers_written" {
						trace.WroteHeaders()
					}
					if kind == "cancelled" {
						cancel()
					}
				}
				if kind == "response_received" {
					return borrowCoreResponse("gpt-6-astra", "OK", ""), errors.New("late error")
				}
				return nil, errors.New("synthetic transport failure")
			})
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, strings.NewReader("identical payload"))
			resp, phase, attempts, err := service.sendBorrowObservationHTTP(req, "", borrowCoreAccount(2), nil)
			if kind == "tls" || kind == "connect" {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
				require.Equal(t, 2, attempts)
				require.Equal(t, int32(2), count.Load())
				_ = resp.Body.Close()
			} else {
				require.Error(t, err)
				require.Equal(t, 1, calls)
				require.Equal(t, 1, attempts)
			}
			if kind == "headers_written" {
				require.Equal(t, "request_write", phase)
			}
		})
	}
}
