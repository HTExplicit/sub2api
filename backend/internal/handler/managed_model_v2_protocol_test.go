//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func managedExecutorAnthropicSuccess() *http.Response {
	return managedExecutorResponse(http.StatusOK, "text/event-stream", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_offline_native\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"provider/model\",\"content\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"managed recovered\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
}

func TestManagedModelV2ExecutorCNProviderUsesProvenWireAcrossIngress(t *testing.T) {
	for _, tc := range []struct {
		name, platform, wire, ingress, body, upstreamPath string
	}{
		{"deepseek messages to Responses", service.PlatformDeepseek, "messages", "/v1/responses", `{"model":"claude-fable-5.1","input":"hello","stream":false}`, "/v1/messages"},
		{"kimi chat to Messages", service.PlatformKimi, "chat_completions", "/v1/messages", `{"model":"claude-fable-5.1","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`, "/v1/chat/completions"},
		{"minimax responses to Chat", service.PlatformMiniMax, "responses", "/v1/chat/completions", `{"model":"claude-fable-5.1","messages":[{"role":"user","content":"hello"}],"stream":false}`, "/v1/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagedExecutorFixture(t, []string{tc.platform}, []managedExecutorBranch{{account: 0, wire: tc.wire, target: "provider/model"}})
			account := &f.repo.accounts[0]
			account.Credentials["api_protocol"] = service.APIProtocolAdaptive
			account.Credentials["api_base_urls"] = map[string]any{
				service.APIProtocolAnthropic: "https://messages.example.test", service.APIProtocolChatCompletions: "https://chat.example.test/v1", service.APIProtocolResponses: "https://responses.example.test/v1",
			}
			f.group.ManagedModelRoutes.Routes[0].Branches[0].Accounts[0].AccountFingerprint = service.ManagedModelAccountFingerprint(account)
			before, err := json.Marshal(account)
			require.NoError(t, err)
			f.upstream.respond = func(call managedExecutorCall, n int) *http.Response {
				if strings.HasSuffix(call.path, "/messages") {
					return managedExecutorAnthropicSuccess()
				}
				return managedExecutorSuccess(call, n)
			}
			response := f.request(tc.ingress, tc.body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "managed recovered")
			require.NotContains(t, response.Body.String(), "s2pub-")
			calls := f.upstream.snapshot()
			require.Len(t, calls, 1, "a proven CN wire must use one existing adapter, not recursively re-enter a handler")
			require.Equal(t, tc.upstreamPath, calls[0].path)
			require.Equal(t, "provider/model", calls[0].model)
			f.requireOnce(t, account.ID)
			require.Positive(t, f.usage.logs[0].ActualCost, "an explicitly priced managed model must charge its public card, not the CN or Claude fallback")
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "native protocol, per-protocol endpoints and private mappings must remain untouched")
		})
	}
}
