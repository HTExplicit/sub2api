//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountViewProbeScopeBeforeCountingAndFrozenOrigin(t *testing.T) {
	manager, plugins, directory, request := newViewFixture(t)
	plugins.installations[1].Bindings[1].RolloutPercent = 50
	applyViewFixture(t, manager, plugins)
	view, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer releaseView()
	ctx, release, err := manager.BindResourceContext(view, 1, request.PackageSHA256, plugins.installations[1].Manifest.Resources[0])
	require.NoError(t, err)
	defer release()
	ctx = WithCindyProbeOperationKey(ctx, "probe-snapshot")
	scope, err := captureCindyProbeOrigin(ctx, CindyBalanceProbeScope{Mode: "all"}, 0.5, 1, "fingerprint")
	require.NoError(t, err)
	accounts := []Account{*directory.accounts[5], *directory.accounts[3], *directory.accounts[2]}
	preview, err := BuildCindyBalanceProbePreviewFromSnapshotContext(ctx, scope, accounts, 0.5, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, preview.CandidateCount)
	require.EqualValues(t, 2, preview.Candidates[0].AccountID)
	scope.Origin.FrozenAccountIDs = []int64{2}
	stored := DecodeCindyBalanceProbeScope(EncodeCindyBalanceProbeScope(scope))
	require.Equal(t, "probe-snapshot", stored.Origin.OperationKey)
	require.Equal(t, []int64{2}, stored.Origin.FrozenAccountIDs)
	worker, workerRelease, err := manager.BindCindyBalanceProbeOrigin(context.Background(), stored)
	require.NoError(t, err)
	defer workerRelease()
	primary, _ := PluginExecutionFromContext(worker)
	origin, _, _ := AccountViewExecutionFromContext(worker)
	require.EqualValues(t, 1, primary.ID)
	require.EqualValues(t, 1, origin.ID)
	plugins.installations[1].Bindings[1].Enabled = false
	applyViewFixture(t, manager, plugins)
	_, _, err = manager.BindCindyBalanceProbeOrigin(context.Background(), stored)
	require.Error(t, err)
	bad := DecodeCindyBalanceProbeScope([]byte(`{"mode":"all","origin":"malformed"}`))
	require.NotNil(t, bad.Origin, "malformed origin must not become a legacy job")
	_, _, err = manager.BindCindyBalanceProbeOrigin(context.Background(), bad)
	require.Error(t, err)
}

func TestAccountViewProbeKeepsProviderFullScopeAndRejectsExplicitSubset(t *testing.T) {
	manager, plugins, directory, request := newViewFixture(t)
	ctx, release, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer release()
	_, err = BuildCindyBalanceProbePreviewFromSnapshotContext(ctx, CindyBalanceProbeScope{Mode: "selected", AccountIDs: []int64{2, 5}}, []Account{*directory.accounts[2], *directory.accounts[5]}, 0.5, time.Now())
	require.Error(t, err, "explicit outside-view selection must reject the whole preview")
	primary, primaryRelease, err := manager.BindResourceContext(ctx, 1, request.PackageSHA256, plugins.installations[1].Manifest.Resources[0])
	require.NoError(t, err)
	defer primaryRelease()
	scope, err := captureCindyProbeOrigin(primary, CindyBalanceProbeScope{Mode: "all"}, 0.5, 1, "fingerprint")
	require.NoError(t, err)
	scope.Origin.FrozenAccountIDs = []int64{2}
	plugins.installations[1].Bindings[0].RolloutPercent = 50
	applyViewFixture(t, manager, plugins)
	_, _, err = manager.BindCindyBalanceProbeOrigin(context.Background(), scope)
	require.Error(t, err, "all/filter probe keeps the existing Provider100 gate")
}

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

