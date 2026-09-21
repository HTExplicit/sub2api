package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type protocolChatWire struct {
	finishes []string
	tools    map[int]apicompat.ChatToolCall
	usages   []apicompat.ChatUsage
	errors   int
	done     int
}

func readProtocolChatWire(t *testing.T, body string) protocolChatWire {
	t.Helper()
	out := protocolChatWire{tools: make(map[int]apicompat.ChatToolCall)}
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			out.done++
			continue
		}
		var envelope struct {
			Error *struct{ Code, Message string } `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(payload), &envelope))
		if envelope.Error != nil {
			out.errors++
			require.Equal(t, "upstream_protocol_error", envelope.Error.Code)
			require.Equal(t, "Upstream Responses function-call stream is incomplete or inconsistent", envelope.Error.Message)
			continue
		}
		var chunk apicompat.ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		if chunk.Usage != nil {
			out.usages = append(out.usages, *chunk.Usage)
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				out.finishes = append(out.finishes, *choice.FinishReason)
			}
			for _, delta := range choice.Delta.ToolCalls {
				require.NotNil(t, delta.Index)
				tool := out.tools[*delta.Index]
				tool.ID += delta.ID
				tool.Function.Name += delta.Function.Name
				tool.Function.Arguments += delta.Function.Arguments
				out.tools[*delta.Index] = tool
			}
		}
	}
	return out
}

func protocolAnthropicStream(arguments string, closeTool bool) string {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","content":[],"model":"fixture","usage":{"input_tokens":13}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_fixture","name":"lookup","input":{}}}`,
	}
	delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": arguments}})
	events = append(events, string(delta))
	if closeTool {
		events = append(events,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":19}}`,
			`{"type":"message_stop"}`,
		)
	} else {
		// The malformed tool is not closed until the real EOF finalizer runs.
		events = append(events, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":19}}`)
	}
	var stream strings.Builder
	for _, event := range events {
		var kind struct{ Type string }
		_ = json.Unmarshal([]byte(event), &kind)
		stream.WriteString("event: " + kind.Type + "\ndata: " + event + "\n\n")
	}
	return stream.String()
}

func TestAnthropicChatCallersSurfaceProtocolFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, native := range []bool{false, true} {
		name := map[bool]string{false: "anthropic_gateway", true: "native_anthropic"}[native]
		for _, mode := range []string{"item_done", "eof_finalize", "valid_control"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				args := `{"x":`
				if mode == "valid_control" {
					args = `{"x":1}`
				}
				resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(protocolAnthropicStream(args, mode != "eof_finalize")))}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				var input, output int
				var err error
				if native {
					var result *OpenAIForwardResult
					result, err = (&OpenAIGatewayService{}).handleCCStreamingFromNativeAnthropic(resp, c, "fixture", "fixture", "fixture", nil, time.Now(), true)
					require.NotNil(t, result)
					input, output = result.Usage.InputTokens, result.Usage.OutputTokens
				} else {
					var result *ForwardResult
					result, err = (&GatewayService{}).handleCCStreamingFromAnthropic(resp, c, "fixture", "fixture", nil, time.Now(), true)
					require.NotNil(t, result)
					input, output = result.Usage.InputTokens, result.Usage.OutputTokens
				}
				require.Equal(t, 13, input)
				require.Equal(t, 19, output, "usage after the bad item must still be drained")
				wire := readProtocolChatWire(t, rec.Body.String())
				require.Equal(t, 1, wire.done)
				require.Equal(t, "call_fixture", wire.tools[0].ID)
				require.Equal(t, "lookup", wire.tools[0].Function.Name)
				require.Equal(t, args, wire.tools[0].Function.Arguments)
				if mode == "valid_control" {
					require.NoError(t, err)
					require.Zero(t, wire.errors)
					require.Equal(t, []string{"tool_calls"}, wire.finishes)
					require.Len(t, wire.usages, 1)
					require.Equal(t, 13, wire.usages[0].PromptTokens)
					require.Equal(t, 19, wire.usages[0].CompletionTokens)
				} else {
					assertProtocolChatFailed(t, c, wire, err)
				}
			})
		}
	}
}

func assertProtocolChatFailed(t *testing.T, c *gin.Context, wire protocolChatWire, err error) {
	t.Helper()
	require.Error(t, err, "the actual caller must not report successful delivery")
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "corruption after output commitment must not request a retry")
	require.True(t, IsResponseCommitted(c), "the handler must not append another error response")
	require.Equal(t, 1, wire.errors)
	require.Equal(t, 1, wire.done)
	require.Empty(t, wire.finishes)
	require.Empty(t, wire.usages)
}

