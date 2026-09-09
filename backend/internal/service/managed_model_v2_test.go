package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func managedV2Fixture(t *testing.T) (*Group, []*Account) {
	t.Helper()
	const groupID int64 = 23
	const public = "claude-fable-5.1"
	endpoints := []string{CompositeRouteEndpointResponses, CompositeRouteEndpointMessages, CompositeRouteEndpointChatCompletions}
	group := &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, ManagedModelRoutes: ManagedModelRoutesConfig{Version: 2, Enabled: true}}
	accounts := []*Account{
		{ID: 41, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "test-native", "base_url": "https://native.example", "model_mapping": map[string]any{"private": "private-native"}}},
		{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "test-compat", "base_url": "https://compat.example", "model_mapping": map[string]any{"private": "private-compat"}}, Extra: map[string]any{"openai_responses_mode": "force_responses", "openai_responses_supported": true}},
	}
	route := ManagedModelRoute{PublicModel: public, Endpoints: endpoints}
	for _, item := range []struct {
		account          int
		protocol, target string
	}{
		{0, "messages", "claude-fable-5.1"}, {1, "responses", "fable-5.1"}, {1, "chat_completions", "provider/fable-5.1-CC"},
	} {
		account := accounts[item.account]
		selector := ManagedModelBranchSelector(groupID, public, account.Platform, item.protocol, item.target)
		account.Credentials["model_mapping"].(map[string]any)[selector] = item.target
		route.Branches = append(route.Branches, ManagedModelRouteBranch{Selector: selector, TargetPlatform: account.Platform, UpstreamProtocol: item.protocol, Endpoints: endpoints,
			Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: item.target, AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints}}})
	}
	group.ManagedModelRoutes.Routes = []ManagedModelRoute{route}
	return group, accounts
}

