package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func capabilityCatalogV2Evidence(t *testing.T, account *Account, id int64, model, protocol, status, classification string, at time.Time) AccountCapabilityItem {
	t.Helper()
	fingerprint, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	result, err := json.Marshal(AccountCapabilityProbeResult{Status: status, Classification: classification, RequestCount: 1})
	require.NoError(t, err)
	itemStatus := "failed"
	if status == "alive" {
		itemStatus = "succeeded"
	} else if status == "uncertain" {
		itemStatus = "indeterminate"
	}
	return AccountCapabilityItem{ID: id, Kind: "probe", AccountID: account.ID, FolderID: *account.ManagementFolderID,
		ConfigFingerprint: fingerprint, UpstreamModel: model, Protocol: protocol, Profile: "text",
		Status: itemStatus, Result: result, RequestCount: 1, FinishedAt: &at}
}

func TestAccountCapabilityCatalogV2KeepsFableVersionsUnknownSuffixesAndAllSources(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{
		"my-fable": "claude-fable-5-1", "second-name": "claude-fable-5-1",
		"namespace-alias": "unfamiliar/fable-release-cc",
	})
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	now := time.Now()
	old := now.Add(-time.Hour)
	evidence := []AccountCapabilityItem{
		{ID: 1, AccountID: 10, FolderID: 7, ConfigFingerprint: fingerprint, Kind: "discover", FinishedAt: &old,
			Result: json.RawMessage(`{"status":"discovered","models":[{"id":"claude-fable-5"},{"id":"claude-fable-5-1-cc"},{"id":"claude-fable-5-ssvip"}]}`)},
		{ID: 2, AccountID: 10, FolderID: 7, ConfigFingerprint: fingerprint, Kind: "discover", FinishedAt: &now, Result: json.RawMessage(`{"status":"empty","models":[]}`)},
		capabilityCatalogV2Evidence(t, &account, 3, "gpt-5.9-sol", "responses", "alive", "text_completed", now),
	}
	rows := buildAccountCapabilityCandidates([]Account{account}, evidence, nil)
	require.Len(t, rows, 12, "six exact targets, two HTTP protocols each; aliases do not multiply targets")
	five := capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses")
	fiveOne := capabilityCatalogFindRow(t, rows, "claude-fable-5-1", "responses")
	require.NotEqual(t, five.PublicModel, fiveOne.PublicModel)
	require.True(t, five.Recognized)
	require.True(t, five.Recommended)
	require.True(t, fiveOne.Configured)
	require.False(t, five.Discovered, "old declarations remain clues after an empty fresh directory")
	cc := capabilityCatalogFindRow(t, rows, "claude-fable-5-1-cc", "responses")
	require.Equal(t, "claude-fable-5-1-cc", cc.PublicModel)
	require.True(t, cc.NeedsNameConfirmation)
	require.Equal(t, "claude(非逆向渠道)", cc.GroupName)
	require.Contains(t, cc.NotPublishableReasons, "needs_name_confirmation")
	require.False(t, cc.ProbeEligible)
	vip := capabilityCatalogFindRow(t, rows, "claude-fable-5-ssvip", "responses")
	require.Equal(t, "claude-fable-5", vip.PublicModel)
	require.Equal(t, "claude-fable-5-ssvip", vip.UpstreamModel)
	newer := capabilityCatalogFindRow(t, rows, "gpt-5.9-sol", "responses")
	require.Equal(t, "gpt-5.9-sol", newer.PublicModel)
	require.True(t, newer.Recognized)
	require.False(t, newer.Recommended, "unlisted versions are visible, not silently added to the launch preference")
}

