//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type cindyProbeScopeRepository struct {
	CindyBalanceProbeRepository
	ready            bool
	completions      int
	healthWrites     int
	outcome, state   string
	networkFailure   bool
	completedAccount int64
	completedLease   string
}

func (r *cindyProbeScopeRepository) ValidateReservationForSend(context.Context, *CindyBalanceProbeReservation, *Account, string) (bool, error) {
	return r.ready, nil
}

func (r *cindyProbeScopeRepository) CompleteStage(_ context.Context, _ *CindyBalanceProbeReservation, account *Account, lease, outcome, state string, networkFailure bool) (bool, bool, error) {
	r.completions++
	r.outcome, r.state, r.networkFailure = outcome, state, networkFailure
	r.completedAccount, r.completedLease = account.ID, lease
	return true, true, nil
}

func (r *cindyProbeScopeRepository) FinalizeRecovery(context.Context, *CindyBalanceProbeReservation, *Account, string, time.Time) (bool, error) {
	r.healthWrites++
	return false, nil
}

func (r *cindyProbeScopeRepository) FinalizeExhausted(context.Context, *CindyBalanceProbeReservation, string, time.Time, time.Duration) (string, error) {
	r.healthWrites++
	return "", nil
}

type cindyProbeScopeUpstream struct {
	calls  int
	onSend func(*http.Request)
}

func (u *cindyProbeScopeUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	if u.onSend != nil {
		u.onSend(req)
	}
	return newCindyBalanceProbeResponse(http.StatusOK, "application/json", `{"id":"scope-fixture","object":"response","status":"completed","output":[{}],"usage":{"input_tokens":1,"output_tokens":1}}`), nil
}

func (u *cindyProbeScopeUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func TestCindyProbeReservationHonorsCurrentAccountScope(t *testing.T) {
	included, excluded := int64(2), int64(3)
	for _, tc := range []struct {
		name        string
		accountID   int64
		stale       bool
		disabled    bool
		unavailable bool
		planFailure bool
		cancel      bool
		lostLease   bool
		wantSend    bool
		wantStale   bool
	}{
		{name: "changed_identity", accountID: included, stale: true, wantStale: true},
		{name: "native_first_account", accountID: included, wantSend: true},
		{name: "native_other_account", accountID: excluded, wantSend: true},
		{name: "capability_disabled", accountID: included, disabled: true},
		{name: "runtime_unavailable_is_not_stale", accountID: included, unavailable: true},
		{name: "plan_failure_is_not_stale", accountID: included, planFailure: true},
		{name: "cancellation_is_not_stale", accountID: included, cancel: true},
		{name: "lost_reservation_is_not_stale", accountID: included, lostLease: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := captureNativeCindyTestInvoker()
			t.Cleanup(func() { restoreNativeCindyTestInvoker(previous) })
			var invocations []extensionv1.Invocation
			setNativeCindyTestInvoker(nativeCindyTestInvoker(func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
				if tc.disabled {
					return extensionv1.Result{Code: "disabled"}, nil
				}
				if tc.unavailable {
					return extensionv1.Result{}, ErrExtensionOperationUnavailable
				}
				invocations = append(invocations, in)
				if tc.planFailure && in.Operation == "cindy.probe.plan" {
					return extensionv1.Result{}, errors.New("synthetic policy failure")
				}
				var payload any
				switch in.Operation {
				case "cindy.features":
					payload = extensionv1.CindyProviderConfig{BalanceDetection: true}
				case "cindy.probe.plan":
					payload = extensionv1.CindyProbePlan{Models: [2]string{"tencent/hy3", "z-ai/glm-5.3-flash"}, Input: "Reply OK.", MaxOutputTokens: 1}
				case "cindy.probe.decide":
					payload = extensionv1.CindyProbeDecision{Action: "complete", Outcome: "success", State: "healthy"}
				default:
					payload = extensionv1.CindyResponseDecision{}
				}
				raw, err := json.Marshal(payload)
				return extensionv1.Result{Payload: raw}, err
			}))

			account := newFirstClassCindyRateLimitAccount(tc.accountID, false)
			fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
			require.NoError(t, err)
			reservation := &CindyBalanceProbeReservation{JobID: 17, ItemID: 18, AccountID: account.ID, Stage: "luna", LeaseToken: "scope-lease", RequestCount: 1, JobRequestCount: 5, IdentityFingerprint: fingerprint, AccountUpdatedAt: account.UpdatedAt}
			if tc.stale {
				reservation.IdentityFingerprint = "different-identity"
			}
			repo := &cindyProbeScopeRepository{ready: !tc.lostLease}
			upstream := &cindyProbeScopeUpstream{}
			svc := &CindyBalanceProbeService{repo: repo, accountRepo: &cindyBalanceProbeAccountRepositoryStub{account: account}, gateway: &OpenAIGatewayService{httpUpstream: upstream}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			keepRunning := svc.executeReservation(ctx, reservation, reservation.LeaseToken)
			require.Equal(t, tc.wantSend || tc.wantStale, keepRunning, "completed/skipped reservations continue; uncertain authority stops the current claim")
			if tc.wantSend {
				require.Equal(t, 1, upstream.calls)
				require.Equal(t, "healthy", repo.state)
				seenPlan, seenDecision := false, false
				for _, invocation := range invocations {
					if invocation.Operation == "cindy.probe.plan" || invocation.Operation == "cindy.probe.decide" {
						require.Equal(t, account.ID, invocation.AccountID)
						seenPlan = seenPlan || invocation.Operation == "cindy.probe.plan"
						seenDecision = seenDecision || invocation.Operation == "cindy.probe.decide"
					}
				}
				require.True(t, seenPlan && seenDecision)
			} else {
				require.Zero(t, upstream.calls, "excluded or unconfirmed policy must not reach HTTPUpstream.Do")
			}
			if tc.wantStale {
				require.Equal(t, "stale", repo.outcome)
				require.Equal(t, "skipped_stale", repo.state)
			}
			if tc.wantSend || tc.wantStale {
				require.Equal(t, 1, repo.completions)
				require.Equal(t, account.ID, repo.completedAccount)
				require.Equal(t, reservation.LeaseToken, repo.completedLease)
			} else {
				require.Zero(t, repo.completions, "cancellation or unavailable policy must not masquerade as a scope rejection")
			}
			require.False(t, repo.networkFailure)
			require.Zero(t, repo.healthWrites)
			require.Equal(t, 1, reservation.RequestCount, "reservation counters are not rewritten as actual-send counters")
			require.Equal(t, 5, reservation.JobRequestCount)
		})
	}
}

