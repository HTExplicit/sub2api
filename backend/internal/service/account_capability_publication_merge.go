package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// mergePublicationGroup starts from the locked configuration, not the subset
// visible in a browser page. Only explicit removals can subtract existing state.
func (s *AccountCapabilityPublicationService) mergePublicationGroup(snap *CapabilityPublicationSnapshot, input CapabilityPublicationGroup, plan *CapabilityPublicationPlan, patch func(int64) *CapabilityPublicationAccountPatch, evidenceByAccount map[int64][]int64) error {
	gs := snap.Groups[input.ID]
	if gs == nil || gs.Group == nil {
		return ErrCapabilityPublicationConflict
	}
	group := gs.Group
	if group.Name != input.Name || group.RateMultiplier != input.RateMultiplier || group.Platform != input.Platform || group.IsExclusive || group.Platform == PlatformCindy || group.StrictCindy || group.ProviderProfile == "cindy" || (gs.Channel != nil && gs.Channel.ID == 1) {
		return ErrCapabilityPublicationConflict
	}
	if group.ManagedModelRoutes.Enabled && group.ManagedModelRoutes.Version != 1 && group.ManagedModelRoutes.Version != 2 {
		return publicationInvalid("the existing managed route version is not supported")
	}
	if !group.ManagedModelRoutes.Enabled && len(input.Models) == 0 {
		if len(input.RemoveModels) > 0 || len(input.RemoveLines) > 0 {
			return publicationInvalid("the explicitly removed managed model is not currently published")
		}
		// An empty legacy draft must not enable an empty managed allowlist on
		// an ordinary group. Only explicit binding removals may still apply.
		for _, id := range snap.Request.DetachAccountIDs {
			if _, bound := gs.Bindings[id]; bound {
				patch(id).RemoveGroupIDs = append(patch(id).RemoveGroupIDs, group.ID)
			}
		}
		return nil
	}
	if !group.ManagedModelRoutes.Enabled && len(group.ModelAllowlist.Models) > 0 {
		// Converting an unmanaged allowlist without its original routes would
		// silently withdraw those names. Such migrations need explicit evidence.
		for _, name := range group.ModelAllowlist.Models {
			found := false
			for _, model := range input.Models {
				found = found || strings.EqualFold(name, model.PublicModel) || publicationHasString(model.Aliases, name)
			}
			if !found && !publicationHasString(input.RemoveModels, name) {
				return publicationInvalid("existing unmanaged models must be retained with explicit route evidence before enabling managed routing")
			}
		}
	}
	gp := CapabilityPublicationGroupPatch{
		GroupID: input.ID, Create: gs.IsNew, Name: group.Name, Platform: group.Platform, RateMultiplier: group.RateMultiplier, Status: group.Status,
		ManagedModelRoutes: domain.ManagedModelRoutesConfig{Version: 2, Enabled: true, Routes: []domain.ManagedModelRoute{}},
		ModelAllowlist:     GroupModelAllowlist{Enabled: true, Models: append([]string{}, group.ModelAllowlist.Models...)},
		MessagesDispatch:   publicationClone(group.MessagesDispatchModelConfig), AllowMessagesDispatch: group.AllowMessagesDispatch, DefaultMappedModel: group.DefaultMappedModel,
		ChannelMapping: map[string]map[string]string{}, CompositeRoutes: append([]CompositeModelRoute{}, gs.Routes...),
	}
	if gp.Status == "" {
		gp.Status = StatusActive
	}
	if gp.MessagesDispatch.ExactModelMappings == nil {
		gp.MessagesDispatch.ExactModelMappings = map[string]string{}
	}
	if gs.Channel != nil {
		gp.ChannelMapping = publicationClone(gs.Channel.ModelMapping)
		gp.ChannelPricing = publicationClone(gs.Channel.ModelPricing)
		gp.ChannelFeatures, gp.ChannelFeaturesConfig = gs.Channel.Features, publicationClone(gs.Channel.FeaturesConfig)
		gp.ChannelApplyPricingToAccountStats, gp.ChannelAccountStatsPricingRules = gs.Channel.ApplyPricingToAccountStats, publicationClone(gs.Channel.AccountStatsPricingRules)
	}
	if gp.ChannelMapping == nil {
		gp.ChannelMapping = map[string]map[string]string{}
	}
	for _, old := range group.ManagedModelRoutes.Routes {
		if !publicationValidOwnedRoute(group.ID, old) {
			return ErrCapabilityPublicationConflict
		}
		route := publicationClone(old)
		route.QuotaPlatform = managedModelQuotaPlatform(group, old)
		route.Branches = publicationClone(ManagedModelRouteBranches(old))
		route.Selector, route.TargetPlatform, route.Accounts = "", "", nil
		gp.ManagedModelRoutes.Routes = append(gp.ManagedModelRoutes.Routes, route)
		if !publicationHasString(gp.ModelAllowlist.Models, route.PublicModel) {
			gp.ModelAllowlist.Models = append(gp.ModelAllowlist.Models, route.PublicModel)
		}
	}

	groupEvidence := []int64{}
	for _, model := range input.Models {
		if !s.hasPrice(gs, model.PublicModel) {
			return publicationInvalid("public model has no identified price: " + model.PublicModel)
		}
		index := publicationRouteIndex(gp.ManagedModelRoutes.Routes, model.PublicModel)
		if index < 0 {
			quotaPlatform := managedModelQuotaPlatform(group, domain.ManagedModelRoute{PublicModel: model.PublicModel})
			if quotaPlatform == "" {
				return publicationInvalid("the public model has no identified quota platform")
			}
			gp.ManagedModelRoutes.Routes = append(gp.ManagedModelRoutes.Routes, domain.ManagedModelRoute{PublicModel: model.PublicModel, QuotaPlatform: quotaPlatform, Aliases: []string{}, Endpoints: []string{}, Branches: []domain.ManagedModelRouteBranch{}})
			index = len(gp.ManagedModelRoutes.Routes) - 1
		}
		route := &gp.ManagedModelRoutes.Routes[index]
		route.Aliases = publicationMergeAliases(route.PublicModel, route.Aliases, model.Aliases)
		metadata := map[string][]string{}
		inferenceEvidence := 0
		for _, eid := range model.EvidenceIDs {
			e, result, err := publicationValidateEvidence(snap, eid)
			if err != nil {
				return err
			}
			isMetadata := e.Protocol == "responses_input_tokens" || e.Protocol == "messages_count_tokens"
			if e.Status != "succeeded" || result.AccountFailure || (!isMetadata && (result.Status != "alive" || e.Profile != AccountCapabilityProfileText)) || (isMetadata && (result.Status != "available" || result.Classification != "metadata_available")) {
				return publicationInvalid("only successful basic inference evidence and independent token-count metadata may be published")
			}
			account := snap.Accounts[e.AccountID].Account
			if publicationHasID(snap.Request.DetachAccountIDs, e.AccountID) {
				return publicationInvalid("the same account cannot be added and explicitly detached")
			}
			if !CapabilityCandidateMatches(account, e.UpstreamModel, model.PublicModel, model.Aliases, model.Tier) {
				return publicationInvalid("public model or alias does not match the verified upstream model")
			}
			if err := publicationPreservePrivateMappings(account); err != nil {
				return err
			}
			if !isMetadata && !ManagedModelBranchProtocolSupported(account.Platform, e.Protocol) {
				return publicationInvalid("the verified protocol cannot create a new managed HTTP branch")
			}
			endpoints := CapabilityIngressEndpoints(account, e.Protocol)
			if len(endpoints) == 0 {
				return publicationInvalid("the verified wire has no compatible forwarding adapter")
			}
			key := fmt.Sprintf("%d|%s", account.ID, e.UpstreamModel)
			if isMetadata {
				metadata[key] = publicationUniqueStrings(append(metadata[key], endpoints...))
				groupEvidence = append(groupEvidence, eid)
				continue
			}
			inferenceEvidence++
			branchIndex, memberIndex := publicationExistingLine(*route, account, e)
			if branchIndex < 0 {
				selector := ManagedModelBranchSelector(group.ID, route.PublicModel, account.Platform, e.Protocol, e.UpstreamModel)
				for bi := range route.Branches {
					if route.Branches[bi].Selector == selector {
						branchIndex = bi
						break
					}
				}
				if branchIndex < 0 {
					route.Branches = append(route.Branches, domain.ManagedModelRouteBranch{Selector: selector, TargetPlatform: account.Platform, UpstreamProtocol: e.Protocol, Endpoints: []string{}, Accounts: []domain.ManagedModelRouteAccount{}})
					branchIndex = len(route.Branches) - 1
				}
			}
			branch := &route.Branches[branchIndex]
			if memberIndex < 0 {
				for mi := range branch.Accounts {
					if branch.Accounts[mi].AccountID == account.ID && branch.Accounts[mi].UpstreamModel == e.UpstreamModel {
						memberIndex = mi
						break
					}
				}
			}
			if memberIndex < 0 {
				branch.Accounts = append(branch.Accounts, domain.ManagedModelRouteAccount{AccountID: account.ID, UpstreamModel: e.UpstreamModel, AccountFingerprint: e.ConfigFingerprint, Endpoints: []string{}})
				memberIndex = len(branch.Accounts) - 1
			}
			member := &branch.Accounts[memberIndex]
			member.AccountFingerprint = e.ConfigFingerprint
			member.Endpoints = publicationUniqueStrings(append(member.Endpoints, endpoints...))
			if account.GetModelMapping()[branch.Selector] != e.UpstreamModel {
				patch(account.ID).ModelMapping[branch.Selector] = e.UpstreamModel
			}
			if _, bound := gs.Bindings[account.ID]; !bound {
				patch(account.ID).AddGroupIDs = append(patch(account.ID).AddGroupIDs, group.ID)
			}
			evidenceByAccount[account.ID] = append(evidenceByAccount[account.ID], eid)
			groupEvidence = append(groupEvidence, eid)
		}
		if inferenceEvidence == 0 {
			return publicationInvalid("token-count metadata cannot establish a live inference route")
		}
		for bi := range route.Branches {
			branch := &route.Branches[bi]
			// The v2 HTTP executor admits generation wires only. Token-count
			// evidence is independent metadata, not an extra generation adapter:
			// attaching it to a new branch would invalidate the complete model.
			// Retained v1 branches keep their already supported metadata path.
			if branch.UpstreamProtocol != "" {
				continue
			}
			for mi := range branch.Accounts {
				member := &branch.Accounts[mi]
				key := fmt.Sprintf("%d|%s", member.AccountID, member.UpstreamModel)
				member.Endpoints = publicationUniqueStrings(append(member.Endpoints, metadata[key]...))
				delete(metadata, key)
			}
		}
		if len(metadata) > 0 {
			plan.Warnings = append(plan.Warnings, input.Name+" / "+model.PublicModel+": token-count evidence is retained independently; it does not enable a new managed HTTP count endpoint")
		}
		if !publicationHasString(gp.ModelAllowlist.Models, model.PublicModel) {
			gp.ModelAllowlist.Models = append(gp.ModelAllowlist.Models, model.PublicModel)
		}
	}

	removedAccounts := map[int64]bool{}
	for _, name := range input.RemoveModels {
		index := publicationRouteIndex(gp.ManagedModelRoutes.Routes, name)
		if index < 0 {
			return publicationInvalid("the explicitly removed model is not currently published")
		}
		old := gp.ManagedModelRoutes.Routes[index]
		for _, branch := range old.Branches {
			for _, member := range branch.Accounts {
				if !publicationRemovalInScope(snap, member.AccountID) {
					return ErrCapabilityPublicationConflict
				}
				removedAccounts[member.AccountID] = true
			}
		}
		gp.ManagedModelRoutes.Routes = append(gp.ManagedModelRoutes.Routes[:index], gp.ManagedModelRoutes.Routes[index+1:]...)
		gp.ModelAllowlist.Models = publicationWithoutNames(gp.ModelAllowlist.Models, append([]string{old.PublicModel}, old.Aliases...))
	}
	for _, remove := range input.RemoveLines {
		if !publicationRemovalInScope(snap, remove.AccountID) {
			return ErrCapabilityPublicationConflict
		}
		index := publicationRouteIndex(gp.ManagedModelRoutes.Routes, remove.PublicModel)
		if index < 0 {
			return publicationInvalid("the explicitly removed line is not currently published")
		}
		route, matched := &gp.ManagedModelRoutes.Routes[index], false
		for bi := range route.Branches {
			branch := &route.Branches[bi]
			members := branch.Accounts[:0]
			for _, member := range branch.Accounts {
				protocol := branch.UpstreamProtocol
				if protocol == "" && remove.Protocol != "" && snap.Accounts[member.AccountID] != nil {
					protocol = publicationConfiguredProtocol(snap.Accounts[member.AccountID].Account)
				}
				if member.AccountID == remove.AccountID && member.UpstreamModel == remove.UpstreamModel && protocol == remove.Protocol {
					matched, removedAccounts[member.AccountID] = true, true
					continue
				}
				members = append(members, member)
			}
			branch.Accounts = members
		}
		if !matched {
			return publicationInvalid("the explicitly removed line does not match its current account, target and protocol")
		}
	}
	for _, accountID := range snap.Request.DetachAccountIDs {
		if _, bound := gs.Bindings[accountID]; !bound {
			continue
		}
		removedAccounts[accountID] = true
		for ri := range gp.ManagedModelRoutes.Routes {
			for bi := range gp.ManagedModelRoutes.Routes[ri].Branches {
				branch := &gp.ManagedModelRoutes.Routes[ri].Branches[bi]
				members := branch.Accounts[:0]
				for _, member := range branch.Accounts {
					if member.AccountID != accountID {
						members = append(members, member)
					}
				}
				branch.Accounts = members
			}
		}
	}
	publicationNormalizeRoutes(&gp)
	if !publicationUniqueRouteNames(gp.ManagedModelRoutes.Routes) {
		return publicationInvalid("a public model or alias collides with a retained publication")
	}
	remaining := publicationRouteMemberSelectors(gp.ManagedModelRoutes.Routes)
	for _, old := range group.ManagedModelRoutes.Routes {
		for _, branch := range ManagedModelRouteBranches(old) {
			for _, member := range branch.Accounts {
				if removedAccounts[member.AccountID] && !remaining[member.AccountID][branch.Selector] {
					patch(member.AccountID).RemoveSelectors = append(patch(member.AccountID).RemoveSelectors, branch.Selector)
				}
			}
		}
	}
	for accountID := range removedAccounts {
		if len(remaining[accountID]) == 0 {
			if _, bound := gs.Bindings[accountID]; bound {
				patch(accountID).RemoveGroupIDs = append(patch(accountID).RemoveGroupIDs, group.ID)
			}
		}
	}
	if len(gp.ManagedModelRoutes.Routes) == 0 && (len(input.RemoveModels) > 0 || len(input.RemoveLines) > 0 || gs.IsNew) {
		gp.Status = "inactive"
		plan.Warnings = append(plan.Warnings, input.Name+": no remaining public models; existing keys are retained")
	} else if len(group.ManagedModelRoutes.Routes) == 0 && len(input.Models) > 0 {
		gp.Status = StatusActive
	}
	publicationProjectMergedRoutes(&gp, group.ManagedModelRoutes.Routes)
	publicationProjectCompositePrices(&gp, gs)
	groupEvidence = publicationUniqueIDs(groupEvidence)
	plan.Changes = append(plan.Changes,
		CapabilityPublicationChange{Kind: "allowlist", GroupID: group.ID, Label: group.Name, Before: group.ModelAllowlist, After: gp.ModelAllowlist, EvidenceIDs: groupEvidence},
		CapabilityPublicationChange{Kind: "routes", GroupID: group.ID, Label: group.Name, Before: group.ManagedModelRoutes, After: gp.ManagedModelRoutes, EvidenceIDs: groupEvidence},
		CapabilityPublicationChange{Kind: "group", GroupID: group.ID, Label: group.Name + " platform/status", Before: map[string]string{"platform": group.Platform, "status": group.Status}, After: map[string]string{"platform": gp.Platform, "status": gp.Status}, EvidenceIDs: groupEvidence},
		CapabilityPublicationChange{Kind: "routes", GroupID: group.ID, Label: group.Name + " protocol dispatch", Before: map[string]any{"messages_dispatch_model_config": group.MessagesDispatchModelConfig, "allow_messages_dispatch": group.AllowMessagesDispatch, "default_mapped_model": group.DefaultMappedModel, "composite_routes": gs.Routes}, After: map[string]any{"messages_dispatch_model_config": gp.MessagesDispatch, "allow_messages_dispatch": gp.AllowMessagesDispatch, "default_mapped_model": gp.DefaultMappedModel, "composite_routes": gp.CompositeRoutes}, EvidenceIDs: groupEvidence},
		CapabilityPublicationChange{Kind: "channel", GroupID: group.ID, Label: group.Name + " dedicated channel", Before: publicationChannelView(gs.Channel), After: map[string]any{"name": fmt.Sprintf("public-capabilities-g%d", group.ID), "billing_model_source": BillingModelSourceRequested, "restrict_models": false, "model_mapping": gp.ChannelMapping, "model_pricing": gp.ChannelPricing, "features": gp.ChannelFeatures, "features_config": gp.ChannelFeaturesConfig, "apply_pricing_to_account_stats": gp.ChannelApplyPricingToAccountStats, "account_stats_pricing_rules": gp.ChannelAccountStatsPricingRules}, EvidenceIDs: groupEvidence})
	plan.Groups = append(plan.Groups, gp)
	return nil
}

