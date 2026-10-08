//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Capture the shared pool's actual lease state when the socket closes. A
// one-shot test socket must be evicted while still leased, before it can be
// offered to a waiting business request.
type pelicanBorrowClosingConn struct {
	*openAIWSCaptureConn
	pool               *openAIWSConnPool
	accountID          int64
	leasedConn         *openAIWSConn
	closedAfterRelease bool
}

func (c *pelicanBorrowClosingConn) WriteJSON(ctx context.Context, value any) error {
	ap := c.pool.getOrCreateAccountPool(c.accountID)
	ap.mu.Lock()
	for _, conn := range ap.conns {
		if conn.ws == c {
			c.leasedConn = conn
			break
		}
	}
	ap.mu.Unlock()
	return c.openAIWSCaptureConn.WriteJSON(ctx, value)
}

func (c *pelicanBorrowClosingConn) Close() error {
	if c.leasedConn != nil {
		c.closedAfterRelease = len(c.leasedConn.leaseCh) > 0
	}
	return c.openAIWSCaptureConn.Close()
}

func TestPelicanBorrowWSUsesAccountWithoutCustomerAnchor(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		t.Run(model, func(t *testing.T) {
			svc, account, borrow, upstream := borrowWSFixture(t, 1)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			capture := &pelicanGenerationCapture{account: account, openaiGateway: svc}
			ctx := context.WithValue(WithAccountObservation(context.Background()), pelicanGenerationContextKey{}, capture)
			c.Request = httptest.NewRequest("POST", "/internal/admin/pelican-tests", nil).WithContext(ctx)
			seedBorrowWSTarget(t, svc, borrow, account, c, model)
			headers, _, err := svc.buildOpenAIWSHeaders(ctx, c, account, "target-token",
				svc.getOpenAIWSProtocolResolver().Resolve(account), true, "", "", "", model, "")
			require.NoError(t, err)
			wsURL, err := svc.buildOpenAIResponsesWSURL(account)
			require.NoError(t, err)
			turn, err := svc.prepareCodexGatewayBorrowWSTurn(ctx, c, account, wsURL, headers,
				[]byte(`{"model":"`+model+`","input":"one question"}`), model, "", account.Proxy.URL())
			require.NoError(t, err)
			require.NotNil(t, turn)
			require.True(t, turn.pelicanOneShot)
			require.Nil(t, turn.store)
			require.Equal(t, account.ID, turn.key.account)
			require.Contains(t, turn.headers.Get("Cookie"), "__oailb=shared-native-route")
			require.Zero(t, getAPIKeyIDFromContext(c))
			require.Empty(t, upstream.requests, "hot qualification must not prepare another route")
			_, err = svc.prepareCodexGatewayBorrowWSTurn(ctx, c, account, wsURL, headers,
				[]byte(`{"model":"`+model+`"}`), model, "previous-response", account.Proxy.URL())
			require.ErrorContains(t, err, "new conversation")
		})
	}
}

func TestPelicanBorrowWSSharedForwardGeneratesOnceAndReleasesItsSocket(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		t.Run(model, func(t *testing.T) {
			gateway, account, borrow, probes := borrowWSFixture(t, 1)
			gateway.cfg.Gateway.MaxLineSize = defaultMaxLineSize
			account.Status, account.Schedulable = StatusError, false
			repo := &pelicanGeneratorAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			gateway.accountRepo = repo
			sender := &AccountTestService{accountRepo: repo, cfg: gateway.cfg, openaiGatewayService: gateway}
			seed, _ := gin.CreateTestContext(httptest.NewRecorder())
			seed.Request = httptest.NewRequest("POST", "/internal/admin/pelican-tests", nil)
			seedBorrowWSTarget(t, gateway, borrow, account, seed, model)
			answer := "<html><svg></svg></html>"
			delta, err := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": answer})
			require.NoError(t, err)
			completed, err := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
				"id": "resp_pelican", "model": model, "status": "completed",
				"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": answer}}}},
				"usage":  map[string]any{"input_tokens": 4, "output_tokens": 8},
			}})
			require.NoError(t, err)
			conn := &pelicanBorrowClosingConn{openAIWSCaptureConn: &openAIWSCaptureConn{events: [][]byte{delta, completed}},
				pool: gateway.openaiWSPool, accountID: account.ID}
			dialer := &borrowWSDialer{conns: []openAIWSClientConn{conn}}
			gateway.openaiWSPool.setClientDialerForTest(dialer)
			state := newPelicanExecutionState(context.Background(), newPelicanExecutionCoordinator(10), 10*time.Minute, time.Now)
			defer state.finish()
			ctx := context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
			result, err := sender.GeneratePelican(ctx, account.ID, model, "high")
			require.NoError(t, err)
			require.Equal(t, "complete", result.Status, result.Error)
			require.Equal(t, answer, result.RawAnswer)
			require.Equal(t, string(delta)+"\n"+string(completed)+"\n", result.RawResponse)
			require.Equal(t, []int64{account.ID}, repo.reads)
			require.Len(t, conn.writes, 1, "the real shared Forward sends the fixed prompt only once")
			wire, err := json.Marshal(conn.writes[0])
			require.NoError(t, err)
			require.Contains(t, string(wire), CodexGatewayBorrowPelicanPrompt)
			require.Equal(t, model, conn.writes[0]["model"])
			require.Len(t, dialer.headers, 1)
			require.Equal(t, "Bearer target-token", dialer.headers[0].Get("Authorization"))
			require.Equal(t, "target-chatgpt", dialer.headers[0].Get("Chatgpt-Account-Id"))
			require.Contains(t, dialer.headers[0].Get("Cookie"), "__oailb=shared-native-route")
			require.Equal(t, []string{account.Proxy.URL()}, dialer.proxies)
			require.Empty(t, probes.requests, "qualified routes must not issue extra probes")
			snapshot := state.finish()
			require.NotNil(t, snapshot.generationStartedAt)
			require.Equal(t, "websocket", snapshot.invocation.Transport)
			require.Equal(t, "responses", snapshot.invocation.Protocol)
			require.Equal(t, model, snapshot.invocation.Model)
			require.Equal(t, "high", snapshot.invocation.Effort)
			require.True(t, snapshot.invocation.BorrowApplied)
			require.True(t, strings.HasPrefix(snapshot.invocation.Endpoint, "wss://"))
			require.True(t, conn.closed, "a one-shot admin test must release its borrowed socket")
			require.NotNil(t, conn.leasedConn)
			require.False(t, conn.closedAfterRelease, "evict before exposing the socket to a waiting business request")
			require.Empty(t, borrow.wsAnchors.entries, "admin tests must not create customer continuation anchors")
		})
	}
}
