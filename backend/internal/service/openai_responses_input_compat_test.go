package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSanitizeOpenAIResponsesOrphanToolOutputs(t *testing.T) {
	t.Run("named standalone inputs do not legitimize orphan results", func(t *testing.T) {
		named := map[string]any{"type": "function_call_output", "name": "send_message_to_thread", "namespace": "codex_app", "output": "delegation"}
		input := []any{
			named,
			map[string]any{"type": "function_call_output", "output": "missing name and call id"},
			map[string]any{"type": "function_call_output", "name": " ", "output": "blank name"},
			map[string]any{"type": "function_call_output", "name": "send_message_to_thread", "call_id": "missing", "output": "orphan result"},
			map[string]any{"type": "custom_tool_call_output", "name": "apply_patch", "output": "missing call id"},
		}
		reqBody := map[string]any{"input": input}

		require.True(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
		require.Equal(t, []any{named}, reqBody["input"])
	})

	for name, input := range map[string][]any{
		"matches regardless of item order": {
			map[string]any{"type": "tool_search_output", "call_id": "search_1", "output": "first"},
			map[string]any{"type": "tool_search_call", "id": "search_1", "query": "docs"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "custom_1", "output": "second"},
			map[string]any{"type": "item_reference", "id": "custom_1"},
		},
		"all matching output types": {
			map[string]any{"type": "function_call", "call_id": "function_1"},
			map[string]any{"type": "function_call_output", "call_id": "function_1", "output": "first"},
			map[string]any{"type": "mcp_tool_call", "call_id": "mcp_1"},
			map[string]any{"type": "mcp_tool_call_output", "call_id": "mcp_1", "output": "second"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			reqBody := map[string]any{"input": input}
			require.False(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
			require.Equal(t, input, reqBody["input"])
		})
	}
	t.Run("outputs do not legitimize each other", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "function_call_output", "call_id": "missing", "output": "one"},
			map[string]any{"type": "tool_search_output", "call_id": "missing", "output": "two"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "missing", "output": "three"},
			map[string]any{"type": "mcp_tool_call_output", "call_id": "missing", "output": "four"},
		}
		reqBody := map[string]any{"input": input}
		require.True(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
		require.Empty(t, reqBody["input"])
		normalizedInput, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.False(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, normalizedInput, false))
	})
	t.Run("previous response preserves remote calls", func(t *testing.T) {
		input := []any{map[string]any{"type": "function_call_output", "call_id": "remote", "output": "ok"}}
		reqBody := map[string]any{"input": input, "previous_response_id": "resp_1"}
		require.False(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, true))
		require.Equal(t, input, reqBody["input"])
	})
	t.Run("unrelated reference and compaction do not exempt an orphan", func(t *testing.T) {
		kept := []any{
			map[string]any{"type": "item_reference", "id": "different"},
			map[string]any{"type": "compaction", "encrypted_content": "opaque"},
		}
		input := append(append([]any{}, kept...), map[string]any{"type": "function_call_output", "call_id": "missing", "output": "orphan"})
		reqBody := map[string]any{"input": input}
		require.True(t, sanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
		require.Equal(t, kept, reqBody["input"])
	})
}

