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

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// The curated launch catalogue is an editorial recommendation, never a filter
// or availability evidence. Discovery and real probes remain independent facts.
type capabilityModelDefinition struct {
	ID      string
	Group   string
	Aliases []string
}

var capabilityLaunchModels = []capabilityModelDefinition{
	{ID: "gpt-6-astra", Group: "gpt"},
	{ID: "gpt-5.6-sol", Group: "gpt", Aliases: []string{"gpt-5.6"}},
	{ID: "gpt-5.6-terra", Group: "gpt"},
	{ID: "gpt-5.6-luna", Group: "gpt"},
	{ID: "gpt-5.4-mini", Group: "gpt"},
	{ID: "gpt-5.3-codex-spark", Group: "gpt"},
	{ID: "claude-fable-5", Group: "claude(非逆向渠道)"},
	{ID: "claude-fable-5-1", Group: "claude(非逆向渠道)"},
	{ID: "claude-opus-5", Group: "claude(非逆向渠道)"},
	{ID: "claude-sonnet-5", Group: "claude(非逆向渠道)"},
	{ID: "claude-haiku-4-5-20251001", Group: "claude(非逆向渠道)", Aliases: []string{"claude-haiku-4-5"}},
	{ID: "gemini-3.1-pro-preview", Group: "gemini"},
	{ID: "gemini-3.8-flash", Group: "gemini"},
	{ID: "gemini-3.5-flash-lite", Group: "gemini"},
	{ID: "grok-4.6", Group: "grok(仅4.6)"},
	{ID: "kimi-k3", Group: "kimi"},
	{ID: "kimi-k2.7-code", Group: "kimi"},
	{ID: "kimi-for-coding", Group: "kimi"},
	{ID: "glm-5.3", Group: "glm"},
	{ID: "glm-5.3-flash", Group: "glm"},
	{ID: "deepseek-v4-pro", Group: "deepseek"},
	{ID: "deepseek-v4-flash", Group: "deepseek"},
	{ID: "qwen3.8-max", Group: "Qwen", Aliases: []string{"qwen3.8-max-0902", "qwen3.8-max-2026-09-02"}},
	{ID: "qwen3.8-flash", Group: "Qwen"},
	{ID: "qwen3-coder-plus", Group: "Qwen"},
	{ID: "qwen3-coder-next", Group: "Qwen"},
	{ID: "MiniMax-M3", Group: "MiniMax"},
	{ID: "MiniMax-M2.7-highspeed", Group: "MiniMax"},
}

type AccountCapabilityCandidate struct {
	CandidateID            string                    `json:"candidate_id"`
	AccountID              int64                     `json:"account_id"`
	AccountName            string                    `json:"account_name"`
	FolderID               int64                     `json:"folder_id"`
	AccountPlatform        string                    `json:"account_platform"`
	ConfigFingerprint      string                    `json:"config_fingerprint"`
	PublicModel            string                    `json:"public_model"`
	UpstreamModel          string                    `json:"upstream_model"`
	Protocol               string                    `json:"protocol"`
	ProbeProtocolPriority  int                       `json:"-"`
	Profile                string                    `json:"profile"`
	Aliases                []string                  `json:"aliases"`
	Tier                   string                    `json:"tier"`
	GroupID                *int64                    `json:"group_id,omitempty"`
	GroupName              string                    `json:"group_name"`
	Mainstream             bool                      `json:"mainstream"`
	Recognized             bool                      `json:"recognized"`
	Recommended            bool                      `json:"recommended"`
	NeedsNameConfirmation  bool                      `json:"needs_name_confirmation"`
	Discovered             bool                      `json:"discovered"`
	Configured             bool                      `json:"configured"`
	Published              bool                      `json:"published"`
	RoutingReady           bool                      `json:"routing_ready"`
	PricingKnown           bool                      `json:"pricing_known"`
	Publishable            bool                      `json:"publishable"`
	NotPublishableReasons  []string                  `json:"not_publishable_reasons"`
	DiscoveryStatus        string                    `json:"discovery_status"`
	LatestProbeItemID      *int64                    `json:"latest_probe_item_id,omitempty"`
	LatestAttempt          *AccountCapabilityAttempt `json:"latest_attempt,omitempty"`
	LastSuccessItemID      *int64                    `json:"last_success_item_id,omitempty"`
	LastSuccessAt          *time.Time                `json:"last_success_at,omitempty"`
	LastSuccessReusable    bool                      `json:"last_success_reusable"`
	AlreadyAttempted       bool                      `json:"already_attempted"`
	HasCompatibleSuccess   bool                      `json:"has_compatible_success"`
	HasPendingProbe        bool                      `json:"has_pending_probe"`
	AttemptedProtocolCount int                       `json:"attempted_protocol_count"`
	ProbeEligible          bool                      `json:"probe_eligible"`
	ProbeStatus            string                    `json:"probe_status"`
	CheckedAt              *time.Time                `json:"checked_at,omitempty"`
	Stale                  bool                      `json:"stale"`
	Schedulable            bool                      `json:"schedulable"`
	Warnings               []string                  `json:"warnings"`
}

