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

func enableBorrowRecovery(t *testing.T, s *CodexGatewayBorrowService) {
	t.Helper()
	cfg := s.ConfigSnapshot()
	cfg.WaitForQualifiedRoute, cfg.NodeProxyIDs = true, []int64{10}
	s.publishConfig(cfg, false)
	s.rotation.mu.Lock()
	err := s.rotation.initializeLocked(s.revision)
	s.rotation.mu.Unlock()
	require.NoError(t, err)
}

func seedBorrowRecoveryCooling(s *CodexGatewayBorrowService, model string, until time.Time) {
	s.rotation.cache(model).reject(2, borrowHash("http://node-10.invalid:8080"), "", until, time.Now())
	s.rotation.mu.Lock()
	s.rotation.retry[codexGatewayBorrowTargetKey{2, model}] = until
	s.rotation.mu.Unlock()
}

func TestCodexBorrowRecoveryRetainsRequestAndDispatchesOnceAfterCooling(t *testing.T) {
	s, calls, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
	enableBorrowRecovery(t, s)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	req.Header.Set("X-Codex-Turn-State", "original-business-state")
	req.Header.Set("Session-Id", "original-session")
	req.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"original-thread"}`)
	body := &struct{ io.ReadCloser }{req.Body}
	req.Body = body
	until := time.Now().Add(100 * time.Millisecond)
	seedBorrowRecoveryCooling(s, "gpt-6-astra", until)
	prepared, elapsed, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved-exit")
	require.NoError(t, err)
	require.GreaterOrEqual(t, elapsed, 80*time.Millisecond)
	require.Same(t, body, prepared.Body)
	require.Equal(t, "original-business-state", prepared.Header.Get("X-Codex-Turn-State"))
	require.Equal(t, req.Header.Get("X-Codex-Turn-Metadata"), prepared.Header.Get("X-Codex-Turn-Metadata"))
	require.Equal(t, "original-session", prepared.Header.Get("Session-Id"))
	require.Empty(t, req.Header.Get("Cookie"))
	require.Len(t, *calls, 3)
	row := s.Status().RecentUsage[0]
	require.False(t, row.Dispatched)
	require.Equal(t, "preparing", row.Outcome)
	require.Equal(t, "recovered", row.Recovery.Phase)
	require.EqualValues(t, 2, row.Recovery.Rounds)
	require.EqualValues(t, 1, row.Recovery.SourceRequests)
	require.EqualValues(t, 2, row.Recovery.ProbeRequests)
	var dispatched atomic.Int32
	s.gateway.httpUpstream = borrowCoreProbe(func(w *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		dispatched.Add(1)
		require.Equal(t, "http://node-10.invalid:8080", proxy)
		require.Equal(t, int64(2), id)
		require.Equal(t, "original-business-state", w.Header.Get("X-Codex-Turn-State"))
		data, err := io.ReadAll(w.Body)
		require.NoError(t, err)
		require.Equal(t, "business body remains unread", string(data))
		return borrowCoreResponse("gpt-6-astra", "done", "returned-business-state"), nil
	})
	resp, err := s.gateway.doOpenAICodexUpstream(prepared, a, "saved-exit")
	require.NoError(t, err)
	require.Equal(t, "returned-business-state", resp.Header.Get("X-Codex-Turn-State"))
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.EqualValues(t, 1, dispatched.Load())
	row = s.Status().RecentUsage[0]
	require.True(t, row.Dispatched)
	require.Equal(t, "completed", row.Outcome)
}

func TestCodexBorrowRecoveryCancelAndConfigChangeStopWithoutProbes(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "configuration"}[change], func(t *testing.T) {
			s, calls, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
			enableBorrowRecovery(t, s)
			a, req := nodeRequest(t, s, "gpt-6-astra")
			seedBorrowRecoveryCooling(s, "gpt-6-astra", time.Now().Add(time.Hour))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
				done <- err
			}()
			require.Eventually(t, func() bool {
				rows := s.Status().RecentUsage
				return len(rows) > 0 && rows[0].Recovery != nil && rows[0].Recovery.Phase == "cooling"
			}, time.Second, time.Millisecond)
			if change {
				cfg := s.ConfigSnapshot()
				cfg.WaitForQualifiedRoute = false
				s.publishConfig(cfg, false)
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if change {
					require.ErrorIs(t, err, ErrCodexGatewayBorrowChanged)
				} else {
					require.ErrorIs(t, err, context.Canceled)
					require.Equal(t, "cancelled", s.Status().RecentUsage[0].Recovery.Phase)
				}
			case <-time.After(time.Second):
				t.Fatal("recovery leaked after cancellation")
			}
			require.Empty(t, *calls)
		})
	}
}

func TestCodexBorrowRecoveryUsesLatestEligibleNodeDeadline(t *testing.T) {
	s, _, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
	enableBorrowRecovery(t, s)
	now := time.Now()
	until := now.Add(time.Hour)
	seedBorrowRecoveryCooling(s, "gpt-6-astra", until)
	next := s.nextBorrowRecoveryAttempt(t.Context(), 2, "gpt-6-astra", ErrCodexGatewayBorrowUnavailable)
	require.Equal(t, until, next)
	other := s.nextBorrowRecoveryAttempt(t.Context(), 2, "gpt-6.1-sol", ErrCodexGatewayBorrowUnavailable)
	require.WithinDuration(t, now.Add(15*time.Second), other, time.Second)
}

func TestCodexBorrowRecoveryDisabledAndDiagnosticsRemainBounded(t *testing.T) {
	s, calls, _ := nodeFixture(t, 1, func(int64, string) bool { return false })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved")
	require.Error(t, err)
	require.Len(t, *calls, 3)
	require.Nil(t, s.Status().RecentUsage[0].Recovery)
	_, err = DecodeCodexGatewayBorrowConfig([]byte(`{"enabled":true,"source_account_ids":[1],"target_account_ids":[2],"models":["gpt-6-astra"],"wait_for_qualified_route":true}`))
	require.ErrorContains(t, err, "requires node selection")
	require.False(t, borrowRecoveryRetryable(context.Canceled))
	require.False(t, borrowRecoveryRetryable(ErrCodexGatewayBorrowChanged))
	require.False(t, borrowRecoveryRetryable(errors.New("not a borrowing failure")))
	require.True(t, borrowRecoveryRetryable(&CodexGatewayBorrowFailure{Cause: io.ErrUnexpectedEOF, Reason: "target_probe_failed"}))
	require.False(t, borrowRecoveryRetryable(&CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Verification: &CodexGatewayBorrowVerification{MintStatus: 401}}))
}

func TestCodexBorrowRecoverySharedPreparationSurvivesOneCallerCancelling(t *testing.T) {
	s, calls, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
	enableBorrowRecovery(t, s)
	base := s.upstream
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
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
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
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
	require.Len(t, *calls, 3, "the remaining caller reuses the same bounded preparation")
}

func TestCodexBorrowRecoveryCoolingDoesNotBlockOtherModel(t *testing.T) {
	s, calls, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
	enableBorrowRecovery(t, s)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	seedBorrowRecoveryCooling(s, "gpt-6-astra", time.Now().Add(time.Hour))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
		done <- err
	}()
	require.Eventually(t, func() bool {
		rows := s.Status().RecentUsage
		return len(rows) > 0 && rows[0].Recovery != nil && rows[0].Recovery.Phase == "cooling"
	}, time.Second, time.Millisecond)
	_, sol := nodeRequest(t, s, "gpt-6.1-sol")
	_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), sol, a, "gpt-6.1-sol", "saved")
	require.NoError(t, err)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Len(t, *calls, 3)
	require.True(t, s.Status().Targets[1].CacheValid)
}

func TestCodexBorrowRecoveryLastCallerCancellationStopsTheProbe(t *testing.T) {
	s, _, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
	enableBorrowRecovery(t, s)
	entered, closed := make(chan struct{}), make(chan struct{})
	s.upstream = borrowCoreProbe(func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		close(closed)
		return nil, req.Context().Err()
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("probe outlived its last waiter")
	}
	require.Eventually(t, func() bool {
		s.rotation.flights.mu.Lock()
		defer s.rotation.flights.mu.Unlock()
		return len(s.rotation.flights.active) == 0
	}, time.Second, time.Millisecond)
}

type borrowRecoveryAccountSwitch struct {
	AccountRepository
	inactive atomic.Bool
}

func (r *borrowRecoveryAccountSwitch) GetByID(ctx context.Context, id int64) (*Account, error) {
	a, err := r.AccountRepository.GetByID(ctx, id)
	if err != nil || a == nil {
		return a, err
	}
	copy := *a
	if id == 2 && r.inactive.Load() {
		copy.Status = "disabled"
	}
	return &copy, nil
}

func TestCodexBorrowRecoveryStopsWhenAccountIsDisabledDuringCooling(t *testing.T) {
	s, calls, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
	enableBorrowRecovery(t, s)
	repo := &borrowRecoveryAccountSwitch{AccountRepository: s.accounts}
	s.accounts = repo
	s.gateway.accountRepo = repo
	a, req := nodeRequest(t, s, "gpt-6-astra")
	seedBorrowRecoveryCooling(s, "gpt-6-astra", time.Now().Add(time.Hour))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
		done <- err
	}()
	require.Eventually(t, func() bool {
		rows := s.Status().RecentUsage
		return len(rows) > 0 && rows[0].Recovery != nil && rows[0].Recovery.Phase == "cooling"
	}, time.Second, time.Millisecond)
	repo.inactive.Store(true)
	select {
	case err := <-done:
		require.ErrorContains(t, err, "account_inactive")
	case <-time.After(8 * time.Second):
		t.Fatal("disabled account kept waiting")
	}
	require.Empty(t, *calls)
}

func TestCodexBorrowRecoveryCoolingGatewayDoesNotHideUntestedNodes(t *testing.T) {
	s, calls, proxies := nodeFixture(t, 3, func(id int64, _ string) bool { return id == 12 })
	cfg := s.ConfigSnapshot()
	cfg.WaitForQualifiedRoute = true
	cfg.NodeProxyIDs = []int64{10, 11, 12}
	s.publishConfig(cfg, false)
	base := s.upstream
	alias := proxies.rows[11]
	s.upstream = borrowCoreProbe(func(req *http.Request, proxy string, id int64, n int, p *tlsfingerprint.Profile) (*http.Response, error) {
		resp, err := base.DoWithTLS(req, proxy, id, n, p)
		if err == nil && id == 1 && proxy == alias.URL() {
			resp.Header.Set("Set-Cookie", "__oailb="+borrowNodeCookie("chat.gateway.unified-10.api.openai.com")+"; Path=/; Secure; Max-Age=230")
		}
		return resp, err
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	prepared, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
	require.NoError(t, err)
	proof, ok := prepared.Context().Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation)
	require.True(t, ok)
	require.Equal(t, int64(12), proof.application.Verification.ProxyID)
	require.Len(t, *calls, 7, "two strict pairs and three source acquisitions; no target call to the cooling alias")
	require.EqualValues(t, 1, s.Status().RecentUsage[0].Recovery.Rounds)
	require.True(t, time.Now().Before(s.rotation.cache("gpt-6-astra").blockedUntil(2, borrowHash("http://node-10.invalid:8080"), "", time.Now())))
}

func TestCodexBorrowRecoveryIdentityEditCancelsCoolingWait(t *testing.T) {
	s, calls, _ := nodeFixture(t, 5, func(int64, string) bool { return true })
	enableBorrowRecovery(t, s)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	seedBorrowRecoveryCooling(s, "gpt-6-astra", time.Now().Add(time.Hour))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(ctx, req, a, "gpt-6-astra", "saved")
		done <- err
	}()
	require.Eventually(t, func() bool {
		rows := s.Status().RecentUsage
		return len(rows) > 0 && rows[0].Recovery != nil && rows[0].Recovery.Phase == "cooling"
	}, time.Second, time.Millisecond)
	notifyCodexFingerprintAccountChanged(2)
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrCodexGatewayBorrowChanged)
	case <-time.After(time.Second):
		t.Fatal("old identity kept recovering")
	}
	require.Empty(t, *calls)
}

func TestCodexBorrowRecoveryRenewsTheQualifiedNodeBeforeRotating(t *testing.T) {
	s, calls, _ := nodeFixture(t, 3, func(int64, string) bool { return true })
	cfg := s.ConfigSnapshot()
	cfg.WaitForQualifiedRoute = true
	cfg.NodeProxyIDs = []int64{10, 11}
	s.publishConfig(cfg, false)
	base := s.upstream
	s.upstream = borrowCoreProbe(func(req *http.Request, proxy string, id int64, n int, p *tlsfingerprint.Profile) (*http.Response, error) {
		resp, err := base.DoWithTLS(req, proxy, id, n, p)
		if err == nil && id == 1 {
			cookies := resp.Cookies()
			require.Len(t, cookies, 1)
			cookies[0].MaxAge = 1
			resp.Header.Set("Set-Cookie", cookies[0].String())
		}
		return resp, err
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	first, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved")
	require.NoError(t, err)
	proof, ok := first.Context().Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation)
	require.True(t, ok)
	timer := time.NewTimer(time.Until(proof.application.ExpiresAt.Add(20 * time.Millisecond)))
	defer timer.Stop()
	<-timer.C
	second, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved")
	require.NoError(t, err)
	next, ok := second.Context().Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation)
	require.True(t, ok)
	require.Equal(t, int64(10), next.application.Verification.ProxyID)
	require.Len(t, *calls, 6, "renewal reacquires a lease and repeats both target probes")
	for _, call := range *calls {
		require.Contains(t, call, "10:")
	}
	require.True(t, next.application.ExpiresAt.After(proof.application.ExpiresAt))
}