func TestValidateOpenAIResponsesToolOutputs(t *testing.T) {
	t.Run("named standalone input is safe for reasoning recovery", func(t *testing.T) {
		input := []any{map[string]any{"type": "function_call_output", "name": "send_message_to_thread", "namespace": "codex_app", "output": "delegation"}}
		require.NoError(t, validateOpenAIResponsesToolOutputs(input, false))
	})
	t.Run("preserves matches regardless of item order", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "tool_search_output", "call_id": "search_1", "output": "first"},
			map[string]any{"type": "tool_search_call", "id": "search_1", "query": "docs"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "custom_1", "output": "second"},
			map[string]any{"type": "item_reference", "id": "custom_1"},
		}
		reqBody := map[string]any{"input": input}

		require.NoError(t, validateOpenAIResponsesToolOutputs(input, false))
		require.Equal(t, input, reqBody["input"])
	})

	t.Run("outputs do not legitimize each other", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "function_call_output", "call_id": "missing", "output": "one"},
			map[string]any{"type": "tool_search_output", "call_id": "missing", "output": "two"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "missing", "output": "three"},
			map[string]any{"type": "mcp_tool_call_output", "call_id": "missing", "output": "four"},
		}
		reqBody := map[string]any{"input": input}

		err := validateOpenAIResponsesToolOutputs(input, false)
		var stateErr *UpstreamFailoverError
		require.ErrorAs(t, err, &stateErr)
		require.True(t, stateErr.IsOpenAIContinuationStateUnavailable())
		got, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Equal(t, input, got, "invalid history must remain intact, not turn into a different successful request")
	})

	t.Run("preserves all output variants with matching calls", func(t *testing.T) {
		pairs := []struct {
			callType   string
			outputType string
		}{
			{callType: "function_call", outputType: "function_call_output"},
			{callType: "tool_search_call", outputType: "tool_search_output"},
			{callType: "custom_tool_call", outputType: "custom_tool_call_output"},
			{callType: "mcp_tool_call", outputType: "mcp_tool_call_output"},
		}
		input := make([]any, 0, len(pairs)*2)
		for index, pair := range pairs {
			callID := string(rune('a' + index))
			input = append(input,
				map[string]any{"type": pair.callType, "call_id": callID},
				map[string]any{"type": pair.outputType, "call_id": callID, "output": "ok"},
			)
		}
		require.NoError(t, validateOpenAIResponsesToolOutputs(input, false))
	})

	t.Run("previous response may contain the missing call", func(t *testing.T) {
		input := []any{map[string]any{"type": "function_call_output", "call_id": "remote", "output": "ok"}}
		reqBody := map[string]any{"input": input, "previous_response_id": "resp_1"}

		require.NoError(t, validateOpenAIResponsesToolOutputs(input, true))
		require.Equal(t, input, reqBody["input"])
	})
	t.Run("unresolved reference belongs to upstream validation", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "item_reference", "id": "fc_remote"},
			map[string]any{"type": "function_call_output", "call_id": "call_remote", "output": "ok"},
		}
		require.NoError(t, validateOpenAIResponsesToolOutputs(input, false))
	})
}

