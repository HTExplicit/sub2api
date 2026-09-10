//go:build unit

package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestManagedModelV2ExecutorNativeMessagesPublicIdentity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			f := newManagedExecutorFixture(t, []string{service.PlatformAnthropic}, []managedExecutorBranch{
				{account: 0, wire: "messages", target: "provider/model"},
			})
			f.upstream.respond = func(_ managedExecutorCall, _ int) *http.Response {
				if stream {
					return managedExecutorAnthropicSuccess()
				}
				return managedExecutorResponse(http.StatusOK, "application/json", `{"id":"msg_offline_native","type":"message","role":"assistant","model":"provider/model","content":[{"type":"text","text":"managed recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`)
			}

			response := f.request("/v1/messages", fmt.Sprintf(`{"model":%q,"max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":%t}`, managedExecutorPublicModel, stream))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "managed recovered")
			require.Contains(t, response.Body.String(), `"model":"`+managedExecutorPublicModel+`"`)
			require.NotContains(t, response.Body.String(), `"model":"provider/model"`)
			require.NotContains(t, response.Body.String(), "s2pub-")

			calls := f.upstream.snapshot()
			require.Len(t, calls, 1, "native success must use one existing upstream forward")
			require.Equal(t, "/v1/messages", calls[0].path)
			require.Equal(t, "provider/model", calls[0].model, "client identity must not alter exact upstream mapping")
			f.requireOnce(t, f.repo.accounts[0].ID)
			require.Equal(t, 4, f.usage.logs[0].InputTokens)
			require.Equal(t, 2, f.usage.logs[0].OutputTokens)
			require.NotNil(t, f.usage.logs[0].UpstreamModel)
			require.Equal(t, "provider/model", *f.usage.logs[0].UpstreamModel)
			require.NotNil(t, f.usage.logs[0].UpstreamResponseModel)
			require.Equal(t, "provider/model", *f.usage.logs[0].UpstreamResponseModel, "upstream audit must observe the unmodified model")
		})
	}
}
