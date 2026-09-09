package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedModelProtocolMatrixUsesExistingAdapters(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformDeepseek, PlatformZhipu, PlatformMiniMax} {
		require.True(t, ManagedModelUsesOpenAIAdapter(platform), platform)
		require.True(t, ManagedModelBranchProtocolSupported(platform, CompositeRouteEndpointMessages), platform)
		require.True(t, ManagedModelBranchProtocolSupported(platform, CompositeRouteEndpointChatCompletions), platform)
		require.Equal(t, (&Account{Platform: platform}).SupportsNativeCNResponses(), ManagedModelBranchProtocolSupported(platform, CompositeRouteEndpointResponses), platform)
		require.False(t, ManagedModelBranchProtocolSupported(platform, CompositeRouteEndpointCountTokens), "basic liveness must not imply metadata support")
	}
	for _, platform := range []string{PlatformGrok, PlatformGemini, PlatformCindy, PlatformAntigravity} {
		require.False(t, ManagedModelBranchProtocolSupported(platform, CompositeRouteEndpointResponses), "a different upstream transport is not proof of this wire")
	}
}

func TestManagedModelCNProtocolOverlayUsesProvenEndpointAndPreservesIdentity(t *testing.T) {
	for _, wire := range []string{CompositeRouteEndpointMessages, CompositeRouteEndpointResponses, CompositeRouteEndpointChatCompletions} {
		t.Run(wire, func(t *testing.T) {
			protocol := wire
			if protocol == CompositeRouteEndpointMessages {
				protocol = APIProtocolAnthropic
			}
			account := &Account{ID: 41, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{23},
				Credentials: map[string]any{"api_key": "offline-key", "api_protocol": APIProtocolAdaptive, "base_url": "https://chat.example.test/v1", "api_base_urls": map[string]any{
					APIProtocolAnthropic: "https://messages.example.test", APIProtocolResponses: "https://responses.example.test", APIProtocolChatCompletions: "https://chat.example.test/v1",
				}}}
			selector := ManagedModelBranchSelector(23, "deepseek-v4", account.Platform, wire, "provider/deepseek-v4")
			account.Credentials["model_mapping"] = map[string]any{selector: "provider/deepseek-v4", "private": "kept"}
			endpoints := []string{CompositeRouteEndpointResponses, CompositeRouteEndpointMessages, CompositeRouteEndpointChatCompletions}
			branch := ManagedModelRouteBranch{Selector: selector, TargetPlatform: account.Platform, UpstreamProtocol: wire, Endpoints: endpoints,
				Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: "provider/deepseek-v4", AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints}}}
			request := &ManagedModelRequest{Version: 2, GroupID: 23, Endpoint: CompositeRouteEndpointResponses, Route: ManagedModelRoute{PublicModel: "deepseek-v4", Branches: []ManagedModelRouteBranch{branch}}}
			before, err := json.Marshal(account)
			require.NoError(t, err)
			ctx, clone, err := WithManagedModelAccountProtocol(WithManagedModelRequest(context.Background(), request), account, branch)
			require.NoError(t, err)
			require.Equal(t, protocol, clone.GetAPIProtocol())
			require.Equal(t, account.GetCNProtocolBaseURL(protocol), clone.GetCredential("base_url"))
			require.True(t, ManagedModelAccountAllowed(ctx, clone, selector))
			require.NoError(t, validateManagedForwardAccount(ctx, &managedLatestAccountRepo{account: account}, clone, selector))
			require.Empty(t, ManagedModelConfiguredProtocol(account), "adaptive is not one fixed wire")
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			clone.Credentials["api_key"] = "changed"
			require.False(t, ManagedModelAccountAllowed(ctx, clone, selector), "protocol overlay cannot waive a credential change")
		})
	}
}

func TestManagedModelCandidateDeduplicatesLegacyWireWithoutLosingPinnedAlias(t *testing.T) {
	group, accounts := managedV2Fixture(t)
	group.Platform = PlatformComposite
	route := &group.ManagedModelRoutes.Routes[0]
	route.QuotaPlatform = PlatformAnthropic
	legacy := route.Branches[1]
	legacy.Selector, legacy.UpstreamProtocol = ManagedModelSelector(group.ID, route.PublicModel), ""
	accounts[1].Credentials["model_mapping"].(map[string]any)[legacy.Selector] = legacy.Accounts[0].UpstreamModel
	route.Branches = append(route.Branches, legacy)
	request, err := ResolveManagedModelRoute(group, route.PublicModel, CompositeRouteEndpointResponses)
	require.NoError(t, err)
	repo := &managedV2AccountRepo{accounts: map[int64]*Account{41: accounts[0], 42: accounts[1]}, reads: make(map[int64]int)}
	svc := &GatewayService{accountRepo: repo}
	candidates, err := svc.ListManagedModelCandidates(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, candidates, 3, "same account/real target/wire must not get another retry under the retained v1 alias")
	for _, candidate := range candidates {
		require.NotEqual(t, legacy.Selector, candidate.Branch.Selector)
		key := candidate.Key()
		candidate.Account = &Account{ID: candidate.Account.ID}
		require.Equal(t, key, candidate.Key(), "a lightweight scheduler refresh must not change the exclusion identity")
	}
	pinned, err := svc.listManagedModelCandidates(context.Background(), request, legacy.Selector)
	require.NoError(t, err)
	require.Len(t, pinned, 3)
	found := false
	for _, candidate := range pinned {
		if candidate.Branch.Selector == legacy.Selector {
			found = true
			explicit := ManagedModelCandidate{Account: candidate.Account, Branch: route.Branches[1], IngressEndpoint: request.Endpoint}
			require.Equal(t, explicit.Key(), candidate.Key(), "exclusion still names the physical target, including for a pinned alias")
		}
	}
	require.True(t, found, "deduplication may not strand a legacy continuation pin")
}
