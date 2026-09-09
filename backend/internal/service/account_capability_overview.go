package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Overview and Recommend are read-only projections of the same complete
// catalogue. Neither method starts an observation or changes public routing.
type AccountCapabilityOverviewFilter struct {
	FolderIDs  []int64
	AccountIDs []int64
	GroupIDs   []int64
	Search     string
}

type AccountCapabilityOverview struct {
	Scope    CapabilityPublicationScope       `json:"scope"`
	Accounts []AccountCapabilityScopeAccount  `json:"accounts"`
	Groups   []AccountCapabilityOverviewGroup `json:"groups"`
	Totals   AccountCapabilityOverviewTotals  `json:"totals"`
}

type AccountCapabilityOverviewTotals struct {
	GroupCount               int `json:"group_count"`
	PublishedModelCount      int `json:"published_model_count"`
	VerifiedAccountCount     int `json:"verified_account_count"`
	RoutingReadyAccountCount int `json:"routing_ready_account_count"`
	AttentionCount           int `json:"attention_count"`
}

type AccountCapabilityOverviewGroup struct {
	ID                       *int64                           `json:"id"`
	Name                     string                           `json:"name"`
	Platform                 string                           `json:"platform"`
	RateMultiplier           float64                          `json:"rate_multiplier"`
	PublishedModelCount      int                              `json:"published_model_count"`
	VerifiedAccountCount     int                              `json:"verified_account_count"`
	RoutingReadyAccountCount int                              `json:"routing_ready_account_count"`
	AttentionCount           int                              `json:"attention_count"`
	Models                   []AccountCapabilityOverviewModel `json:"models"`
}

type AccountCapabilityOverviewModel struct {
	PublicModel                  string                       `json:"public_model"`
	Aliases                      []string                     `json:"aliases"`
	Tier                         string                       `json:"tier"`
	Published                    bool                         `json:"published"`
	VerifiedAccountCount         int                          `json:"verified_account_count"`
	RoutingReadyAccountCount     int                          `json:"routing_ready_account_count"`
	UntestedAccountCount         int                          `json:"untested_account_count"`
	TemporaryFailureAccountCount int                          `json:"temporary_failure_account_count"`
	PricingKnown                 bool                         `json:"pricing_known"`
	NeedsNameConfirmation        bool                         `json:"needs_name_confirmation"`
	LastCheckedAt                *time.Time                   `json:"last_checked_at"`
	LastCheckStatus              string                       `json:"last_check_status"`
	Action                       string                       `json:"action"`
	Reasons                      []string                     `json:"reasons"`
	Candidates                   []AccountCapabilityCandidate `json:"candidates"`
}

type AccountCapabilityModelSelection struct {
	GroupID     int64  `json:"group_id,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
	PublicModel string `json:"public_model"`
}

type AccountCapabilityRecommendationRequest struct {
	Scope          CapabilityPublicationScope        `json:"scope"`
	GroupIDs       []int64                           `json:"group_ids,omitempty"`
	Models         []AccountCapabilityModelSelection `json:"models,omitempty"`
	MainstreamOnly *bool                             `json:"mainstream_only,omitempty"`
}

type AccountCapabilityRecommendation struct {
	Scope               CapabilityPublicationScope                 `json:"scope"`
	PreviewRequest      *CapabilityPublicationRequest              `json:"preview_request"`
	ProbeRequest        *AccountCapabilityCreateRequest            `json:"probe_request"`
	MaximumRequestCount int                                        `json:"maximum_request_count"`
	Impact              AccountCapabilityRecommendationImpact      `json:"impact"`
	Exclusions          []AccountCapabilityRecommendationExclusion `json:"exclusions"`
	Warnings            []string                                   `json:"warnings"`
}

type AccountCapabilityModelReference struct {
	GroupID     int64  `json:"group_id"`
	GroupName   string `json:"group_name"`
	PublicModel string `json:"public_model"`
}

type AccountCapabilityAccountReference struct {
	GroupID     int64  `json:"group_id"`
	GroupName   string `json:"group_name"`
	PublicModel string `json:"public_model"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
}

type AccountCapabilityRecommendationImpact struct {
	AddedModels        []AccountCapabilityModelReference   `json:"added_models"`
	AddedAccounts      []AccountCapabilityAccountReference `json:"added_accounts"`
	AddedRoutes        []AccountCapabilityRouteReference   `json:"added_routes"`
	RetainedModels     []AccountCapabilityModelReference   `json:"retained_models"`
	RemovedModels      []AccountCapabilityModelReference   `json:"removed_models"`
	RemovedAccounts    []AccountCapabilityAccountReference `json:"removed_accounts"`
	ReusedSuccessCount int                                 `json:"reused_success_count"`
}

