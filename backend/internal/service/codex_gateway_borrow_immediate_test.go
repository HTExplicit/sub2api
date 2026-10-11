package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func enableImmediateBorrow(t *testing.T, s *CodexGatewayBorrowService) {
	t.Helper()
	cfg := s.ConfigSnapshot()
	cfg.WaitForQualifiedRoute, cfg.ImmediateReborrowOnFailure = true, true
	cfg.NodeProxyIDs, cfg.MaxNodeAttempts = []int64{10}, 1
	s.publishConfig(cfg, false)
}

func TestCodexBorrowImmediateConfigPreservesLegacyDefaults(t *testing.T) {
	cfg, err := DecodeCodexGatewayBorrowConfig([]byte(`{"enabled":false,"node_cooldown_seconds":0}`))
	require.NoError(t, err)
	require.False(t, cfg.ImmediateReborrowOnFailure)
	_, err = DecodeCodexGatewayBorrowConfig([]byte(`{"enabled":true,"immediate_reborrow_on_failure":true}`))
	require.ErrorContains(t, err, "requires recovery waiting and node selection")
	cfg, err = DecodeCodexGatewayBorrowConfig([]byte(`{"enabled":true,"source_account_ids":[1],"target_account_ids":[2],"rotate_nodes":true,"node_proxy_ids":[10],"wait_for_qualified_route":true,"immediate_reborrow_on_failure":true,"node_cooldown_seconds":0}`))
	require.NoError(t, err)
	require.True(t, cfg.ImmediateReborrowOnFailure)
	require.Zero(t, cfg.NodeCooldownSeconds)
}

func TestCodexBorrowImmediateReacquiresAfterCachedRejectionAndEachFailedRound(t *testing.T) {
	var pairs atomic.Int32
	s, calls, _ := nodeFixture(t, 1, func(int64, string) bool { return pairs.Add(1) >= 3 })
	enableImmediateBorrow(t, s)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	req.Header.Set("X-Codex-Turn-State", "business-state")
	_, _, err := s.Apply(req, a, "gpt-6-astra", "saved", nil, false)
	require.Error(t, err)
	require.Len(t, *calls, 3)
	identity := borrowHash("http://node-10.invalid:8080")
	child := s.rotation.children[identity]
	child.mu.Lock()
	originalExpiry := child.candidate.expires
	child.prepareFailedUntil = time.Now().Add(time.Hour)
	child.prepareFailure = "old source failure"
	child.mu.Unlock()
	s.rotation.sourceCache.rejectSource(1, identity, time.Now())
	s.rotation.cache("gpt-6-astra").pauseSourceSearch(2, time.Now())
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	prepared, elapsed, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
	require.NoError(t, err)
	require.Less(t, elapsed, time.Second, "must not wait 15 seconds, source cooling, or a target gateway deadline")
	require.Len(t, *calls, 9, "old pair plus two fresh source acquisitions and complete target pairs")
	require.Equal(t, originalExpiry, child.candidate.expires, "reacquiring the same cookie must not extend its lease")
	require.Equal(t, "business-state", prepared.Header.Get("X-Codex-Turn-State"))
	row := s.Status().RecentUsage[0]
	require.EqualValues(t, 2, row.Recovery.Rounds)
	require.EqualValues(t, 2, row.Recovery.SourceRequests)
	require.EqualValues(t, 4, row.Recovery.ProbeRequests)
	require.Equal(t, "recovered", row.Recovery.Phase)
	require.False(t, row.Dispatched)
	// The failed evidence is not erased or converted into success for Sol.
	require.True(t, time.Now().Before(s.rotation.cache("gpt-6-astra").blockedUntil(2, identity, "", time.Now())))
	require.Zero(t, s.rotation.cache("gpt-6.1-sol").blockedUntil(2, identity, "", time.Now()))
	var sent atomic.Int32
	s.gateway.httpUpstream = borrowCoreProbe(func(w *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		sent.Add(1)
		require.Equal(t, int64(2), id)
		require.Equal(t, "http://node-10.invalid:8080", proxy)
		require.Equal(t, "business-state", w.Header.Get("X-Codex-Turn-State"))
		body, err := io.ReadAll(w.Body)
		require.NoError(t, err)
		require.Equal(t, "business body remains unread", string(body))
		return borrowCoreResponse("gpt-6-astra", "done", "actual-response-state"), nil
	})
	resp, err := s.gateway.doOpenAICodexUpstream(prepared, a, "saved")
	require.NoError(t, err)
	require.Equal(t, "actual-response-state", resp.Header.Get("X-Codex-Turn-State"))
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.EqualValues(t, 1, sent.Load())
}