// AccountCapabilityAttempt is the most recent basic-text observation, not a
// combined verdict. In particular a temporary failure does not erase an older
// successful observation made with the same account configuration.
type AccountCapabilityAttempt struct {
	ItemID         int64      `json:"item_id"`
	Status         string     `json:"status"`
	Classification string     `json:"classification"`
	CheckedAt      *time.Time `json:"checked_at,omitempty"`
	Stale          bool       `json:"stale"`
	AccountFailure bool       `json:"account_failure"`
}

// The catalog needs both recent attempts and reusable successes. The ordinary
// LatestItems endpoint deliberately keeps its existing latest-attempt meaning.
// This optional reader allows the durable repository to supply the extra
// projection without forcing administrative history clients to change.
type AccountCapabilityCatalogEvidenceRepository interface {
	EvidenceItems(context.Context, AccountCapabilityFilter) (*AccountCapabilityItemPage, error)
}

type accountCapabilityCatalogSnapshot struct {
	Rows                        []AccountCapabilityCandidate
	Accounts                    []AccountCapabilityScopeAccount
	Groups                      map[string]*Group
	GroupChannels               map[int64]*Channel
	AccountPublicationRevisions map[int64]string
	ExpectedInputRevisions      *CapabilityPublicationInputRevisions
}

type AccountCapabilityCandidateFilter struct {
	FolderIDs  []int64
	AccountIDs []int64
	GroupIDs   []int64
	Search     string
	Status     string
	Page       int
	PageSize   int
}

type AccountCapabilityCandidatePage struct {
	Items    []AccountCapabilityCandidate    `json:"items"`
	Accounts []AccountCapabilityScopeAccount `json:"accounts"`
	Total    int                             `json:"total"`
	Page     int                             `json:"page"`
	PageSize int                             `json:"page_size"`
}

type AccountCapabilityScopeAccount struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	FolderID          int64  `json:"folder_id"`
	Platform          string `json:"platform"`
	Status            string `json:"status"`
	Schedulable       bool   `json:"schedulable"`
	ConfigFingerprint string `json:"config_fingerprint"`
}

type capabilityConsoleLister interface {
	ListAccountsConsole(context.Context, int, int, AccountConsoleFilters) ([]Account, int64, error)
}

type AccountCapabilityCatalogService struct {
	repo     AccountCapabilityRepository
	admin    AdminService
	groups   GroupRepository
	billing  *BillingService
	channels *ChannelService
}

func NewAccountCapabilityCatalogService(repo AccountCapabilityRepository, admin AdminService, groups GroupRepository, billing *BillingService, channels *ChannelService) *AccountCapabilityCatalogService {
	return &AccountCapabilityCatalogService{repo: repo, admin: admin, groups: groups, billing: billing, channels: channels}
}

