package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// observeNativeCodexWS records a native WebSocket handshake or response.create
// frame as the account's last outbound request (transport "ws").
func (s *OpenAIGatewayService) observeNativeCodexWS(ctx context.Context, account *Account, headers, handshake http.Header, payload []byte) {
	if !isCodexCredentialOwner(account) || s.nativeCodexRuntime == nil {
		return
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(payload))
	request.Header = headers.Clone()
	request = request.WithContext(context.WithValue(request.Context(), codexIdentityBodyKey{}, inspectCodexIdentityBody(request)))
	request = request.WithContext(context.WithValue(request.Context(), codexWireIngressKey{}, "ws"))
	s.observeCodexWire(ctx, account, request, &http.Response{StatusCode: http.StatusSwitchingProtocols, Proto: "HTTP/1.1", Header: handshake}, "ws")
}

func codexWSMetadataBody(payload map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"client_metadata": payload["client_metadata"]})
	return raw
}

type codexObservedNativeFrameConn struct {
	openaiwsv2.FrameConn
	observe func(context.Context, []byte)
}

func (connection *codexObservedNativeFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	if err := connection.FrameConn.WriteFrame(ctx, kind, payload); err != nil {
		return err
	}
	if kind == coderws.MessageText && gjson.GetBytes(payload, "type").String() == "response.create" {
		connection.observe(ctx, payload)
	}
	return nil
}
