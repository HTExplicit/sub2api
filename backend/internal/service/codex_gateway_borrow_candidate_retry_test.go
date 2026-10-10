package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

func TestCodexBorrowNonRotationRejectsWithoutReplacingSource(t *testing.T) {
	var sources, targets int
	account, other := borrowCoreAccount(2), borrowCoreAccount(3)
	reject := true
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 {
			sources++
			return borrowCoreResponse("gpt-6-astra", "OK", "source", "__oailb=fresh; Secure; Path=/; Max-Age=230"), nil
		}
		targets++
		state := "first"
		if id == 2 && reject && req.Header.Get("X-Codex-Turn-State") != "" {
			state = "changed"
		}
		return borrowCoreResponse("gpt-6.1-sol", "OK", state), nil
	}, borrowCoreAccount(1), account, other)
	borrowCoreCandidate(s, time.Now().Add(120*time.Second))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	original := s.candidate
	for range 2 {
		_, applied, err := s.Apply(req, account, "gpt-6.1-sol", "", nil, false)
		require.Nil(t, applied)
		var failure *CodexGatewayBorrowFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "target_state_changed", failure.Reason)
		require.NotNil(t, failure.RetryAfter)
	}
	require.Zero(t, sources)
	require.Equal(t, 2, targets, "cooldown retains verdict without another probe")
	require.Same(t, original, s.candidate, "target failure never retires source")
	_, otherReq, _, err := s.accountTemplate(context.Background(), 3, "gpt-6.1-sol")
	require.NoError(t, err)
	_, app, err := s.Apply(otherReq, other, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.True(t, app.Applied)
	reject = false
	for key, check := range s.qualifications {
		check.retryAfter = time.Now().Add(-time.Second)
		s.qualifications[key] = check
	}
	_, app, err = s.Apply(req, account, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.True(t, app.Applied, "next request resumes after cooldown")
	require.Equal(t, 6, targets)
	require.Zero(t, sources)
	original.expires = time.Now().Add(-time.Second)
	_, app, err = s.Apply(req, account, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.True(t, app.Applied)
	require.Equal(t, 1, sources)
	require.Equal(t, 8, targets)
	status := s.Status()
	require.Equal(t, "expired_route", status.Acquisition.Trigger)
	require.Equal(t, "candidate_acquired", status.Acquisition.Phase)
	require.NotNil(t, status.Acquisition.FinishedAt)
	require.False(t, status.Preparing)
}
func TestCodexBorrowSourcePoolPreservesOtherCandidates(t *testing.T) {
	s := newBorrowCoreTest(t, nil)
	borrowCoreCandidate(s, time.Now().Add(time.Minute))
	first := s.candidate
	s.storeCandidateLocked(first)
	second := *first
	second.sourceID, second.cookie.Value = 4, "second"
	s.config.SourceAccountIDs = []int64{1, 4}
	s.storeCandidateLocked(&second)
	require.True(t, s.candidateCurrentLocked(s.revision, first))
	repeated := *first
	repeated.expires = time.Now().Add(230 * time.Second)
	s.storeCandidateLocked(&repeated)
	require.Equal(t, first.expires, repeated.expires)
	repeated.expires = time.Now().Add(-time.Second)
	require.Same(t, &second, s.currentCandidateLocked())
	oldRev := s.revision
	s.publishConfig(CodexGatewayBorrowConfig{}, false)
	require.False(t, s.candidateCurrentLocked(oldRev, &second))
	require.Empty(t, s.candidates)
}
