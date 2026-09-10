//go:build unit

package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func managedModelResponseIdentityContext(version int, publicModel string) context.Context {
	return WithManagedModelRequest(context.Background(), &ManagedModelRequest{
		Version: version,
		Route:   ManagedModelRoute{PublicModel: publicModel},
	})
}

func TestManagedModelResponseIdentityContext(t *testing.T) {
	require.Nil(t, managedModelResponseContext(nil))
	require.Nil(t, managedModelResponseContext(&gin.Context{}))
	ctx := managedModelResponseIdentityContext(2, "fable-5.1")
	c := &gin.Context{Request: httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)}
	require.Same(t, ctx, managedModelResponseContext(c))
	require.Equal(t, "fable-5.1", managedModelResponseModel(managedModelResponseContext(c), "vendor/private"))
}

func TestManagedModelResponseIdentityModel(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "public v2", ctx: managedModelResponseIdentityContext(2, "fable-5.1"), want: "fable-5.1"},
		{name: "unmanaged", ctx: context.Background(), want: "vendor/Fable-5.1-VIP"},
		{name: "nil context", want: "vendor/Fable-5.1-VIP"},
		{name: "v1", ctx: managedModelResponseIdentityContext(1, "fable-5.1"), want: "vendor/Fable-5.1-VIP"},
		{name: "unknown version", ctx: managedModelResponseIdentityContext(3, "fable-5.1"), want: "vendor/Fable-5.1-VIP"},
		{name: "empty public", ctx: managedModelResponseIdentityContext(2, " \t"), want: "vendor/Fable-5.1-VIP"},
		{name: "reserved public", ctx: managedModelResponseIdentityContext(2, " S2PUB-g7-bprivate "), want: "vendor/Fable-5.1-VIP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, managedModelResponseModel(test.ctx, "vendor/Fable-5.1-VIP"))
		})
	}
}

func TestManagedModelResponseIdentityJSON(t *testing.T) {
	ctx := managedModelResponseIdentityContext(2, "fable-5.1")
	for _, test := range []struct {
		name, body, want string
	}{
		{
			name: "chat preserves usage and user-owned model fields byte for byte",
			body: ` { "model" : "vendor/Fable-5.1-VIP", "choices":[{"message":{"content":"s2pub-g7-bprivate vendor/Fable-5.1-VIP","model":"s2pub-g7-bprivate"}}], "usage" : {"total_tokens":9007199254740993}, "model_hint":"vendor/Fable-5.1-VIP" } `,
			want: ` { "model" : "fable-5.1", "choices":[{"message":{"content":"s2pub-g7-bprivate vendor/Fable-5.1-VIP","model":"s2pub-g7-bprivate"}}], "usage" : {"total_tokens":9007199254740993}, "model_hint":"vendor/Fable-5.1-VIP" } `,
		},
		{
			name: "messages start only rewrites its model envelope",
			body: `{"type":"message_start","message":{"model":"s2pub-g7-bprivate","content":[{"type":"text","text":"s2pub-g7-bprivate","model":"vendor/keep"}],"usage":{"input_tokens":3}},"response":{"model":"vendor/keep"}}`,
			want: `{"type":"message_start","message":{"model":"fable-5.1","content":[{"type":"text","text":"s2pub-g7-bprivate","model":"vendor/keep"}],"usage":{"input_tokens":3}},"response":{"model":"vendor/keep"}}`,
		},
		{
			name: "responses event projects differing upstream and top level names",
			body: `{"type":"response.completed","model":"s2pub-g7-bprivate","response":{"model":"provider/Fable-5.1-CC","output":[{"model":"vendor/keep","text":"provider/Fable-5.1-CC"}],"usage":{"total_tokens":6}},"message":{"model":"vendor/keep"}}`,
			want: `{"type":"response.completed","model":"fable-5.1","response":{"model":"fable-5.1","output":[{"model":"vendor/keep","text":"provider/Fable-5.1-CC"}],"usage":{"total_tokens":6}},"message":{"model":"vendor/keep"}}`,
		},
		{
			name: "responses JSON uses top level model without event envelope",
			body: `{"object":"response","model":"provider/Fable-5.1-CC","output":[]}`,
			want: `{"object":"response","model":"fable-5.1","output":[]}`,
		},
		{
			name: "unrelated nested models are not protocol model fields",
			body: `{"type":"content_block_delta","message":{"model":"vendor/keep"},"response":{"model":"vendor/keep"},"delta":{"text":"s2pub-g7-bprivate"}}`,
			want: `{"type":"content_block_delta","message":{"model":"vendor/keep"},"response":{"model":"vendor/keep"},"delta":{"text":"s2pub-g7-bprivate"}}`,
		},
		{
			name: "does not insert or coerce model fields",
			body: `{"type":"response.created","model":null,"response":{"model":7},"usage":{"total_tokens":0}}`,
			want: `{"type":"response.created","model":null,"response":{"model":7},"usage":{"total_tokens":0}}`,
		},
		{name: "missing model", body: `{"usage":{"total_tokens":4}}`, want: `{"usage":{"total_tokens":4}}`},
		{name: "invalid JSON", body: `{"model":"vendor/private"`, want: `{"model":"vendor/private"`},
		{name: "array is not a protocol envelope", body: `[{"model":"vendor/private"}]`, want: `[{"model":"vendor/private"}]`},
		{name: "already public", body: ` {"model" : "fable-5.1"} `, want: ` {"model" : "fable-5.1"} `},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(test.body)
			require.Equal(t, test.want, string(managedModelResponseJSON(ctx, body)))
			require.Equal(t, test.body, string(body), "response projection must not mutate the upstream buffer")
		})
	}
}

