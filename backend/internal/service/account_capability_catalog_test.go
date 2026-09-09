package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func capabilityCatalogTestAccount(id int64, mapping map[string]any) Account {
	folder := int64(7)
	return Account{
		ID: id, Name: "catalog fixture", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		ManagementFolderID: &folder, Status: StatusActive, Schedulable: false,
		Credentials: map[string]any{"api_key": "fixture-not-live", "base_url": "https://catalog.example", "model_mapping": mapping},
		Extra:       map[string]any{"openai_responses_supported": true},
	}
}

func capabilityCatalogFindRow(t *testing.T, rows []AccountCapabilityCandidate, upstream, protocol string) AccountCapabilityCandidate {
	t.Helper()
	for _, row := range rows {
		if row.UpstreamModel == upstream && row.Protocol == protocol {
			return row
		}
	}
	t.Fatalf("missing candidate for %q via %q", upstream, protocol)
	return AccountCapabilityCandidate{}
}

func TestAccountCapabilityCatalogSeparatesDiscoveryConfigurationAndVIP(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"gpt-6-astra": "gpt-6-astra-ssvip", "gpt-image-2": "gpt-image-2"})
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]any{"status": "discovered", "models": []map[string]string{
		{"id": "gpt-6-astra"}, {"id": "gpt-6-astra-ssvip"}, {"id": "gpt-image-2"}, {"id": "gpt-3.5-turbo"},
	}})
	require.NoError(t, err)
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{
		{ID: 1, Kind: AccountCapabilityKindDiscover, AccountID: 10, FolderID: 7, ConfigFingerprint: fingerprint, Result: raw},
	}, nil)
	require.Len(t, rows, 8)
	byGroup := map[string]AccountCapabilityCandidate{}
	for _, row := range rows {
		if row.PublicModel == "gpt-6-astra" && row.Protocol == "responses" {
			byGroup[row.GroupName] = row
		}
	}
	require.Equal(t, "gpt-6-astra", byGroup["gpt"].UpstreamModel)
	require.False(t, byGroup["gpt"].Configured)
	require.True(t, byGroup["gpt"].Discovered)
	require.Equal(t, "ssvip", byGroup["gpt-vip"].Tier)
	require.True(t, byGroup["gpt-vip"].Configured)
	require.Equal(t, "untested", byGroup["gpt-vip"].ProbeStatus)
	require.False(t, capabilityCatalogFindRow(t, rows, "gpt-3.5-turbo", "responses").Recommended)
	require.True(t, capabilityCatalogFindRow(t, rows, "gpt-image-2", "responses").NeedsNameConfirmation)
}

func TestAccountCapabilityCatalogDoesNotReplaceGenerationOrLoseNamespace(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{
		"gpt-6-astra":   "gpt-3.5-turbo",
		"gpt-5.6-luna":  "cx/gpt-5.6-luna",
		"s2pub-g4-mold": "gpt-6-astra",
	})
	rows := buildAccountCapabilityCandidates([]Account{account}, nil, nil)
	require.Len(t, rows, 4)
	luna := capabilityCatalogFindRow(t, rows, "cx/gpt-5.6-luna", "responses")
	require.Equal(t, "gpt-5.6-luna", luna.PublicModel)
	require.False(t, luna.Discovered)
	require.Contains(t, luna.Warnings, "configured_hint_not_live_discovery")
	old := capabilityCatalogFindRow(t, rows, "gpt-3.5-turbo", "responses")
	require.Equal(t, "gpt-3.5-turbo", old.PublicModel, "a private alias cannot upgrade a model generation")
}