func publicationClone[T any](value T) T {
	data, _ := json.Marshal(value)
	var clone T
	_ = json.Unmarshal(data, &clone)
	return clone
}

func publicationRouteIndex(routes []domain.ManagedModelRoute, name string) int {
	for i := range routes {
		if strings.EqualFold(routes[i].PublicModel, name) {
			return i
		}
	}
	return -1
}

func publicationConfiguredProtocol(account *Account) string {
	return ManagedModelConfiguredProtocol(account)
}

func publicationExistingLine(route domain.ManagedModelRoute, account *Account, evidence CapabilityPublicationEvidence) (int, int) {
	for bi, branch := range route.Branches {
		protocol := branch.UpstreamProtocol
		if protocol == "" {
			protocol = publicationConfiguredProtocol(account)
		}
		if branch.TargetPlatform != account.Platform || protocol != evidence.Protocol {
			continue
		}
		for mi, member := range branch.Accounts {
			if member.AccountID == account.ID && member.UpstreamModel == evidence.UpstreamModel {
				return bi, mi
			}
		}
	}
	return -1, -1
}

func publicationRemovalInScope(snap *CapabilityPublicationSnapshot, accountID int64) bool {
	return publicationHasID(snap.Request.Scope.AccountIDs, accountID) || publicationHasID(snap.Request.DetachAccountIDs, accountID)
}

