package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// A native socket that started outside the Cookie policy cannot acquire an
// HTTP connection's qualification through a later session/model switch. The
// client must reconnect with the gated model so ingress chooses its bridge.
func (s *OpenAIGatewayService) guardCodexRoutingNativeModel(account *Account, model string) error {
	if s.codexRoutingApplies(account, model) {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "this model requires a verified HTTP route; reconnect with this model", errCodexRoutingUnavailable)
	}
	return nil
}

func (s *OpenAIGatewayService) observeNativeCodexWS(ctx context.Context, account *Account, headers, handshake http.Header, payload []byte, connection string) {
	if !isOpenAICodexTicketAccount(account) || s.pluginManager == nil {
		return
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(payload))
	request.Header = headers.Clone()
	request = request.WithContext(context.WithValue(request.Context(), codexIdentityBodyKey{}, inspectCodexIdentityBody(request)))
	request = request.WithContext(context.WithValue(request.Context(), codexRoutingIngressKey{}, "ws"))
	scope := extensionv1.CodexRoutingScope{AccountID: account.ID, Identity: CodexTicketAccountIdentity(account), Transport: "ws", ConnectionLeaseID: connection, RouteEvidence: "native_connection_observed"}
	q := &extensionv1.CodexRoutingQualification{Scope: scope}
	s.observeCodexWire(ctx, account, request, &http.Response{StatusCode: http.StatusSwitchingProtocols, Proto: "HTTP/1.1", Header: handshake}, q)
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
