package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexGatewayBorrowWS_PassthroughFinalModelAndContinuationUseOneDirectDial(t *testing.T) {
	svc, account, borrow, probe := borrowWSFixture(t, 1)
	upstream := newStagedPassthroughConn()
	dialer := &borrowWSDialer{conns: []openAIWSClientConn{upstream}}
	svc.openaiWSPassthroughDialer = dialer
	seedBorrowWSTarget(t, svc, borrow, account, borrowWSContext(), "gpt-6.1-sol")
	turns := make(chan struct{}, 3)
	hooks := &OpenAIWSIngressHooks{
		MapRequestModel: func(int, string) (string, error) { return "gpt-6.1-sol", nil },
		AfterTurn:       func(_ int, _ *OpenAIForwardResult, _ error) { turns <- struct{}{} },
	}
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		client, err := coderws.Accept(w, req, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = client.CloseNow() }()
		_, first, err := client.Read(context.Background())
		if err != nil {
			serverErr <- err
			return
		}
		c := borrowWSContext()
		c.Request = req
		serverErr <- svc.proxyResponsesWebSocketV2Passthrough(context.Background(), c, client, account, "target-token", first,
			hooks, svc.getOpenAIWSProtocolResolver().Resolve(account))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{HTTPHeader: borrowWSContext().Request.Header})
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"public-sol-alias","input":"hello","store":false}`)))
	firstWire := requirePassthroughUpstreamWrite(t, upstream, time.Second)
	require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(firstWire, "model").String(), "borrowing is chosen by the final normalized model")
	upstream.Send(string(borrowWSCompleted("gpt-6.1-sol", "resp_direct_1")))
	_, _, err = client.Read(ctx)
	require.NoError(t, err)
	select {
	case <-turns:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	borrow.mu.Lock()
	borrow.config.Enabled = false
	borrow.candidate.expires = time.Now().Add(-time.Second)
	borrow.targets = nil
	borrow.mu.Unlock()
	require.True(t, borrow.targetProbeMu.TryLock())
	defer borrow.targetProbeMu.Unlock()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"public-sol-alias","previous_response_id":"resp_direct_1","input":"next","store":false}`)))
	secondWire := requirePassthroughUpstreamWrite(t, upstream, time.Second)
	require.Equal(t, "resp_direct_1", gjson.GetBytes(secondWire, "previous_response_id").String())
	upstream.Send(string(borrowWSCompleted("gpt-6.1-sol", "resp_direct_2")))
	_, _, err = client.Read(ctx)
	require.NoError(t, err)
	select {
	case <-turns:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_ = client.Close(coderws.StatusNormalClosure, "done")
	select {
	case <-serverErr:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Len(t, dialer.headers, 1)
	require.Equal(t, "Bearer target-token", dialer.headers[0].Get("Authorization"))
	require.Equal(t, "__oailb=shared-native-route", dialer.headers[0].Get("Cookie"))
	require.Equal(t, account.Proxy.URL(), dialer.proxies[0])
	require.Empty(t, probe.requests)
	borrow.wsAnchors.mu.Lock()
	require.Len(t, borrow.wsAnchors.entries, 1)
	for _, entry := range borrow.wsAnchors.entries {
		require.Equal(t, "resp_direct_2", entry.responseID)
		require.Empty(t, entry.connID)
	}
	borrow.wsAnchors.mu.Unlock()
	reconnectBody := []byte(`{"type":"response.create","model":"public-sol-alias","previous_response_id":"resp_direct_2","input":"reconnect"}`)
	err = svc.proxyResponsesWebSocketV2Passthrough(context.Background(), borrowWSContext(), client, account, "target-token", reconnectBody,
		hooks, svc.getOpenAIWSProtocolResolver().Resolve(account))
	require.Error(t, err)
	require.Len(t, dialer.headers, 1, "a closed direct anchor must not be redialed even after feature disable")
	require.Empty(t, probe.requests)
}