func TestCindyProbeReservationPolicyCancellationReachesActualHTTP(t *testing.T) {
	previous := captureNativeCindyTestInvoker()
	t.Cleanup(func() { restoreNativeCindyTestInvoker(previous) })
	setNativeCindyTestInvoker(nativeCindyTestInvoker(func(_ context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		var payload any = extensionv1.CindyProviderConfig{BalanceDetection: true}
		if in.Operation == "cindy.probe.plan" {
			payload = extensionv1.CindyProbePlan{Models: [2]string{"tencent/hy3", "z-ai/glm-5.3-flash"}, Input: "Reply OK.", MaxOutputTokens: 1}
		}
		raw, err := json.Marshal(payload)
		return extensionv1.Result{Payload: raw}, err
	}))
	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	account := newFirstClassCindyRateLimitAccount(37, false)
	fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	require.NoError(t, err)
	reservation := &CindyBalanceProbeReservation{JobID: 17, ItemID: 18, AccountID: account.ID, Stage: "luna", LeaseToken: "scope-lease", IdentityFingerprint: fingerprint, AccountUpdatedAt: account.UpdatedAt}
	repo := &cindyProbeScopeRepository{ready: true}
	requestCanceled := false
	upstream := &cindyProbeScopeUpstream{onSend: func(request *http.Request) {
		cancelRequest()
		requestCanceled = errors.Is(request.Context().Err(), context.Canceled)
	}}
	svc := &CindyBalanceProbeService{repo: repo, accountRepo: &cindyBalanceProbeAccountRepositoryStub{account: account}, gateway: &OpenAIGatewayService{httpUpstream: upstream}}
	require.False(t, svc.executeReservation(requestContext, reservation, reservation.LeaseToken))
	require.Equal(t, 1, upstream.calls)
	require.True(t, requestCanceled, "native cancellation must reach the actual HTTP context")
	require.Zero(t, repo.completions)
	require.Zero(t, repo.healthWrites)
	require.False(t, repo.networkFailure)
}