func (s *AccountCapabilityCatalogService) Candidates(ctx context.Context, filter AccountCapabilityCandidateFilter) (*AccountCapabilityCandidatePage, error) {
	snapshot, err := s.loadCapabilityCatalog(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows, scopeAccounts := snapshot.Rows, snapshot.Accounts
	search := strings.ToLower(strings.TrimSpace(filter.Search))
	groupIDs := make(map[int64]bool, len(filter.GroupIDs))
	for _, id := range filter.GroupIDs {
		groupIDs[id] = true
	}
	filtered := make([]AccountCapabilityCandidate, 0, len(rows))
	for _, row := range rows {
		if len(groupIDs) > 0 && (row.GroupID == nil || !groupIDs[*row.GroupID]) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(row.AccountName+" "+row.PublicModel+" "+row.UpstreamModel+" "+row.GroupName), search) {
			continue
		}
		statusMatches := filter.Status == "" || filter.Status == row.ProbeStatus
		switch filter.Status {
		case "published":
			statusMatches = row.Published
		case "unpublished":
			statusMatches = !row.Published
		case "stale":
			statusMatches = row.Stale
		case "needs_name_confirmation":
			statusMatches = row.NeedsNameConfirmation
		case "untested":
			statusMatches = !row.AlreadyAttempted
		}
		if statusMatches {
			filtered = append(filtered, row)
		}
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 50
	}
	if filter.PageSize > 1000 {
		filter.PageSize = 1000
	}
	start := min((filter.Page-1)*filter.PageSize, len(filtered))
	end := min(start+filter.PageSize, len(filtered))
	return &AccountCapabilityCandidatePage{Items: filtered[start:end], Accounts: scopeAccounts, Total: len(filtered), Page: filter.Page, PageSize: filter.PageSize}, nil
}

