package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestCodexBorrowFlightCancellationIsPerWaiter(t *testing.T) {
	var flights codexBorrowFlights
	var calls atomic.Int32
	started, finish := make(chan struct{}), make(chan struct{})
	run := func(ctx context.Context) (any, error) {
		calls.Add(1)
		close(started)
		select {
		case <-finish:
			return "ready", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	first, cancel := context.WithCancel(context.Background())
	firstResult, secondResult := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := flights.do(first, context.Background(), "same", time.Second, run)
		firstResult <- err
	}()
	<-started
	go func() {
		value, err := flights.do(context.Background(), context.Background(), "same", time.Second, run)
		if err == nil && value != "ready" {
			err = errors.New("missing shared value")
		}
		secondResult <- err
	}()
	require.Eventually(t, func() bool { flights.mu.Lock(); defer flights.mu.Unlock(); return flights.active["same"].waiters == 2 }, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-firstResult, context.Canceled)
	close(finish)
	require.NoError(t, <-secondResult)
	require.EqualValues(t, 1, calls.Load())
}

func TestCodexBorrowFlightRevisionCancelsPreparation(t *testing.T) {
	var flights codexBorrowFlights
	revision, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := flights.do(context.Background(), revision, "old", time.Second, func(ctx context.Context) (any, error) { close(started); <-ctx.Done(); return nil, ctx.Err() })
		result <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
}

func TestCodexBorrowPreparePreservesPartialSuccess(t *testing.T) {
	s := newBorrowCoreTest(t, func(r *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 2 {
			return nil, errors.New("target two rejected")
		}
		return borrowCoreResponse("gpt-6-astra", "OK", "stable-target-state", ""), nil
	}, borrowCoreAccount(1), borrowCoreAccount(2), borrowCoreAccount(3))
	s.publishConfig(CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2, 3}, Models: []string{"gpt-6-astra"}}, false)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	status, err := s.Prepare(context.Background())
	require.NoError(t, err)
	require.Equal(t, "partial", status.Setup.State)
	require.Equal(t, 2, status.Setup.Completed)
	require.Equal(t, 1, status.Setup.Failed)
	require.False(t, status.Targets[0].CacheValid)
	require.True(t, status.Targets[1].CacheValid)
	repo, ok := s.accounts.(*borrowCoreAccounts)
	require.True(t, ok)
	account := repo.rows[3]
	account.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-luna"}
	require.Equal(t, "model_mapping_mismatch", s.CurrentStatus(context.Background()).Targets[1].Reason)
}