type AccountCapabilityRouteReference struct {
	GroupID       int64  `json:"group_id"`
	GroupName     string `json:"group_name"`
	PublicModel   string `json:"public_model"`
	AccountID     int64  `json:"account_id"`
	AccountName   string `json:"account_name"`
	UpstreamModel string `json:"upstream_model"`
	Protocol      string `json:"protocol"`
}

type AccountCapabilityRecommendationExclusion struct {
	AccountID     int64  `json:"account_id"`
	PublicModel   string `json:"public_model"`
	UpstreamModel string `json:"upstream_model"`
	Reason        string `json:"reason"`
}

type capabilityFolderLister interface {
	ListAccountFolders(context.Context) ([]AccountManagementFolder, error)
}

func (s *AccountCapabilityCatalogService) Overview(ctx context.Context, filter AccountCapabilityOverviewFilter) (*AccountCapabilityOverview, error) {
	scope, snapshot, err := s.capabilityOverviewSnapshot(ctx, CapabilityPublicationScope{FolderIDs: filter.FolderIDs, AccountIDs: filter.AccountIDs})
	if err != nil {
		return nil, err
	}
	return buildAccountCapabilityOverview(scope, snapshot, filter.GroupIDs, filter.Search, s.billing)
}

func (s *AccountCapabilityCatalogService) Recommend(ctx context.Context, request AccountCapabilityRecommendationRequest) (*AccountCapabilityRecommendation, error) {
	if len(request.Models) > 3000 {
		return nil, ErrAccountCapabilityInvalid
	}
	for _, model := range request.Models {
		if model.GroupID < 0 || !publicationValidModel(model.PublicModel) || len(model.GroupName) > 100 {
			return nil, ErrAccountCapabilityInvalid
		}
	}
	scope, snapshot, err := s.capabilityOverviewSnapshot(ctx, request.Scope)
	if err != nil {
		return nil, err
	}
	overview, err := buildAccountCapabilityOverview(scope, snapshot, request.GroupIDs, "", s.billing)
	if err != nil {
		return nil, err
	}
	return buildAccountCapabilityRecommendation(scope, snapshot, overview, request)
}

// Resolve named defaults only when neither folder nor account was specified.
// A deep link containing account_ids must not intersect them with those defaults.
func (s *AccountCapabilityCatalogService) capabilityOverviewSnapshot(ctx context.Context, input CapabilityPublicationScope) (CapabilityPublicationScope, *accountCapabilityCatalogSnapshot, error) {
	scope, err := s.normalizeCapabilityOverviewScope(ctx, input)
	if err != nil {
		return scope, nil, err
	}
	snapshot, err := s.loadCapabilityCatalog(ctx, AccountCapabilityCandidateFilter{FolderIDs: scope.FolderIDs, AccountIDs: scope.AccountIDs})
	if err != nil {
		return scope, nil, err
	}
	actual := make([]int64, 0, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		actual = append(actual, account.ID)
	}
	actual = publicationUniqueIDs(actual)
	for _, requested := range scope.AccountIDs {
		if !publicationHasID(actual, requested) {
			return scope, nil, ErrAccountCapabilityScope
		}
	}
	scope.AccountIDs = actual
	return scope, snapshot, nil
}

func (s *AccountCapabilityCatalogService) normalizeCapabilityOverviewScope(ctx context.Context, input CapabilityPublicationScope) (CapabilityPublicationScope, error) {
	if s == nil || s.admin == nil || len(input.FolderIDs) > 1000 || len(input.AccountIDs) > 1000 {
		return input, ErrAccountCapabilityInvalid
	}
	folders, err := capabilityIDs(input.FolderIDs)
	if err != nil {
		return input, err
	}
	accounts, err := capabilityIDs(input.AccountIDs)
	if err != nil {
		return input, err
	}
	if len(folders) == 0 && len(accounts) > 0 {
		for _, id := range accounts {
			account, getErr := s.admin.GetAccount(ctx, id)
			if getErr != nil {
				return input, getErr
			}
			if account == nil || account.ManagementFolderID == nil || *account.ManagementFolderID <= 0 {
				return input, ErrAccountCapabilityScope
			}
			folders = append(folders, *account.ManagementFolderID)
		}
		folders = publicationUniqueIDs(folders)
	}
	if len(folders) == 0 {
		lister, supported := s.admin.(capabilityFolderLister)
		if !supported {
			return input, ErrAccountCapabilityInvalid
		}
		available, listErr := lister.ListAccountFolders(ctx)
		if listErr != nil {
			return input, listErr
		}
		for _, folder := range available {
			if folder.ID > 0 && (folder.Name == "dmxapi" || folder.Name == "白嫖") {
				folders = append(folders, folder.ID)
			}
		}
		folders = publicationUniqueIDs(folders)
		if len(folders) == 0 {
			return input, ErrAccountCapabilityScope
		}
	}
	return CapabilityPublicationScope{FolderIDs: folders, AccountIDs: accounts}, nil
}