func publicationValidOwnedRoute(groupID int64, route domain.ManagedModelRoute) bool {
	if !publicationValidModel(route.PublicModel) {
		return false
	}
	for _, branch := range ManagedModelRouteBranches(route) {
		if branch.UpstreamProtocol == "" {
			if branch.Selector != ManagedModelSelector(groupID, route.PublicModel) {
				return false
			}
			continue
		}
		for _, member := range branch.Accounts {
			if branch.Selector != ManagedModelBranchSelector(groupID, route.PublicModel, branch.TargetPlatform, branch.UpstreamProtocol, member.UpstreamModel) {
				return false
			}
		}
	}
	return true
}

func publicationUniqueRouteNames(routes []domain.ManagedModelRoute) bool {
	seen := map[string]bool{}
	for _, route := range routes {
		for _, name := range append([]string{route.PublicModel}, route.Aliases...) {
			key := strings.ToLower(name)
			if seen[key] {
				return false
			}
			seen[key] = true
		}
	}
	return true
}

func publicationMergeAliases(public string, existing, additional []string) []string {
	aliases := []string{}
	seen := map[string]bool{strings.ToLower(public): true}
	for _, name := range append(append([]string{}, existing...), additional...) {
		if !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			aliases = append(aliases, name)
		}
	}
	return aliases
}