// loadCapabilityCatalog is shared by the candidate page and the server-side
// overview/planner. Counts and recommendations must never depend on a UI page.
func (s *AccountCapabilityCatalogService) loadCapabilityCatalog(ctx context.Context, filter AccountCapabilityCandidateFilter) (*accountCapabilityCatalogSnapshot, error) {
	if len(filter.FolderIDs) == 0 || len(filter.FolderIDs) > 1000 || len(filter.AccountIDs) > 1000 || len(filter.GroupIDs) > 50 {
		return nil, ErrAccountCapabilityInvalid
	}
	for _, id := range append(append(append([]int64{}, filter.FolderIDs...), filter.AccountIDs...), filter.GroupIDs...) {
		if id <= 0 {
			return nil, ErrAccountCapabilityInvalid
		}
	}
	lister, ok := s.admin.(capabilityConsoleLister)
	if !ok || s.repo == nil || s.groups == nil {
		return nil, ErrAccountCapabilityInvalid
	}
	accounts := make([]Account, 0)
	for page := 1; ; page++ {
		batch, total, err := lister.ListAccountsConsole(ctx, page, 1000, AccountConsoleFilters{
			FolderIDs: filter.FolderIDs, AccountIDs: filter.AccountIDs, SortBy: "name", SortOrder: "asc",
		})
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, batch...)
		if len(batch) == 0 || int64(len(accounts)) >= total {
			break
		}
		if len(accounts) > 10000 {
			return nil, ErrAccountCapabilityInvalid
		}
	}
	groups := make([]Group, 0)
	for page := 1; ; page++ {
		batch, paging, listErr := s.groups.List(ctx, pagination.PaginationParams{Page: page, PageSize: 1000})
		if listErr != nil {
			return nil, listErr
		}
		groups = append(groups, batch...)
		if len(batch) == 0 || (paging != nil && int64(len(groups)) >= paging.Total) || (paging == nil && len(batch) < 1000) {
			break
		}
	}
	groupByName := make(map[string]*Group)
	for i := range groups {
		if !groups[i].IsExclusive {
			groupByName[strings.ToLower(groups[i].Name)] = &groups[i]
		}
	}
	evidenceReader := s.repo.LatestItems
	if reader, supported := s.repo.(AccountCapabilityCatalogEvidenceRepository); supported {
		evidenceReader = reader.EvidenceItems
	}
	latest := make([]AccountCapabilityItem, 0)
	for _, kind := range []string{AccountCapabilityKindDiscover, AccountCapabilityKindProbe} {
		for page := 1; ; page++ {
			result, listErr := evidenceReader(ctx, AccountCapabilityFilter{Kind: kind, FolderIDs: filter.FolderIDs, AccountIDs: filter.AccountIDs, Page: page, PageSize: 1000})
			if listErr != nil {
				return nil, listErr
			}
			latest = append(latest, result.Items...)
			if len(result.Items) == 0 || int64((result.Page-1)*result.PageSize+len(result.Items)) >= result.Total {
				break
			}
		}
	}
	rows := buildAccountCapabilityCandidates(accounts, latest, groupByName)
	groupChannels := make(map[int64]*Channel)
	configuredChannels := make(map[int64]*Channel)
	channelsByID := make(map[int64]*Channel)
	for _, group := range groupByName {
		if s.channels == nil {
			continue
		}
		if s.channels.repo == nil {
			return nil, ErrAccountCapabilityInvalid
		}
		channelID, channelErr := s.channels.repo.GetChannelIDByGroupID(ctx, group.ID)
		if channelErr != nil {
			return nil, channelErr
		}
		if channelID == 0 {
			continue
		}
		channel := channelsByID[channelID]
		if channel == nil {
			channel, channelErr = s.channels.repo.GetByID(ctx, channelID)
			if channelErr != nil {
				return nil, channelErr
			}
			if channel == nil {
				return nil, ErrAccountCapabilityConflict
			}
			channelsByID[channelID] = channel
		}
		// Management CAS includes inactive saved configuration, while price
		// availability continues to use only an active channel. Read the fresh
		// repository snapshot instead of the gateway's active-only cache.
		configuredChannels[group.ID] = channel
		if channel.IsActive() {
			groupChannels[group.ID] = channel
		}
	}
	applyCapabilityCandidatePricing(rows, s.billing, groupByName, groupChannels)
	scopeAccounts := make([]AccountCapabilityScopeAccount, 0, len(accounts))
	publicationRevisions := make(map[int64]string, len(accounts))
	expectedInputRevisions := &CapabilityPublicationInputRevisions{Accounts: map[int64]string{}, Groups: map[int64]string{}}
	for _, group := range groupByName {
		revision := CapabilityPublicationGroupInputRevision(group, configuredChannels[group.ID])
		if revision == "" {
			return nil, ErrAccountCapabilityInvalid
		}
		expectedInputRevisions.Groups[group.ID] = revision
	}
	for _, account := range accounts {
		if account.ManagementFolderID != nil {
			revision := capabilityAccountPublicationRevision(&account)
			inputRevision := CapabilityPublicationAccountInputRevision(&account)
			if revision == "" || inputRevision == "" {
				return nil, ErrAccountCapabilityInvalid
			}
			publicationRevisions[account.ID] = revision
			expectedInputRevisions.Accounts[account.ID] = inputRevision
			scopeAccounts = append(scopeAccounts, AccountCapabilityScopeAccount{
				ID: account.ID, Name: account.Name, FolderID: *account.ManagementFolderID,
				Platform: account.Platform, Status: account.Status, Schedulable: account.Schedulable,
				ConfigFingerprint: ManagedModelAccountFingerprint(&account),
			})
		}
	}
	return &accountCapabilityCatalogSnapshot{Rows: rows, Accounts: scopeAccounts, Groups: groupByName, GroupChannels: groupChannels,
		AccountPublicationRevisions: publicationRevisions, ExpectedInputRevisions: expectedInputRevisions}, nil
}

func applyCapabilityCandidatePricing(rows []AccountCapabilityCandidate, billing *BillingService, groupByName map[string]*Group, groupChannels map[int64]*Channel) {
	for i := range rows {
		group := groupByName[strings.ToLower(rows[i].GroupName)]
		var channel *Channel
		if group != nil {
			channel = groupChannels[group.ID]
		}
		rows[i].PricingKnown = CapabilityHasIdentifiedPricing(billing, group, channel, rows[i].PublicModel)
		if !rows[i].PricingKnown {
			rows[i].Publishable = false
			rows[i].NotPublishableReasons = append(rows[i].NotPublishableReasons, "pricing_unavailable")
		}
	}
}

