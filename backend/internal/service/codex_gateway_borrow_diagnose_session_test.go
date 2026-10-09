package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexBorrowSessionRebuildsCompletedStreamItems(t *testing.T) {
	// Captures the production shape without retaining any real response data:
	// complete items are emitted in SSE, while response.completed has output:[].
	first := "data: " + `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","name":"codex_borrow_echo","call_id":"synthetic-call","arguments":"{\"value\":\"borrow-continuation-check\"}"}}` + "\n\n" +
		"data: " + `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","encrypted_content":"synthetic-encrypted-item"}}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"id":"synthetic-1","model":"gpt-6.1-sol","status":"completed","output":[]}}` + "\n\n"
	diagnostic := &codexBorrowDiagnostic{history: []any{map[string]string{"role": "user"}}}
	result := CodexBorrowDiagnosticResult{RawResponse: first}
	diagnostic.completeHTTPSessionTurn(&result, nil, 200)
	require.True(t, result.Completed, result.Error)
	require.Len(t, diagnostic.history, 4, "preserve reasoning, tool call and its linked output")
	require.Contains(t, string(diagnostic.history[1].(json.RawMessage)), "synthetic-encrypted-item")
	second := "data: " + `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"borrow-continuation-check"}]}}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"id":"synthetic-2","model":"gpt-6.1-sol","status":"completed","output":[]}}` + "\n\n"
	result = CodexBorrowDiagnosticResult{RawResponse: second}
	diagnostic.completeHTTPSessionTurn(&result, nil, 200)
	require.True(t, result.Completed, result.Error)
	require.True(t, result.ToolRoundTrip)
	require.Equal(t, codexBorrowEchoValue, result.Answer)
}

func TestCodexBorrowSessionItemsStillRequireSuccessfulTerminal(t *testing.T) {
	item := "data: " + `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"borrow-continuation-check"}]}}` + "\n\n"
	for _, ending := range []string{"", "data: " + `{"type":"response.failed","response":{"status":"failed"}}` + "\n\n"} {
		_, err := codexBorrowSessionTerminal(item + ending)
		require.Error(t, err)
	}
}