func publicationWithoutNames(names, removed []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		found := false
		for _, old := range removed {
			found = found || strings.EqualFold(name, old)
		}
		if !found {
			out = append(out, name)
		}
	}
	return out
}

func publicationNormalizeRoutes(gp *CapabilityPublicationGroupPatch) {
	routes := gp.ManagedModelRoutes.Routes[:0]
	for _, route := range gp.ManagedModelRoutes.Routes {
		branches := route.Branches[:0]
		endpoints := []string{}
		for _, branch := range route.Branches {
			if len(branch.Accounts) == 0 {
				continue
			}
			branch.Endpoints = []string{}
			for _, member := range branch.Accounts {
				branch.Endpoints = append(branch.Endpoints, member.Endpoints...)
			}
			branch.Endpoints = publicationUniqueStrings(branch.Endpoints)
			sort.Slice(branch.Accounts, func(i, j int) bool { return branch.Accounts[i].AccountID < branch.Accounts[j].AccountID })
			endpoints = append(endpoints, branch.Endpoints...)
			branches = append(branches, branch)
		}
		if len(branches) == 0 {
			gp.ModelAllowlist.Models = publicationWithoutNames(gp.ModelAllowlist.Models, append([]string{route.PublicModel}, route.Aliases...))
			continue
		}
		sort.Slice(branches, func(i, j int) bool { return branches[i].Selector < branches[j].Selector })
		route.Branches, route.Endpoints = branches, publicationUniqueStrings(endpoints)
		routes = append(routes, route)
	}
	gp.ManagedModelRoutes.Routes = routes
}

