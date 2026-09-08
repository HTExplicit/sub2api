package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func capabilityProbeToolResponse(protocol string) string {
	switch protocol {
	case AccountCapabilityProtocolResponses:
		return `{"status":"completed","output":[{"type":"reasoning","encrypted_content":"opaque-private-state","summary":[]},{"type":"function_call","status":"completed","call_id":"call_one","name":"capability_ping","arguments":"{\"value\":\"ok\"}"}],"usage":{"input_tokens":7,"output_tokens":5}}`
	case AccountCapabilityProtocolChatCompletions:
		return `{"choices":[{"index":0,"message":{"role":"assistant","content":"","reasoning_content":"opaque-private-state","tool_calls":[{"id":"call_one","type":"function","function":{"name":"capability_ping","arguments":"{\"value\":\"ok\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":5}}`
	default:
		return `{"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"private-thought","signature":"opaque-private-state"},{"type":"tool_use","id":"call_one","name":"capability_ping","input":{"value":"ok"}}],"stop_reason":"tool_use","usage":{"input_tokens":7,"output_tokens":5}}`
	}
}

func TestAccountCapabilityProbeToolRoundtripRequiresBothActualRequests(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, body []byte, count int) (*http.Response, error) {
				require.Equal(t, "ExactCase/Model-ssvip", gjson.GetBytes(body, "model").String())
				require.Nil(t, req.GetBody)
				if count == 1 {
					require.True(t, gjson.GetBytes(body, "tools").IsArray())
					return capabilityProbeResponse(200, capabilityProbeToolResponse(protocol), false), nil
				}
				require.Equal(t, 2, count, "tool profiles never send a third request")
				require.Contains(t, string(body), "opaque-private-state", "request-local provider history must survive the tool result roundtrip")
				require.Contains(t, string(body), "call_one")
				switch protocol {
				case "responses":
					require.Equal(t, "function_call_output", gjson.GetBytes(body, "input.3.type").String())
					require.Equal(t, "call_one", gjson.GetBytes(body, "input.3.call_id").String())
					require.JSONEq(t, `{"value":"ok"}`, gjson.GetBytes(body, "input.3.output").String())
					require.Equal(t, "none", gjson.GetBytes(body, "tool_choice").String())
					require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
				case "chat_completions":
					require.Equal(t, "tool", gjson.GetBytes(body, "messages.2.role").String())
					require.Equal(t, "call_one", gjson.GetBytes(body, "messages.2.tool_call_id").String())
					require.Equal(t, "none", gjson.GetBytes(body, "tool_choice").String())
				case "messages":
					require.Equal(t, "tool_result", gjson.GetBytes(body, "messages.2.content.0.type").String())
					require.Equal(t, "call_one", gjson.GetBytes(body, "messages.2.content.0.tool_use_id").String())
				}
				return capabilityProbeResponse(200, capabilityProbeTextResponse(protocol), false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), capabilityProbeTestAccount(), "ExactCase/Model-ssvip", protocol, "tool_roundtrip")
			require.Equal(t, "alive", result.Status)
			require.Equal(t, "tool_roundtrip_passed", result.Classification)
			require.Equal(t, 2, result.RequestCount)
			require.Len(t, result.Attempts, 2)
			require.Equal(t, "tool_call_completed", result.Attempts[0].Classification)
			require.Equal(t, "text_completed", result.Attempts[1].Classification)
			require.EqualValues(t, 10, *result.Usage.InputTokens)
			require.EqualValues(t, 6, *result.Usage.OutputTokens)
			wire, err := json.Marshal(result)
			require.NoError(t, err)
			for _, private := range []string{"opaque-private-state", "private-thought", "call_one", "test-secret"} {
				require.NotContains(t, string(wire), private)
			}
		})
	}
}

func TestAccountCapabilityProbeToolContinuationRejectsInvalidHistory(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			observation, err := accountCapabilityReadObservation(strings.NewReader(capabilityProbeToolResponse(protocol)), protocol, false)
			require.NoError(t, err)
			_, expectedTool := observation.singleExpectedTool()
			require.True(t, expectedTool)
			for _, invalid := range []map[string]any{nil, {}, {"input": "wrong type", "messages": "wrong type"}} {
				payload, ok := accountCapabilityToolContinuation(invalid, protocol, observation)
				require.False(t, ok)
				require.Nil(t, payload)
			}
		})
	}
}