func TestWebSocketCompatibilityOrphanCleanupAccountScope(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"gpt-5.5","input":[{"type":"message","role":"user","content":"hello"},{"type":"function_call_output","call_id":"missing","output":"orphan"}]}`)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			normalized, changed, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, &Account{Platform: PlatformOpenAI, Type: accountType}, false)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, int64(1), gjson.GetBytes(normalized, "input.#").Int())
			require.Equal(t, "hello", gjson.GetBytes(normalized, "input.0.content").String())
		})
	}
	for _, account := range []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformCindy, Type: AccountTypeAPIKey},
	} {
		t.Run(account.Platform+"_apikey", func(t *testing.T) {
			normalized, changed, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, account, false)
			require.NoError(t, err)
			require.False(t, changed)
			require.JSONEq(t, string(body), string(normalized))
		})
	}
}

func TestNormalizeOpenAIWSPassthroughSelectedCompatibilityPreservesRawItems(t *testing.T) {
	kept := []string{
		`{ "type" : "reasoning", "id":"rs_signed", "phase":"analysis", "encrypted_content":"opaque\\signed", "future":9007199254740993 }`,
		`{"type":"function_call_output","call_id":"call_one","output":"result\\nraw","future":1.2345678901234567890123456789}`,
		`{"type":"function_call","id":"fc_one","call_id":"call_one","name":"lookup","arguments":"{\"x\":1}"}`,
		`{"type":"item_reference","id":"remote_custom"}`,
		`{"type":"custom_tool_call_output","call_id":"remote_custom","output":"raw patch"}`,
		`{ "type":"function_call_output", "name":"send_message_to_thread", "namespace":"codex_app", "output":"delegation" }`,
		`{"type":"compaction","encrypted_content":"opaque_compaction","unknown":{"n":9007199254740993}}`,
		`null`,
	}
	orphan := `{"type":"function_call_output","call_id":"missing","output":"orphan"}`
	items := append(append([]string{}, kept[:2]...), orphan)
	items = append(items, kept[2:]...)
	body := []byte(`{"type":"response.create","model":"assistant","instructions":"keep raw instructions","commands":["native"],"reasoning":{"mode":"pro","context":"all_turns"},"future":9007199254740993,"input":[` + strings.Join(items, ",") + `]}`)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Type: accountType}
			normalized, changed, err := normalizeOpenAIWSPassthroughSelectedCompatibilityForModel(body, account, "gpt-6-astra")
			require.NoError(t, err)
			require.True(t, changed)
			gotItems := gjson.GetBytes(normalized, "input").Array()
			require.Len(t, gotItems, len(kept))
			for index, wantRaw := range kept {
				require.Equal(t, wantRaw, gotItems[index].Raw, "surviving item %d must stay byte-identical", index)
			}
			for _, field := range []string{"type", "model", "instructions", "commands", "reasoning", "future"} {
				require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(normalized, field).Raw, field)
			}
			var input, gotInput []any
			require.NoError(t, decodeOpenAIJSONUseNumber([]byte(gjson.GetBytes(body, "input").Raw), &input))
			require.NoError(t, decodeOpenAIJSONUseNumber([]byte(gjson.GetBytes(normalized, "input").Raw), &gotInput))
			typed := map[string]any{"input": input}
			require.True(t, sanitizeOpenAIResponsesOrphanToolOutputs(typed, input, false))
			require.Equal(t, typed["input"], gotInput, "raw and decoded adapters share the official orphan decision")
			again, changedAgain, err := normalizeOpenAIWSPassthroughSelectedCompatibilityForModel(normalized, account, "gpt-6-astra")
			require.NoError(t, err)
			require.False(t, changedAgain)
			require.Equal(t, normalized, again)
		})
	}
}

func TestNormalizeOpenAIWSPassthroughSelectedCompatibilityOnlyUsesSelectedRules(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"assistant","commands":["native"],"previous_response_id":"resp_previous","reasoning":{"mode":"pro","effort":"high","future":9007199254740993},"input":[ {"type":"function_call_output","call_id":"remote","output":"keep raw"} ]}`)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		account := &Account{Platform: PlatformOpenAI, Type: accountType}
		normalized, changed, err := normalizeOpenAIWSPassthroughSelectedCompatibilityForModel(body, account, "gpt-5.4")
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(normalized, "reasoning.mode").Exists())
		require.Equal(t, "high", gjson.GetBytes(normalized, "reasoning.effort").String())
		for _, field := range []string{"type", "model", "commands", "previous_response_id", "input", "reasoning.future"} {
			require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(normalized, field).Raw, field)
		}
		astraBody, changed, err := normalizeOpenAIWSPassthroughSelectedCompatibilityForModel(body, account, "gpt-6-astra")
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, astraBody)
	}
	for _, account := range []*Account{
		nil,
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformCindy, Type: AccountTypeAPIKey},
		{Platform: PlatformCindy, Type: AccountTypeOAuth},
	} {
		normalized, changed, err := normalizeOpenAIWSPassthroughSelectedCompatibilityForModel(body, account, "gpt-5.4")
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, normalized)
	}
}