func TestAccountCapabilityCatalogV2OldSuccessSurvivesTemporaryResults(t *testing.T) {
	for _, tc := range []struct{ status, classification string }{
		{"failed", "upstream_unavailable"}, {"failed", "rate_limited"},
		{"uncertain", "timeout"}, {"uncertain", "network_error"}, {"failed", "request_rejected"},
	} {
		t.Run(tc.classification, func(t *testing.T) {
			account := capabilityCatalogTestAccount(10, map[string]any{"claude-fable-5-1": "claude-fable-5-1"})
			now := time.Now()
			old := now.Add(-72 * time.Hour)
			rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{
				capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5-1", "responses", "alive", "text_completed", old),
				capabilityCatalogV2Evidence(t, &account, 2, "claude-fable-5-1", "responses", tc.status, tc.classification, now),
			}, nil)
			row := capabilityCatalogFindRow(t, rows, "claude-fable-5-1", "responses")
			require.Equal(t, tc.status, row.ProbeStatus)
			require.EqualValues(t, 2, *row.LatestProbeItemID)
			require.EqualValues(t, 1, *row.LastSuccessItemID)
			require.Equal(t, tc.classification, row.LatestAttempt.Classification)
			require.True(t, row.LastSuccessReusable)
			require.True(t, row.Publishable)
			require.True(t, row.AlreadyAttempted)
			require.Contains(t, row.Warnings, "historical_success")
			require.NotContains(t, row.NotPublishableReasons, "evidence_expired")
			alternate := capabilityCatalogFindRow(t, rows, "claude-fable-5-1", "chat_completions")
			require.False(t, alternate.AlreadyAttempted)
			require.True(t, alternate.HasCompatibleSuccess)
			require.False(t, alternate.ProbeEligible, "an earlier success prevents automatic protocol-expansion calls")
		})
	}
}

func TestAccountCapabilityCatalogV2DefinitiveFailuresRemainNarrow(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"claude-fable-5": "claude-fable-5"})
	now := time.Now()
	old := now.Add(-time.Hour)
	success := capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5", "responses", "alive", "text_completed", old)
	for _, tc := range []struct {
		name, model, protocol, classification string
		accountFailure, reusable              bool
	}{
		{"same model", "claude-fable-5", "responses", "model_unavailable", false, false},
		{"different model", "claude-fable-5-1", "responses", "model_unavailable", false, true},
		{"different protocol", "claude-fable-5", "chat_completions", "model_unavailable", false, true},
		{"same protocol unavailable", "claude-fable-5", "responses", "protocol_unsupported", false, false},
		{"explicit credentials", "claude-fable-5-1", "chat_completions", "credential_invalid", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := capabilityCatalogV2Evidence(t, &account, 2, tc.model, tc.protocol, "failed", tc.classification, now)
			if tc.accountFailure {
				failure.Result = json.RawMessage(`{"status":"failed","classification":"credential_invalid","account_failure":true}`)
			}
			rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{success, failure}, nil)
			row := capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses")
			require.Equal(t, tc.reusable, row.LastSuccessReusable)
			require.Equal(t, tc.reusable, row.Publishable)
			require.True(t, row.HasCompatibleSuccess, "an immutable historical success prevents automatic additional testing even when no longer publishable")
		})
	}
}

func TestAccountCapabilityCatalogV2ToolsAndCountsNeverReplaceTextEvidence(t *testing.T) {
	account := capabilityCatalogTestAccount(10, nil)
	now := time.Now()
	old := now.Add(-time.Hour)
	text := capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5", "responses", "alive", "text_completed", old)
	tool := capabilityCatalogV2Evidence(t, &account, 2, "claude-fable-5", "responses", "failed", "model_unavailable", now)
	tool.Profile = "tool_roundtrip"
	tool.Result = json.RawMessage(`{"status":"failed","classification":"credential_invalid","account_failure":true}`)
	count := capabilityCatalogV2Evidence(t, &account, 3, "claude-fable-5", "responses_input_tokens", "failed", "model_unavailable", now)
	count.Result = tool.Result
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{text, tool, count}, nil)
	row := capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses")
	require.EqualValues(t, 1, *row.LatestProbeItemID)
	require.True(t, row.LastSuccessReusable)
	tool.Status, tool.Result = "succeeded", json.RawMessage(`{"status":"alive","classification":"tool_roundtrip_passed"}`)
	rows = buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{tool, count}, nil)
	row = capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses")
	require.Equal(t, "untested", row.ProbeStatus)
	require.Nil(t, row.LastSuccessItemID)
	require.False(t, row.HasCompatibleSuccess)
}

