package service

import (
	"context"
	"time"
)

// A public model may have more than one real target on the same account. Keep
// those paths separate until the catalog capability intersection is complete.
type managedCatalogAccountTarget struct {
	publicModel   string
	upstreamModel string
	branch        ManagedModelRouteBranch
	request       *ManagedModelRequest
}

func managedCatalogPlatforms(group *Group) []string {
	platforms := make([]string, 0)
	seen := make(map[string]bool)
	if group == nil || !group.ManagedModelRoutes.Enabled || !ManagedModelRoutesVersionSupported(group.ManagedModelRoutes.Version) {
		return platforms
	}
	for _, route := range group.ManagedModelRoutes.Routes {
		for _, branch := range ManagedModelRouteBranches(route) {
			if isConcreteRequestPlatform(branch.TargetPlatform) && !seen[branch.TargetPlatform] {
				platforms = append(platforms, branch.TargetPlatform)
				seen[branch.TargetPlatform] = true
			}
		}
	}
	return platforms
}

func managedCatalogAccountTargets(group *Group, account *Account, endpoint string, requireSchedulable bool) []managedCatalogAccountTarget {
	if group == nil || !group.ManagedModelRoutes.Enabled || !ManagedModelRoutesVersionSupported(group.ManagedModelRoutes.Version) ||
		account == nil || !account.IsActive() || !account.Schedulable || !managedModelAccountInGroup(account, group.ID) {
		return nil
	}
	if requireSchedulable && !account.IsSchedulable() {
		return nil
	}
	if account.ProxyID != nil && (account.Proxy == nil || !account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now())) {
		return nil
	}
	if group.RequirePrivacySet && !account.IsPrivacySet() {
		return nil
	}
	targets := make([]managedCatalogAccountTarget, 0)
	for _, route := range group.ManagedModelRoutes.Routes {
		for _, branch := range ManagedModelRouteBranches(route) {
			if branch.UpstreamProtocol != "" && account.Type != AccountTypeAPIKey {
				continue
			}
			for _, candidateEndpoint := range branch.Endpoints {
				if endpoint != "" && candidateEndpoint != endpoint {
					continue
				}
				request, err := ResolveManagedModelRoute(group, route.PublicModel, candidateEndpoint)
				if err != nil || request == nil {
					continue
				}
				ctx := WithManagedModelBranch(WithManagedModelRequest(context.Background(), request), branch)
				if !ManagedModelAccountAllowed(ctx, account, branch.Selector) {
					continue
				}
				targets = append(targets, managedCatalogAccountTarget{publicModel: route.PublicModel,
					upstreamModel: account.GetModelMapping()[branch.Selector], branch: branch, request: request})
				break
			}
		}
	}
	return targets
}

// managedCatalogAccountMapping is a read-only, request-local projection. The
// account's private mappings are never edited merely to make public IDs visible.
func managedCatalogAccountMapping(group *Group, account *Account, endpoint string) map[string]string {
	return managedCatalogAccountMappingWithScheduling(group, account, endpoint, true)
}

func managedCatalogAccountMappingWithScheduling(group *Group, account *Account, endpoint string, requireSchedulable bool) map[string]string {
	out := make(map[string]string)
	for _, target := range managedCatalogAccountTargets(group, account, endpoint, requireSchedulable) {
		// This compatibility view is useful for membership, not for intersecting
		// v2 capabilities. The latter must retain every exact branch target.
		if _, exists := out[target.publicModel]; !exists {
			out[target.publicModel] = target.upstreamModel
		}
		out[target.branch.Selector] = target.upstreamModel
	}
	return out
}

func managedPublicModelIDsForAccounts(group *Group, accounts []Account, endpoint string) []string {
	models := make([]string, 0)
	if group == nil || !group.IsActive() || !group.ManagedModelRoutes.Enabled {
		return models
	}
	available := make(map[string]bool)
	for i := range accounts {
		// V1 keeps its established live-only listing policy. V2 exposes the
		// published, valid pool independently of momentary cooldowns.
		for _, target := range managedCatalogAccountTargets(group, &accounts[i], endpoint, group.ManagedModelRoutes.Version == 1) {
			available[target.publicModel] = true
		}
	}
	for _, route := range group.ManagedModelRoutes.Routes {
		if available[route.PublicModel] {
			models = append(models, route.PublicModel)
			delete(available, route.PublicModel)
		}
	}
	return models
}

func managedModelCatalogAccounts(group *Group, accounts []Account, endpoint string) []Account {
	if group == nil || !group.ManagedModelRoutes.Enabled {
		return accounts
	}
	projected := make([]Account, 0, len(accounts))
	for i := range accounts {
		// Capability/context ceilings include temporary cooling members because
		// they may rejoin the same published pool during the client's session.
		for _, target := range managedCatalogAccountTargets(group, &accounts[i], endpoint, false) {
			account := accounts[i]
			account.Credentials = make(map[string]any, len(accounts[i].Credentials))
			for key, value := range accounts[i].Credentials {
				account.Credentials[key] = value
			}
			account.Credentials["model_mapping"] = map[string]any{target.publicModel: target.upstreamModel, target.branch.Selector: target.upstreamModel}
			account.modelMappingCacheReady = false
			projected = append(projected, account)
		}
	}
	return projected
}

// ManagedPublicModelIDs is shared by the OpenAI-style and Codex catalog
// handlers. An empty valid result stays empty; it never becomes a static list.
func (s *GatewayService) ManagedPublicModelIDs(ctx context.Context, group *Group, endpoint string) ([]string, error) {
	if s == nil || s.accountRepo == nil || group == nil || !group.ManagedModelRoutes.Enabled {
		return nil, ErrManagedModelRouteUnavailable
	}
	var accounts []Account
	var err error
	if group.ManagedModelRoutes.Version == ManagedModelRoutesVersion {
		accounts, err = s.accountRepo.ListModelAvailabilityCandidates(ctx, &group.ID, managedCatalogPlatforms(group), false)
	} else {
		accounts, err = s.accountRepo.ListSchedulableByGroupID(ctx, group.ID)
	}
	if err != nil {
		return nil, ErrManagedModelRouteUnavailable
	}
	return managedPublicModelIDsForAccounts(group, accounts, endpoint), nil
}
