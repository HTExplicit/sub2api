package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexBorrowNilRequestDoesNotCreateDiagnosticEvidence(t *testing.T) {
	s := newBorrowCoreTest(t, nil)
	wire, application, err := s.Apply(nil, borrowCoreAccount(2), "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.Nil(t, wire)
	require.Nil(t, application)
}

// The pinned ranxi probe changes only the legacy session_id per shot. Native
// session/thread headers belong to the template and must survive both shots.
// A synthetic upstream rotates STATE when those headers change, reproducing
// the false rejection introduced by manufacturing a new native session per shot.
func TestCodexBorrowProbePreservesNativeSessionAndTier(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		t.Run(model, func(t *testing.T) {
			var shots []*http.Request
			a := borrowCoreAccount(2)
			s := newBorrowCoreTest(t, func(req *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				assert.Equal(t, int64(2), id)
				assert.Equal(t, "target-exit", proxy)
				body := borrowCoreBody(t, req)
				assert.Equal(t, model, gjson.GetBytes(body, "model").String())
				assert.Equal(t, "priority", gjson.GetBytes(body, "service_tier").String())
				assert.Equal(t, "model="+model+";tier=priority", req.Header.Get(openAICodexRoutingHintHeader))
				assert.Empty(t, req.Header.Get("Content-Encoding"))
				assert.Empty(t, req.Header.Get("X-Codex-Turn-Metadata"))
				assert.NotEmpty(t, req.Header.Get("session_id"))
				shots = append(shots, req.Clone(context.Background()))
				state := "stable-probe-state"
				if len(shots) == 2 {
					assert.NotEqual(t, shots[0].Header.Get("session_id"), req.Header.Get("session_id"))
					assert.Equal(t, "stable-probe-state", req.Header.Get("X-Codex-Turn-State"))
					assert.Contains(t, req.Header.Get("Cookie"), "__cflb=target-route")
					if shots[0].Header.Get("session-id") != req.Header.Get("session-id") || shots[0].Header.Get("thread-id") != req.Header.Get("thread-id") {
						state = "different-session-state"
					}
				} else {
					assert.Empty(t, req.Header.Get("X-Codex-Turn-State"))
				}
				return borrowCoreResponse(model, "OK", state, "__cflb=target-route; Secure; Path=/"), nil
			}, a)
			borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
			_, req, _, err := s.accountTemplate(context.Background(), 2, model)
			require.NoError(t, err)
			for k, v := range map[string]string{"session-id": "client-session", "thread-id": "client-thread", "x-client-request-id": "client-request", "x-codex-window-id": "client-window", "X-Codex-Turn-State": "business-state", "X-Codex-Turn-Metadata": `{"session_id":"client-session"}`, "Content-Encoding": "zstd"} {
				req.Header.Set(k, v)
			}
			setOpenAICodexRoutingHint(req.Header, a, model, "priority")
			original := req.Header.Clone()
			wire, applied, err := s.Apply(req, a, model, "target-exit", nil, false)
			require.NoError(t, err)
			require.True(t, applied.Applied)
			require.Equal(t, original, req.Header)
			require.Equal(t, "business-state", wire.Header.Get("X-Codex-Turn-State"))
			for _, shot := range shots {
				for _, key := range []string{"session-id", "thread-id", "x-client-request-id", "x-codex-window-id"} {
					require.Equal(t, original.Get(key), shot.Header.Get(key), key)
				}
			}
		})
	}
}

func TestCodexBorrowPreparationFailureRetainsReasonAndCounts(t *testing.T) {
	var calls int
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		state := "first-state"
		if req.Header.Get("X-Codex-Turn-State") != "" {
			state = "replacement-state"
		}
		return borrowCoreResponse("gpt-6.1-sol", "OK", state), nil
	}, a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	for _, id := range []string{"first-client", "retry-client"} {
		ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, id)
		ctx = context.WithValue(ctx, ctxkey.RequestID, "gateway-"+id)
		req.Header.Set("x-client-request-id", id)
		_, _, err = s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6.1-sol", "")
		var failure *CodexGatewayBorrowFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "target_validation", failure.Stage)
		require.Equal(t, "target_state_changed", failure.Reason)
		require.NotNil(t, failure.RetryAfter)
		require.NotNil(t, failure.Verification)
		require.True(t, failure.Verification.MintCompleted)
		require.True(t, failure.Verification.ContinueCompleted)
		require.Equal(t, len("replacement-state"), failure.Verification.ContinueStateLength)
	}
	require.Equal(t, 2, calls, "a different tracing ID does not bypass the failure cooldown")
	usage := s.Status().RecentUsage
	require.Len(t, usage, 1)
	require.EqualValues(t, 2, usage[0].AttemptCount)
	require.EqualValues(t, 2, usage[0].BlockedCount)
	require.Zero(t, usage[0].Count)
	require.Zero(t, usage[0].AppliedCount)
	require.False(t, usage[0].Dispatched)
	require.Equal(t, "retry-client", usage[0].ClientRequestID)
	require.Equal(t, "gateway-retry-client", usage[0].GatewayRequestID)
	require.Equal(t, "target_state_changed", usage[0].Reason)
}

func TestCodexBorrowProofIncludesNativeSessionAndRoutingPolicy(t *testing.T) {
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		return borrowCoreResponse("gpt-6.1-sol", "OK", "stable"), nil
	}, a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	_, _, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	for _, header := range []string{"session-id", "thread-id", "x-codex-window-id", "x-codex-installation-id", "x-codex-beta-features", openAICodexRoutingHintHeader} {
		changed := req.Clone(req.Context())
		changed.Header.Set(header, "changed")
		_, _, err = s.Apply(changed, a, "gpt-6.1-sol", "", nil, true)
		require.Error(t, err, header)
	}
	req.Header.Set("x-client-request-id", "another-trace")
	_, application, err := s.Apply(req, a, "gpt-6.1-sol", "", nil, true)
	require.NoError(t, err)
	require.True(t, application.Applied)
}

func TestCodexBorrowAttemptCountersSurviveOutOfOrderDispatch(t *testing.T) {
	s := newBorrowCoreTest(t, nil)
	first := s.beginAttempt(context.Background(), 2, "gpt-6.1-sol", "http")
	newer := s.beginAttempt(context.Background(), 2, "gpt-6.1-sol", "http")
	first.dispatched(true)
	newer.blocked(&CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "target_validation", Reason: "target_state_changed"})
	newer.blocked(ErrCodexGatewayBorrowUnavailable)
	first.observe([]byte(`{"type":"response.completed","response":{"status":"completed"}}`))
	first.finish(nil)
	row := s.Status().RecentUsage[0]
	require.Equal(t, "blocked", row.Outcome)
	require.EqualValues(t, 2, row.AttemptCount)
	require.EqualValues(t, 1, row.BlockedCount)
	require.EqualValues(t, 1, row.Count)
	require.EqualValues(t, 1, row.AppliedCount)
	require.False(t, row.Dispatched)
}