func TestAccountCapabilityCatalogV2ProbeEligibilityOnlyForNewCompatibleIdentity(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"claude-fable-5": "claude-fable-5"})
	now := time.Now()
	failure := capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5", "responses", "failed", "upstream_unavailable", now)
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{failure}, nil)
	failed := capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses")
	alternate := capabilityCatalogFindRow(t, rows, "claude-fable-5", "chat_completions")
	require.True(t, failed.AlreadyAttempted)
	require.False(t, failed.ProbeEligible)
	require.True(t, alternate.ProbeEligible)
	for _, status := range []string{"pending", "running", "stale", "indeterminate"} {
		reserved := failure
		reserved.ID, reserved.Protocol, reserved.Status, reserved.Result = 2, "chat_completions", status, json.RawMessage(`{}`)
		rows = buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{failure, reserved}, nil)
		require.False(t, capabilityCatalogFindRow(t, rows, "claude-fable-5", "chat_completions").ProbeEligible, status)
	}
	canceled := failure
	canceled.Protocol, canceled.Status, canceled.RequestCount, canceled.Result = "chat_completions", "canceled", 0, json.RawMessage(`{}`)
	rows = buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{failure, canceled}, nil)
	require.True(t, capabilityCatalogFindRow(t, rows, "claude-fable-5", "chat_completions").ProbeEligible)
	canceled.DispatchedAt = &now
	rows = buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{failure, canceled}, nil)
	require.False(t, capabilityCatalogFindRow(t, rows, "claude-fable-5", "chat_completions").ProbeEligible)
	canceled.DispatchedAt, canceled.Result = nil, json.RawMessage(`{"request_count_unknown":true}`)
	rows = buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{failure, canceled}, nil)
	require.False(t, capabilityCatalogFindRow(t, rows, "claude-fable-5", "chat_completions").ProbeEligible)
}

func TestAccountCapabilityCatalogV2ConfigurationChangesAreNotFreshSuccess(t *testing.T) {
	account := capabilityCatalogTestAccount(10, nil)
	success := capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5", "responses", "alive", "text_completed", time.Now())
	account.Credentials["api_key"] = "changed-test-only"
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{success}, nil)
	row := capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses")
	require.True(t, row.Stale)
	require.True(t, row.LatestAttempt.Stale)
	require.Equal(t, "alive", row.LatestAttempt.Status, "the historical outcome itself is immutable")
	require.False(t, row.LastSuccessReusable)
	require.False(t, row.HasCompatibleSuccess)
	require.False(t, row.AlreadyAttempted)
	require.Contains(t, row.NotPublishableReasons, "configuration_changed")
}