func TestNativeAnthropicChatProtocolGuardPreservesDisconnectDrain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The wire is valid, but the client disconnects after the first argument
	// prefix. The caller deliberately stops converting and only drains usage.
	body := protocolAnthropicStream(`{"x":`, true)
	body = strings.Replace(body, "event: content_block_stop\n", "event: content_block_delta\ndata: "+
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"1}"}}`+"\n\nevent: content_block_stop\n", 1)
	resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Writer = &antigravityFailingWriter{ResponseWriter: c.Writer, failAfter: 2}
	result, err := (&OpenAIGatewayService{}).handleCCStreamingFromNativeAnthropic(resp, c, "fixture", "fixture", "fixture", nil, time.Now(), true)
	require.NoError(t, err, "an intentionally unconverted tail is not evidence of upstream corruption")
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 19, result.Usage.OutputTokens)
}

func TestGeminiChatCallerSurfacesProtocolFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"closed_by_text", "eof_finalize", "valid_control"} {
		t.Run(mode, func(t *testing.T) {
			args := `{"x":`
			if mode == "valid_control" {
				args = `{"x":1}`
			}
			encoded, err := json.Marshal(args)
			require.NoError(t, err)
			body := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":` + string(encoded) + `}}]}}],"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":2}}` + "\n\n"
			if mode == "closed_by_text" {
				body += `data: {"candidates":[{"content":{"parts":[{"text":"after invalid tool"}]}}]}` + "\n\n"
			}
			body += `data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":19}}` + "\n\n"
			resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			result, err := (&GeminiMessagesCompatService{}).handleChatCompletionsStreamingResponseFromGemini(c, resp, time.Now(), "fixture", false, true)
			require.NotNil(t, result)
			require.Equal(t, 13, result.usage.InputTokens)
			require.Equal(t, 19, result.usage.OutputTokens)
			wire := readProtocolChatWire(t, rec.Body.String())
			require.Equal(t, args, wire.tools[0].Function.Arguments)
			require.Equal(t, "lookup", wire.tools[0].Function.Name)
			if mode == "valid_control" {
				require.NoError(t, err)
				require.Zero(t, wire.errors)
				require.Equal(t, 1, wire.done)
				require.Equal(t, []string{"tool_calls"}, wire.finishes)
				require.Len(t, wire.usages, 1)
				require.Equal(t, 13, wire.usages[0].PromptTokens)
				require.Equal(t, 19, wire.usages[0].CompletionTokens)
			} else {
				assertProtocolChatFailed(t, c, wire, err)
			}
		})
	}
}

func TestAntigravityChatCallerSurfacesProtocolFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"duplicate_call", "read_error_after_failure", "valid_control"} {
		t.Run(mode, func(t *testing.T) {
			secondID := "call_fixture"
			if mode == "valid_control" {
				secondID = "call_second"
			}
			body := `data: {"response":{"responseId":"resp_fixture","candidates":[{"content":{"parts":[{"functionCall":{"id":"call_fixture","name":"lookup","args":{"x":1}}},{"functionCall":{"id":"` + secondID + `","name":"lookup","args":{"x":2}}}]}}],"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":2}}}` + "\n\n" +
				`data: {"response":{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":19}}}` + "\n\n"
			var reader io.ReadCloser = io.NopCloser(strings.NewReader(body))
			if mode == "read_error_after_failure" {
				reader = &antigravityCompatErrorReader{data: []byte(body), err: io.ErrUnexpectedEOF}
			}
			resp := &http.Response{Header: make(http.Header), Body: reader}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			result, err := (&AntigravityGatewayService{}).handleChatCompletionsStreamingFromAntigravity(c, resp, time.Now(), "fixture", true)
			require.NotNil(t, result)
			require.Equal(t, 13, result.usage.InputTokens)
			require.Equal(t, 19, result.usage.OutputTokens)
			wire := readProtocolChatWire(t, rec.Body.String())
			require.Equal(t, "call_fixture", wire.tools[0].ID)
			if mode == "valid_control" {
				require.NoError(t, err)
				require.Zero(t, wire.errors)
				require.Equal(t, 1, wire.done)
				require.Equal(t, []string{"tool_calls"}, wire.finishes)
				require.Len(t, wire.usages, 1)
				require.Equal(t, 13, wire.usages[0].PromptTokens)
				require.Equal(t, 19, wire.usages[0].CompletionTokens)
				require.Equal(t, "call_second", wire.tools[1].ID)
				require.Equal(t, `{"x":1}`, wire.tools[0].Function.Arguments)
				require.Equal(t, `{"x":2}`, wire.tools[1].Function.Arguments)
			} else {
				assertProtocolChatFailed(t, c, wire, err)
			}
		})
	}
}

func TestResponsesAgentMessageRoutingPreservesChatEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		native     bool
	}{
		{"supported_agent_task", `{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Task:\n"},{"type":"text","text":"fixture task"}]}]}`, false},
		{"encrypted_agent_task", `{"input":[{"type":"agent_message","content":[{"type":"encrypted_content","encrypted_content":"opaque-fixture"}]}]}`, true},
		{"mixed_encrypted_agent_task", `{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Task:"},{"type":"encrypted_content","encrypted_content":"opaque-fixture"}]}]}`, true},
		{"agent_image", `{"input":[{"type":"agent_message","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`, true},
		{"unknown_item", `{"input":[{"type":"future_agent_state","content":"fixture"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.native, OpenAIResponsesRequireNativeUpstream([]byte(tc.body)))
		})
	}
}
