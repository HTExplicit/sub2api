//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// A real HTTP stream can deliver its final bytes together with cancellation
// while the gateway shuts down read-ahead after committing response.completed.
type borrowDiagnosticCancelledTail struct{ *strings.Reader }

func (r *borrowDiagnosticCancelledTail) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.Reader.Len() == 0 {
		return n, context.Canceled
	}
	return n, err
}
func (*borrowDiagnosticCancelledTail) Close() error { return nil }

func TestCodexBorrowDiagnoseHTTPTerminalAndDispatchEvidence(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "completed then read cancellation", false: "missing terminal"}[complete], func(t *testing.T) {
			account := borrowCoreAccount(2)
			generator, repo := newPelicanGeneratorForTest(account, &pelicanGeneratorUpstream{})
			gateway := generator.openaiGatewayService
			raw := borrowCoreSSE("gpt-6-astra", "OK")
			if !complete {
				raw = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n"
			}
			gateway.httpUpstream = borrowCoreProbe(func(req *http.Request, _ string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				_, finish, err := PreparePelicanHTTPRequest(req, id, n, "http")
				if err != nil {
					return nil, err
				}
				return finish(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &borrowDiagnosticCancelledTail{strings.NewReader(raw)}}, nil)
			})
			s := NewCodexGatewayBorrowService(nil, repo, gateway, borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				return borrowCoreResponse("gpt-6-astra", "OK", "stable", ""), nil
			}), nil)
			s.publishConfig(CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2}, Models: []string{"gpt-6-astra"}}, false)
			defer s.Stop()
			borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
			var results []CodexBorrowDiagnosticResult
			err := s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{AccountID: 2, Model: "gpt-6-astra", Transport: "http"}, func(e CodexBorrowDiagnosticEvent) {
				if e.Result != nil {
					results = append(results, *e.Result)
				}
			})
			require.NoError(t, err)
			require.Len(t, results, 2)
			for _, r := range results {
				require.Equal(t, complete, r.Completed, r.Error)
				require.True(t, r.Dispatched)
			}
			require.False(t, results[0].Applied)
			require.True(t, results[1].Applied)
			usage := s.Status().RecentUsage
			require.Len(t, usage, 1)
			require.True(t, usage[0].Applied)
			require.EqualValues(t, 1, usage[0].AppliedCount)
			if complete {
				require.Equal(t, "completed", usage[0].Outcome)
			} else {
				require.NotEqual(t, "completed", usage[0].Outcome)
			}
		})
	}
}

var _ io.ReadCloser = (*borrowDiagnosticCancelledTail)(nil)

func TestCodexBorrowDiagnoseHTTPUsesRealSenderAndIsolatesModes(t *testing.T) {
	account := borrowCoreAccount(2)
	upstream := &pelicanGeneratorUpstream{body: pelicanResponsesSSE("gpt-6-astra", "completed", "")}
	generator, repo := newPelicanGeneratorForTest(account, upstream)
	var probes int
	s := NewCodexGatewayBorrowService(nil, repo, generator.openaiGatewayService, borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		probes++
		return borrowCoreResponse("gpt-6-astra", "OK", "stable", ""), nil
	}), nil)
	cfg := CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2}, Models: []string{"gpt-6-astra"}}
	s.publishConfig(cfg, false)
	defer s.Stop()
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	var events []CodexBorrowDiagnosticEvent
	err := s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{AccountID: 2, Model: "gpt-6-astra", Transport: "http"}, func(event CodexBorrowDiagnosticEvent) { events = append(events, event) })
	require.NoError(t, err)
	var results []CodexBorrowDiagnosticResult
	for _, event := range events {
		if event.Result != nil {
			results = append(results, *event.Result)
		}
	}
	require.Len(t, results, 2)
	require.True(t, results[0].Completed, results[0].Error)
	require.False(t, results[0].Applied)
	require.True(t, results[1].Completed, results[1].Error)
	require.True(t, results[1].Applied)
	require.Len(t, upstream.requests, 2)
	require.NotContains(t, upstream.requests[0].Header.Get("Cookie"), "__oailb")
	require.Contains(t, upstream.requests[1].Header.Get("Cookie"), "__oailb=synthetic-borrowed-cookie")
	require.Equal(t, cfg, s.ConfigSnapshot())
	require.EqualValues(t, len(upstream.requests)+probes, events[len(events)-1].Requests)
	require.LessOrEqual(t, events[len(events)-1].Requests, int32(8))
	require.Equal(t, "diagnostic", s.Status().RecentUsage[0].Origin)
}

func testCodexBorrowDiagnoseWS(t *testing.T, prewarm bool) {
	gateway, account, borrow, probes := borrowWSFixture(t, 2)
	gateway.cfg.Gateway.MaxLineSize = defaultMaxLineSize
	gateway.cfg.Gateway.OpenAIWS.PrewarmGenerateEnabled = prewarm
	repo := &borrowCoreAccounts{rows: map[int64]*Account{account.ID: account}}
	gateway.accountRepo = repo
	borrow.accounts = repo
	borrow.upstream = borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		return borrowCoreResponse("gpt-6-astra", "OK", "stable", ""), nil
	})
	completed := func(id string) []byte {
		raw, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "model": "gpt-6-astra", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "OK"}}}}, "usage": map[string]any{"input_tokens": 2, "output_tokens": 1}}})
		return raw
	}
	ordinary := &openAIWSCaptureConn{events: [][]byte{completed("ordinary-1"), completed("ordinary-2")}}
	borrowed := &openAIWSCaptureConn{events: [][]byte{completed("borrowed-1"), completed("borrowed-2")}}
	if prewarm {
		ordinary.events = append([][]byte{completed("ordinary-prewarm")}, ordinary.events...)
		borrowed.events = append([][]byte{completed("borrowed-prewarm")}, borrowed.events...)
	}
	dialer := &borrowWSDialer{conns: []openAIWSClientConn{ordinary, borrowed}}
	gateway.openaiWSPool.setClientDialerForTest(dialer)
	var requests int32
	var results []CodexBorrowDiagnosticResult
	err := borrow.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{AccountID: account.ID, Model: "gpt-6-astra", Transport: "ws"}, func(event CodexBorrowDiagnosticEvent) {
		requests = event.Requests
		if event.Result != nil {
			results = append(results, *event.Result)
		}
	})
	require.NoError(t, err)
	require.Len(t, results, 4)
	for _, result := range results {
		require.True(t, result.Completed, result.Error)
	}
	require.False(t, results[0].Applied)
	require.True(t, results[2].Applied)
	require.Len(t, dialer.headers, 2)
	expected := 2
	if prewarm {
		expected = 3
		require.EqualValues(t, 8, requests)
	}
	require.Len(t, ordinary.writes, expected)
	require.Len(t, borrowed.writes, expected)
	require.Equal(t, "ordinary-1", ordinary.writes[len(ordinary.writes)-1]["previous_response_id"])
	require.Equal(t, "borrowed-1", borrowed.writes[len(borrowed.writes)-1]["previous_response_id"])
	require.Empty(t, probes.requests)
	require.Empty(t, borrow.wsAnchors.entries, "diagnostics cannot create client anchors")
}

func TestCodexBorrowDiagnoseWSContinuesOwnSockets(t *testing.T) { testCodexBorrowDiagnoseWS(t, false) }
func TestCodexBorrowDiagnoseWSCountsConfiguredPrewarm(t *testing.T) {
	testCodexBorrowDiagnoseWS(t, true)
}