func TestManagedModelV2AllProvenPlatformsAndTargetsRemainEligible(t *testing.T) {
	group, accounts := managedV2Fixture(t)
	request, err := ResolveManagedModelRoute(group, "claude-fable-5.1", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	require.Len(t, ManagedModelRouteBranches(request.Route), 3)
	ctx := WithManagedModelRequest(context.Background(), request)
	for i, branch := range request.Route.Branches {
		account := accounts[0]
		if i > 0 {
			account = accounts[1]
		}
		require.True(t, ManagedModelAccountAllowed(ctx, account, branch.Selector))
		branchCtx := WithManagedModelBranch(ctx, branch)
		require.True(t, ManagedModelAccountAllowed(branchCtx, account, branch.Selector))
		selected, ok := ManagedModelRequestFromContext(branchCtx)
		require.True(t, ok)
		require.Equal(t, branch.Selector, selected.RoutingModel())
		require.Equal(t, branch.TargetPlatform, selected.TargetPlatform())
		for _, other := range request.Route.Branches {
			if other.Selector != branch.Selector {
				require.False(t, ManagedModelAccountAllowed(branchCtx, account, other.Selector))
			}
		}
	}
	require.Equal(t, "private-compat", accounts[1].GetMappedModel("private"))
	_, err = ResolveManagedModelRoute(group, "claude-fable-5", CompositeRouteEndpointResponses)
	require.ErrorIs(t, err, ErrManagedModelRouteUnavailable, "5.1 must not manufacture support for 5")
	_, err = ResolveManagedModelRoute(group, request.Route.Branches[1].Selector, CompositeRouteEndpointResponses)
	require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
}

func TestManagedModelV2WireOverridePreservesIdentityAndOriginalAccount(t *testing.T) {
	group, accounts := managedV2Fixture(t)
	request, err := ResolveManagedModelRoute(group, "claude-fable-5.1", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	account, branch := accounts[1], request.Route.Branches[2]
	before, err := json.Marshal(account)
	require.NoError(t, err)
	ctx, overlay, err := WithManagedModelAccountProtocol(WithManagedModelRequest(context.Background(), request), account, branch)
	require.NoError(t, err)
	require.NotSame(t, account, overlay)
	require.True(t, shouldForwardOpenAIResponsesViaRawChatCompletions(overlay))
	require.False(t, shouldForwardOpenAIResponsesViaRawChatCompletions(account))
	require.NotEqual(t, ManagedModelAccountFingerprint(account), ManagedModelAccountFingerprint(overlay))
	require.True(t, ManagedModelAccountAllowed(ctx, overlay, branch.Selector), "verified original config remains the identity, not the per-request protocol switch")
	require.NoError(t, validateManagedForwardAccount(ctx, &managedLatestAccountRepo{account: account}, overlay, branch.Selector))
	after, err := json.Marshal(account)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	overlay.Credentials["api_key"] = "unexpected-other-key"
	require.False(t, ManagedModelAccountAllowed(ctx, overlay, branch.Selector), "overlay must not waive unrelated credential changes")
	require.Equal(t, "test-compat", account.Credentials["api_key"])
}

func TestManagedModelV2RejectsForgedBranchAndKeepsLegacyProjection(t *testing.T) {
	for _, mutate := range []func(*ManagedModelRouteBranch){
		func(b *ManagedModelRouteBranch) { b.Selector += "forged" },
		func(b *ManagedModelRouteBranch) { b.UpstreamProtocol = "responses_websocket" },
		func(b *ManagedModelRouteBranch) { b.Accounts[0].UpstreamModel += "-ssvip" },
		func(b *ManagedModelRouteBranch) { b.TargetPlatform = PlatformCindy },
	} {
		group, _ := managedV2Fixture(t)
		mutate(&group.ManagedModelRoutes.Routes[0].Branches[0])
		_, err := ResolveManagedModelRoute(group, "claude-fable-5.1", CompositeRouteEndpointMessages)
		require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
	}
	group, account := managedRouteTestFixture(23, "unchanged-upstream")
	group.ManagedModelRoutes.Version = 2
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	legacy := ManagedModelRouteBranches(request.Route)[0]
	require.Empty(t, legacy.UpstreamProtocol)
	require.Equal(t, request.Route.Selector, legacy.Selector)
	require.True(t, ManagedModelAccountAllowed(WithManagedModelBranch(WithManagedModelRequest(context.Background(), request), legacy), account, legacy.Selector))
	group.ManagedModelRoutes.Routes[0].Branches = []ManagedModelRouteBranch{legacy}
	group.ManagedModelRoutes.Routes[0].Selector = ""
	group.ManagedModelRoutes.Routes[0].Accounts = nil
	_, err = ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err, "incremental extension can retain the exact v1 selector as a legacy branch")
}

type managedV2AccountRepo struct {
	AccountRepository
	accounts map[int64]*Account
	reads    map[int64]int
	batchReads int
}

func (r *managedV2AccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.reads[id]++
	return r.accounts[id], nil
}

func (r *managedV2AccountRepo) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	r.batchReads++
	out := make([]*Account, 0, len(ids))
	for _, id := range ids {
		r.reads[id]++
		if account := r.accounts[id]; account != nil { out = append(out, account) }
	}
	return out, nil
}