func TestWebSocketCompatibilityPreservesNamedDelegation(t *testing.T) {
	for _, toolName := range []string{"create_thread", "send_message_to_thread"} {
		for _, withHistory := range []bool{false, true} {
			for _, previousResponseID := range []string{"", "resp_previous"} {
				name := fmt.Sprintf("%s/history=%t/previous=%t", toolName, withHistory, previousResponseID != "")
				t.Run(name, func(t *testing.T) {
					input := []any{}
					if withHistory {
						input = append(input,
							map[string]any{"type": "function_call", "call_id": "fc_history", "name": "lookup", "arguments": "{}"},
							map[string]any{"type": "function_call_output", "call_id": "fc_history", "output": "historical result"},
						)
					}
					input = append(input,
						map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Updated environment context."}}},
						map[string]any{"type": "function_call_output", "name": toolName, "namespace": "codex_app", "output": "<codex_delegation>\n  <source_thread_id>source-task</source_thread_id>\n  <input>Reply with DELEGATION_OK.</input>\n</codex_delegation>"},
					)
					reqBody := map[string]any{"type": "response.create", "model": "gpt-5.5", "input": input}
					if previousResponseID != "" {
						reqBody["previous_response_id"] = previousResponseID
					}
					body, err := json.Marshal(reqBody)
					require.NoError(t, err)
					account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}

					normalized, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, account, false)
					require.NoError(t, err)
					var got map[string]any
					require.NoError(t, json.Unmarshal(normalized, &got))
					require.Equal(t, input, got["input"], "preserve the delegation envelope, native type and history in order")
					require.Equal(t, reqBody["previous_response_id"], got["previous_response_id"])

					again, changed, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(normalized, account, false)
					require.NoError(t, err)
					require.False(t, changed, "normalization must be idempotent")
					require.JSONEq(t, string(normalized), string(again))
				})
			}
		}
	}
}

func TestOpenAIResponsesInputTextIsNeverSilentlyTruncated(t *testing.T) {
	atLimit := strings.Repeat("z", openAIResponsesInputTextMaxChars)
	oversized := strings.Repeat("a", openAIResponsesInputTextMaxChars) + "中"
	input := []any{
		map[string]any{"type": "function_call_output", "call_id": "limit", "output": atLimit},
		map[string]any{"type": "function_call_output", "call_id": "a", "output": oversized},
		map[string]any{"type": "tool_search_output", "call_id": "b", "output": oversized},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c", "output": oversized},
		map[string]any{"type": "mcp_tool_call_output", "call_id": "d", "output": oversized},
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "short"},
				map[string]any{"type": "input_text", "text": oversized},
			},
		},
	}
	reqBody := map[string]any{"input": input}

	require.False(t, truncateOpenAIResponsesInputText(reqBody))
	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, atLimit, first["output"])
	for _, rawItem := range input[1:5] {
		item, ok := rawItem.(map[string]any)
		require.True(t, ok)
		require.Equal(t, oversized, item["output"])
	}
	last, ok := input[5].(map[string]any)
	require.True(t, ok)
	content, ok := last["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	shortPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	oversizedPart, ok := content[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "short", shortPart["text"])
	require.Equal(t, oversized, oversizedPart["text"])
}

func TestOpenAIResponsesInputNeverRequestsPreemptiveTruncation(t *testing.T) {
	short := []byte(`{"input":[{"type":"function_call_output","output":"ok"}]}`)
	largeUnrelated := []byte(`{"input":"` + strings.Repeat("x", openAIResponsesInputTextMaxChars+1) + `"}`)
	largeOutput := []byte(`{"input":[{"type":"function_call_output","output":"` + strings.Repeat("x", openAIResponsesInputTextMaxChars+1) + `"}]}`)

	require.False(t, openAIResponsesInputMayNeedTruncation(short))
	require.False(t, openAIResponsesInputMayNeedTruncation(largeUnrelated))
	require.False(t, openAIResponsesInputMayNeedTruncation(largeOutput))
}

func TestOpenAIGatewayService_OAuthRejectsHTTPAnchorWithoutDroppingToolResult(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"previous_response_id":"resp_missing","input":[{"type":"function_call_output","call_id":"call_missing","output":"keep this result"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIOAuthNamespaceTestAccount(),
		body,
	)

	require.Error(t, err)
	require.Nil(t, result)
	require.Empty(t, upstream.bodies)
	require.Equal(t, "resp_missing", gjson.GetBytes(body, "previous_response_id").String())
	require.Equal(t, "keep this result", gjson.GetBytes(body, "input.0.output").String())
}

func TestOpenAIGatewayService_PreservesOversizedToolOutputForUpstream(t *testing.T) {
	oversized := strings.Repeat("x", openAIResponsesInputTextMaxChars) + "中"
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"` + oversized + `"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestAccount(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, oversized, gjson.GetBytes(upstream.bodies[0], "input.1.output").String())
}
