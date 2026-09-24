package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

// Native WS validation is one bounded response on a fresh connection and a
// fresh logical session. It is never exported as qualification for a different
// socket or client session. Normal qualified WS ingress uses the HTTP bridge.
func (s *OpenAIGatewayService) executeCodexRoutingWSProbe(ctx context.Context, query extensionv1.CodexRoutingQuery, cookies string, deadline time.Time, scope extensionv1.CodexRoutingScope) (*http.Response, extensionv1.CodexRoutingScope, error) {
	if _, validation := codexValidationFromContext(ctx); !validation || query.Stage != "verify" || cookies == "" || !time.Now().Before(deadline) {
		return nil, scope, errCodexRoutingUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, query.AccountID)
	if err != nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	token, _, err := s.GetAccessToken(withCodexTicketCredentials(ctx), account)
	if err != nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	request, err := s.buildCodexRoutingProbe(ctx, account, query.Model, token, cookies, query.ReasoningEffort)
	if err != nil {
		return nil, scope, err
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, codexRoutingProbeReadLimit))
	_ = request.Body.Close()
	if err != nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	if request.Header.Get("Content-Encoding") == "zstd" {
		decoder, decoderErr := zstd.NewReader(nil)
		if decoderErr != nil {
			return nil, scope, errCodexRoutingUnavailable
		}
		raw, err = decoder.DecodeAll(raw, nil)
		decoder.Close()
		if err != nil {
			return nil, scope, errCodexRoutingUnavailable
		}
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	delete(payload, "stream")
	payload["type"] = "response.create"
	headers := request.Header.Clone()
	headers.Del("Accept")
	headers.Del("Content-Type")
	headers.Del("Content-Encoding")
	headers.Del("Content-Length")
	headers.Set("OpenAI-Beta", openAIWSBetaV2Value)
	connection, status, handshake, err := s.getOpenAIWSPassthroughDialer().Dial(ctx, "wss://chatgpt.com/backend-api/codex/responses", headers, resolveAccountProxyURL(account))
	if err != nil || connection == nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	defer func() { _ = connection.Close() }()
	// The validation read cap covers every frame, not only the gaps between them.
	if limited, ok := connection.(interface{ SetReadLimit(int64) }); ok {
		limited.SetReadLimit(codexRoutingProbeReadLimit)
	}
	scope.ConnectionLeaseID, scope.RouteEvidence, scope.Transport = "ws-validation-"+uuid.NewString(), "closed_validation_connection", "ws"
	if err := connection.WriteJSON(ctx, payload); err != nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	completion := codexRoutingCompletion{RequestedModel: query.Model}
	completion.headers(handshake)
	var terminal []byte
	readBytes := 0
	for readBytes < codexRoutingProbeReadLimit {
		frame, readErr := connection.ReadMessage(ctx)
		if readErr != nil {
			return nil, scope, errCodexRoutingUnavailable
		}
		readBytes += len(frame)
		if readBytes > codexRoutingProbeReadLimit {
			return nil, scope, errCodexRoutingUnavailable
		}
		completion.json(frame)
		if completion.Failed || completion.Completed {
			terminal = frame
			break
		}
	}
	if terminal == nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	if status == 0 {
		status = http.StatusSwitchingProtocols
	}
	response := &http.Response{StatusCode: status, Proto: "HTTP/1.1", Header: handshake.Clone(), Body: io.NopCloser(bytes.NewReader(terminal))}
	if response.Header == nil {
		response.Header = make(http.Header)
	}
	response.Header.Set("Content-Type", "application/json")
	q := &extensionv1.CodexRoutingQualification{Scope: scope, Model: query.Model}
	request.Header = headers
	request = request.WithContext(context.WithValue(request.Context(), codexRoutingIngressKey{}, "ws"))
	s.observeCodexWire(ctx, account, request, response, q)
	return response, scope, nil
}