func capabilityOverviewGroupNames() []string {
	names := []string{}
	seen := map[string]bool{}
	for _, definition := range capabilityLaunchModels {
		key := strings.ToLower(definition.Group)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, definition.Group)
		if key == "gpt" {
			names = append(names, "gpt-vip")
			seen["gpt-vip"] = true
		}
	}
	return names
}

// Editorial launch groups establish the familiar order, not the universe of
// recognizable families. A concrete Grok version outside the 4.6 launch group,
// for example, remains in its actual family instead of becoming an unknown
// name. Unrelated account/private groups are deliberately not added here.
func capabilityOverviewSnapshotGroupNames(snapshot *accountCapabilityCatalogSnapshot) []string {
	names := capabilityOverviewGroupNames()
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[strings.ToLower(name)] = true
	}
	present := make(map[string]bool, len(snapshot.Groups))
	for name := range snapshot.Groups {
		present[strings.ToLower(name)] = true
	}
	for _, row := range snapshot.Rows {
		present[strings.ToLower(row.GroupName)] = true
	}
	for _, family := range capabilityFamilyPatterns {
		key := strings.ToLower(family.group)
		if !known[key] && present[key] {
			known[key] = true
			names = append(names, family.group)
		}
	}
	return names
}

type capabilityOverviewModelState struct {
	model     AccountCapabilityOverviewModel
	verified  map[int64]bool
	ready     map[int64]bool
	untested  map[int64]bool
	temporary map[int64]bool
	seen      map[string]int
	latest    *AccountCapabilityAttempt
	canAdd    bool
	canProbe  bool
	waiting   bool
}

func newCapabilityOverviewModelState(publicModel, groupName string) *capabilityOverviewModelState {
	tier := "standard"
	if strings.EqualFold(groupName, "gpt-vip") {
		tier = "vip"
	}
	return &capabilityOverviewModelState{
		model: AccountCapabilityOverviewModel{PublicModel: publicModel, Tier: tier, Aliases: []string{},
			Reasons: []string{}, Candidates: []AccountCapabilityCandidate{}, LastCheckStatus: "untested"},
		verified: map[int64]bool{}, ready: map[int64]bool{}, untested: map[int64]bool{},
		temporary: map[int64]bool{}, seen: map[string]int{},
	}
}

