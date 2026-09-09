package service

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func providerIdentityManagedFixture() (*Group, *Account) {
	const groupID int64 = 23
	const model = "claude-fable-5.1"
	const upstream = "provider/fable-5.1-CC"
	endpoints := []string{CompositeRouteEndpointMessages}
	selector := ManagedModelBranchSelector(groupID, model, PlatformOpenAI, CompositeRouteEndpointChatCompletions, upstream)
	account := &Account{
		ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, GroupIDs: []int64{groupID},
		Credentials: map[string]any{
			"api_key": "test-key", "base_url": "https://compat.example",
			"model_mapping": map[string]any{selector: upstream, "private": "private-target"},
		},
	}
	group := &Group{ID: groupID, Platform: PlatformAnthropic, ManagedModelRoutes: ManagedModelRoutesConfig{
		Version: ManagedModelRoutesVersion, Enabled: true,
		Routes: []ManagedModelRoute{{PublicModel: model, Endpoints: endpoints, Branches: []ManagedModelRouteBranch{{
			Selector: selector, TargetPlatform: PlatformOpenAI, UpstreamProtocol: CompositeRouteEndpointChatCompletions, Endpoints: endpoints,
			Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: upstream,
				AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints}},
		}}}},
	}}
	return group, account
}

func TestValidateProviderIdentityGroupBindingsRetainsOnlyVerifiedManagedMembers(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*Group, *Account)
		allow  bool
	}{
		{name: "existing verified member", allow: true},
		{name: "ordinary fields and temporary cooldown do not revoke membership", allow: true, mutate: func(_ *Group, a *Account) {
			notes, until := "updated note", time.Now().Add(time.Hour)
			a.Notes, a.Concurrency, a.Priority = &notes, 3, 7
			a.Schedulable, a.TempUnschedulableUntil = false, &until
		}},
		{name: "ordinary group", mutate: func(g *Group, _ *Account) { g.ManagedModelRoutes.Enabled = false }},
		{name: "legacy version cannot authorize new cross platform binding", mutate: func(g *Group, _ *Account) { g.ManagedModelRoutes.Version = 1 }},
		{name: "new binding even with old route entry", mutate: func(_ *Group, a *Account) { a.GroupIDs = []int64{24} }},
		{name: "different account with identical credentials", mutate: func(_ *Group, a *Account) { a.ID++ }},
		{name: "changed exact target", allow: true, mutate: func(g *Group, a *Account) {
			a.Credentials["model_mapping"].(map[string]any)[g.ManagedModelRoutes.Routes[0].Branches[0].Selector] = "other-target"
		}},
		{name: "changed credential identity", allow: true, mutate: func(_ *Group, a *Account) { a.Credentials["api_key"] = "new-key" }},
		{name: "malformed publication", mutate: func(g *Group, _ *Account) { g.ManagedModelRoutes.Routes[0].Branches[0].Selector += "forged" }},
		{name: "account provider profile remains isolated", mutate: func(g *Group, a *Account) {
			a.ProviderProfile = ProviderProfileCindyLaxaV1
			g.ManagedModelRoutes.Routes[0].Branches[0].Accounts[0].AccountFingerprint = ManagedModelAccountFingerprint(a)
		}},
		{name: "group provider profile remains isolated", mutate: func(g *Group, _ *Account) { g.ProviderProfile = ProviderProfileCindyLaxaV1 }},
		{name: "account wire identity remains isolated", mutate: func(g *Group, a *Account) {
			a.WirePlatform = PlatformAnthropic
			g.ManagedModelRoutes.Routes[0].Branches[0].Accounts[0].AccountFingerprint = ManagedModelAccountFingerprint(a)
		}},
		{name: "legacy Cindy remains isolated", mutate: func(g *Group, a *Account) {
			a.Credentials["base_url"] = "https://api.laxarouter.ai"
			g.ManagedModelRoutes.Routes[0].Branches[0].Accounts[0].AccountFingerprint = ManagedModelAccountFingerprint(a)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			group, account := providerIdentityManagedFixture()
			if tt.mutate != nil {
				tt.mutate(group, account)
			}
			before := maps.Clone(account.Credentials["model_mapping"].(map[string]any))
			repo := providerIdentityGroupRepoStub{groups: map[int64]*Group{group.ID: group}}
			err := validateProviderIdentityGroupBindings(context.Background(), repo, account, []int64{group.ID})
			if tt.allow {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, before, account.Credentials["model_mapping"], "validation must not rewrite public or private mappings")
		})
	}
}

type providerIdentityManagedAccountRepo struct {
	AccountRepository
	account *Account
	proxies map[int64]*Proxy
	updates int
	binds   int
}

func (r *providerIdentityManagedAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	copy := *r.account
	copy.Credentials, copy.Extra = maps.Clone(r.account.Credentials), maps.Clone(r.account.Extra)
	if copy.ProxyID != nil && copy.Proxy == nil {
		copy.Proxy = r.proxies[*copy.ProxyID]
	}
	return &copy, nil
}

func (r *providerIdentityManagedAccountRepo) Update(_ context.Context, account *Account) error {
	r.account, r.updates = account, r.updates+1
	return nil
}

