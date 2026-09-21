//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestCindyCleanupFixedScopeResourceAdmission(t *testing.T) {
	for _, tc := range []struct {
		provider, admin int
		allowed         bool
	}{{100, 100, true}, {99, 100, false}, {100, 99, false}, {0, 100, false}, {100, 0, false}} {
		manager, repo, _, request := newViewFixture(t)
		repo.installations[1].Bindings[0].RolloutPercent, repo.installations[1].Bindings[1].RolloutPercent = tc.provider, tc.admin
		applyViewFixture(t, manager, repo)
		request.PresetID = "banned"
		request.Query = extensionv1.AccountViewQueryV1{Search: "no visible candidates", AccountIDs: []int64{999}}
		view, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
		require.NoError(t, err)
		policy := CindyCleanupResourcePolicy()
		policy.Name = "cindy.cleanup.insufficient.submit"
		ctx, release, err := manager.BindResourceContext(view, 1, request.PackageSHA256, policy.ResourceGrant)
		require.NoError(t, err)
		require.Equal(t, tc.allowed, manager.ValidateResourcePolicy(ctx, policy, nil, true) == nil, "full-domain cleanup must not depend on view preset/search/selection")
		release()
		releaseView()
	}
}

func TestCindyCleanupResourceAvailabilityCache(t *testing.T) {
	manager, repo, _, _ := newViewFixture(t)
	first := CindyCleanupResourcePolicy()
	first.Name = "cindy.cleanup.insufficient.preview"
	second := CindyCleanupResourcePolicy()
	second.Name = "cindy.cleanup.banned.preview"
	second.FilterPlatform, second.FilterAccountType = "*", "*"
	for _, descriptors := range [][]extensionv1.ResourceDescriptor{{first, second}, {second, first}} {
		items, err := manager.ResourceDescriptors(context.Background(), 1, "admin", descriptors)
		require.NoError(t, err)
		require.Len(t, items, 2)
		for _, item := range items {
			require.Equal(t, item.Name == first.Name, item.Available, "different fixed domains cannot share a cache result")
		}
	}
	repo.installations[1].Bindings[1].Enabled = false
	applyViewFixture(t, manager, repo)
	require.Empty(t, manager.Contributions(), "disabled Admin also withdraws the account view")
}

func TestAccountViewRetainedCatalogDoesNotAuthorizeNewIO(t *testing.T) {
	manager, repo, _, request := newViewFixture(t)
	read := extensionv1.ResourceDescriptor{ResourceGrant: extensionv1.ResourceGrant{Name: "cindy.probe.get", Capability: extensionv1.CapabilityProvider, Permission: "admin"}, Retained: true}
	repo.installations[1].Manifest.Resources = append(repo.installations[1].Manifest.Resources, read.ResourceGrant)
	repo.installations[1].State = PluginStateDisabled
	for index := range repo.installations[1].Bindings {
		repo.installations[1].Bindings[index].Enabled = false
	}
	applyViewFixture(t, manager, repo)
	ctx, err := manager.BindRetainedAccountView(context.Background(), request)
	require.NoError(t, err)
	_, business := AccountViewFromContext(ctx)
	require.False(t, business)
	create := CindyCleanupResourcePolicy()
	create.Name = "cindy.cleanup.insufficient.submit"
	items, err := manager.ResourceDescriptors(ctx, 1, "admin", []extensionv1.ResourceDescriptor{read, create})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.True(t, items[0].Available)
	require.False(t, items[1].Available)
	_, _, err = manager.BindAccountViewRequest(ctx, request)
	require.Error(t, err)
	raw, err := json.Marshal(items[0])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"retained":true`)
}

func TestAccountViewResourceActionRequiresAdminAndRowPredicate(t *testing.T) {
	manager, repo, _, _ := newViewFixture(t)
	yes := true
	action := extensionv1.Contribution{ID: "recover", Slot: "account.actions", Permission: "admin", Capability: extensionv1.CapabilityProvider, Label: map[string]string{"en": "Recover"}, ResourceAction: &extensionv1.AccountResourceActionV1{Version: 1, Resource: "cindy.balance.recover", AccountParameter: "id", AccountSource: "row.id", Effect: "refresh_current_account_view", RowPredicate: &extensionv1.AccountViewPredicate{CindyOnly: &yes, CindyBalanceStatus: "insufficient"}}}
	repo.installations[1].Manifest.Contributions = append(repo.installations[1].Manifest.Contributions, action)
	applyViewFixture(t, manager, repo)
	require.Equal(t, []string{extensionv1.CapabilityProvider, extensionv1.CapabilityAdmin}, contributionRequiredCapabilities(&action))
	descriptor := extensionv1.ResourceDescriptor{ResourceGrant: extensionv1.ResourceGrant{Name: "cindy.balance.recover", Capability: extensionv1.CapabilityProvider, Permission: "admin"}, RequiredCapabilities: []string{extensionv1.CapabilityAdmin}}
	ctx, release, err := manager.BindResourceContext(context.Background(), 1, repo.installations[1].PackageSHA256, descriptor.ResourceGrant)
	require.NoError(t, err)
	defer release()
	require.NoError(t, manager.ValidateResourcePolicy(ctx, descriptor, []int64{3}, false))
	require.Error(t, manager.ValidateResourcePolicy(ctx, descriptor, []int64{2}, false), "row predicate is rechecked, not just hidden in the UI")
}