func TestManagedModelResponseIdentitySSELine(t *testing.T) {
	ctx := managedModelResponseIdentityContext(2, "fable-5.1")
	for _, test := range []struct {
		name, line, want string
		eventType        string
	}{
		{
			name: "preserves prefix whitespace trailing whitespace and CRLF",
			line: "data:\t  {\"model\" : \"vendor/private\",\"usage\":{\"total_tokens\":3}} \t\r\n",
			want: "data:\t  {\"model\" : \"fable-5.1\",\"usage\":{\"total_tokens\":3}} \t\r\n",
		},
		{
			name: "no space after colon",
			line: `data:{"type":"message_start","message":{"model":"vendor/private"}}`,
			want: `data:{"type":"message_start","message":{"model":"fable-5.1"}}`,
		},
		{
			name:      "responses hint without payload type",
			eventType: "response.completed",
			line:      `data: {"response":{"model":"vendor/private","output":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}]}}`,
			want:      `data: {"response":{"model":"fable-5.1","output":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}]}}`,
		},
		{
			name:      "anthropic hint without payload type",
			eventType: "message_start",
			line:      `data: {"message":{"model":"vendor/private","content":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}]}}`,
			want:      `data: {"message":{"model":"fable-5.1","content":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}]}}`,
		},
		{name: "event", line: "event: message_start\n", want: "event: message_start\n"},
		{name: "comment", line: ": s2pub-g7-bprivate\r\n", want: ": s2pub-g7-bprivate\r\n"},
		{name: "done", line: "data: [DONE] \r\n", want: "data: [DONE] \r\n"},
		{name: "empty data", line: "data:\t\n", want: "data:\t\n"},
		{name: "invalid data", line: "data: {\"model\":", want: "data: {\"model\":"},
		{name: "blank", line: "\r\n", want: "\r\n"},
		{name: "unknown SSE field", line: ` data:{"model":"vendor/private"}`, want: ` data:{"model":"vendor/private"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, managedModelResponseSSELine(ctx, test.line, test.eventType))
		})
	}
}

func TestManagedModelResponseIdentityPayloadTypePrecedesEventHint(t *testing.T) {
	ctx := managedModelResponseIdentityContext(2, "fable-5.1")
	for _, test := range []struct {
		name, body, want string
	}{
		{
			name: "payload envelope wins",
			body: `{"type":"message_start","message":{"model":"vendor/private"},"response":{"model":"vendor/keep"}}`,
			want: `{"type":"message_start","message":{"model":"fable-5.1"},"response":{"model":"vendor/keep"}}`,
		},
		{
			name: "unrelated payload type is not replaced",
			body: `{"type":"content_block_delta","response":{"model":"vendor/keep"}}`,
			want: `{"type":"content_block_delta","response":{"model":"vendor/keep"}}`,
		},
		{
			name: "empty payload type is still present",
			body: `{"type":"","response":{"model":"vendor/keep"}}`,
			want: `{"type":"","response":{"model":"vendor/keep"}}`,
		},
		{
			name: "null payload type is still present",
			body: `{"type":null,"response":{"model":"vendor/keep"}}`,
			want: `{"type":null,"response":{"model":"vendor/keep"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, string(managedModelResponseJSON(ctx, []byte(test.body), "response.completed")))
		})
	}
}