func buildAccountCapabilityOverview(scope CapabilityPublicationScope, snapshot *accountCapabilityCatalogSnapshot, groupIDs []int64, search string, billing *BillingService) (*AccountCapabilityOverview, error) {
	if snapshot == nil || len(groupIDs) > 50 || !publicationPositiveUnique(groupIDs) {
		return nil, ErrAccountCapabilityInvalid
	}
	result := &AccountCapabilityOverview{Scope: scope, Accounts: append([]AccountCapabilityScopeAccount{}, snapshot.Accounts...), Groups: []AccountCapabilityOverviewGroup{}}
	names := capabilityOverviewSnapshotGroupNames(snapshot)
	known := map[string]bool{}
	knownIDs := map[int64]bool{}
	for _, name := range names {
		known[strings.ToLower(name)] = true
		if group := snapshot.Groups[strings.ToLower(name)]; group != nil {
			knownIDs[group.ID] = true
		}
	}
	for _, id := range groupIDs {
		if !knownIDs[id] {
			return nil, ErrAccountCapabilityScope
		}
	}
	rows := map[string][]AccountCapabilityCandidate{}
	for _, row := range snapshot.Rows {
		key := strings.ToLower(row.GroupName)
		if !known[key] {
			key = strings.ToLower(capabilityUnclassifiedGroup)
		}
		rows[key] = append(rows[key], row)
	}
	if len(rows[strings.ToLower(capabilityUnclassifiedGroup)]) > 0 {
		names = append(names, capabilityUnclassifiedGroup)
	}
	allVerified, allReady := map[int64]bool{}, map[int64]bool{}
	search = strings.ToLower(strings.TrimSpace(search))
	for _, name := range names {
		key := strings.ToLower(name)
		group := snapshot.Groups[key]
		if !known[key] { // A same-named private/unrelated group is never selected.
			group = nil
		}
		if len(groupIDs) > 0 && (group == nil || !publicationHasID(groupIDs, group.ID)) {
			continue
		}
		view := AccountCapabilityOverviewGroup{Name: name, Platform: PlatformOpenAI, Models: []AccountCapabilityOverviewModel{}}
		if name == "Qwen" || name == "MiniMax" {
			view.RateMultiplier = 0.3
		}
		if group != nil {
			id := group.ID
			view.ID, view.Name, view.Platform, view.RateMultiplier = &id, group.Name, group.Platform, group.RateMultiplier
		}
		states := map[string]*capabilityOverviewModelState{}
		get := func(publicModel string) *capabilityOverviewModelState {
			modelKey := strings.ToLower(publicModel)
			state := states[modelKey]
			if state == nil {
				state = newCapabilityOverviewModelState(publicModel, name)
				states[modelKey] = state
			}
			return state
		}
		for _, row := range rows[key] {
			state := get(row.PublicModel)
			state.model.Aliases = append(state.model.Aliases, row.Aliases...)
			identity := capabilityOverviewCandidateIdentity(row)
			if index, exists := state.seen[identity]; exists {
				candidate := &state.model.Candidates[index]
				candidate.Aliases = capabilityOverviewAliases(candidate.PublicModel, append(append([]string{}, candidate.Aliases...), row.Aliases...))
				continue
			}
			state.seen[identity] = len(state.model.Candidates)
			state.model.Candidates = append(state.model.Candidates, row)
			state.model.Published = state.model.Published || row.Published
			state.model.PricingKnown = state.model.PricingKnown || row.PricingKnown
			state.model.NeedsNameConfirmation = state.model.NeedsNameConfirmation || row.NeedsNameConfirmation
			if row.LastSuccessReusable && row.LastSuccessItemID != nil {
				state.verified[row.AccountID] = true
			}
			if row.RoutingReady {
				state.ready[row.AccountID] = true
			}
			if !row.AlreadyAttempted && !row.HasCompatibleSuccess {
				state.untested[row.AccountID] = true
			}
			state.canProbe = state.canProbe || capabilityRecommendationCanProbe(row)
			state.waiting = state.waiting || row.HasPendingProbe
			if capabilityOverviewTemporaryAttempt(row.LatestAttempt) {
				state.temporary[row.AccountID] = true
			}
			if capabilityOverviewAttemptNewer(row.LatestAttempt, state.latest) {
				state.latest = row.LatestAttempt
			}
			state.canAdd = state.canAdd || (capabilityRecommendationCanPublish(row) && !capabilityRecommendationHasRoute(group, row))
			if row.LastSuccessReusable && !row.Schedulable {
				state.model.Reasons = append(state.model.Reasons, "account_scheduling_paused")
			}
			state.model.Reasons = append(state.model.Reasons, row.NotPublishableReasons...)
		}
		if group != nil {
			aliasOwners := map[string]string{}
			for _, route := range group.ManagedModelRoutes.Routes {
				state := get(route.PublicModel)
				state.model.Published = state.model.Published || group.ManagedModelRoutes.Enabled
				state.model.Aliases = append(state.model.Aliases, route.Aliases...)
				for _, alias := range route.Aliases {
					aliasOwners[strings.ToLower(alias)] = route.PublicModel
				}
			}
			for _, publicModel := range group.ModelAllowlist.Models {
				if owner := aliasOwners[strings.ToLower(publicModel)]; owner != "" {
					publicModel = owner
				}
				state := get(publicModel)
				// An enabled managed group cannot serve an allowlisted model
				// without a route. Keep the saved name visible as configuration.
				if group.ModelAllowlist.Enabled && group.ManagedModelRoutes.Enabled && !state.model.Published {
					state.model.Reasons = append(state.model.Reasons, "published_configuration_incomplete")
				}
				state.model.Published = state.model.Published || (group.ModelAllowlist.Enabled && !group.ManagedModelRoutes.Enabled)
			}
		}
		modelKeys := make([]string, 0, len(states))
		for modelKey := range states {
			modelKeys = append(modelKeys, modelKey)
		}
		sort.Strings(modelKeys)
		groupVerified, groupReady := map[int64]bool{}, map[int64]bool{}
		for _, modelKey := range modelKeys {
			state := states[modelKey]
			model := &state.model
			if model.PublicModel == "" || strings.HasPrefix(strings.ToLower(model.PublicModel), "s2pub-") || !capabilityOverviewMatchesSearch(view.Name, model, search) {
				continue
			}
			model.Aliases = capabilityOverviewAliases(model.PublicModel, model.Aliases)
			model.VerifiedAccountCount, model.RoutingReadyAccountCount = len(state.verified), len(state.ready)
			model.UntestedAccountCount, model.TemporaryFailureAccountCount = len(state.untested), len(state.temporary)
			if len(model.Candidates) == 0 {
				model.PricingKnown = CapabilityHasIdentifiedPricing(billing, group, snapshot.GroupChannels[capabilityOverviewGroupID(view)], model.PublicModel)
				_, _, recognized := resolveCapabilityLaunchModel(model.PublicModel)
				model.NeedsNameConfirmation = !recognized
				model.Reasons = append(model.Reasons, "no_scoped_candidates")
			}
			if state.latest != nil {
				model.LastCheckedAt, model.LastCheckStatus = state.latest.CheckedAt, capabilityOverviewAttemptStatus(state.latest)
			}
			switch {
			case model.NeedsNameConfirmation:
				model.Action = "review_name"
				model.Reasons = append(model.Reasons, "needs_name_confirmation")
			case model.VerifiedAccountCount > 0 && !model.PricingKnown:
				model.Action = "set_price"
				model.Reasons = append(model.Reasons, "pricing_unavailable")
			case state.canAdd && (group != nil || name == "Qwen" || name == "MiniMax"):
				model.Action = "add"
			case state.canProbe && (group != nil || name == "Qwen" || name == "MiniMax"):
				model.Action = "check_untested"
				model.Reasons = append(model.Reasons, "needs_basic_probe")
			case state.waiting || (model.TemporaryFailureAccountCount > 0 && model.RoutingReadyAccountCount == 0):
				model.Action = "wait"
			default:
				model.Action = "view"
			}
			if group == nil && !model.NeedsNameConfirmation && name != "Qwen" && name != "MiniMax" {
				model.Reasons = append(model.Reasons, "group_missing")
			}
			if model.TemporaryFailureAccountCount > 0 {
				model.Reasons = append(model.Reasons, "temporary_failure")
			}
			if model.Published && model.RoutingReadyAccountCount == 0 {
				model.Reasons = append(model.Reasons, "no_routing_ready_accounts")
			}
			model.Reasons = publicationUniqueStrings(model.Reasons)
			if model.Published {
				view.PublishedModelCount++
			}
			if model.Action != "view" || model.TemporaryFailureAccountCount > 0 || (model.Published && model.RoutingReadyAccountCount == 0) || publicationHasString(model.Reasons, "account_scheduling_paused") {
				view.AttentionCount++
			}
			for id := range state.verified {
				groupVerified[id], allVerified[id] = true, true
			}
			for id := range state.ready {
				groupReady[id], allReady[id] = true, true
			}
			view.Models = append(view.Models, *model)
		}
		if search != "" && len(view.Models) == 0 && !strings.Contains(strings.ToLower(view.Name), search) {
			continue
		}
		view.VerifiedAccountCount, view.RoutingReadyAccountCount = len(groupVerified), len(groupReady)
		result.Totals.PublishedModelCount += view.PublishedModelCount
		result.Totals.AttentionCount += view.AttentionCount
		result.Groups = append(result.Groups, view)
	}
	result.Totals.GroupCount = len(result.Groups)
	result.Totals.VerifiedAccountCount, result.Totals.RoutingReadyAccountCount = len(allVerified), len(allReady)
	return result, nil
}