func TestCodexBorrowImmediateFailedSourceCannotRetainRejectedCandidate(t *testing.T) {
	var pairs atomic.Int32
	s, calls, _ := nodeFixture(t, 1, func(int64, string) bool { return pairs.Add(1) > 1 })
	enableImmediateBorrow(t, s)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, _, err := s.Apply(req, a, "gpt-6-astra", "saved", nil, false)
	require.Error(t, err)
	child := s.rotation.children[borrowHash("http://node-10.invalid:8080")]
	base := child.upstream
	var sources atomic.Int32
	child.upstream = borrowCoreProbe(func(w *http.Request, proxy string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 && sources.Add(1) == 1 {
			return nil, errors.New("synthetic source transport failure")
		}
		return base.DoWithTLS(w, proxy, id, n, profile)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, _, err = s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
	require.NoError(t, err)
	require.EqualValues(t, 2, sources.Load())
	require.Len(t, *calls, 6, "a failed recapture must not start probes using the retained old candidate")
}

func TestCodexBorrowImmediateNoEligibleNodesStopsWithoutSpinning(t *testing.T) {
	s, calls, proxies := nodeFixture(t, 1, func(int64, string) bool { return false })
	enableImmediateBorrow(t, s)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	delete(proxies.rows, 10)
	_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved")
	require.ErrorContains(t, err, "node_candidates_unavailable")
	require.Empty(t, *calls)
	require.Equal(t, "stopped", s.Status().RecentUsage[0].Recovery.Phase)
}

func TestCodexBorrowImmediateExpiredProofRequiresFreshAcquisitionAndProbes(t *testing.T) {
	s, calls, _ := nodeFixture(t, 1, func(int64, string) bool { return true })
	enableImmediateBorrow(t, s)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, _, err := s.Apply(req, a, "gpt-6-astra", "saved", nil, false)
	require.NoError(t, err)
	child := s.rotation.children[borrowHash("http://node-10.invalid:8080")]
	expired := time.Now().Add(-time.Second)
	child.mu.Lock()
	child.candidate.expires = expired
	value := child.candidate.cookie.Value
	child.mu.Unlock()
	s.rotation.mu.Lock()
	s.rotation.leases[borrowHash(value)] = expired
	s.rotation.mu.Unlock()
	_, _, err = s.Apply(req, a, "gpt-6-astra", "saved", nil, true)
	require.Error(t, err)
	require.Len(t, *calls, 3, "an expired proof cannot dispatch or probe underneath business sending")
	_, _, err = s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved")
	require.NoError(t, err)
	require.Len(t, *calls, 6, "lease expiry requires a new source response and two complete target probes, even if the routing cookie value is the same")
	require.True(t, time.Now().Before(child.candidate.expires))
	require.LessOrEqual(t, time.Until(child.candidate.expires), 230*time.Second)
}

func TestCodexBorrowImmediateCancelledProbeReleasesPreparation(t *testing.T) {
	s, _, _ := nodeFixture(t, 1, func(int64, string) bool { return false })
	enableImmediateBorrow(t, s)
	base := s.upstream
	started := make(chan struct{}, 1)
	s.upstream = borrowCoreProbe(func(w *http.Request, proxy string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 2 {
			started <- struct{}{}
			<-w.Context().Done()
			return nil, w.Context().Err()
		}
		return base.DoWithTLS(w, proxy, id, n, profile)
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
		done <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Eventually(t, func() bool {
		s.rotation.flights.mu.Lock()
		defer s.rotation.flights.mu.Unlock()
		return len(s.rotation.flights.active) == 0
	}, time.Second, time.Millisecond)
	require.Equal(t, "cancelled", s.Status().RecentUsage[0].Recovery.Phase)
}

func TestCodexBorrowImmediateSharedPreparationSurvivesOneCancellation(t *testing.T) {
	s, calls, _ := nodeFixture(t, 1, func(int64, string) bool { return true })
	enableImmediateBorrow(t, s)
	base := s.upstream
	entered, release := make(chan struct{}, 1), make(chan struct{})
	s.upstream = borrowCoreProbe(func(req *http.Request, proxy string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		return base.DoWithTLS(req, proxy, id, n, profile)
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
		first <- err
	}()
	<-entered
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved")
		second <- err
	}()
	require.Eventually(t, func() bool {
		s.rotation.flights.mu.Lock()
		defer s.rotation.flights.mu.Unlock()
		for _, f := range s.rotation.flights.active {
			if f.waiters == 2 {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	close(release)
	require.NoError(t, <-second)
	require.Len(t, *calls, 3)
}

type immediateBorrowAccountGuard struct {
	AccountRepository
	inactive atomic.Bool
}

func (r *immediateBorrowAccountGuard) GetByID(ctx context.Context, id int64) (*Account, error) {
	a, err := r.AccountRepository.GetByID(ctx, id)
	if err == nil && id == 2 && r.inactive.Load() {
		copy := *a
		copy.Status = "disabled"
		return &copy, nil
	}
	return a, err
}

func TestCodexBorrowImmediateDisabledAccountCancelsAnOngoingProbe(t *testing.T) {
	s, _, _ := nodeFixture(t, 1, func(int64, string) bool { return false })
	enableImmediateBorrow(t, s)
	guard := &immediateBorrowAccountGuard{AccountRepository: s.accounts}
	s.accounts, s.gateway.accountRepo = guard, guard
	base := s.upstream
	entered := make(chan struct{}, 1)
	s.upstream = borrowCoreProbe(func(w *http.Request, proxy string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 2 {
			entered <- struct{}{}
			<-w.Context().Done()
			return nil, w.Context().Err()
		}
		return base.DoWithTLS(w, proxy, id, n, profile)
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
		done <- err
	}()
	<-entered
	guard.inactive.Store(true)
	require.ErrorContains(t, <-done, "account_inactive")
	require.False(t, s.Status().RecentUsage[0].Dispatched)
}
