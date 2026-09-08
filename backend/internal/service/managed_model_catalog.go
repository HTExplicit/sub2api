package service

import (
	"context"
	"strings"
	"time"
)

// managedCatalogAccountMapping is a read-only, request-local projection. The
// account's private mappings are never edited merely to make public IDs visible.
func managedCatalogAccountMapping(group *Group, account *Account, endpoint string) map[string]string {
	return managedCatalogAccountMappingWithScheduling(group, account, endpoint, true)
}

func managedCatalogAccountMappingWithScheduling(group *Group, account *Account, endpoint string, requireSchedulable bool) map[string]string {
	out := make(map[string]string)
	if group == nil || !group.ManagedModelRoutes.Enabled || group.ManagedModelRoutes.Version != ManagedModelRoutesVersion ||
		account == nil || !account.IsActive() || !account.Schedulable || !managedModelAccountInGroup(account, group.ID) {
		return out
	}
	if requireSchedulable && !account.IsSchedulable() {
		return out
	}
	if account.ProxyID != nil && (account.Proxy == nil || !account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now())) {
		return out
	}
	for _, route := range group.ManagedModelRoutes.Routes {
		for _, candidateEndpoint := range route.Endpoints {
			if endpoint != "" && candidateEndpoint != endpoint {
				continue
			}
			request, err := ResolveManagedModelRoute(group, route.PublicModel, candidateEndpoint)
			if err != nil || request == nil || !ManagedModelAccountAllowed(WithManagedModelRequest(context.Background(), request), account, route.Selector) {
				continue
			}
			upstream := account.GetModelMapping()[route.Selector]
			out[route.PublicModel] = upstream
			out[route.Selector] = upstream
			break
		}
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
		for model := range managedCatalogAccountMapping(group, &accounts[i], endpoint) {
			if !strings.HasPrefix(model, "s2pub-") {
				available[model] = true
			}
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
		mapping := managedCatalogAccountMappingWithScheduling(group, &accounts[i], endpoint, false)
		if len(mapping) == 0 {
			continue
		}
		account := accounts[i]
		account.Credentials = make(map[string]any, len(accounts[i].Credentials))
		for key, value := range accounts[i].Credentials {
			account.Credentials[key] = value
		}
		rawMapping := make(map[string]any, len(mapping))
		for model, upstream := range mapping {
			rawMapping[model] = upstream
		}
		account.Credentials["model_mapping"] = rawMapping
		projected = append(projected, account)
	}
	return projected
}

// ManagedPublicModelIDs is shared by the OpenAI-style and Codex catalog
// handlers. An empty valid result stays empty; it never becomes a static list.
func (s *GatewayService) ManagedPublicModelIDs(ctx context.Context, group *Group, endpoint string) ([]string, error) {
	if s == nil || s.accountRepo == nil || group == nil || !group.ManagedModelRoutes.Enabled {
		return nil, ErrManagedModelRouteUnavailable
	}
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, group.ID)
	if err != nil {
		return nil, ErrManagedModelRouteUnavailable
	}
	return managedPublicModelIDsForAccounts(group, accounts, endpoint), nil
}