func TestAccountCapabilityCatalogV2PublicationIsIndependentOfCoolingAndDrift(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"claude-fable-5": "claude-fable-5"})
	account.Schedulable, account.GroupIDs = true, []int64{23}
	fingerprint, err := AccountCapabilityFingerprint(&account)
	require.NoError(t, err)
	selector := ManagedModelBranchSelector(23, "claude-fable-5", PlatformOpenAI, "chat_completions", "claude-fable-5")
	account.Credentials["model_mapping"].(map[string]any)[selector] = "claude-fable-5"
	group := &Group{ID: 23, Name: "claude(非逆向渠道)", Status: StatusActive, Platform: PlatformComposite,
		ManagedModelRoutes: ManagedModelRoutesConfig{Enabled: true, Version: 2, Routes: []ManagedModelRoute{{
			PublicModel: "claude-fable-5", Endpoints: []string{"responses"}, Branches: []ManagedModelRouteBranch{{
				Selector: selector, TargetPlatform: PlatformOpenAI, UpstreamProtocol: "chat_completions", Endpoints: []string{"responses"},
				Accounts: []ManagedModelRouteAccount{{AccountID: 10, UpstreamModel: "claude-fable-5", AccountFingerprint: fingerprint, Endpoints: []string{"responses"}}},
			}},
		}}}}
	row := AccountCapabilityCandidate{PublicModel: "claude-fable-5", UpstreamModel: "claude-fable-5", Protocol: "chat_completions", ConfigFingerprint: fingerprint}
	require.True(t, capabilityCandidatePublished(&account, group, &row))
	require.True(t, capabilityCandidateRoutingReady(&account, group, &row))
	coolUntil := time.Now().Add(time.Hour)
	account.TempUnschedulableUntil = &coolUntil
	require.True(t, capabilityCandidatePublished(&account, group, &row))
	require.False(t, capabilityCandidateRoutingReady(&account, group, &row))
	account.TempUnschedulableUntil, account.GroupIDs = nil, nil
	require.True(t, capabilityCandidatePublished(&account, group, &row))
	require.False(t, capabilityCandidateRoutingReady(&account, group, &row))
	account.GroupIDs = []int64{23}
	row.ConfigFingerprint = "configuration-changed"
	require.True(t, capabilityCandidatePublished(&account, group, &row))
	require.False(t, capabilityCandidateRoutingReady(&account, group, &row))
	row.ConfigFingerprint = fingerprint
	group.ManagedModelRoutes.Routes[0].Endpoints = nil
	require.True(t, capabilityCandidatePublished(&account, group, &row))
	require.False(t, capabilityCandidateRoutingReady(&account, group, &row), "an unavailable public ingress is not ready even if a branch lists it")
}

func TestAccountCapabilityCatalogV2ReportsPrivatePreservationBlockersBeforeProbingOrPlanning(t *testing.T) {
	for _, mapping := range []map[string]any{nil, {"*": "*"}, {"gpt-*": "claude-fable-5"}} {
		account := capabilityCatalogTestAccount(10, mapping)
		evidence := capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5", "responses", "alive", "text_completed", time.Now())
		rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{evidence}, nil)
		row := capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses")
		require.True(t, row.LastSuccessReusable, "the capability observation stays factual")
		require.False(t, row.Publishable)
		require.Contains(t, row.NotPublishableReasons, "private_mapping_requires_review")
		evidence.Status, evidence.Result = "failed", json.RawMessage(`{"status":"failed","classification":"upstream_unavailable"}`)
		rows = buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{evidence}, nil)
		require.False(t, capabilityCatalogFindRow(t, rows, "claude-fable-5", "chat_completions").ProbeEligible, "do not offer paid checks for a configuration the planner cannot safely publish")
	}
}

func TestAccountCapabilityCatalogV2PublicationRevisionCatchesMappingAndBindingChanges(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"claude-fable-5": "claude-fable-5"})
	account.GroupIDs = []int64{9, 3}
	account.AccountGroups = []AccountGroup{{GroupID: 9, Priority: 4}, {GroupID: 3, Priority: 2}}
	original := capabilityAccountPublicationRevision(&account)
	probeIdentity := ManagedModelAccountFingerprint(&account)
	require.Len(t, original, 64)
	account.GroupIDs = []int64{3, 9}
	account.AccountGroups[0], account.AccountGroups[1] = account.AccountGroups[1], account.AccountGroups[0]
	require.Equal(t, original, capabilityAccountPublicationRevision(&account), "list order is not a configuration change")
	account.AccountGroups[0].Priority++
	require.NotEqual(t, original, capabilityAccountPublicationRevision(&account))
	account.AccountGroups[0].Priority--
	account.Credentials["model_mapping"].(map[string]any)["s2pub-fixture"] = "claude-fable-5"
	mappingChanged := capabilityAccountPublicationRevision(&account)
	require.NotEqual(t, original, mappingChanged)
	require.Equal(t, probeIdentity, ManagedModelAccountFingerprint(&account), "management CAS must not invalidate previously paid probe evidence")
	account.Credentials["api_key"] = "different-test-only"
	require.Equal(t, mappingChanged, capabilityAccountPublicationRevision(&account), "credentials are represented separately by the probe fingerprint")
	account.Credentials["compact_model_mapping"] = map[string]any{"claude-fable-5": "claude-fable-5-1"}
	require.NotEqual(t, mappingChanged, capabilityAccountPublicationRevision(&account))
}