func publicationRouteMemberSelectors(routes []domain.ManagedModelRoute) map[int64]map[string]bool {
	out := map[int64]map[string]bool{}
	for _, route := range routes {
		for _, branch := range ManagedModelRouteBranches(route) {
			for _, member := range branch.Accounts {
				if out[member.AccountID] == nil {
					out[member.AccountID] = map[string]bool{}
				}
				out[member.AccountID][branch.Selector] = true
			}
		}
	}
	return out
}

// A scalar channel/composite mapping cannot represent multiple branches. It
// only preserves the public billing identity; the verified managed executor
// chooses the real branch. No platform or successful backup is discarded here.
func publicationProjectMergedRoutes(gp *CapabilityPublicationGroupPatch, old []domain.ManagedModelRoute) {
	oldNames := map[string]bool{}
	for _, route := range old {
		ownedSelectors := map[string]bool{}
		for _, branch := range ManagedModelRouteBranches(route) {
			ownedSelectors[branch.Selector] = true
		}
		for _, name := range append([]string{route.PublicModel}, route.Aliases...) {
			oldNames[name] = true
			if ownedSelectors[gp.MessagesDispatch.ExactModelMappings[name]] {
				delete(gp.MessagesDispatch.ExactModelMappings, name)
			}
			for _, mapping := range gp.ChannelMapping {
				if ownedSelectors[mapping[name]] {
					delete(mapping, name)
				}
			}
		}
		for _, branch := range ManagedModelRouteBranches(route) {
			for _, mapping := range gp.ChannelMapping {
				delete(mapping, branch.Selector)
			}
		}
	}
	composite := gp.CompositeRoutes[:0]
	for _, route := range gp.CompositeRoutes {
		if !oldNames[route.PublicModel] || route.Notes != "account-capabilities managed" {
			composite = append(composite, route)
		}
	}
	gp.CompositeRoutes = composite
	for _, route := range gp.ManagedModelRoutes.Routes {
		for _, branch := range route.Branches {
			platform := gp.Platform
			if platform == PlatformComposite {
				platform = branch.TargetPlatform
			}
			if gp.ChannelMapping[platform] == nil {
				gp.ChannelMapping[platform] = map[string]string{}
			}
			gp.ChannelMapping[platform][branch.Selector] = branch.Selector
			for _, name := range append([]string{route.PublicModel}, route.Aliases...) {
				if _, configured := gp.ChannelMapping[platform][name]; !configured {
					gp.ChannelMapping[platform][name] = route.PublicModel
				}
			}
		}
	}
	// Preserve this feature switch and all unrelated dispatch defaults. Managed
	// public routing is performed before the old scalar Messages dispatcher.
}