func TestAccountViewProbeRequiresAtomicTerminalFactWithoutDeferredRetry(t *testing.T) {
	manager, plugins, directory, request := newViewFixture(t)
	view, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer releaseView()
	ctx, release, err := manager.BindResourceContext(view, 1, request.PackageSHA256, plugins.installations[1].Manifest.Resources[0])
	require.NoError(t, err)
	defer release()
	scope, err := captureCindyProbeOrigin(ctx, CindyBalanceProbeScope{Mode: "selected", AccountIDs: []int64{2}}, 0.5, 1, "fingerprint")
	require.NoError(t, err)
	scope.Origin.FrozenAccountIDs = []int64{2}
	worker, workerRelease, err := manager.BindCindyBalanceProbeOrigin(context.Background(), scope)
	require.NoError(t, err)
	defer workerRelease()
	projector := &committedProbeProjectorFixture{}
	repo := &scopedProbeTerminalFixture{committed: &CindyHealthEpisode{AccountID: 2, Generation: 1, EpisodeID: "committed", Fingerprint: strings.Repeat("a", 64), Status: CindyHealthStatusBalanceInsufficient, Evidence: CindyHealthEvidenceExactBudget, ObservedAt: time.Now()}}
	service := NewCindyBalanceProbeService(repo, nil, &OpenAIGatewayService{cindyHealth: projector}, nil)
	defer service.Stop()
	require.True(t, service.finalizeExhausted(worker, &CindyBalanceProbeReservation{AccountID: 2}, directory.accounts[2], "lease"))
	require.Equal(t, 1, repo.scopedCalls)
	require.Zero(t, repo.legacyCalls)
	require.Equal(t, 1, projector.projected)
	require.Zero(t, projector.observed, "scoped success cannot create an unscoped pending terminal retry")
	repo.err = errors.New("terminal transaction rolled back")
	require.False(t, service.finalizeExhausted(worker, &CindyBalanceProbeReservation{AccountID: 2}, directory.accounts[2], "lease"))
	require.Equal(t, 1, projector.projected)
	repo.err, repo.committed = nil, nil
	require.False(t, service.finalizeExhausted(worker, &CindyBalanceProbeReservation{AccountID: 2}, directory.accounts[2], "lease"), "marker-only success is not complete success")
	require.Equal(t, 1, projector.projected)
}

func TestAccountViewProbeOriginContainsNoExecutionQueryOrAccountMaps(t *testing.T) {
	manager, plugins, _, request := newViewFixture(t)
	request.Query.Search = "private-search-not-metadata"
	view, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer releaseView()
	ctx, release, err := manager.BindResourceContext(view, 1, request.PackageSHA256, plugins.installations[1].Manifest.Resources[0])
	require.NoError(t, err)
	defer release()
	scope, err := captureCindyProbeOrigin(ctx, CindyBalanceProbeScope{Mode: "all"}, 0.5, 1, "fingerprint")
	require.NoError(t, err)
	raw, err := json.Marshal(scope.Origin)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-search-not-metadata")
	require.NotContains(t, string(raw), "credentials")
	require.NotContains(t, string(raw), "synthetic-private-key")
}

func TestAccountViewProbeExplicitResumeReadmitsSameDefinitionOnly(t *testing.T) {
	manager, plugins, _, request := newViewFixture(t)
	view, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer releaseView()
	ctx, release, err := manager.BindResourceContext(view, 1, request.PackageSHA256, plugins.installations[1].Manifest.Resources[0])
	require.NoError(t, err)
	defer release()
	scope, err := captureCindyProbeOrigin(ctx, CindyBalanceProbeScope{Mode: "all"}, 0.5, 2, "fingerprint")
	require.NoError(t, err)
	scope.Origin.FrozenAccountIDs = []int64{2, 3}
	original, _ := json.Marshal(scope.Origin)
	plugins.installations[1].RuntimeGeneration++
	plugins.installations[1].Revision++
	applyViewFixture(t, manager, plugins)
	_, _, err = manager.BindCindyBalanceProbeOrigin(context.Background(), scope)
	require.Error(t, err, "automatic worker cannot adopt a fresh generation")
	resumed, resumedRelease, err := manager.RebindCindyBalanceProbeOrigin(context.Background(), scope)
	require.NoError(t, err)
	next := resumed.Value(cindyProbeOriginContextKey{}).(*CindyBalanceProbeOrigin)
	require.Equal(t, scope.Origin.RuntimeGeneration+1, next.RuntimeGeneration)
	require.Equal(t, scope.Origin.View.RuntimeGeneration+1, next.View.RuntimeGeneration)
	require.Equal(t, scope.Origin.RequestDigest, next.RequestDigest)
	require.Equal(t, []int64{2, 3}, next.FrozenAccountIDs)
	unchanged, _ := json.Marshal(scope.Origin)
	require.Equal(t, original, unchanged, "re-admission must not mutate the saved origin in place")
	resumedRelease()
	plugins.installations[1].Bindings[1].RolloutPercent = 50
	applyViewFixture(t, manager, plugins)
	_, _, err = manager.RebindCindyBalanceProbeOrigin(context.Background(), scope)
	require.Error(t, err, "narrowed live scope cannot silently drop a frozen target")
	plugins.installations[1].Bindings[1].RolloutPercent = 100
	plugins.installations[1].PackageSHA256 = strings.Repeat("d", 64)
	applyViewFixture(t, manager, plugins)
	_, _, err = manager.RebindCindyBalanceProbeOrigin(context.Background(), scope)
	require.Error(t, err, "explicit resume cannot cross package identity")
}
