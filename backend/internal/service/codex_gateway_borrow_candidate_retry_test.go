package service

import (
	"context"
	"net/http"
	"sync/atomic"
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
				s.validatingRoutes = map[string]int{key: 1}
			}
			err := &CodexGatewayBorrowFailure{Revision: s.revision, Cause: ErrCodexGatewayBorrowUnavailable, Reason: "target_state_changed", CookieFingerprint: key}
			require.False(t, s.rejectCandidateForRetry(err, false))
			require.Empty(t, s.rejectedCookie)
		})
	}
}

func TestCodexBorrowReplacementWaitsForHiddenRequestIdentity(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if first.CompareAndSwap(false, true) {
			close(started)
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		return borrowCoreResponse("gpt-6.1-sol", "OK", "stable"), nil
	}, a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, _, runErr := s.Apply(req, a, "gpt-6.1-sol", "", nil, false); done <- runErr }()
	<-started
	s.mu.Lock()
	key := borrowHash(s.candidate.cookie.Value)
	failure := &CodexGatewayBorrowFailure{Revision: s.revision, Cause: ErrCodexGatewayBorrowUnavailable, Reason: "target_state_changed", CookieFingerprint: key}
	// Another identity completed later and owns the presentation row, while
	// the first identity's real validation is still using this candidate.
	s.targets[codexGatewayBorrowTargetKey{2, "gpt-6.1-sol"}] = codexGatewayBorrowTargetCheck{cookieKey: key, validationID: 999}
	s.mu.Unlock()
	retired := s.rejectCandidateForRetry(failure, false)
	close(release)
	require.False(t, retired)
	require.NoError(t, <-done)
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Empty(t, s.validatingRoutes)
}

func TestCodexBorrowReplacementCannotRetireNewConfiguration(t *testing.T) {
	s := newBorrowCoreTest(t, nil)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	err := &CodexGatewayBorrowFailure{Revision: s.revision, Cause: ErrCodexGatewayBorrowUnavailable, Reason: "target_state_changed", CookieFingerprint: borrowHash(s.candidate.cookie.Value)}
	s.publishConfig(CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{4}, TargetAccountIDs: []int64{2}, Models: []string{"gpt-6-astra"}}, false)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	require.False(t, s.rejectCandidateForRetry(err, false), "even an identical cookie belongs to the new config revision")
	require.Empty(t, s.rejectedCookie)
}

func TestCodexBorrowReplacementBudgetDoesNotCoolOtherCallers(t *testing.T) {
	var sourceCalls int
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 {
			sourceCalls++
			return borrowCoreResponse("gpt-6-astra", "OK", "source", "__oailb=replacement-cookie; Secure; Path=/; Max-Age=230"), nil
		}
		state := "first"
		cookie, cookieErr := req.Cookie("__oailb")
		assert.NoError(t, cookieErr)
		if req.Header.Get("X-Codex-Turn-State") != "" && cookie != nil && cookie.Value != "replacement-cookie" {
			state = "changed"
		}
		return borrowCoreResponse("gpt-6.1-sol", "OK", state), nil
	}, borrowCoreAccount(1), a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	var used atomic.Int32
	limited := context.WithValue(req.Context(), codexBorrowDiagnosticContextKey{}, &codexBorrowDiagnostic{limit: 2, requests: &used, borrow: true})
	_, _, err = s.Apply(req.WithContext(limited), a, "gpt-6.1-sol", "", nil, false)
	require.ErrorIs(t, err, ErrCodexBorrowDiagnosticBudget)
	require.EqualValues(t, 2, used.Load())
	require.Zero(t, sourceCalls)
	require.True(t, s.prepareFailedUntil.IsZero(), "a caller's cap is not a source failure")
	_, applied, err := s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.Equal(t, 1, sourceCalls)
}

func TestCodexBorrowReplacementCannotReplayEarlierRejectedCredential(t *testing.T) {
	var sources, targets int
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 {
			sources++
			cookie := "replacement-cookie"
			if sources == 2 {
				cookie = "synthetic-borrowed-cookie"
			}
			return borrowCoreResponse("gpt-6-astra", "OK", "source", "__oailb="+cookie+"; Secure; Path=/; Max-Age=230"), nil
		}
		targets++
		state := "first"
		if req.Header.Get("X-Codex-Turn-State") != "" {
			state = "changed"
		}
		return borrowCoreResponse("gpt-6.1-sol", "OK", state), nil
	}, borrowCoreAccount(1), a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	_, _, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.Error(t, err)
	require.Equal(t, 1, sources)
	require.Equal(t, 4, targets)
	expires := s.candidate.expires
	s.prepareFailedUntil = time.Time{}
	_, _, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.Error(t, err)
	require.Equal(t, 2, sources)
	require.Equal(t, 4, targets, "an earlier rejected cookie is not qualified again")
	require.Equal(t, "replacement-cookie", s.candidate.cookie.Value)
	require.Equal(t, expires, s.candidate.expires)
}

func TestCodexBorrowExpiredRejectionRequiresFreshSourceAndProof(t *testing.T) {
	var sources, targets int
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(_ *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 {
			sources++
			return borrowCoreResponse("gpt-6-astra", "OK", "source", "__oailb=synthetic-borrowed-cookie; Secure; Path=/; Max-Age=230"), nil
		}
		targets++
		return borrowCoreResponse("gpt-6.1-sol", "OK", "fresh-stable-state"), nil
	}, borrowCoreAccount(1), a)
	oldExpiry := time.Now().Add(-time.Second)
	borrowCoreCandidate(s, oldExpiry)
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	key := borrowHash(s.candidate.cookie.Value)
	s.rejectedCookie = key
	s.rejectedRoutes = map[string]time.Time{key: oldExpiry}
	s.targets[codexGatewayBorrowTargetKey{2, "gpt-6.1-sol"}] = codexGatewayBorrowTargetCheck{
		key: borrowTargetFingerprint(req, a, "gpt-6.1-sol", "", nil, s.candidate.cookie.Value), cookieKey: key,
		expires: oldExpiry, retryAfter: time.Now().Add(time.Minute),
		result: CodexGatewayBorrowVerification{Reason: "target_state_changed", ExpiresAt: &oldExpiry},
	}
	_, applied, err := s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.Equal(t, 1, sources, "expiration requires a new source observation")
	require.Equal(t, 2, targets, "an old failure cooldown cannot certify or reject the new lease")
	require.Empty(t, s.rejectedRoutes)
}