func (r *providerIdentityManagedAccountRepo) BindGroups(_ context.Context, _ int64, groupIDs []int64) error {
	r.account.GroupIDs, r.binds = append([]int64(nil), groupIDs...), r.binds+1
	return nil
}

func (r *providerIdentityManagedAccountRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}

func TestAdminUpdateAccountManagedMemberConfigurationChangesRemainEditable(t *testing.T) {
	for _, tt := range []struct {
		name         string
		credential   string
		value        string
		proxyID      int64
		protocol     bool
		newBinding   bool
		reject       bool
		routingReady bool
	}{
		{name: "unchanged hydrated proxy permits ordinary save", proxyID: 7, routingReady: true},
		{name: "new key keeps membership but invalidates evidence", credential: "api_key", value: "replacement-key", proxyID: 7},
		{name: "new base URL keeps membership but invalidates evidence", credential: "base_url", value: "https://replacement.example", proxyID: 7},
		{name: "new protocol keeps membership but invalidates evidence", protocol: true, proxyID: 7},
		{name: "new Cindy endpoint cannot borrow ordinary membership", credential: "base_url", value: "https://api.laxarouter.ai", proxyID: 7, reject: true},
		{name: "different proxy cannot reuse old evidence", proxyID: 8},
		{name: "removing proxy cannot reuse old evidence", proxyID: 0},
		{name: "new binding cannot borrow existing route authorization", newBinding: true, proxyID: 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			group, account := providerIdentityManagedFixture()
			proxyID := int64(7)
			account.ProxyID, account.Proxy = &proxyID, &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example", Port: 8080}
			fingerprint := ManagedModelAccountFingerprint(account)
			group.ManagedModelRoutes.Routes[0].Branches[0].Accounts[0].AccountFingerprint = fingerprint
			request, err := ResolveManagedModelRoute(group, group.ManagedModelRoutes.Routes[0].PublicModel, CompositeRouteEndpointMessages)
			require.NoError(t, err)
			branch := request.Route.Branches[0]
			ctx := WithManagedModelBranch(WithManagedModelRequest(context.Background(), request), branch)
			require.True(t, ManagedModelAccountAllowed(ctx, account, branch.Selector))
			if tt.newBinding {
				account.GroupIDs = []int64{24}
			}
			repo := &providerIdentityManagedAccountRepo{account: account, proxies: map[int64]*Proxy{
				8: {ID: 8, Protocol: "http", Host: "replacement-proxy.example", Port: 8080},
			}}
			svc := &adminServiceImpl{accountRepo: repo, groupRepo: providerIdentityGroupRepoStub{groups: map[int64]*Group{group.ID: group}}}
			groupIDs, notes, concurrency := []int64{group.ID}, "ordinary edit", 4
			input := &UpdateAccountInput{
				Notes: &notes, Concurrency: &concurrency, ProxyID: &tt.proxyID, GroupIDs: &groupIDs, SkipMixedChannelCheck: true,
			}
			if tt.credential != "" {
				input.Credentials = maps.Clone(account.Credentials)
				input.Credentials[tt.credential] = tt.value
			}
			if tt.protocol {
				input.Extra = map[string]any{"openai_responses_mode": "force_responses", "openai_responses_supported": true}
			}
			updated, err := svc.UpdateAccount(context.Background(), account.ID, input)
			if tt.newBinding || tt.reject {
				require.Error(t, err)
				require.Zero(t, repo.updates, "preserving membership must not grant a new binding or cross provider isolation")
				require.Zero(t, repo.binds)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, repo.updates)
			require.Equal(t, 1, repo.binds)
			if tt.routingReady {
				require.Same(t, account.Proxy, updated.Proxy, "retain the existing hydrated proxy, not a fabricated identity")
				require.Equal(t, fingerprint, ManagedModelAccountFingerprint(updated))
			} else {
				require.NotEqual(t, fingerprint, ManagedModelAccountFingerprint(updated))
			}
			require.Equal(t, tt.routingReady, ManagedModelAccountAllowed(ctx, updated, branch.Selector), "saved configuration must not activate old evidence")
			require.Equal(t, notes, *updated.Notes)
			require.Equal(t, concurrency, updated.Concurrency)
			require.Equal(t, groupIDs, updated.GroupIDs)
			require.Equal(t, "private-target", updated.GetMappedModel("private"))
			notes = "second ordinary edit after configuration change"
			updated, err = svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{
				Notes: &notes, GroupIDs: &groupIDs, SkipMixedChannelCheck: true,
			})
			require.NoError(t, err, "stale evidence must not prevent later administrative edits")
			require.Equal(t, 2, repo.updates)
			require.Equal(t, 2, repo.binds)
			require.Equal(t, notes, *updated.Notes)
			require.Equal(t, groupIDs, updated.GroupIDs)
			require.Equal(t, fingerprint, group.ManagedModelRoutes.Routes[0].Branches[0].Accounts[0].AccountFingerprint)
			require.Equal(t, tt.routingReady, ManagedModelAccountAllowed(ctx, updated, branch.Selector))
		})
	}
}