func TestAccountCapabilityCatalogLatestFailureAndStaleEvidenceAreNotAlive(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"glm-5.3": "glm-5.3"})
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	now := time.Now()
	latest := []AccountCapabilityItem{
		{ID: 4, Kind: AccountCapabilityKindProbe, AccountID: 10, FolderID: 7, UpstreamModel: "glm-5.3", Protocol: "responses", Profile: "text", ConfigFingerprint: fingerprint, Result: json.RawMessage(`{"status":"alive"}`), FinishedAt: &now},
		{ID: 5, Kind: AccountCapabilityKindProbe, AccountID: 10, FolderID: 7, UpstreamModel: "glm-5.3", Protocol: "responses", Profile: "text", ConfigFingerprint: fingerprint, Result: json.RawMessage(`{"status":"failed"}`), FinishedAt: &now},
	}
	rows := buildAccountCapabilityCandidates([]Account{account}, latest, nil)
	require.Len(t, rows, 2)
	row := capabilityCatalogFindRow(t, rows, "glm-5.3", "responses")
	require.Equal(t, "failed", row.ProbeStatus)
	require.Equal(t, int64(5), *row.LatestProbeItemID)
	account.Credentials["api_key"] = "changed-fixture"
	rows = buildAccountCapabilityCandidates([]Account{account}, latest, nil)
	row = capabilityCatalogFindRow(t, rows, "glm-5.3", "responses")
	require.Equal(t, "stale", row.ProbeStatus)
	require.True(t, row.Stale)
	require.False(t, row.LastSuccessReusable)
}

func TestAccountCapabilityCatalogKeepsGrok46AndProtocolScope(t *testing.T) {
	oldDefinition, _, old := resolveCapabilityLaunchModel("grok-4.5")
	require.True(t, old)
	require.Equal(t, "grok", oldDefinition.Group, "an older Grok remains visible outside the 4.6-only recommendation")
	definition, _, ok := resolveCapabilityLaunchModel("grok-4.6")
	require.True(t, ok)
	require.Equal(t, "grok(仅4.6)", definition.Group)
	account := capabilityCatalogTestAccount(10, nil)
	account.Extra["openai_responses_supported"] = false
	require.Equal(t, []string{"chat_completions", "responses"}, capabilityAccountProtocols(&account))
	account.Platform = PlatformAnthropic
	require.Equal(t, []string{"messages"}, capabilityAccountProtocols(&account))
}

func TestAccountCapabilityPublicationModelIdentityUsesTrustedCatalogue(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"gpt-6-astra": "deepseek-v4-pro", "gpt-5.6-luna": "cx/gpt-5.6-luna"})
	require.False(t, CapabilityCandidateMatches(&account, "deepseek-v4-pro", "gpt-6-astra", nil, "standard"))
	require.False(t, CapabilityCandidateMatches(&account, "gpt-6-astra-ssvip", "gpt-6-astra", nil, "standard"))
	require.False(t, CapabilityCandidateMatches(&account, "gpt-6-astra-ssvip", "gpt-6-astra", nil, "vip"), "a suffix alone is not equivalence evidence")
	account.Credentials["model_mapping"].(map[string]any)["gpt-6-astra"] = "gpt-6-astra-ssvip"
	require.True(t, CapabilityCandidateMatches(&account, "gpt-6-astra-ssvip", "gpt-6-astra", nil, "vip"))
	require.True(t, CapabilityCandidateMatches(&account, "cx/gpt-5.6-luna", "gpt-5.6-luna", nil, "standard"))
	require.False(t, CapabilityCandidateMatches(&account, "gpt-5.6-sol", "gpt-5.6-sol", []string{"gpt-6-astra"}, "standard"))
}

