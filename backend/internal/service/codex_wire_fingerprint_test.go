package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// The view shows the configured identity and the last final outbound request.
// Dormant route-qualification records (per-model state, cookies in an older
// wire record) are neither read nor returned, and GET writes nothing.
func TestCodexFingerprintShowsLastOutboundRequestWithoutRoutingState(t *testing.T) {
	account := codexOAuthTestAccount(7)
	manager := nativeCodexTestRuntime(t)
	store := &nativeCodexMemoryStore{values: map[string]extensionv1.StateResult{}}
	manager.repo = store
	service := &OpenAIGatewayService{nativeCodexRuntime: manager, accountRepo: &codexAccountRepositoryFixture{account: account}}
	_, err := store.CompareSwapExtensionState(context.Background(), NativeCodexPluginKey, extensionv1.StateRequest{Namespace: "tickets", Key: "7.model", Value: json.RawMessage(`{"schema":2,"phase":"ready"}`)})
	require.NoError(t, err)
	_, err = store.CompareSwapExtensionState(context.Background(), NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: "wire.7", Value: json.RawMessage(`{"user_agent":"older-agent","cookies":[{"name":"route","value":"dormant-cookie"}],"route_hash":"dormant-route"}`)})
	require.NoError(t, err)
	view, err := service.CodexFingerprint(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, view.ObservedError, "an older wire record still decodes")
	require.Equal(t, "older-agent", view.Observed.UserAgent)
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	for _, retired := range []string{"dormant-cookie", "dormant-route", "routing_enabled", "harvest_proxy_url", "current_scope", "cookie_max_age_seconds", `"models"`} {
		require.NotContains(t, string(raw), retired)
	}

	request, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	request.Header.Set("User-Agent", "final-agent")
	request.Header.Set(openAICodexTurnStateHeader, "raw-request-state")
	service.observeCodexWire(context.Background(), account, request, &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/2.0"}, "http")
	view, err = service.CodexFingerprint(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "final-agent", view.Observed.UserAgent)
	require.Equal(t, "http", view.Observed.Transport)
	require.Equal(t, "raw-request-state", view.Observed.State)
	require.NotEmpty(t, view.CapturedReference)
	before := len(store.values)
	_, err = service.CodexFingerprint(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, store.values, before, "read-only fingerprint GET wrote state")
	require.False(t, resolveCodexIdentitySnapshotContext(context.Background(), account, account, "").UAOverridePresent)
	require.True(t, resolveCodexIdentitySnapshotContext(context.Background(), account, account, "explicit UA override").UAOverridePresent)
}

func TestCodexWireObservesOrdinaryHTTPAndNativeFrames(t *testing.T) {
	account := codexOAuthTestAccount(7)
	manager := nativeCodexTestRuntime(t)
	store := &nativeCodexMemoryStore{values: map[string]extensionv1.StateResult{}}
	manager.repo = store
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Proto: "HTTP/2.0", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}}
	service := &OpenAIGatewayService{httpUpstream: upstream, nativeCodexRuntime: manager}
	request, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-5.5","input":"private-prompt","client_metadata":{"session_id":"session"}}`))
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("User-Agent", "actual-wire-agent")
	request.Header.Set("session-id", "session")
	response, err := service.doOpenAICodexUpstream(request, account, "")
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Empty(t, upstream.lastReq.Header.Get("Cookie"), "an ordinary OAuth request carries no Cookie header")
	record, err := store.ReadExtensionState(context.Background(), NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: "wire.7"})
	require.NoError(t, err)
	var observation CodexWireFingerprint
	require.NoError(t, json.Unmarshal(record.Value, &observation))
	require.Equal(t, "http", observation.Transport)
	require.Equal(t, "actual-wire-agent", observation.UserAgent)
	require.Equal(t, "match", observation.IdentityFields["session"].Consistency)
	require.NotContains(t, string(record.Value), "private-token")
	require.NotContains(t, string(record.Value), "private-prompt")
	frames := newStagedPassthroughConn()
	observed := &codexObservedNativeFrameConn{FrameConn: frames, observe: func(ctx context.Context, body []byte) {
		service.observeNativeCodexWS(ctx, account, request.Header, http.Header{"Sec-Websocket-Extensions": []string{"permessage-deflate"}}, body)
	}}
	require.NoError(t, observed.WriteFrame(context.Background(), websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5.5","client_metadata":{"session_id":"session"}}`)))
	record, err = store.ReadExtensionState(context.Background(), NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: "wire.7"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(record.Value, &observation))
	require.Equal(t, "ws", observation.Transport)
	require.Equal(t, "ws", observation.Ingress)
	require.Equal(t, "permessage-deflate", observation.WebSocketExtensions)
	require.Equal(t, "unknown", observation.JA3)
	require.Empty(t, observation.TLSVersion)
	require.Equal(t, "match", observation.IdentityFields["session"].Consistency)
}
