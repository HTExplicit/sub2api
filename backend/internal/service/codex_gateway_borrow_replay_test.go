//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func borrowReplayFixture() CodexBorrowDiagnosticRequest {
	return CodexBorrowDiagnosticRequest{Scenario: "codex_replay", Mode: "ordinary", Transport: "http", AccountID: 2, Model: "gpt-6-astra", ServiceTier: "default", Replay: &CodexBorrowReplay{
		Body:    json.RawMessage(`{"model":"gpt-6-astra","stream":true,"store":false,"service_tier":"default","reasoning":{"effort":"xhigh"},"prompt_cache_key":"codex-acceptance-synthetic","input":[{"role":"user","content":[{"type":"input_text","text":"synthetic task"}]}],"tools":[{"type":"function","name":"read_file","parameters":{"type":"object","properties":{}}}]}`),
		Headers: map[string]string{"session-id": "codex-acceptance-synthetic", "thread-id": "codex-acceptance-synthetic", "User-Agent": "Codex Desktop/0.162.0-alpha.17.2", "x-codex-turn-state": "previous-business-state"},
	}}
}

func TestCodexGatewayBorrowReplayBoundaries(t *testing.T) {
	require.NoError(t, validateCodexBorrowReplay(borrowReplayFixture()))
	for _, tc := range []struct {
		name string
		edit func(*CodexBorrowDiagnosticRequest)
	}{
		{"credentials", func(r *CodexBorrowDiagnosticRequest) { r.Replay.Headers["Authorization"] = "Bearer other" }},
		{"cookies", func(r *CodexBorrowDiagnosticRequest) { r.Replay.Headers["Cookie"] = "__oailb=other" }},
		{"ambiguous header", func(r *CodexBorrowDiagnosticRequest) { r.Replay.Headers["Session-Id"] = "another" }},
		{"real session", func(r *CodexBorrowDiagnosticRequest) {
			r.Replay.Body = json.RawMessage(strings.ReplaceAll(string(r.Replay.Body), "codex-acceptance-synthetic", "real-user-session"))
		}},
		{"wrong model", func(r *CodexBorrowDiagnosticRequest) { r.Model = "gpt-6.1-sol" }},
		{"different history anchor", func(r *CodexBorrowDiagnosticRequest) {
			r.Replay.Body = append([]byte(`{"previous_response_id":"real-user-response",`), r.Replay.Body[1:]...)
		}},
		{"duplicate body key", func(r *CodexBorrowDiagnosticRequest) {
			r.Replay.Body = append([]byte(`{"model":"gpt-6-astra",`), r.Replay.Body[1:]...)
		}},
		{"implicit mode", func(r *CodexBorrowDiagnosticRequest) { r.Mode = "" }},
		{"wrong scenario", func(r *CodexBorrowDiagnosticRequest) { r.Scenario = "short" }},
		{"oversize", func(r *CodexBorrowDiagnosticRequest) {
			r.Replay.Body = json.RawMessage(strings.Repeat(" ", 1<<20) + string(r.Replay.Body))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := borrowReplayFixture()
			tc.edit(&request)
			require.Error(t, validateCodexBorrowReplay(request))
		})
	}
}

func TestCodexGatewayBorrowReplayPreservesTasksAndReturnsBusinessState(t *testing.T) {
	for _, id := range []int64{1, 2} {
		account := borrowCoreAccount(id)
		generator, repo := newPelicanGeneratorForTest(account, &pelicanGeneratorUpstream{})
		gateway := generator.openaiGatewayService
		var calls int
		gateway.httpUpstream = borrowCoreProbe(func(req *http.Request, _ string, accountID int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
			calls++
			require.Equal(t, id, accountID)
			body := borrowCoreBody(t, req)
			require.Equal(t, "xhigh", gjson.GetBytes(body, "reasoning.effort").String())
			require.Equal(t, "default", gjson.GetBytes(body, "service_tier").String())
			require.Equal(t, "synthetic task", gjson.GetBytes(body, "input.0.content.0.text").String())
			require.Equal(t, "read_file", gjson.GetBytes(body, "tools.0.name").String())
			require.Equal(t, "previous-business-state", req.Header.Get("x-codex-turn-state"))
			_, finish, err := PreparePelicanHTTPRequest(req, accountID, n, "http")
			require.NoError(t, err)
			raw := `data: {"type":"response.completed","response":{"id":"synthetic-response","model":"gpt-6-astra","status":"completed","output":[{"type":"function_call","name":"read_file","call_id":"test-call","arguments":"{}"}]}}` + "\n\n"
			return finish(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Codex-Turn-State": {"actual-business-state"}}, Body: io.NopCloser(strings.NewReader(raw))}, nil)
		})
		s := NewCodexGatewayBorrowService(nil, repo, gateway, nil, nil)
		s.publishConfig(CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2}, Models: []string{"gpt-6-astra"}}, false)
		request := borrowReplayFixture()
		request.AccountID = id
		var result *CodexBorrowDiagnosticResult
		err := s.Diagnose(context.Background(), request, func(e CodexBorrowDiagnosticEvent) {
			if e.Result != nil {
				result = e.Result
			}
		})
		s.Stop()
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.NotNil(t, result)
		require.True(t, result.Dispatched)
		require.True(t, result.Completed, result.Error)
		require.False(t, result.Applied)
		require.Equal(t, "actual-business-state", result.TurnState)
		require.Contains(t, result.RawResponse, "function_call")
	}
}