func TestAccountCapabilityTierIsolationOnlyAppliesToGPTProducts(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{
		"gemini-3.8-flash": "gemini-3.8-flash-ssvip", "grok-4.6": "grok-4.6-vip", "gpt-6-astra": "gpt-6-astra-ssvip",
	})
	require.True(t, CapabilityCandidateMatches(&account, "gemini-3.8-flash", "gemini-3.8-flash", nil, "standard"))
	require.True(t, CapabilityCandidateMatches(&account, "gemini-3.8-flash-ssvip", "gemini-3.8-flash", nil, "standard"))
	require.True(t, CapabilityCandidateMatches(&account, "grok-4.6-vip", "grok-4.6", nil, "standard"))
	require.False(t, CapabilityCandidateMatches(&account, "gemini-3.8-flash-ssvip", "gemini-3.8-flash", nil, "vip"))
	require.False(t, CapabilityCandidateMatches(&account, "gpt-6-astra-ssvip", "gpt-6-astra", nil, "standard"))
	require.False(t, CapabilityCandidateMatches(&account, "gpt-6-astra", "gpt-6-astra", nil, "vip"))
	require.True(t, CapabilityCandidateMatches(&account, "gpt-6-astra-ssvip", "gpt-6-astra", nil, "vip"))
}

func TestAccountCapabilityCatalogPricingDoesNotInventOrRevive(t *testing.T) {
	inputPrice, outputPrice := 0.000001, 0.000002
	group := &Group{ID: 23, ModelPricing: []ChannelModelPricing{{
		Models: []string{"gpt-6-astra"}, InputPrice: &inputPrice, OutputPrice: &outputPrice,
	}}}
	rows := []AccountCapabilityCandidate{
		{PublicModel: "qwen3.8-max", GroupName: "Qwen", Publishable: true, NotPublishableReasons: []string{}},
		{PublicModel: "gpt-6-astra", GroupName: "gpt", Publishable: true, NotPublishableReasons: []string{}},
		{PublicModel: "gpt-6-astra", GroupName: "gpt", Publishable: false, NotPublishableReasons: []string{"evidence_superseded"}},
	}
	applyCapabilityCandidatePricing(rows, nil, map[string]*Group{"gpt": group}, nil)
	require.False(t, rows[0].PricingKnown)
	require.False(t, rows[0].Publishable)
	require.Contains(t, rows[0].NotPublishableReasons, "pricing_unavailable")
	require.True(t, rows[1].PricingKnown)
	require.True(t, rows[1].Publishable)
	require.True(t, rows[2].PricingKnown)
	require.False(t, rows[2].Publishable)
	require.Equal(t, []string{"evidence_superseded"}, rows[2].NotPublishableReasons)
}

func TestAccountCapabilityCatalogPreservesDistinctVIPAlternatives(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{
		"gpt-5.6-sol": "gpt-5.6-sol-vip", "gpt-5.6": "gpt-5.6-sol-ssvip",
	})
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	now := time.Now()
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{
		{ID: 1, Kind: "probe", AccountID: 10, FolderID: 7, UpstreamModel: "gpt-5.6-sol-vip", Protocol: "responses", Profile: "text", ConfigFingerprint: fingerprint, Result: json.RawMessage(`{"status":"alive"}`), FinishedAt: &now},
		{ID: 2, Kind: "probe", AccountID: 10, FolderID: 7, UpstreamModel: "gpt-5.6-sol-ssvip", Protocol: "responses", Profile: "text", ConfigFingerprint: fingerprint, Result: json.RawMessage(`{"status":"failed"}`), FinishedAt: &now},
	}, nil)
	require.Len(t, rows, 4)
	targets := map[string]AccountCapabilityCandidate{}
	for _, row := range rows {
		if row.Protocol == "responses" {
			targets[row.UpstreamModel] = row
		}
	}
	require.True(t, targets["gpt-5.6-sol-vip"].Publishable)
	require.False(t, targets["gpt-5.6-sol-ssvip"].Publishable)
	require.NotEqual(t, rows[0].CandidateID, rows[1].CandidateID)
}