func buildAccountCapabilityCandidates(accounts []Account, latest []AccountCapabilityItem, groups map[string]*Group) []AccountCapabilityCandidate {
	evidenceByAccount := make(map[int64][]AccountCapabilityItem)
	probes := make(map[string][]AccountCapabilityItem)
	for _, item := range latest {
		evidenceByAccount[item.AccountID] = append(evidenceByAccount[item.AccountID], item)
		if capabilityIsBasicTextEvidence(item) {
			key := capabilityProbeIdentity(item.AccountID, item.UpstreamModel, item.Protocol)
			probes[key] = append(probes[key], item)
		}
	}
	rows := make([]AccountCapabilityCandidate, 0)
	for i := range accounts {
		account := &accounts[i]
		if account.ManagementFolderID == nil || account.Type != AccountTypeAPIKey {
			continue
		}
		fingerprint, err := AccountCapabilityFingerprint(account)
		if err != nil {
			continue
		}
		privateMappingsCompatible := publicationPreservePrivateMappings(account) == nil
		accountEvidence := evidenceByAccount[account.ID]
		accountFailure := capabilityHasCurrentAccountFailure(account, fingerprint, accountEvidence)
		var discovery *AccountCapabilityItem
		names := make(map[string]bool)
		for n := range accountEvidence {
			item := &accountEvidence[n]
			if item.Kind == AccountCapabilityKindDiscover {
				for _, model := range capabilityDiscoveryModelNames(item.Result) {
					names[model] = true
				}
				if discovery == nil || capabilityItemNewer(*item, *discovery) {
					discovery = item
				}
			} else if item.Kind == AccountCapabilityKindProbe && item.UpstreamModel != "" {
				names[item.UpstreamModel] = true
			}
		}
		discoveryStatus := "not_discovered"
		observed := make(map[string]bool)
		if discovery != nil {
			var result struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(discovery.Result, &result) == nil {
				discoveryStatus = result.Status
			}
			if !capabilityEvidenceCurrent(*discovery, account, fingerprint) {
				discoveryStatus = "stale"
			} else {
				for _, model := range capabilityDiscoveryModelNames(discovery.Result) {
					observed[model] = true
				}
			}
		}
		mapping := account.GetModelMapping()
		configuredTargets := make(map[string]bool)
		for public, upstream := range mapping {
			if public == "" || upstream == "" || strings.ContainsAny(public+upstream, "*?") || IsManagedModelSelector(public) {
				continue
			}
			configuredTargets[upstream] = true
			names[upstream] = true
		}
		publishedHints := make(map[string][]capabilityPublishedModelHint)
		for _, group := range groups {
			for _, route := range group.ManagedModelRoutes.Routes {
				for _, branch := range ManagedModelRouteBranches(route) {
					for _, member := range branch.Accounts {
						if member.AccountID == account.ID && member.UpstreamModel != "" {
							names[member.UpstreamModel] = true
							publishedHints[member.UpstreamModel] = append(publishedHints[member.UpstreamModel], capabilityPublishedModelHint{Group: group, PublicModel: route.PublicModel, Aliases: route.Aliases})
						}
					}
				}
			}
		}
		for upstream := range names {
			if strings.TrimSpace(upstream) == "" || IsManagedModelSelector(upstream) {
				continue
			}
			protocols := capabilityCandidateProtocols(account, upstream, accountEvidence)
			start := len(rows)
			for _, model := range capabilityCandidateModels(account, upstream, publishedHints[upstream]) {
				for protocolPriority, protocol := range protocols {
					row := AccountCapabilityCandidate{
						AccountID: account.ID, AccountName: account.Name, FolderID: *account.ManagementFolderID,
						AccountPlatform: account.Platform, ConfigFingerprint: fingerprint, PublicModel: model.ID,
						UpstreamModel: upstream, Protocol: protocol, ProbeProtocolPriority: protocolPriority, Profile: AccountCapabilityProfileText,
						Aliases: append([]string{}, model.Aliases...), Tier: model.Tier, GroupName: model.Group,
						Mainstream: model.Group != capabilityUnclassifiedGroup, Recognized: model.Recognized,
						Recommended: model.Recommended, NeedsNameConfirmation: !model.Recognized,
						Discovered: observed[upstream], Configured: configuredTargets[upstream],
						DiscoveryStatus: discoveryStatus, ProbeStatus: "untested", Schedulable: account.Schedulable,
						Warnings: []string{}, NotPublishableReasons: []string{},
					}
					if !row.Discovered {
						row.Warnings = append(row.Warnings, "configured_hint_not_live_discovery")
					}
					capabilityApplyCandidateEvidence(&row, account, probes[capabilityProbeIdentity(account.ID, upstream, protocol)], accountEvidence)
					if group := groups[strings.ToLower(row.GroupName)]; group != nil {
						row.GroupID = &group.ID
						row.Published = capabilityCandidatePublished(account, group, &row)
						row.RoutingReady = capabilityCandidateRoutingReady(account, group, &row)
					}
					if row.NeedsNameConfirmation {
						row.NotPublishableReasons = append(row.NotPublishableReasons, "needs_name_confirmation")
					}
					if !privateMappingsCompatible {
						row.NotPublishableReasons = append(row.NotPublishableReasons, "private_mapping_requires_review")
					}
					if accountFailure {
						row.NotPublishableReasons = append(row.NotPublishableReasons, "account_failure")
					}
					if !row.LastSuccessReusable {
						row.NotPublishableReasons = append(row.NotPublishableReasons, "no_current_inference_evidence")
					}
					if len(CapabilityIngressEndpoints(account, protocol)) == 0 || !ManagedModelBranchProtocolSupported(account.Platform, protocol) {
						row.NotPublishableReasons = append(row.NotPublishableReasons, "unsupported_public_protocol")
					}
					row.Publishable = len(row.NotPublishableReasons) == 0
					sum := sha256.Sum256([]byte(capabilityProbeIdentity(account.ID, upstream, protocol) + "|" + row.GroupName + "|" + row.PublicModel))
					row.CandidateID = hex.EncodeToString(sum[:])
					rows = append(rows, row)
				}
			}
			compatibleSuccess := capabilityHasCompatibleHistoricalSuccess(account, fingerprint, upstream, accountEvidence)
			pending, attemptedProtocols := capabilityTargetProbeHistory(account, fingerprint, upstream, accountEvidence)
			for n := start; n < len(rows); n++ {
				rows[n].HasCompatibleSuccess = compatibleSuccess
				rows[n].HasPendingProbe, rows[n].AttemptedProtocolCount = pending, attemptedProtocols
				if pending {
					rows[n].NotPublishableReasons = append(rows[n].NotPublishableReasons, "check_in_progress")
				}
				if attemptedProtocols >= 2 && !compatibleSuccess {
					rows[n].NotPublishableReasons = append(rows[n].NotPublishableReasons, "compatible_probe_limit_reached")
				}
				rows[n].ProbeEligible = !rows[n].AlreadyAttempted && !compatibleSuccess && !pending && !accountFailure && attemptedProtocols < 2 && privateMappingsCompatible && !rows[n].NeedsNameConfirmation && ManagedModelBranchProtocolSupported(account.Platform, rows[n].Protocol) && len(CapabilityIngressEndpoints(account, rows[n].Protocol)) > 0
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.GroupName != b.GroupName {
			return a.GroupName < b.GroupName
		}
		if a.PublicModel != b.PublicModel {
			return a.PublicModel < b.PublicModel
		}
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		if a.UpstreamModel != b.UpstreamModel {
			return a.UpstreamModel < b.UpstreamModel
		}
		return a.Protocol < b.Protocol
	})
	return rows
}

func capabilityProbeIdentity(accountID int64, upstream, protocol string) string {
	return fmt.Sprintf("%d|%s|%s", accountID, upstream, protocol)
}

func capabilityItemNewer(a, b AccountCapabilityItem) bool {
	if a.FinishedAt != nil && b.FinishedAt != nil && !a.FinishedAt.Equal(*b.FinishedAt) {
		return a.FinishedAt.After(*b.FinishedAt)
	}
	return a.ID > b.ID
}

func capabilityTargetTier(model string) string {
	lower := strings.ToLower(model)
	if strings.HasSuffix(lower, "-ssvip") {
		return "ssvip"
	}
	if strings.HasSuffix(lower, "-vip") {
		return "vip"
	}
	return "standard"
}

func resolveCapabilityLaunchModel(model string) (capabilityModelDefinition, string, bool) {
	tier := capabilityTargetTier(model)
	// A commercial suffix is a concrete upstream product name, not evidence
	// that it is interchangeable with the unsuffixed model. Account mappings
	// may explicitly establish that relationship in resolveCapabilityAccountModel.
	if tier != "standard" {
		return capabilityModelDefinition{}, tier, false
	}
	name := model
	if prefix, tail, found := strings.Cut(name, "/"); found {
		switch strings.ToLower(prefix) {
		case "openai", "anthropic", "google", "xai", "deepseek-ai", "qwen", "moonshotai", "minimax", "z-ai", "zhipuai":
			name = tail
		}
	}
	for _, definition := range capabilityLaunchModels {
		if strings.EqualFold(name, definition.ID) {
			return definition, tier, true
		}
		for _, alias := range definition.Aliases {
			if strings.EqualFold(name, alias) {
				return definition, tier, true
			}
		}
	}
	definition, recognized := capabilityResolveRecognizedFamily(name)
	return definition, tier, recognized
}

func resolveCapabilityAccountModel(account *Account, upstream string) (capabilityModelDefinition, string, bool) {
	definition, tier, ok := resolveCapabilityLaunchModel(upstream)
	if ok {
		return definition, tier, true
	}
	if account == nil {
		return capabilityModelDefinition{}, tier, false
	}
	tail := upstream[strings.LastIndex(upstream, "/")+1:]
	// Only documented concrete suffix forms are considered, and stripping
	// one here never establishes identity by itself. The exact mapped target
	// and its base version must both agree with a recognized public name.
	for _, suffix := range []string{"-ssvip", "-vip", "-cc"} {
		if strings.HasSuffix(strings.ToLower(tail), suffix) {
			tail = tail[:len(tail)-len(suffix)]
			break
		}
	}
	target, _, targetOK := resolveCapabilityLaunchModel(tail)
	if !targetOK {
		return capabilityModelDefinition{}, tier, false
	}
	for public, actual := range account.GetModelMapping() {
		if actual != upstream || strings.HasPrefix(public, "s2pub-") {
			continue
		}
		hinted, _, hintOK := resolveCapabilityLaunchModel(public)
		if hintOK && strings.EqualFold(hinted.ID, target.ID) {
			return target, tier, true
		}
	}
	return capabilityModelDefinition{}, tier, false
}

// CapabilityCandidateMatches is also used by publication. An administrator may
// choose evidence, but cannot turn a successful different model into a popular
// public name merely by editing a request body.
func CapabilityCandidateMatches(account *Account, upstream, public string, aliases []string, tier string) bool {
	definition, actualTier, ok := resolveCapabilityAccountModel(account, upstream)
	if !ok || public != definition.ID {
		return false
	}
	if definition.Group == "gpt" {
		switch tier {
		case "", "standard":
			if actualTier != "standard" {
				return false
			}
		case "vip":
			if actualTier != "vip" && actualTier != "ssvip" {
				return false
			}
		case "ssvip":
			if actualTier != "ssvip" {
				return false
			}
		default:
			return false
		}
	} else if tier != "" && tier != "standard" {
		// Only GPT has separate public products. Other vendors retain their
		// actual upstream suffix without inventing an additional VIP product.
		return false
	}
	for _, alias := range aliases {
		allowed := alias == definition.ID
		for _, known := range definition.Aliases {
			if alias == known {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func capabilityAccountProtocols(account *Account) []string {
	if account.IsAnthropic() {
		return []string{AccountCapabilityProtocolMessages}
	}
	if account.IsCNProvider() {
		switch account.GetAPIProtocol() {
		case APIProtocolAnthropic:
			return []string{AccountCapabilityProtocolMessages}
		case APIProtocolChatCompletions:
			return []string{AccountCapabilityProtocolChatCompletions}
		case APIProtocolResponses:
			return []string{AccountCapabilityProtocolResponses}
		case APIProtocolAdaptive:
			return []string{AccountCapabilityProtocolResponses, AccountCapabilityProtocolMessages, AccountCapabilityProtocolChatCompletions}
		}
	}
	if account.IsOpenAI() || account.IsGrok() {
		protocols := []string{}
		if openai_compat.ShouldUseResponsesAPI(account.Extra) {
			protocols = append(protocols, AccountCapabilityProtocolResponses, AccountCapabilityProtocolChatCompletions)
		} else {
			protocols = append(protocols, AccountCapabilityProtocolChatCompletions, AccountCapabilityProtocolResponses)
		}
		if account.IsOpenAIResponsesWebSocketV2Enabled() {
			protocols = append(protocols, "responses_websocket")
		}
		return protocols
	}
	return nil
}

func capabilityCandidatePublished(account *Account, group *Group, row *AccountCapabilityCandidate) bool {
	return capabilityCandidateRouteState(account, group, row, false)
}

func capabilityCandidateRoutingReady(account *Account, group *Group, row *AccountCapabilityCandidate) bool {
	return capabilityCandidateRouteState(account, group, row, true)
}

func capabilityCandidateRouteState(account *Account, group *Group, row *AccountCapabilityCandidate, requireReady bool) bool {
	if account == nil || group == nil || row == nil || !group.ManagedModelRoutes.Enabled {
		return false
	}
	if requireReady {
		if group.Status != StatusActive || !account.IsSchedulable() {
			return false
		}
		bound := false
		for _, id := range account.GroupIDs {
			if id == group.ID {
				bound = true
				break
			}
		}
		if !bound {
			return false
		}
	}
	for _, route := range group.ManagedModelRoutes.Routes {
		if route.PublicModel != row.PublicModel {
			continue
		}
		for _, branch := range ManagedModelRouteBranches(route) {
			if branch.UpstreamProtocol != "" && branch.UpstreamProtocol != row.Protocol {
				continue
			}
			if branch.UpstreamProtocol == "" && row.Protocol != AccountCapabilityProtocolResponsesWebSocket {
				// Legacy routes did not store a protocol. Their forwarding mode
				// was the account's configured mode, not every newly offered
				// alternative that happens to expose the same client endpoints.
				protocols := capabilityAccountProtocols(account)
				if len(protocols) > 0 && row.Protocol != protocols[0] {
					continue
				}
			}
			for _, member := range branch.Accounts {
				if member.AccountID != account.ID || member.UpstreamModel != row.UpstreamModel {
					continue
				}
				if requireReady && (member.AccountFingerprint != row.ConfigFingerprint || account.GetModelMapping()[branch.Selector] != row.UpstreamModel || branch.TargetPlatform != account.Platform) {
					continue
				}
				// A v2 branch stores its exact upstream protocol. Its configured
				// presence stays visible after account flags or credentials change.
				if !requireReady && branch.UpstreamProtocol != "" {
					return true
				}
				for _, endpoint := range CapabilityIngressEndpoints(account, row.Protocol) {
					if !managedModelHasEndpoint(route.Endpoints, endpoint) || !managedModelHasEndpoint(branch.Endpoints, endpoint) || !managedModelHasEndpoint(member.Endpoints, endpoint) {
						continue
					}
					if requireReady {
						request, err := ResolveManagedModelRoute(group, row.PublicModel, endpoint)
						if err != nil || request == nil || !ManagedModelAccountAllowed(WithManagedModelRequest(context.Background(), request), account, branch.Selector) {
							continue
						}
					}
					return true
				}
			}
		}
	}
	return false
}