func capabilityOverviewGroupID(group AccountCapabilityOverviewGroup) int64 {
	if group.ID != nil {
		return *group.ID
	}
	return 0
}

func capabilityOverviewCandidateIdentity(row AccountCapabilityCandidate) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", row.AccountID, row.ConfigFingerprint, row.UpstreamModel, row.Protocol)
}

func capabilityOverviewAliases(publicModel string, aliases []string) []string {
	result, seen := []string{}, map[string]bool{strings.ToLower(publicModel): true}
	for _, alias := range aliases {
		key := strings.ToLower(alias)
		if alias != "" && !seen[key] && !strings.HasPrefix(key, "s2pub-") {
			seen[key] = true
			result = append(result, alias)
		}
	}
	sort.Strings(result)
	return result
}

func capabilityOverviewMatchesSearch(group string, model *AccountCapabilityOverviewModel, search string) bool {
	if search == "" || strings.Contains(strings.ToLower(group+" "+model.PublicModel+" "+strings.Join(model.Aliases, " ")), search) {
		return true
	}
	for _, candidate := range model.Candidates {
		if strings.Contains(strings.ToLower(candidate.AccountName+" "+candidate.UpstreamModel), search) {
			return true
		}
	}
	return false
}

func capabilityOverviewTemporaryAttempt(attempt *AccountCapabilityAttempt) bool {
	if attempt == nil || attempt.Stale || attempt.AccountFailure || attempt.Status == "alive" {
		return false
	}
	switch attempt.Classification {
	case "upstream_unavailable", "rate_limited", "timeout", "network_error", "quota_exhausted":
		return true
	}
	return false
}

func capabilityOverviewAttemptStatus(attempt *AccountCapabilityAttempt) string {
	if attempt.Stale {
		return "stale"
	}
	if attempt.AccountFailure {
		return "account_failure"
	}
	if capabilityOverviewTemporaryAttempt(attempt) {
		return "temporary_failure"
	}
	if attempt.Classification == "model_unavailable" {
		return "model_unavailable"
	}
	return attempt.Status
}

func capabilityOverviewAttemptNewer(candidate, current *AccountCapabilityAttempt) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if candidate.CheckedAt != nil && current.CheckedAt != nil && !candidate.CheckedAt.Equal(*current.CheckedAt) {
		return candidate.CheckedAt.After(*current.CheckedAt)
	}
	return candidate.ItemID > current.ItemID
}