func TestAccountCapabilityCatalogKeepsPublishedTargetAfterEmptyDiscovery(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"gpt-image-2": "gpt-image-2"})
	account.GroupIDs, account.Schedulable = []int64{23}, true
	selector := ManagedModelSelector(23, "gpt-6-astra")
	mapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping[selector] = "gpt-6-astra-ssvip"
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	group := &Group{ID: 23, Name: "gpt-vip", Status: StatusActive, Platform: PlatformOpenAI,
		ManagedModelRoutes: ManagedModelRoutesConfig{Enabled: true, Version: 1, Routes: []ManagedModelRoute{{
			PublicModel: "gpt-6-astra", Selector: selector, TargetPlatform: PlatformOpenAI, Endpoints: []string{"responses"},
			Accounts: []ManagedModelRouteAccount{{AccountID: 10, UpstreamModel: "gpt-6-astra-ssvip", AccountFingerprint: fingerprint, Endpoints: []string{"responses"}}},
		}}}}
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{
		{ID: 2, Kind: "discover", AccountID: 10, FolderID: 7, ConfigFingerprint: fingerprint, Result: json.RawMessage(`{"status":"empty","models":[]}`)},
	}, map[string]*Group{"gpt-vip": group})
	require.Len(t, rows, 6, "both the exact unconfirmed target and its already-saved public name remain visible")
	row := capabilityCatalogFindRow(t, rows, "gpt-6-astra-ssvip", "responses")
	require.False(t, row.Discovered)
	require.True(t, row.Published)
	require.False(t, capabilityCatalogFindRow(t, rows, "gpt-6-astra-ssvip", "chat_completions").Published)
}

func TestAccountCapabilityCatalogPublishedDoesNotInferWebSocket(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"gpt-6-astra": "gpt-6-astra"})
	account.GroupIDs, account.Schedulable = []int64{23}, true
	selector := ManagedModelSelector(23, "gpt-6-astra")
	mapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping[selector] = "gpt-6-astra"
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	group := &Group{ID: 23, Status: StatusActive, Platform: PlatformOpenAI, ManagedModelRoutes: ManagedModelRoutesConfig{Enabled: true,
		Routes: []ManagedModelRoute{{PublicModel: "gpt-6-astra", Selector: selector, TargetPlatform: PlatformOpenAI, Endpoints: []string{"responses"},
			Accounts: []ManagedModelRouteAccount{{AccountID: 10, UpstreamModel: "gpt-6-astra", AccountFingerprint: fingerprint, Endpoints: []string{"responses"}}}}}}}
	row := AccountCapabilityCandidate{PublicModel: "gpt-6-astra", UpstreamModel: "gpt-6-astra", ConfigFingerprint: fingerprint, Protocol: "responses"}
	require.True(t, capabilityCandidatePublished(&account, group, &row))
	row.Protocol = "responses_websocket"
	require.False(t, capabilityCandidatePublished(&account, group, &row))
}

func TestAccountCapabilityCatalogSeparatesHistoricalAliveFromPublishable(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"glm-5.3": "glm-5.3"})
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	old := time.Now().Add(-25 * time.Hour)
	now := time.Now()
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{
		{ID: 1, Kind: "probe", AccountID: 10, FolderID: 7, UpstreamModel: "glm-5.3", Protocol: "responses", Profile: "text", ConfigFingerprint: fingerprint, Result: json.RawMessage(`{"status":"alive"}`), FinishedAt: &old, PublicationSuperseded: true},
		{ID: 2, Kind: "discover", AccountID: 10, FolderID: 7, ConfigFingerprint: fingerprint, Result: json.RawMessage(`{"status":"failed","account_failure":true,"models":[]}`), FinishedAt: &now},
	}, nil)
	require.Len(t, rows, 2)
	row := capabilityCatalogFindRow(t, rows, "glm-5.3", "responses")
	require.Equal(t, "alive", row.ProbeStatus)
	require.False(t, row.Publishable)
	require.Contains(t, row.Warnings, "historical_success")
	require.NotContains(t, row.NotPublishableReasons, "evidence_expired")
	require.Contains(t, row.NotPublishableReasons, "evidence_superseded")
}
