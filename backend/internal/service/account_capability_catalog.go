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

// The curated launch catalogue is an editorial filter, never availability
// evidence. Discovery and real probes remain independent facts.
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
	CandidateID           string     `json:"candidate_id"`
	AccountID             int64      `json:"account_id"`
	AccountName           string     `json:"account_name"`
	FolderID              int64      `json:"folder_id"`
	AccountPlatform       string     `json:"account_platform"`
	ConfigFingerprint     string     `json:"config_fingerprint"`
	PublicModel           string     `json:"public_model"`
	UpstreamModel         string     `json:"upstream_model"`
	Protocol              string     `json:"protocol"`
	Profile               string     `json:"profile"`
	Aliases               []string   `json:"aliases"`
	Tier                  string     `json:"tier"`
	GroupID               *int64     `json:"group_id,omitempty"`
	GroupName             string     `json:"group_name"`
	Mainstream            bool       `json:"mainstream"`
	Discovered            bool       `json:"discovered"`
	Configured            bool       `json:"configured"`
	Published             bool       `json:"published"`
	PricingKnown          bool       `json:"pricing_known"`
	Publishable           bool       `json:"publishable"`
	NotPublishableReasons []string   `json:"not_publishable_reasons"`
	DiscoveryStatus       string     `json:"discovery_status"`
	LatestProbeItemID     *int64     `json:"latest_probe_item_id,omitempty"`
	ProbeStatus           string     `json:"probe_status"`
	CheckedAt             *time.Time `json:"checked_at,omitempty"`
	Stale                 bool       `json:"stale"`
	Schedulable           bool       `json:"schedulable"`
	Warnings              []string   `json:"warnings"`
}