type capabilityCatalogV2Admin struct {
	AdminService
	accounts []Account
}

func (a *capabilityCatalogV2Admin) ListAccountsConsole(_ context.Context, page, _ int, _ AccountConsoleFilters) ([]Account, int64, error) {
	if page > 1 {
		return nil, int64(len(a.accounts)), nil
	}
	return a.accounts, int64(len(a.accounts)), nil
}

type capabilityCatalogV2Groups struct {
	GroupRepository
	groups []Group
}

func (g *capabilityCatalogV2Groups) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	return g.groups, nil, nil
}

type capabilityCatalogV2Repository struct {
	AccountCapabilityRepository
	evidence []AccountCapabilityItem
}

func (r *capabilityCatalogV2Repository) EvidenceItems(_ context.Context, filter AccountCapabilityFilter) (*AccountCapabilityItemPage, error) {
	items := []AccountCapabilityItem{}
	for _, item := range r.evidence {
		if item.Kind == filter.Kind {
			items = append(items, item)
		}
	}
	return &AccountCapabilityItemPage{Items: items, Page: 1, PageSize: 1000, Total: int64(len(items))}, nil
}

func TestAccountCapabilityCatalogV2GroupContextFiltersBeforePagination(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"claude-fable-5": "claude-fable-5", "gpt-6-astra": "gpt-6-astra"})
	groups := &capabilityCatalogV2Groups{groups: []Group{{ID: 23, Name: "claude(非逆向渠道)"}, {ID: 24, Name: "gpt"}}}
	old := time.Now().Add(-72 * time.Hour)
	now := time.Now()
	repo := &capabilityCatalogV2Repository{evidence: []AccountCapabilityItem{
		capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5", "responses", "alive", "text_completed", old),
		capabilityCatalogV2Evidence(t, &account, 2, "claude-fable-5", "responses", "failed", "upstream_unavailable", now),
	}}
	svc := NewAccountCapabilityCatalogService(repo, &capabilityCatalogV2Admin{accounts: []Account{account}}, groups, nil, nil)
	filter := AccountCapabilityCandidateFilter{FolderIDs: []int64{7}, GroupIDs: []int64{23}, Page: 2, PageSize: 1}
	page, err := svc.Candidates(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 23, *page.Items[0].GroupID)
	require.Equal(t, "claude-fable-5", page.Items[0].PublicModel)
	require.True(t, page.Items[0].LastSuccessReusable, "the catalog uses its evidence reader, not the latest-attempt-only projection")
	snapshot, err := svc.loadCapabilityCatalog(context.Background(), filter)
	require.NoError(t, err)
	require.Len(t, snapshot.Rows, 4, "the shared snapshot remains unpaginated for full server-side aggregation")
	require.Len(t, snapshot.AccountPublicationRevisions[10], 64)
	filter.GroupIDs = []int64{-1}
	_, err = svc.Candidates(context.Background(), filter)
	require.ErrorIs(t, err, ErrAccountCapabilityInvalid)
}

func TestAccountCapabilityCatalogV2RetainsWebSocketEvidenceWithoutPromotingNewBranches(t *testing.T) {
	account := capabilityCatalogTestAccount(10, map[string]any{"claude-fable-5": "claude-fable-5"})
	evidence := capabilityCatalogV2Evidence(t, &account, 1, "claude-fable-5", "responses_websocket", "alive", "text_completed", time.Now())
	rows := buildAccountCapabilityCandidates([]Account{account}, []AccountCapabilityItem{evidence}, nil)
	row := capabilityCatalogFindRow(t, rows, "claude-fable-5", "responses_websocket")
	require.True(t, row.LastSuccessReusable, "the recorded generation result is retained")
	require.False(t, row.Publishable, "new v2 branches support only the explicit HTTP adapter set")
	require.Contains(t, row.NotPublishableReasons, "unsupported_public_protocol")
	require.False(t, row.ProbeEligible, "the assistant never expands testing into WebSocket")
}
