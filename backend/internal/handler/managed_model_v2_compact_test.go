//go:build unit

package handler

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestManagedModelV2ExecutorPreservesCompactIngressBeforeBranchSelection(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantPath string
	}{
		{"legacy body signal", `{"model":"claude-fable-5.1","stream":false,"input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`, "/v1/responses/compact"},
		{"native v2 streaming", `{"model":"claude-fable-5.1","stream":true,"input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`, "/v1/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagedExecutorFixture(t, []string{service.PlatformOpenAI, service.PlatformOpenAI}, []managedExecutorBranch{
				{account: 0, wire: "chat_completions", target: "wrong-chat-target"}, {account: 1, wire: "responses", target: "compact-target"},
			})
			f.upstream.respond = func(call managedExecutorCall, n int) *http.Response {
				if call.path == "/v1/responses/compact" {
					return managedExecutorResponse(http.StatusOK, "application/json", `{"id":"resp_compact_offline","object":"response.compaction","output":[],"usage":{"input_tokens":4,"output_tokens":2}}`)
				}
				return managedExecutorSuccess(call, n)
			}
			response := f.request("/v1/responses", tc.body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			calls := f.upstream.snapshot()
			require.Len(t, calls, 1, "normalization must happen before selecting any branch")
			require.Equal(t, tc.wantPath, calls[0].path)
			require.Equal(t, "compact-target", calls[0].model)
			if tc.name == "legacy body signal" {
				require.False(t, gjson.GetBytes(calls[0].body, "stream").Bool())
			}
			f.requireOnce(t, calls[0].accountID)
		})
	}
}