func buildAccountCapabilityRecommendation(scope CapabilityPublicationScope, snapshot *accountCapabilityCatalogSnapshot, overview *AccountCapabilityOverview, request AccountCapabilityRecommendationRequest) (*AccountCapabilityRecommendation, error) {
	result := &AccountCapabilityRecommendation{
		Scope: scope, Exclusions: []AccountCapabilityRecommendationExclusion{}, Warnings: []string{},
		Impact: AccountCapabilityRecommendationImpact{AddedModels: []AccountCapabilityModelReference{}, AddedAccounts: []AccountCapabilityAccountReference{}, AddedRoutes: []AccountCapabilityRouteReference{},
			RetainedModels: []AccountCapabilityModelReference{}, RemovedModels: []AccountCapabilityModelReference{}, RemovedAccounts: []AccountCapabilityAccountReference{}},
	}
	preview := &CapabilityPublicationRequest{Operation: "merge", Scope: scope, Groups: []CapabilityPublicationGroup{}}
	probe := &AccountCapabilityCreateRequest{Kind: AccountCapabilityKindProbe, FolderIDs: append([]int64{}, scope.FolderIDs...), AccountIDs: []int64{},
		Items: []AccountCapabilityProbeTarget{}, OnlyUntested: true, ExpectedConfigFingerprints: map[int64]string{}}
	reused, excluded, plannedProbeTargets := map[int64]bool{}, map[string]bool{}, map[string]bool{}
	mainstreamOnly := request.MainstreamOnly == nil || *request.MainstreamOnly
	for _, group := range overview.Groups {
		if !capabilityRecommendationGroupSelected(group, request.Models) {
			continue
		}
		gp := CapabilityPublicationGroup{ID: capabilityOverviewGroupID(group), Name: group.Name, Platform: group.Platform,
			RateMultiplier: group.RateMultiplier, Models: []CapabilityPublicationModel{}}
		var current *Group
		if group.ID != nil {
			current = snapshot.Groups[strings.ToLower(group.Name)]
		}
		groupAvailable := current != nil || group.Name == "Qwen" || group.Name == "MiniMax"
		for _, model := range group.Models {
			ref := AccountCapabilityModelReference{GroupID: gp.ID, GroupName: gp.Name, PublicModel: model.PublicModel}
			// Retention is intentionally independent of a partial model selection.
			if model.Published || capabilityRecommendationModelConfigured(current, model.PublicModel) {
				result.Impact.RetainedModels = append(result.Impact.RetainedModels, ref)
			}
			if !capabilityRecommendationModelSelected(group, model.PublicModel, request.Models) {
				continue
			}
			selected := CapabilityPublicationModel{PublicModel: model.PublicModel, Tier: model.Tier, Aliases: []string{}, EvidenceIDs: []int64{}}
			seenCandidates, addedAccounts := map[string]bool{}, map[int64]bool{}
			candidates := append([]AccountCapabilityCandidate{}, model.Candidates...)
			sort.SliceStable(candidates, func(i, j int) bool {
				a, b := candidates[i], candidates[j]
				if a.AccountID != b.AccountID {
					return a.AccountID < b.AccountID
				}
				if a.UpstreamModel != b.UpstreamModel {
					return a.UpstreamModel < b.UpstreamModel
				}
				if a.ProbeProtocolPriority != b.ProbeProtocolPriority {
					return a.ProbeProtocolPriority < b.ProbeProtocolPriority
				}
				return capabilityRecommendationProtocolRank(a.Protocol) < capabilityRecommendationProtocolRank(b.Protocol)
			})
			for _, row := range candidates {
				if len(request.Models) == 0 && mainstreamOnly && !row.Mainstream {
					continue
				}
				exclude := func(reason string) {
					key := fmt.Sprintf("%d\x00%s\x00%s\x00%s", row.AccountID, row.PublicModel, row.UpstreamModel, reason)
					if !excluded[key] {
						excluded[key] = true
						result.Exclusions = append(result.Exclusions, AccountCapabilityRecommendationExclusion{AccountID: row.AccountID, PublicModel: row.PublicModel, UpstreamModel: row.UpstreamModel, Reason: reason})
					}
				}
				if row.NeedsNameConfirmation || !row.Recognized {
					exclude("needs_name_confirmation")
					continue
				}
				if !groupAvailable {
					exclude("group_missing")
					continue
				}
				if capabilityRecommendationCanPublish(row) {
					selected.Aliases = append(selected.Aliases, row.Aliases...)
					identity := capabilityOverviewCandidateIdentity(row)
					if !seenCandidates[identity] {
						seenCandidates[identity] = true
						selected.EvidenceIDs = append(selected.EvidenceIDs, *row.LastSuccessItemID)
						reused[*row.LastSuccessItemID] = true
						if !capabilityRecommendationHasRoute(current, row) {
							result.Impact.AddedRoutes = append(result.Impact.AddedRoutes, AccountCapabilityRouteReference{GroupID: gp.ID, GroupName: gp.Name,
								PublicModel: model.PublicModel, AccountID: row.AccountID, AccountName: row.AccountName, UpstreamModel: row.UpstreamModel, Protocol: row.Protocol})
						}
					}
					if !addedAccounts[row.AccountID] && !capabilityRecommendationHasAccount(current, model.PublicModel, row.AccountID) {
						addedAccounts[row.AccountID] = true
						result.Impact.AddedAccounts = append(result.Impact.AddedAccounts, AccountCapabilityAccountReference{GroupID: gp.ID, GroupName: gp.Name,
							PublicModel: model.PublicModel, AccountID: row.AccountID, AccountName: row.AccountName})
					}
					continue
				}
				for _, reason := range row.NotPublishableReasons {
					exclude(reason)
				}
				if !ManagedModelBranchProtocolSupported(row.AccountPlatform, row.Protocol) {
					exclude("unsupported_public_protocol")
				}
				if capabilityRecommendationCanProbe(row) {
					key := fmt.Sprintf("%d\x00%s\x00%s", row.AccountID, row.ConfigFingerprint, row.UpstreamModel)
					if !plannedProbeTargets[key] {
						plannedProbeTargets[key] = true
						probe.Items = append(probe.Items, AccountCapabilityProbeTarget{AccountID: row.AccountID, UpstreamModel: row.UpstreamModel,
							Protocol: row.Protocol, Profile: AccountCapabilityProfileText, Aliases: capabilityOverviewAliases(row.UpstreamModel, append([]string{row.PublicModel}, row.Aliases...))})
						probe.AccountIDs = append(probe.AccountIDs, row.AccountID)
						probe.ExpectedConfigFingerprints[row.AccountID] = row.ConfigFingerprint
					}
				} else if row.AlreadyAttempted && !row.LastSuccessReusable {
					exclude("previous_attempt_not_retried")
				}
			}
			if len(selected.EvidenceIDs) > 0 {
				selected.EvidenceIDs = publicationUniqueIDs(selected.EvidenceIDs)
				selected.Aliases = capabilityOverviewAliases(selected.PublicModel, selected.Aliases)
				gp.Models = append(gp.Models, selected)
				if !model.Published {
					result.Impact.AddedModels = append(result.Impact.AddedModels, ref)
				}
			}
		}
		if len(gp.Models) > 0 {
			preview.Groups = append(preview.Groups, gp)
		}
	}
	result.Impact.ReusedSuccessCount = len(reused)
	if len(preview.Groups) > 0 && len(scope.AccountIDs) > 0 {
		if snapshot.ExpectedInputRevisions != nil {
			expected := &CapabilityPublicationInputRevisions{Accounts: map[int64]string{}, Groups: map[int64]string{}}
			for _, id := range scope.AccountIDs {
				if revision := snapshot.ExpectedInputRevisions.Accounts[id]; len(revision) == 64 {
					expected.Accounts[id] = revision
				} else {
					return nil, ErrAccountCapabilityConflict
				}
			}
			for _, group := range preview.Groups {
				if group.ID == 0 {
					continue
				}
				if revision := snapshot.ExpectedInputRevisions.Groups[group.ID]; len(revision) == 64 {
					expected.Groups[group.ID] = revision
				} else {
					return nil, ErrAccountCapabilityConflict
				}
			}
			preview.ExpectedConfigRevisions = expected
		}
		key, err := capabilityRecommendationPreviewKey(preview, snapshot)
		if err != nil {
			return nil, err
		}
		preview.IdempotencyKey = key
		result.PreviewRequest = preview
	}
	if len(probe.Items) > 0 {
		probe.AccountIDs = publicationUniqueIDs(probe.AccountIDs)
		result.ProbeRequest, result.MaximumRequestCount = probe, len(probe.Items)
	}
	if len(result.Impact.RetainedModels) > 0 {
		result.Warnings = append(result.Warnings, "existing_configuration_retained")
	}
	if len(reused) > 0 {
		result.Warnings = append(result.Warnings, "existing_success_reused")
	}
	return result, nil
}