func TestManagedModelV2CandidatesDoNotCollapseSameAccountTargets(t *testing.T) {
	group, accounts := managedV2Fixture(t)
	accounts[0].Priority = 50
	accounts[1].Priority = 1
	request, err := ResolveManagedModelRoute(group, "claude-fable-5.1", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	repo := &managedV2AccountRepo{accounts: map[int64]*Account{41: accounts[0], 42: accounts[1]}, reads: make(map[int64]int)}
	svc := &GatewayService{accountRepo: repo}
	candidates, err := svc.ListManagedModelCandidates(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, candidates, 3)
	require.Equal(t, int64(42), candidates[0].Account.ID)
	require.Equal(t, int64(42), candidates[1].Account.ID)
	require.NotEqual(t, candidates[0].Key(), candidates[1].Key())
	require.Equal(t, 1, repo.reads[42], "same account is hydrated once per selection graph")
	require.Equal(t, 1, repo.batchReads, "all branch accounts use one bulk load, not one database roundtrip per account")
	reset := time.Now().Add(time.Hour)
	accounts[0].RateLimitResetAt = &reset
	candidates, err = svc.ListManagedModelCandidates(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, candidates, 2, "one native path cooling does not remove successful compatible paths")
	accounts[1].GroupIDs = []int64{99}
	candidates, err = svc.ListManagedModelCandidates(context.Background(), request)
	require.NoError(t, err)
	require.Empty(t, candidates, "membership is rechecked independently of fingerprint")
}

func TestManagedModelV2ChannelPriceStaysOnPublicGroupAndModel(t *testing.T) {
	group, _ := managedV2Fixture(t)
	request, err := ResolveManagedModelRoute(group, "claude-fable-5.1", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	branch := request.Route.Branches[2]
	ctx := WithManagedModelBranch(WithManagedModelRequest(context.Background(), request), branch)
	require.Equal(t, PlatformAnthropic, channelLookupPlatform(ctx, group.Platform))
	require.Equal(t, branch.TargetPlatform, channelLookupPlatform(ctx, PlatformComposite))
	require.Equal(t, request.Route.PublicModel, managedModelBillingModel(ctx, group.ID, branch.Selector))
	require.Equal(t, branch.Selector, managedModelBillingModel(ctx, 99, branch.Selector), "unrelated group prices are not rewritten")
	price := 0.00001
	channel := Channel{ID: 7, Status: StatusActive, GroupIDs: []int64{group.ID}, BillingModelSource: BillingModelSourceRequested,
		RestrictModels: true, ModelPricing: []ChannelModelPricing{{Platform: group.Platform, Models: []string{request.Route.PublicModel}, InputPrice: &price}},
		ModelMapping: map[string]map[string]string{group.Platform: {}}}
	for _, b := range request.Route.Branches {
		channel.ModelMapping[group.Platform][b.Selector] = b.Selector
	}
	channels := &ChannelService{}
	channels.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{group.ID: group.Platform}))
	svc := &GatewayService{channelService: channels}
	require.NoError(t, svc.ValidateManagedModelCompilation(ctx, group, request), "v2 needs no single-target composite route or messages dispatch rule")
	require.False(t, channels.IsModelRestricted(ctx, group.ID, branch.Selector), "a proven branch uses the existing public price, never a private or unpriced selector")
	require.NotNil(t, channels.GetChannelModelPricing(ctx, group.ID, branch.Selector))
	require.Equal(t, &price, channels.GetChannelModelPricing(ctx, group.ID, branch.Selector).InputPrice)
	require.False(t, svc.checkChannelPricingRestriction(ctx, &group.ID, branch.Selector))
	require.True(t, channels.IsModelRestricted(ctx, group.ID, "unpublished-private"))
	channel.BillingModelSource = BillingModelSourceUpstream
	channels.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{group.ID: group.Platform}))
	require.ErrorIs(t, svc.ValidateManagedModelCompilation(ctx, group, request), ErrManagedModelRouteUnavailable)
}

