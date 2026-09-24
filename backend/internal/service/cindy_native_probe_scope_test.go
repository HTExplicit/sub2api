//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

type scopedProbeTerminalFixture struct {
	CindyBalanceProbeRepository
	committed                *CindyHealthEpisode
	err                      error
	scopedCalls, legacyCalls int
}

func (r *scopedProbeTerminalFixture) FinalizeScopedExhausted(context.Context, *CindyBalanceProbeReservation, string, time.Time, time.Duration) (string, *CindyHealthEpisode, error) {
	r.scopedCalls++
	return "exhausted", r.committed, r.err
}
func (r *scopedProbeTerminalFixture) FinalizeExhausted(context.Context, *CindyBalanceProbeReservation, string, time.Time, time.Duration) (string, error) {
	r.legacyCalls++
	return "exhausted", nil
}

type committedProbeProjectorFixture struct{ observed, projected int }

func (p *committedProbeProjectorFixture) ObserveCindyHealthSignal(context.Context, *Account, CindyHealthSignal) {
	p.observed++
}
func (*committedProbeProjectorFixture) ObserveCindyHealthSuccess(context.Context, *Account) {}
func (p *committedProbeProjectorFixture) ApplyCommittedProbeTerminal(context.Context, *Account, CindyHealthEpisode) {
	p.projected++
}

func TestNativeCindyProbeRequiresAtomicTerminalFactWithoutDeferredRetry(t *testing.T) {
	worker := context.WithValue(context.Background(), cindyNativeProbeContextKey{}, true)
	account := &Account{ID: 2, Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: cindyCredentials()}
	projector := &committedProbeProjectorFixture{}
	repo := &scopedProbeTerminalFixture{committed: &CindyHealthEpisode{AccountID: 2, Generation: 1, EpisodeID: "committed", Fingerprint: strings.Repeat("a", 64), Status: CindyHealthStatusBalanceInsufficient, Evidence: CindyHealthEvidenceExactBudget, ObservedAt: time.Now()}}
	service := NewCindyBalanceProbeService(repo, nil, &OpenAIGatewayService{cindyHealth: projector}, nil)
	defer service.Stop()
	require.True(t, service.finalizeExhausted(worker, &CindyBalanceProbeReservation{AccountID: 2}, account, "lease"))
	require.Equal(t, 1, repo.scopedCalls)
	require.Zero(t, repo.legacyCalls)
	require.Equal(t, 1, projector.projected)
	require.Zero(t, projector.observed, "scoped success cannot create an unscoped pending terminal retry")
	repo.err = errors.New("terminal transaction rolled back")
	require.False(t, service.finalizeExhausted(worker, &CindyBalanceProbeReservation{AccountID: 2}, account, "lease"))
	require.Equal(t, 1, projector.projected)
	repo.err, repo.committed = nil, nil
	require.False(t, service.finalizeExhausted(worker, &CindyBalanceProbeReservation{AccountID: 2}, account, "lease"), "marker-only success is not complete success")
	require.Equal(t, 1, projector.projected)
}

func TestNativeCindyProbeKeepsHistoricalFrozenScope(t *testing.T) {
	origin := &CindyBalanceProbeOrigin{Version: 1, PluginID: 3, PluginKey: "codexrip.cindy-provider", RuntimeGeneration: 4,
		PackageSHA256: strings.Repeat("a", 64), RequestDigest: strings.Repeat("b", 64), FrozenAccountIDs: []int64{2, 7},
		View: json.RawMessage(`{"runtime_generation":4,"policy_revision":7,"opaque_future_field":true}`)}
	original, err := json.Marshal(origin)
	require.NoError(t, err)
	scope := CindyBalanceProbeScope{Mode: "all", Origin: origin}
	ctx, release, err := bindCindyProbeOrigin(context.Background(), scope)
	require.NoError(t, err)
	defer release()
	saved := ctx.Value(cindyProbeOriginContextKey{}).(*CindyBalanceProbeOrigin)
	raw, err := json.Marshal(saved)
	require.NoError(t, err)
	require.JSONEq(t, string(original), string(raw))
	require.NoError(t, ValidateCindyBalanceProbeOriginContext(ctx, origin))
	changed := *origin
	changed.FrozenAccountIDs = []int64{2, 7, 99}
	require.ErrorIs(t, ValidateCindyBalanceProbeOriginContext(ctx, &changed), ErrCindyBalanceProbeChanged)
	next, done, err := rebindCindyProbeOrigin(context.Background(), scope)
	require.NoError(t, err)
	defer done()
	require.NoError(t, ValidateCindyBalanceProbeOriginContext(next, origin))
	_, _, err = bindCindyProbeOrigin(context.Background(), CindyBalanceProbeScope{Mode: "all", Origin: &CindyBalanceProbeOrigin{Version: 1, RequestDigest: origin.RequestDigest}})
	require.ErrorIs(t, err, ErrCindyBalanceProbeChanged)
	native, err := captureCindyProbeOrigin(context.Background(), scope, 0.5, 2, "unused")
	require.NoError(t, err)
	require.Nil(t, native.Origin)
}
