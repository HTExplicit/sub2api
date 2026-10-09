package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexBorrowRejectedCandidateGetsOneReplacement(t *testing.T) {
	for _, replacement := range []string{"usable", "rejected", "identical"} {
		t.Run(replacement, func(t *testing.T) {
			var sourceCalls, targetCalls int
			a := borrowCoreAccount(2)
			s := newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				if id == 1 {
					sourceCalls++
					cookie := "replacement-cookie"
					if replacement == "identical" {
						cookie = "synthetic-borrowed-cookie"
					}
					return borrowCoreResponse("gpt-6-astra", "OK", "source-state", "__oailb="+cookie+"; Secure; Path=/; Max-Age=230"), nil
				}
				targetCalls++
				cookie, err := req.Cookie("__oailb")
				assert.NoError(t, err)
				model := gjson.GetBytes(borrowCoreBody(t, req), "model").String()
				state := "first"
				if req.Header.Get("X-Codex-Turn-State") != "" {
					state = "changed"
					if cookie != nil && cookie.Value == "replacement-cookie" && replacement == "usable" {
						state = "first"
					}
				}
				return borrowCoreResponse(model, "OK", state), nil
			}, borrowCoreAccount(1), a)
			originalExpiry := time.Now().Add(120 * time.Second)
			borrowCoreCandidate(s, originalExpiry)
			_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
			require.NoError(t, err)
			wire, applied, err := s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
			require.Equal(t, 1, sourceCalls, "at most one replacement source acquisition")
			if replacement == "usable" {
				require.NoError(t, err)
				require.True(t, applied.Applied)
				require.NoError(t, wire.Context().Err(), "retry cleanup must not cancel the business request")
				require.Contains(t, wire.Header.Get("Cookie"), "__oailb=replacement-cookie")
				require.Equal(t, 4, targetCalls)
				return
			}
			require.Error(t, err)
			require.Nil(t, applied)
			before := targetCalls
			_, _, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
			require.Error(t, err)
			require.Equal(t, 1, sourceCalls, "exhausted replacement cools acquisition")
			require.Equal(t, before, targetCalls, "cooldown does not revalidate a known rejected candidate")
			if replacement == "identical" {
				require.Equal(t, originalExpiry, s.candidate.expires, "the same rejected credential never gains lifetime")
				require.Equal(t, 2, targetCalls)
			} else {
				require.Equal(t, 4, targetCalls)
			}
		})
	}
}

func TestCodexBorrowCandidateReplacementPreservesOtherProofsAndWaiters(t *testing.T) {
	for _, other := range []string{"passed", "validating"} {
		t.Run(other, func(t *testing.T) {
			s := newBorrowCoreTest(t, nil)
			borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
			key := borrowHash(s.candidate.cookie.Value)
			check := codexGatewayBorrowTargetCheck{cookieKey: key, expires: s.candidate.expires, validating: other == "validating", result: CodexGatewayBorrowVerification{AccountID: 3, Model: "gpt-6-astra", Success: other == "passed"}}
			if other == "passed" {
				s.qualifications["other"] = check
			} else {
				s.targets[codexGatewayBorrowTargetKey{3, "gpt-6-astra"}] = check
			}
			err := &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Reason: "target_state_changed", CookieFingerprint: key}
			require.False(t, s.rejectCandidateForRetry(err, false))
			require.Empty(t, s.rejectedCookie)
		})
	}
}
