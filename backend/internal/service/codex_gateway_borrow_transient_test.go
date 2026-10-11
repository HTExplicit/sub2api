package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise real net/http framing: a response advertises more bytes than it
// delivers. Neither a partial stream nor a completed event followed by a
// transport error may qualify the target. Only a new, complete pair can do so.
func TestCodexGatewayBorrowTransientEOFFreshPairOnNextRequest(t *testing.T) {
	for _, tc := range []struct {
		name               string
		shot               int32
		completedBeforeEOF bool
		cleanEOF           bool
	}{{"mint", 1, false, false}, {"continue", 2, false, false}, {"completed_then_eof", 2, true, false}, {"clean_eof_without_completion", 1, false, true}, {"unterminated_completion", 2, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var statesMu sync.Mutex
			var states []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				statesMu.Lock()
				states = append(states, r.Header.Get("X-Codex-Turn-State"))
				statesMu.Unlock()
				w.Header().Set("X-Codex-Turn-State", "probe-state")
				w.Header().Set("Content-Type", "text/event-stream")
				body := borrowCoreSSE("gpt-6.1-sol", "OK")
				if n == tc.shot {
					if !tc.completedBeforeEOF {
						body = "data: {\"type\":\"response.created\"}\n\n"
					}
					if tc.cleanEOF && tc.completedBeforeEOF {
						body = strings.TrimSuffix(body, "\n\n")
					}
					if !tc.cleanEOF {
						w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
					}
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			targetURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			account := borrowCoreAccount(2)
			s := newBorrowCoreTest(t, func(r *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				if id != 2 {
					return nil, fmt.Errorf("unexpected source acquisition")
				}
				wire := r.Clone(r.Context())
				wire.URL = targetURL
				return server.Client().Do(wire)
			}, account)
			expires := time.Now().Add(codexGatewayBorrowTTL)
			borrowCoreCandidate(s, expires)
			_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
			require.NoError(t, err)
			req.Header.Set("X-Codex-Turn-State", "business-state")
			_, app, err := s.Apply(req, account, "gpt-6.1-sol", "", nil, false)
			require.Nil(t, app)
			var failure *CodexGatewayBorrowFailure
			require.ErrorAs(t, err, &failure)
			require.Equal(t, "target_probe_failed", failure.Reason)
			if !tc.cleanEOF {
				require.Contains(t, failure.Detail, "unexpected EOF")
			}
			require.False(t, failure.CacheHit)
			require.Nil(t, failure.RetryAfter)
			require.True(t, failure.Verification.RetryOnNextRequest)
			expectedKind := "transport"
			if tc.cleanEOF {
				expectedKind = "stream_incomplete"
			}
			require.Equal(t, expectedKind, failure.Verification.FailureKind)
			expectedShot := "mint"
			if tc.shot == 2 {
				expectedShot = "continue"
			}
			require.Equal(t, expectedShot, failure.Verification.FailedShot)
			require.Equal(t, tc.shot, calls.Load(), "the failed request never replays an uncertain probe")
			status := s.Status().Targets[1]
			require.Equal(t, "retry_pending", status.State)
			require.False(t, status.CacheValid)
			require.False(t, status.Success)
			require.Nil(t, status.RetryAfter)

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			RecordCodexGatewayBorrowPreparationFailure(c, account, failure)
			events, exists := c.Get(OpsUpstreamErrorsKey)
			require.True(t, exists)
			records, ok := events.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, records, 1)
			evidence := records[0].BorrowQualification
			require.NotNil(t, evidence)
			require.False(t, evidence.CacheHit)
			require.Equal(t, expectedShot, evidence.Verification.FailedShot)

			// No sleep, manual Verify, STATE deletion or cached-proof override.
			wire, app, err := s.Apply(req, account, "gpt-6.1-sol", "", nil, false)
			require.NoError(t, err)
			require.True(t, app.Applied)
			require.True(t, app.Verification.MintCompleted)
			require.True(t, app.Verification.ContinueCompleted)
			require.False(t, app.Verification.RetryOnNextRequest)
			require.Equal(t, expires, app.ExpiresAt, "recovery does not extend the lease")
			require.Equal(t, tc.shot+2, calls.Load(), "both new shots are required")
			statesMu.Lock()
			lastStates := append([]string(nil), states[len(states)-2:]...)
			statesMu.Unlock()
			require.Equal(t, []string{"", "probe-state"}, lastStates)
			require.Equal(t, "business-state", wire.Header.Get("X-Codex-Turn-State"))
			require.Equal(t, "business-state", req.Header.Get("X-Codex-Turn-State"))
			require.True(t, s.Status().Targets[1].CacheValid)
			_, _, err = s.Apply(req, account, "gpt-6.1-sol", "", nil, false)
			require.NoError(t, err)
			require.Equal(t, tc.shot+2, calls.Load(), "a fully qualified proof remains reusable")
		})
	}
}

func TestCodexGatewayBorrowTransientRecoveryStillRejectsChangedState(t *testing.T) {
	var calls int
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		state := "first"
		if req.Header.Get("X-Codex-Turn-State") != "" {
			state = "changed"
		}
		return borrowCoreResponse("gpt-6.1-sol", "OK", state), nil
	}, a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	_, app, err := s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.Error(t, err)
	require.Nil(t, app)
	_, app, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	var failure *CodexGatewayBorrowFailure
	require.ErrorAs(t, err, &failure)
	require.Nil(t, app)
	require.Equal(t, "target_state_changed", failure.Reason)
	require.False(t, failure.Verification.RetryOnNextRequest)
	require.NotNil(t, failure.RetryAfter)
	require.False(t, failure.CacheHit)
	_, app, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.ErrorAs(t, err, &failure)
	require.Nil(t, app)
	require.True(t, failure.CacheHit)
	require.Equal(t, 3, calls, "a real rejection still cools down without new probes")
	require.Equal(t, "rejected", s.Status().Targets[1].State)
}

func TestCodexGatewayBorrowTransientClassificationNeverGrantsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		cancelled bool
		retry     bool
	}{
		{"eof", io.ErrUnexpectedEOF, false, true},
		{"caller_cancelled", io.ErrUnexpectedEOF, true, false},
		{"protocol_incomplete", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			var result CodexGatewayBorrowVerification
			borrowTargetObservationFailure(ctx, &result, "mint", codexGatewayBorrowObservation{status: 200, raw: strings.Repeat(" ", 3)}, tc.err)
			require.False(t, result.Success)
			require.Equal(t, tc.retry, result.RetryOnNextRequest)
		})
	}
}