func TestAccountCapabilityProbeToolRoundtripStopsAtFirstFailedEvidence(t *testing.T) {
	tests := []struct {
		name, first, second, classification string
		count                               int
	}{
		{"text is not tool success", capabilityProbeTextResponse("responses"), "", "tool_contract_mismatch", 1},
		{"wrong tool", strings.ReplaceAll(capabilityProbeToolResponse("responses"), "capability_ping", "unexpected_tool"), "", "tool_contract_mismatch", 1},
		{"wrong fixed argument", strings.ReplaceAll(capabilityProbeToolResponse("responses"), `\"ok\"`, `\"unexpected\"`), "", "tool_contract_mismatch", 1},
		{"unknown arguments", `{"status":"completed","output":[{"type":"function_call","call_id":"call_one","name":"capability_ping","arguments":"{\"value\":\"ok\",\"extra\":true}"}]}`, "", "tool_contract_mismatch", 1},
		{"duplicate argument keys", `{"status":"completed","output":[{"type":"function_call","call_id":"call_one","name":"capability_ping","arguments":"{\"value\":\"bad\",\"value\":\"ok\"}"}]}`, "", "tool_contract_mismatch", 1},
		{"additional malformed tool is not omitted", `{"status":"completed","output":[{"type":"function_call","call_id":"call_one","name":"capability_ping","arguments":"{\"value\":\"ok\"}"},{"type":"function_call","call_id":"bad","name":"capability_ping","arguments":"{"}]}`, "", "tool_contract_mismatch", 1},
		{"second turn repeats tool", capabilityProbeToolResponse("responses"), capabilityProbeToolResponse("responses"), "tool_continuation_incomplete", 2},
		{"second turn empty", capabilityProbeToolResponse("responses"), `{"status":"completed","output":[]}`, "no_semantic_output", 2},
		{"second turn lacks authority", capabilityProbeToolResponse("responses"), `{"status":"in_progress","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`, "incomplete_response", 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := &capabilityProbeFakeUpstream{do: func(_ *http.Request, _ []byte, count int) (*http.Response, error) {
				if count == 1 {
					return capabilityProbeResponse(200, test.first, false), nil
				}
				return capabilityProbeResponse(200, test.second, false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), capabilityProbeTestAccount(), "Exact", "responses", "tool_roundtrip")
			require.NotEqual(t, "alive", result.Status)
			require.Equal(t, test.classification, result.Classification)
			require.Equal(t, test.count, result.RequestCount)
			require.Len(t, upstream.requests, test.count)
		})
	}
}

func TestAccountCapabilityProbeStreamingToolHistoryPreservesOrderedProtocolBlocks(t *testing.T) {
	tests := []struct{ protocol, first string }{
		{"responses", "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"opaque-private-state\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_one\",\"name\":\"capability_ping\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
		{"chat_completions", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"opaque-private-state\",\"tool_calls\":[{\"index\":0,\"id\":\"call_one\",\"type\":\"function\",\"function\":{\"name\":\"capability_ping\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"ok\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"},
		{"messages", "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque-private-state\"}}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_one\",\"name\":\"capability_ping\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"value\\\":\\\"ok\\\"}\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
	}
	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			upstream := &capabilityProbeFakeUpstream{do: func(_ *http.Request, body []byte, count int) (*http.Response, error) {
				if count == 1 {
					return capabilityProbeResponse(200, test.first, true), nil
				}
				require.Contains(t, string(body), "opaque-private-state")
				require.Contains(t, string(body), "call_one")
				return capabilityProbeResponse(200, capabilityProbeTextResponse(test.protocol), false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), capabilityProbeTestAccount(), "Exact", test.protocol, "tool_roundtrip")
			require.Equal(t, "alive", result.Status)
			require.Equal(t, 2, result.RequestCount)
			require.True(t, result.Attempts[0].Streaming)
			require.False(t, result.Streaming, "both turns must be streaming before declaring the profile a streaming proof")
		})
	}
}