// Evidence reuse must not replay a previously applied change set after a
// browser edit removed a route. Bind the idempotency key to the actual current
// inputs as well as the desired additions; unchanged inputs remain repeatable.
func capabilityRecommendationPreviewKey(request *CapabilityPublicationRequest, snapshot *accountCapabilityCatalogSnapshot) (string, error) {
	copyRequest := *request
	copyRequest.IdempotencyKey = ""
	groups := map[string]any{}
	for _, selected := range request.Groups {
		key := strings.ToLower(selected.Name)
		group := snapshot.Groups[key]
		if group == nil {
			groups[key] = nil
			continue
		}
		groups[key] = map[string]any{
			"id": group.ID, "name": group.Name, "platform": group.Platform, "wire_platform": group.WirePlatform,
			"rate_multiplier": group.RateMultiplier, "status": group.Status,
			"model_pricing": group.ModelPricing, "long_context_pricing_enabled": group.LongContextPricingEnabled,
			"managed_model_routes": group.ManagedModelRoutes, "model_allowlist": group.ModelAllowlist,
			"messages_dispatch": group.MessagesDispatchModelConfig, "allow_messages_dispatch": group.AllowMessagesDispatch,
			"default_mapped_model": group.DefaultMappedModel, "channel": publicationChannelView(snapshot.GroupChannels[group.ID]),
		}
	}
	accounts := append([]AccountCapabilityScopeAccount{}, snapshot.Accounts...)
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	raw, err := json.Marshal(struct {
		Request                     CapabilityPublicationRequest    `json:"request"`
		Groups                      map[string]any                  `json:"groups"`
		Accounts                    []AccountCapabilityScopeAccount `json:"accounts"`
		AccountPublicationRevisions map[int64]string                `json:"account_publication_revisions"`
	}{copyRequest, groups, accounts, snapshot.AccountPublicationRevisions})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "cap-plan-v2-" + hex.EncodeToString(digest[:]), nil
}