func TestCodexBorrowDifferentTargetsDoNotReturnBusy(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	s := newBorrowCoreTest(t, func(r *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 2 && blocked.CompareAndSwap(false, true) {
			close(started)
			select {
			case <-finish:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return borrowCoreResponse("gpt-6-astra", "OK", "stable", ""), nil
	}, borrowCoreAccount(1), borrowCoreAccount(2), borrowCoreAccount(3))
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	first := make(chan error, 1)
	go func() { _, err := s.Verify(context.Background(), 2, "gpt-6-astra"); first <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.Verify(ctx, 3, "gpt-6-astra")
	close(finish)
	require.NoError(t, err)
	require.NoError(t, <-first)
}

func TestCodexBorrowUsagePreservesStreamAndNewestAttempt(t *testing.T) {
	s := newBorrowCoreTest(t, nil)
	raw := borrowCoreSSE("gpt-6-astra", "OK")
	tracker := s.beginUsage(context.Background(), 2, "gpt-6-astra", "http", true)
	response, _ := s.trackHTTPResponse(tracker, &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}, nil)
	read, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, raw, string(read))
	status := s.Status()
	require.Len(t, status.RecentUsage, 1)
	require.Equal(t, "completed", status.RecentUsage[0].Outcome)
	old := s.beginUsage(context.Background(), 2, "gpt-6-astra", "http", false)
	newer := s.beginUsage(context.Background(), 2, "gpt-6-astra", "http", true)
	old.finish(errors.New("old failure"))
	newer.observe([]byte(`{"type":"response.completed","response":{"id":"latest","model":"gpt-6-astra","status":"completed"}}`))
	newer.finish(nil)
	require.Equal(t, "latest", s.Status().RecentUsage[0].RequestID)
	require.EqualValues(t, 3, s.Status().RecentUsage[0].Count)
}

func TestCodexBorrowDiagnosticBudgetIncludesPreparation(t *testing.T) {
	var requests atomic.Int32
	ctx := context.WithValue(context.Background(), codexBorrowDiagnosticContextKey{}, &codexBorrowDiagnostic{requests: &requests, limit: 2})
	require.NoError(t, consumeBorrowDiagnosticRequest(ctx))
	require.NoError(t, consumeBorrowDiagnosticRequest(WithCodexGatewayBorrowObservation(ctx)))
	require.ErrorIs(t, consumeBorrowDiagnosticRequest(ctx), ErrCodexBorrowDiagnosticBudget)
	require.EqualValues(t, 2, requests.Load())
}

func TestCodexBorrowDistinctRequestProofsRemainReusable(t *testing.T) {
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		return borrowCoreResponse("gpt-6-astra", "OK", "stable", ""), nil
	}, a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, first, _, err := s.accountTemplate(context.Background(), 2, "gpt-6-astra")
	require.NoError(t, err)
	second := first.Clone(first.Context())
	second.Header.Set("X-Codex-Turn-State", "a-different-client-state")
	_, _, err = s.Apply(first, a, "gpt-6-astra", "", nil, false)
	require.NoError(t, err)
	_, _, err = s.Apply(second, a, "gpt-6-astra", "", nil, false)
	require.NoError(t, err)
	_, applied, err := s.Apply(first, a, "gpt-6-astra", "", nil, true)
	require.NoError(t, err)
	require.True(t, applied.Applied, "another request's validation must not replace this proof")
}

func TestCodexBorrowUsageKeepsFirstTerminal(t *testing.T) {
	completed := []byte(`{"type":"response.completed","response":{"status":"completed"}}`)
	failed := []byte(`{"type":"error","error":{"code":"synthetic_failure"}}`)
	for _, firstError := range []bool{false, true} {
		s := newBorrowCoreTest(t, nil)
		tracker := s.beginUsage(context.Background(), 2, "gpt-6-astra", "http", true)
		if firstError {
			tracker.observe(failed)
			tracker.observe(completed)
		} else {
			tracker.observe(completed)
			tracker.observe(failed)
		}
		tracker.finish(context.Canceled)
		want := "completed"
		if firstError {
			want = "upstream_error"
		}
		require.Equal(t, want, s.Status().RecentUsage[0].Outcome)
	}
}

func TestCodexBorrowHTTPAppliedChecksFinalCookie(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, chatgptCodexURL, nil)
	require.NoError(t, err)
	proof := codexGatewayBorrowHTTPPreparation{application: &CodexGatewayBorrowApplication{Applied: true, CookieFingerprint: borrowHash("qualified-cookie")}}
	req = req.WithContext(context.WithValue(req.Context(), codexGatewayBorrowHTTPPreparationContextKey{}, proof))
	require.False(t, codexBorrowHTTPApplied(req))
	req.Header.Set("Cookie", "__oailb=changed-cookie")
	require.False(t, codexBorrowHTTPApplied(req))
	req.Header.Set("Cookie", "__oailb=qualified-cookie")
	require.True(t, codexBorrowHTTPApplied(req))
}

func TestCodexBorrowCancelledParentCannotAdmitWaitingChild(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	coordinator := newPelicanExecutionCoordinator(1)
	state := newPelicanExecutionState(parent, coordinator, time.Minute, time.Now)
	defer state.finish()
	// Represent the cancellation propagation gap: the parent is cancelled,
	// but this child's cancellation signal is not visible to its waiter yet.
	ctx := context.WithValue(context.WithoutCancel(state.parent), pelicanExecutionContextKey{}, state)
	cancel()
	_, release, err := AcquirePelicanExecution(ctx, 2)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, release)
	require.Zero(t, coordinator.active)
}