type AccountCapabilityCandidateFilter struct {
	FolderIDs  []int64
	AccountIDs []int64
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
	if len(filter.FolderIDs) == 0 || len(filter.FolderIDs) > 1000 || len(filter.AccountIDs) > 1000 {
		return nil, ErrAccountCapabilityInvalid
	}
	for _, id := range append(append([]int64{}, filter.FolderIDs...), filter.AccountIDs...) {
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
	groups, _, err := s.groups.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	groupByName := make(map[string]*Group)
	for i := range groups {
		if !groups[i].IsExclusive {
			groupByName[strings.ToLower(groups[i].Name)] = &groups[i]
		}
	}
	latest := make([]AccountCapabilityItem, 0)
	for _, kind := range []string{AccountCapabilityKindDiscover, AccountCapabilityKindProbe} {
		for page := 1; ; page++ {
			result, listErr := s.repo.LatestItems(ctx, AccountCapabilityFilter{Kind: kind, FolderIDs: filter.FolderIDs, Page: page, PageSize: 1000})
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
	for _, group := range groupByName {
		if s.channels == nil {
			continue
		}
		channel, channelErr := s.channels.GetChannelForGroup(ctx, group.ID)
		if channelErr != nil {
			return nil, channelErr
		}
		groupChannels[group.ID] = channel
	}
	applyCapabilityCandidatePricing(rows, s.billing, groupByName, groupChannels)
	scopeAccounts := make([]AccountCapabilityScopeAccount, 0, len(accounts))
	for _, account := range accounts {
		if account.ManagementFolderID != nil {
			scopeAccounts = append(scopeAccounts, AccountCapabilityScopeAccount{
				ID: account.ID, Name: account.Name, FolderID: *account.ManagementFolderID,
				Platform: account.Platform, Status: account.Status, Schedulable: account.Schedulable,
				ConfigFingerprint: ManagedModelAccountFingerprint(&account),
			})
		}
	}
	search := strings.ToLower(strings.TrimSpace(filter.Search))
	filtered := make([]AccountCapabilityCandidate, 0, len(rows))
	for _, row := range rows {
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
		}
		if !statusMatches {
			continue
		}
		filtered = append(filtered, row)
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
	start := (filter.Page - 1) * filter.PageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := min(start+filter.PageSize, len(filtered))
	return &AccountCapabilityCandidatePage{Items: filtered[start:end], Accounts: scopeAccounts, Total: len(filtered), Page: filter.Page, PageSize: filter.PageSize}, nil
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
	discoveries := make(map[int64]AccountCapabilityItem)
	probes := make(map[string]AccountCapabilityItem)
	for _, item := range latest {
		if item.Kind == AccountCapabilityKindDiscover {
			prior, exists := discoveries[item.AccountID]
			if !exists || capabilityItemNewer(item, prior) {
				discoveries[item.AccountID] = item
			}
		} else if item.Profile == AccountCapabilityProfileText {
			key := capabilityProbeIdentity(item.AccountID, item.UpstreamModel, item.Protocol)
			prior, exists := probes[key]
			if !exists || capabilityItemNewer(item, prior) {
				probes[key] = item
			}
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
		discovery, hasDiscovery := discoveries[account.ID]
		discoveryStatus := "not_discovered"
		currentDiscovery := hasDiscovery && discovery.ConfigFingerprint == fingerprint && discovery.FolderID == *account.ManagementFolderID
		observed := make(map[string]bool)
		if hasDiscovery {
			var result struct {
				Status string            `json:"status"`
				Models []json.RawMessage `json:"models"`
			}
			if json.Unmarshal(discovery.Result, &result) == nil {
				discoveryStatus = result.Status
				for _, raw := range result.Models {
					var model struct {
						ID string `json:"id"`
					}
					if json.Unmarshal(raw, &model) == nil && model.ID != "" {
						observed[model.ID] = currentDiscovery
					}
				}
			}
			if !currentDiscovery {
				discoveryStatus = "stale"
			}
		}
		mapping := account.GetModelMapping()
		configuredTargets := make(map[string]bool)
		names := make(map[string]string)
		for upstream := range observed {
			names[upstream] = upstream
		}
		for public, upstream := range mapping {
			if public == "" || upstream == "" || strings.ContainsAny(public, "*?") || strings.HasPrefix(public, "s2pub-") {
				continue
			}
			configuredTargets[upstream] = true
			if _, exists := names[upstream]; !exists {
				names[upstream] = public
			}
		}
		// A failed/empty fresh discovery must not erase previously tested or
		// still-published targets. They remain explicitly non-discovered clues.
		for _, item := range latest {
			if item.AccountID == account.ID && item.Kind == AccountCapabilityKindProbe && item.UpstreamModel != "" {
				if _, exists := names[item.UpstreamModel]; !exists {
					names[item.UpstreamModel] = item.UpstreamModel
				}
			}
		}
		for _, group := range groups {
			if !group.ManagedModelRoutes.Enabled {
				continue
			}
			for _, route := range group.ManagedModelRoutes.Routes {
				for _, member := range route.Accounts {
					if member.AccountID == account.ID && member.UpstreamModel != "" {
						names[member.UpstreamModel] = route.PublicModel
					}
				}
			}
		}
		for upstream := range names {
			definition, tier, recognized := resolveCapabilityAccountModel(account, upstream)
			if !recognized {
				continue
			}
			groupName := definition.Group
			if definition.Group == "gpt" && tier != "standard" {
				groupName = "gpt-vip"
			}
			for _, protocol := range capabilityAccountProtocols(account) {
				row := AccountCapabilityCandidate{
					AccountID: account.ID, AccountName: account.Name, FolderID: *account.ManagementFolderID,
					AccountPlatform: account.Platform, ConfigFingerprint: fingerprint, PublicModel: definition.ID,
					UpstreamModel: upstream, Protocol: protocol, Profile: AccountCapabilityProfileText,
					Aliases: append([]string{}, definition.Aliases...), Tier: tier, GroupName: groupName,
					Mainstream: true, Discovered: observed[upstream], Configured: configuredTargets[upstream],
					DiscoveryStatus: discoveryStatus, ProbeStatus: "untested", Schedulable: account.Schedulable, Warnings: []string{},
					NotPublishableReasons: []string{},
				}
				if !row.Discovered {
					row.Warnings = append(row.Warnings, "configured_hint_not_live_discovery")
				}
				if probe, exists := probes[capabilityProbeIdentity(account.ID, upstream, protocol)]; exists {
					var result AccountCapabilityProbeResult
					if json.Unmarshal(probe.Result, &result) == nil {
						row.LatestProbeItemID = &probe.ID
						row.ProbeStatus = result.Status
						row.CheckedAt = probe.FinishedAt
						row.Stale = probe.ConfigFingerprint != fingerprint || probe.FolderID != *account.ManagementFolderID
						if row.Stale {
							row.ProbeStatus = "stale"
							row.NotPublishableReasons = append(row.NotPublishableReasons, "configuration_changed")
						}
						if probe.FinishedAt == nil || probe.FinishedAt.Before(time.Now().Add(-24*time.Hour)) {
							row.NotPublishableReasons = append(row.NotPublishableReasons, "evidence_expired")
						}
						if probe.PublicationSuperseded {
							row.NotPublishableReasons = append(row.NotPublishableReasons, "evidence_superseded")
						}
					}
				}
				if group := groups[strings.ToLower(groupName)]; group != nil {
					row.GroupID = &group.ID
					row.Published = capabilityCandidatePublished(account, group, &row)
				}
				if row.ProbeStatus != "alive" {
					row.NotPublishableReasons = append(row.NotPublishableReasons, "no_current_inference_evidence")
				}
				if len(CapabilityIngressEndpoints(account, protocol)) == 0 {
					row.NotPublishableReasons = append(row.NotPublishableReasons, "unsupported_public_protocol")
				}
				row.Publishable = len(row.NotPublishableReasons) == 0
				sum := sha256.Sum256([]byte(capabilityProbeIdentity(account.ID, upstream, protocol) + "|" + groupName + "|" + definition.ID))
				row.CandidateID = hex.EncodeToString(sum[:])
				rows = append(rows, row)
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
	name := model
	if tier != "standard" {
		name = name[:len(name)-len(tier)-1]
	}
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
	return capabilityModelDefinition{}, tier, false
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
	target, targetTier, targetOK := resolveCapabilityLaunchModel(tail)
	if !targetOK {
		return capabilityModelDefinition{}, tier, false
	}
	for public, actual := range account.GetModelMapping() {
		if actual != upstream || strings.HasPrefix(public, "s2pub-") {
			continue
		}
		hinted, _, hintOK := resolveCapabilityLaunchModel(public)
		if hintOK && hinted.ID == target.ID {
			return target, targetTier, true
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
			protocols = append(protocols, AccountCapabilityProtocolResponses)
		} else {
			protocols = append(protocols, AccountCapabilityProtocolChatCompletions)
		}
		if account.IsOpenAIResponsesWebSocketV2Enabled() {
			protocols = append(protocols, "responses_websocket")
		}
		return protocols
	}
	return nil
}

func capabilityCandidatePublished(account *Account, group *Group, row *AccountCapabilityCandidate) bool {
	if !group.ManagedModelRoutes.Enabled || group.Status != StatusActive || !account.IsSchedulable() {
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
	for _, route := range group.ManagedModelRoutes.Routes {
		if route.PublicModel != row.PublicModel {
			continue
		}
		for _, member := range route.Accounts {
			if member.AccountID == account.ID && member.UpstreamModel == row.UpstreamModel &&
				member.AccountFingerprint == row.ConfigFingerprint && account.GetModelMapping()[route.Selector] == row.UpstreamModel {
				if route.TargetPlatform != account.Platform {
					continue
				}
				for _, endpoint := range CapabilityIngressEndpoints(account, row.Protocol) {
					if managedModelHasEndpoint(route.Endpoints, endpoint) && managedModelHasEndpoint(member.Endpoints, endpoint) {
						return true
					}
				}
			}
		}
	}
	return false
}
