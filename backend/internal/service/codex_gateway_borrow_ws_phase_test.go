package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestCodexGatewayBorrowWS_PreparationFailureUsesNeutralExistingFailoverBeforeDial(t *testing.T) {
	for _, mode := range []string{"http_ws_v2", "native_ctx_pool", "native_passthrough"} {
		t.Run(mode, func(t *testing.T) {
			svc, account, borrow, probe := borrowWSFixture(t, 1)
			dialer := &borrowWSDialer{}
			svc.openaiWSPool.setClientDialerForTest(dialer)
			svc.openaiWSPassthroughDialer = dialer
			seedBorrowWSTarget(t, svc, borrow, account, borrowWSContext(), "gpt-6.1-sol")
			for key, check := range borrow.targets {
				check.result.Success = false
				check.result.Reason = "target_state_changed"
				check.retryAfter = time.Now().Add(codexGatewayBorrowFailureWait)
				borrow.targets[key] = check
			}
			var resultErr error
			if mode == "http_ws_v2" {
				_, resultErr = runBorrowWSV2(t, svc, account, "gpt-6.1-sol", "")
			} else {
				serverResult := make(chan error, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					client, err := coderws.Accept(w, req, nil)
					if err != nil {
						serverResult <- err
						return
					}
					defer func() { _ = client.CloseNow() }()
					_, first, err := client.Read(context.Background())
					if err != nil {
						serverResult <- err
						return
					}
					c := borrowWSContext()
					c.Request = req
					if mode == "native_ctx_pool" {
						err = svc.ProxyResponsesWebSocketFromClient(context.Background(), c, client, account, "target-token", first, nil)
					} else {
						err = svc.proxyResponsesWebSocketV2Passthrough(context.Background(), c, client, account, "target-token", first, nil,
							svc.getOpenAIWSProtocolResolver().Resolve(account))
					}
					serverResult <- err
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{HTTPHeader: borrowWSContext().Request.Header})
				require.NoError(t, err)
				defer func() { _ = client.CloseNow() }()
				require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6.1-sol","input":"hello","store":false}`)))
				select {
				case resultErr = <-serverResult:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			var failure *UpstreamFailoverError
			require.ErrorAs(t, resultErr, &failure)
			require.Equal(t, CodexGatewayBorrowPreparationFailureReason, failure.Reason)
			require.Equal(t, GatewayFailureScopeRequest, failure.Scope)
			require.True(t, failure.SuppressAccountHealthPenalty)
			require.True(t, failure.RequestScopedTransient)
			require.False(t, failure.ShouldReportAccountScheduleFailure())
			require.True(t, failure.ShouldRetryNextAccount(), "the existing failover owner may switch accounts")
			require.False(t, failure.RetryableOnSameAccount)
			_, reconnect := classifyOpenAIWSReconnectReason(resultErr)
			require.False(t, reconnect, "local qualification must not enter the WS reconnect loop")
			require.NotErrorIs(t, resultErr, errOpenAIWSPassthroughRetrySameAccount)
			require.Empty(t, dialer.headers, "no business dial occurs for a failed local preparation")
			require.Empty(t, probe.requests, "the cached failed qualification is not reprobed")
			require.Empty(t, borrow.wsAnchors.busy)
			usage := borrow.Status().RecentUsage
			require.Len(t, usage, 1)
			require.EqualValues(t, 1, usage[0].AttemptCount)
			require.EqualValues(t, 1, usage[0].BlockedCount)
			require.Zero(t, usage[0].Count)
			require.Equal(t, "target_state_changed", usage[0].Reason)
		})
	}
}

func TestCodexGatewayBorrowWS_PreparationDiagnosticsKeepCompleteCause(t *testing.T) {
	c := borrowWSContext()
	cause := errors.New("dial tcp synthetic-source.invalid:443: connect: connection refused; complete local observation")
	err := codexGatewayBorrowWSPreparationError(c, &Account{ID: 96, Platform: PlatformOpenAI}, &CodexGatewayBorrowFailure{Cause: cause})
	require.True(t, IsCodexGatewayBorrowRequestFailure(err))
	rawEvents, exists := c.Get(OpsUpstreamErrorsKey)
	require.True(t, exists)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Contains(t, events[0].Detail, cause.Error())
	ordinary := errors.New("ordinary websocket business dial failure")
	require.Same(t, ordinary, codexGatewayBorrowWSPreparationError(c, nil, ordinary))
}