// Composite pricing caches are partitioned by actual adapter platform. A new
// branch must retain an already identified public product's exact override,
// rather than silently fall back to a different registry price. Only missing
// platform projections are added; existing prices and unrelated models remain.
func publicationProjectCompositePrices(gp *CapabilityPublicationGroupPatch, gs *CapabilityPublicationGroupSnapshot) {
	if gp.Platform != PlatformComposite {
		return
	}
	sources := append([]ChannelModelPricing{}, gs.Group.ModelPricing...)
	if gs.Channel != nil {
		sources = append(sources, gs.Channel.ModelPricing...)
	}
	find := func(prices []ChannelModelPricing, model, platform string, exactPlatform bool) *ChannelModelPricing {
		for i := range prices {
			if exactPlatform && prices[i].Platform != platform {
				continue
			}
			if !publicationHasTokenPrice(prices[i]) {
				continue
			}
			for _, name := range prices[i].Models {
				if strings.EqualFold(name, model) {
					return &prices[i]
				}
			}
		}
		return nil
	}
	for _, route := range gp.ManagedModelRoutes.Routes {
		for _, branch := range route.Branches {
			platform := branch.TargetPlatform
			if find(gp.ChannelPricing, route.PublicModel, platform, true) != nil {
				continue
			}
			source := find(sources, route.PublicModel, platform, true)
			if source == nil {
				source = find(sources, route.PublicModel, platform, false)
			}
			if source == nil {
				// Registry pricing is already independent of platform. No invented
				// override is necessary when there is no existing exact override.
				continue
			}
			price := publicationClone(*source)
			price.ID, price.ChannelID = 0, 0
			price.Platform, price.Models = platform, []string{route.PublicModel}
			for i := range price.Intervals {
				price.Intervals[i].ID, price.Intervals[i].PricingID = 0, 0
			}
			gp.ChannelPricing = append(gp.ChannelPricing, price)
		}
	}
}