func TestManagedModelV2QuotaIdentityIsFrozenBeforeBranchSelection(t *testing.T) {
	group, _ := managedV2Fixture(t)
	for _, tc := range []struct{ name, groupPlatform, savedQuota, originalForce, expected string }{
		{"native group", PlatformAnthropic, "", "", PlatformAnthropic},
		{"new composite public family", PlatformComposite, "", "", PlatformAnthropic},
		{"retained composite ledger", PlatformComposite, PlatformOpenAI, "", PlatformOpenAI},
		{"explicit original route", PlatformAnthropic, "", PlatformAntigravity, PlatformAntigravity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *group
			copy.Platform = tc.groupPlatform
			copy.ManagedModelRoutes.Routes = append([]ManagedModelRoute(nil), group.ManagedModelRoutes.Routes...)
			copy.ManagedModelRoutes.Routes[0].QuotaPlatform = tc.savedQuota
			request, err := ResolveManagedModelRoute(&copy, "claude-fable-5.1", CompositeRouteEndpointResponses)
			require.NoError(t, err)
			ctx := context.Background()
			if tc.originalForce != "" {
				ctx = context.WithValue(ctx, ctxkey.ForcePlatform, tc.originalForce)
			}
			ctx = WithManagedModelRequest(ctx, request)
			key := &APIKey{Group: &copy}
			require.Equal(t, tc.expected, QuotaPlatform(ctx, key))
			for _, branch := range request.Route.Branches {
				branchCtx := WithManagedModelBranch(ctx, branch)
				require.Equal(t, tc.expected, QuotaPlatform(branchCtx, key), "retrying through another wire cannot change the quota ledger")
			}
		})
	}
	unmanaged := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformOpenAI)
	require.Equal(t, PlatformOpenAI, QuotaPlatform(unmanaged, &APIKey{Group: group}), "ordinary forced routes keep their original semantics")
}

func TestManagedModelV2SelectionPrefersAvailableBranchAndKeepsNormalGate(t *testing.T) {
	group, accounts := managedV2Fixture(t)
	group.Hydrated, group.ProfitControlEnabled, group.RateMultiplier = true, true, 0.5
	accounts[0].Schedulable = false
	accounts[1].Concurrency, accounts[1].Priority = 1, 1
	rate := 0.1
	accounts[1].RateMultiplier = &rate
	backup := *accounts[1]
	backup.ID, backup.Priority = 43, 2
	route := &group.ManagedModelRoutes.Routes[0]
	member := route.Branches[1].Accounts[0]
	member.AccountID = backup.ID
	route.Branches[1].Accounts = append(route.Branches[1].Accounts, member)
	request, err := ResolveManagedModelRoute(group, "claude-fable-5.1", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	repo := managedV2SchedulerRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*accounts[0], *accounts[1], backup}}}
	available := map[int64]bool{42: false, 43: true}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	openAI := &OpenAIGatewayService{accountRepo: repo, cfg: cfg, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: available})}
	svc := &GatewayService{accountRepo: repo, cfg: cfg}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx, _ = WithGatewayTokenRequestPricing(ctx)
	selected, err := svc.SelectManagedModelCandidate(ctx, openAI, request, ManagedModelSelectionOptions{})
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.True(t, selected.Selection.Acquired)
	require.Equal(t, int64(43), selected.Candidate.Account.ID, "do not queue on a busy branch while another verified branch has a slot")
	require.True(t, selected.Selection.ProfitGateActive(), "Anthropic public-group gate also applies to OpenAI adapter candidates")
	selected.Selection.ReleaseFunc()
	available[42] = true
	firstKey := (ManagedModelCandidate{Branch: route.Branches[1], Account: accounts[1]}).Key()
	selected, err = svc.SelectManagedModelCandidate(ctx, openAI, request, ManagedModelSelectionOptions{PreferredAccountID: 42, Excluded: map[string]struct{}{firstKey: {}}})
	require.NoError(t, err)
	require.Equal(t, int64(42), selected.Candidate.Account.ID)
	require.Equal(t, route.Branches[2].Selector, selected.Candidate.Branch.Selector, "excluding one same-account target retains its independently verified target")
	selected.Selection.ReleaseFunc()
}

type managedV2SchedulerRepo struct { schedulerTestOpenAIAccountRepo }

func (r managedV2SchedulerRepo) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	accounts := make([]*Account, 0, len(ids))
	for _, id := range ids {
		account, err := r.GetByID(ctx, id)
		if err == nil { accounts = append(accounts, account) }
	}
	return accounts, nil
}
