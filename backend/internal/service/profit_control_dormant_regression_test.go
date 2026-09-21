package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type dormantProfitGroupRepository struct {
	GroupRepository
	group *Group
	reads []int64
}

func (r *dormantProfitGroupRepository) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	r.reads = append(r.reads, id)
	if r.group == nil || r.group.ID != id {
		return nil, fmt.Errorf("unexpected synthetic group lookup %d", id)
	}
	copy := *r.group
	return &copy, nil
}

func TestDormantProfitControlCrossGroupSnapshotCannotEnableAdmission(t *testing.T) {
	for _, engine := range []string{"openai", "gateway"} {
		t.Run(engine, func(t *testing.T) {
			// Auth-cache zeroing cannot sanitize a different scheduled group.
			// Keep a legacy-enabled row as the real snapshot service's repository
			// result, matching the current Ent projection's retained fields.
			legacy := &Group{ID: 902, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
				RateMultiplier: 0.2, ProfitControlEnabled: true, ProfitMinMargin: 0.3, ProfitSafetyBuffer: 0.05}
			if engine == "gateway" {
				legacy.Platform = PlatformAnthropic
			}
			preserved := *legacy
			repository := &dormantProfitGroupRepository{group: legacy}
			snapshot := NewSchedulerSnapshotService(nil, nil, nil, repository, nil)
			projected, err := snapshot.GetGroupByIDLite(context.Background(), legacy.ID)
			require.NoError(t, err)
			require.True(t, projected.ProfitControlEnabled, "fixture must not silently zero the legacy projection")
			require.Equal(t, []int64{legacy.ID}, repository.reads)
			billing := &Group{ID: 901, Platform: PlatformComposite, Status: StatusActive, Hydrated: true,
				RateMultiplier: 0.7, SubscriptionType: SubscriptionTypeStandard}
			base := context.WithValue(context.Background(), ctxkey.Group, billing)
			var ctx context.Context
			var pricingAt time.Time
			if engine == "openai" {
				ctx, pricingAt = (&OpenAIGatewayService{schedulerSnapshot: snapshot}).WithOpenAIRequestPricingContext(base, &legacy.ID)
				require.Equal(t, pricingAt, OpenAIPricingAtFromContext(ctx))
			} else {
				ctx, pricingAt = WithGatewayTokenRequestPricing(base)
				ctx = (&GatewayService{schedulerSnapshot: snapshot}).withGatewayProfitControlGate(ctx, &legacy.ID)
				actual, ok := gatewayTokenRequestPricingAtFromContext(ctx)
				require.True(t, ok)
				require.Equal(t, pricingAt, actual)
			}
			require.False(t, pricingAt.IsZero(), "retirement cannot remove the billing pricing instant")
			gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
			require.Nil(t, gate, "legacy cross-group fields cannot reactivate the retired gate; lookups=%v", repository.reads)
			require.Equal(t, []int64{legacy.ID}, repository.reads, "runtime retirement must not depend on reading or rewriting a legacy row")
			rate := 5.0
			vetoed, reason := OpenAIProfitControlVeto(ctx, &Account{ID: 903, RateMultiplier: &rate})
			require.False(t, vetoed)
			require.Empty(t, reason)
			require.Equal(t, preserved, *legacy, "stored configuration remains untouched")
			require.Equal(t, 0.7, billing.RateMultiplier, "ordinary billing multipliers remain untouched")
		})
	}
}

func TestDormantProfitControlClearsGateAtBoundariesButKeepsPrivateMath(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	gate := &openAIProfitControlGate{groupID: 12, platform: PlatformOpenAI, threshold: 0, pricingAt: now}
	base := context.WithValue(context.Background(), openAIPricingAtCtxKey{}, now)
	base = context.WithValue(base, openAIProfitControlGateCtxKey{}, gate)
	rate := 5.0
	account := &Account{ID: 13, RateMultiplier: &rate}

	vetoed, reason := openAIProfitControlVetoReason(base, account)
	require.True(t, vetoed, "private numerical predicates remain available to upstream compatibility fixtures")
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	latest, vetoed, reason := profitControlVetoLatest(base, account, nil)
	require.Same(t, account, latest)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)

	request, requestAt := (&OpenAIGatewayService{}).WithOpenAIRequestPricingContext(base, &gate.groupID)
	carried, _ := request.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Nil(t, carried)
	require.Equal(t, requestAt, OpenAIPricingAtFromContext(request))
	require.False(t, gatewayProfitControlGateActive(request))
	vetoed, reason = openAIProfitControlVetoReason(request, account)
	require.False(t, vetoed)
	require.Empty(t, reason)
	selection := &AccountSelectionResult{Account: account}
	require.Same(t, selection, attachSelectionProfitGate(request, selection))
	require.Nil(t, selection.profitGate)
	require.Same(t, request, ContextWithSelectionProfitGate(request, selection))
	turn, turnAt := (&OpenAIGatewayService{}).WithOpenAITurnPricingContext(base, &gate.groupID)
	carried, _ = turn.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Nil(t, carried)
	require.Equal(t, turnAt, OpenAIPricingAtFromContext(turn))
	require.False(t, turnAt.Before(now))
	general := (&GatewayService{}).withGatewayProfitControlGate(base, &gate.groupID)
	carried, _ = general.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Nil(t, carried)
}