func capabilityRecommendationGroupSelected(group AccountCapabilityOverviewGroup, models []AccountCapabilityModelSelection) bool {
	if len(models) == 0 {
		return true
	}
	for _, model := range models {
		if (model.GroupID == 0 || model.GroupID == capabilityOverviewGroupID(group)) &&
			(model.GroupName == "" || strings.EqualFold(model.GroupName, group.Name)) {
			return true
		}
	}
	return false
}

func capabilityRecommendationModelSelected(group AccountCapabilityOverviewGroup, publicModel string, models []AccountCapabilityModelSelection) bool {
	if len(models) == 0 {
		return true
	}
	for _, model := range models {
		if strings.EqualFold(model.PublicModel, publicModel) && capabilityRecommendationGroupSelected(group, []AccountCapabilityModelSelection{model}) {
			return true
		}
	}
	return false
}

func capabilityRecommendationHasAccount(group *Group, publicModel string, accountID int64) bool {
	if group == nil {
		return false
	}
	for _, route := range group.ManagedModelRoutes.Routes {
		if !strings.EqualFold(route.PublicModel, publicModel) {
			continue
		}
		for _, branch := range ManagedModelRouteBranches(route) {
			for _, member := range branch.Accounts {
				if member.AccountID == accountID {
					return true
				}
			}
		}
	}
	return false
}

func capabilityRecommendationModelConfigured(group *Group, publicModel string) bool {
	if group == nil {
		return false
	}
	for _, model := range group.ModelAllowlist.Models {
		if strings.EqualFold(model, publicModel) {
			return true
		}
	}
	for _, route := range group.ManagedModelRoutes.Routes {
		if strings.EqualFold(route.PublicModel, publicModel) {
			return true
		}
	}
	return false
}

func capabilityRecommendationHasRoute(group *Group, row AccountCapabilityCandidate) bool {
	if group == nil {
		return false
	}
	for _, route := range group.ManagedModelRoutes.Routes {
		if !strings.EqualFold(route.PublicModel, row.PublicModel) {
			continue
		}
		for _, branch := range ManagedModelRouteBranches(route) {
			if branch.TargetPlatform != row.AccountPlatform ||
				(branch.UpstreamProtocol != "" && branch.UpstreamProtocol != row.Protocol) ||
				(branch.UpstreamProtocol == "" && !row.Published) {
				continue
			}
			for _, member := range branch.Accounts {
				if member.AccountID == row.AccountID && member.UpstreamModel == row.UpstreamModel && member.AccountFingerprint == row.ConfigFingerprint {
					return true
				}
			}
		}
	}
	return false
}

func capabilityRecommendationCanPublish(row AccountCapabilityCandidate) bool {
	return row.Publishable && row.Recognized && !row.NeedsNameConfirmation && row.LastSuccessReusable &&
		row.LastSuccessItemID != nil && *row.LastSuccessItemID > 0 && ManagedModelBranchProtocolSupported(row.AccountPlatform, row.Protocol)
}

func capabilityRecommendationCanProbe(row AccountCapabilityCandidate) bool {
	return row.ProbeEligible && row.Recognized && !row.NeedsNameConfirmation && !row.AlreadyAttempted && !row.HasCompatibleSuccess &&
		!row.HasPendingProbe && row.AttemptedProtocolCount < 2 &&
		ManagedModelBranchProtocolSupported(row.AccountPlatform, row.Protocol)
}

// Existing successful WS evidence can be retained, but the organizer never
// widens an HTTP gap check into a new WS or token-count experiment.
func capabilityRecommendationProtocolRank(protocol string) int {
	switch protocol {
	case AccountCapabilityProtocolResponses:
		return 0
	case AccountCapabilityProtocolMessages:
		return 1
	case AccountCapabilityProtocolChatCompletions:
		return 2
	default:
		return 10
	}
}