func TestManagedModelResponseIdentitySSEBody(t *testing.T) {
	ctx := managedModelResponseIdentityContext(2, "fable-5.1")
	body := ": keep s2pub-g7-bprivate\r\nevent: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"vendor/private\",\"output\":[{\"model\":\"s2pub-g7-bprivate\",\"text\":\"vendor/private\"}],\"usage\":{\"total_tokens\":9}}}\r\n\r\nevent: final\ndata: [DONE]"
	want := ": keep s2pub-g7-bprivate\r\nevent: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"fable-5.1\",\"output\":[{\"model\":\"s2pub-g7-bprivate\",\"text\":\"vendor/private\"}],\"usage\":{\"total_tokens\":9}}}\r\n\r\nevent: final\ndata: [DONE]"
	require.Equal(t, want, managedModelResponseSSEBody(ctx, body))
}

func TestManagedModelResponseIdentitySSEBodyEventHints(t *testing.T) {
	ctx := managedModelResponseIdentityContext(2, "fable-5.1")
	const response = `{"response":{"model":"vendor/private","output":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}],"usage":{"total_tokens":9}}}`
	const publicResponse = `{"response":{"model":"fable-5.1","output":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}],"usage":{"total_tokens":9}}}`
	const message = `{"message":{"model":"vendor/private","content":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}],"usage":{"input_tokens":3}}}`
	const publicMessage = `{"message":{"model":"fable-5.1","content":[{"model":"s2pub-g7-bprivate","text":"vendor/private"}],"usage":{"input_tokens":3}}}`
	body := ": keep\r\nevent: response.completed\r\nid: first\r\ndata: " + response + "\r\n\r\n" +
		"data: " + response + "\n\n" +
		"event:message_start\n: keep s2pub-g7-bprivate\ndata: " + message + "\n\n" +
		"event: response.completed\nevent:\ndata: " + response + "\n\n" +
		"event: message_start\nevent\ndata: " + message
	want := ": keep\r\nevent: response.completed\r\nid: first\r\ndata: " + publicResponse + "\r\n\r\n" +
		"data: " + response + "\n\n" +
		"event:message_start\n: keep s2pub-g7-bprivate\ndata: " + publicMessage + "\n\n" +
		"event: response.completed\nevent:\ndata: " + response + "\n\n" +
		"event: message_start\nevent\ndata: " + message
	require.Equal(t, want, managedModelResponseSSEBody(ctx, body))
}

func TestManagedModelResponseIdentityUnmanagedAndV1Unchanged(t *testing.T) {
	body := " { \"type\": \"response.completed\", \"model\": \"s2pub-g7-bprivate\", \"response\": {\"model\": \"vendor/private\"}, \"usage\": {\"total_tokens\":9007199254740993} } \r\n"
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "unmanaged", ctx: context.Background()},
		{name: "v1", ctx: managedModelResponseIdentityContext(1, "fable-5.1")},
		{name: "invalid public", ctx: managedModelResponseIdentityContext(2, "s2pub-g7-bprivate")},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, body, string(managedModelResponseJSON(test.ctx, []byte(body))))
			require.Equal(t, "data:"+body, managedModelResponseSSELine(test.ctx, "data:"+body))
			stream := "event: response.completed\n" + "data:" + body + "\ndata: [DONE]\n"
			require.Equal(t, stream, managedModelResponseSSEBody(test.ctx, stream))
		})
	}
}
