package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func capabilityOverviewFixtureRow(accountID int64, model, group, protocol string, successID int64) AccountCapabilityCandidate {
	row := AccountCapabilityCandidate{AccountID: accountID, AccountName: fmt.Sprintf("fixture-%d", accountID), FolderID: 7,
		AccountPlatform: PlatformOpenAI, ConfigFingerprint: fmt.Sprintf("%064x", accountID), PublicModel: model, UpstreamModel: model,
		Protocol: protocol, Profile: AccountCapabilityProfileText, Aliases: []string{}, Tier: "standard", GroupName: group,
		Mainstream: true, Recognized: true, PricingKnown: true, ProbeEligible: successID == 0, Schedulable: true,
		NotPublishableReasons: []string{}, Warnings: []string{}, ProbeStatus: "untested"}
	if successID > 0 {
		checked := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		row.LastSuccessItemID, row.LastSuccessAt = &successID, &checked
		row.LastSuccessReusable, row.HasCompatibleSuccess, row.AlreadyAttempted, row.Publishable = true, true, true, true
		row.LatestAttempt = &AccountCapabilityAttempt{ItemID: successID, Status: "alive", Classification: "text_completed", CheckedAt: &checked}
		row.ProbeStatus = "alive"
	}
	return row
}

func capabilityOverviewFixtureGroup(id int64, name string, models ...string) *Group {
	group := &Group{ID: id, Name: name, Platform: PlatformComposite, RateMultiplier: 0.5, Status: StatusActive,
		ManagedModelRoutes: ManagedModelRoutesConfig{Enabled: true, Version: 2, Routes: []ManagedModelRoute{}},
		ModelAllowlist:     GroupModelAllowlist{Enabled: true, Models: append([]string{}, models...)}}
	for _, model := range models {
		group.ManagedModelRoutes.Routes = append(group.ManagedModelRoutes.Routes, ManagedModelRoute{PublicModel: model})
	}
	return group
}

func capabilityOverviewFixtureSnapshot(rows []AccountCapabilityCandidate, groups ...*Group) (*accountCapabilityCatalogSnapshot, CapabilityPublicationScope) {
	snapshot := &accountCapabilityCatalogSnapshot{Rows: rows, Accounts: []AccountCapabilityScopeAccount{}, Groups: map[string]*Group{}, GroupChannels: map[int64]*Channel{}}
	scope := CapabilityPublicationScope{FolderIDs: []int64{7, 8}, AccountIDs: []int64{}}
	seen := map[int64]bool{}
	for _, row := range rows {
		if !seen[row.AccountID] {
			seen[row.AccountID] = true
			scope.AccountIDs = append(scope.AccountIDs, row.AccountID)
			snapshot.Accounts = append(snapshot.Accounts, AccountCapabilityScopeAccount{ID: row.AccountID, Name: row.AccountName,
				FolderID: row.FolderID, Platform: row.AccountPlatform, Status: StatusActive, Schedulable: row.Schedulable, ConfigFingerprint: row.ConfigFingerprint})
		}
	}
	scope.AccountIDs = publicationUniqueIDs(scope.AccountIDs)
	for _, group := range groups {
		snapshot.Groups[strings.ToLower(group.Name)] = group
	}
	return snapshot, scope
}

func capabilityOverviewFindGroup(t *testing.T, overview *AccountCapabilityOverview, name string) AccountCapabilityOverviewGroup {
	t.Helper()
	for _, group := range overview.Groups {
		if group.Name == name {
			return group
		}
	}
	t.Fatalf("missing group %q", name)
	return AccountCapabilityOverviewGroup{}
}

func capabilityOverviewFindModel(t *testing.T, group AccountCapabilityOverviewGroup, name string) AccountCapabilityOverviewModel {
	t.Helper()
	for _, model := range group.Models {
		if model.PublicModel == name {
			return model
		}
	}
	t.Fatalf("missing model %q", name)
	return AccountCapabilityOverviewModel{}
}

func TestAccountCapabilityOverviewSeparatesConfiguredReadyAndRecentAttempt(t *testing.T) {
	const groupName = "claude(非逆向渠道)"
	group := capabilityOverviewFixtureGroup(23, groupName, "claude-fable-5", "claude-fable-5-1")
	one := capabilityOverviewFixtureRow(1, "claude-fable-5", groupName, "messages", 101)
	one.Published, one.AccountPlatform = true, PlatformAnthropic
	duplicate := one
	duplicate.Aliases = []string{"claude-fable-5", "known-fixture-alias"}
	two := capabilityOverviewFixtureRow(2, "claude-fable-5", groupName, "chat_completions", 102)
	three := capabilityOverviewFixtureRow(3, "claude-fable-5", groupName, "responses", 0)
	otherProtocol := three
	otherProtocol.Protocol = "chat_completions"
	temporary := one
	temporary.Protocol = "chat_completions"
	checked := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	temporary.LatestAttempt = &AccountCapabilityAttempt{ItemID: 201, Status: "failed", Classification: "upstream_unavailable", CheckedAt: &checked}
	unknown := capabilityOverviewFixtureRow(4, "new-unknown-product", capabilityUnclassifiedGroup, "responses", 0)
	unknown.Recognized, unknown.Mainstream, unknown.ProbeEligible, unknown.NeedsNameConfirmation = false, false, false, true
	private := capabilityOverviewFixtureGroup(35, "临时", "private-model")
	snapshot, scope := capabilityOverviewFixtureSnapshot([]AccountCapabilityCandidate{one, duplicate, two, three, otherProtocol, temporary, unknown}, group, private)
	overview, err := buildAccountCapabilityOverview(scope, snapshot, nil, "", nil)
	require.NoError(t, err)
	require.Len(t, overview.Groups, 11)
	view := capabilityOverviewFindGroup(t, overview, groupName)
	fable := capabilityOverviewFindModel(t, view, "claude-fable-5")
	require.True(t, fable.Published, "cooldown does not undo saved publication")
	require.Equal(t, 2, fable.VerifiedAccountCount)
	require.Zero(t, fable.RoutingReadyAccountCount)
	require.Equal(t, 1, fable.UntestedAccountCount, "two protocols are one untested account")
	require.Equal(t, 1, fable.TemporaryFailureAccountCount)
	require.Equal(t, "temporary_failure", fable.LastCheckStatus)
	require.Equal(t, "add", fable.Action, "a successful spare route is still recommended after another route's 503")
	require.Len(t, fable.Candidates, 5)
	require.Equal(t, []string{"known-fixture-alias"}, fable.Aliases)
	require.Empty(t, snapshot.Rows[0].Aliases, "merging aliases must not mutate the catalog snapshot")
	recommendation, err := buildAccountCapabilityRecommendation(scope, snapshot, overview, AccountCapabilityRecommendationRequest{
		Models: []AccountCapabilityModelSelection{{GroupID: 23, PublicModel: "claude-fable-5"}},
	})
	require.NoError(t, err)
	require.Contains(t, recommendation.PreviewRequest.Groups[0].Models[0].Aliases, "known-fixture-alias", "deduplicated successful line aliases survive into the recommendation")
	fable51 := capabilityOverviewFindModel(t, view, "claude-fable-5-1")
	require.True(t, fable51.Published)
	require.Empty(t, fable51.Candidates, "existing published models remain visible with no scoped candidates")
	require.Equal(t, "untested", fable51.LastCheckStatus)
	require.Equal(t, 2, view.PublishedModelCount)
	require.Equal(t, 2, view.VerifiedAccountCount)
	unknownView := capabilityOverviewFindModel(t, capabilityOverviewFindGroup(t, overview, capabilityUnclassifiedGroup), unknown.PublicModel)
	require.Equal(t, "review_name", unknownView.Action)
	require.Equal(t, 1, unknownView.UntestedAccountCount, "awaiting name confirmation is not a completed failed probe")
	for _, public := range overview.Groups {
		require.NotEqual(t, "临时", public.Name)
	}
	_, err = buildAccountCapabilityOverview(scope, snapshot, []int64{35}, "", nil)
	require.ErrorIs(t, err, ErrAccountCapabilityScope)
}

func TestAccountCapabilityOverviewCountsFullCatalogEvenWhenSearchMatchesOneAccount(t *testing.T) {
	rows := []AccountCapabilityCandidate{}
	for id := int64(1); id <= 61; id++ {
		rows = append(rows, capabilityOverviewFixtureRow(id, "claude-fable-5", "claude(非逆向渠道)", "responses", id))
	}
	snapshot, scope := capabilityOverviewFixtureSnapshot(rows, capabilityOverviewFixtureGroup(23, "claude(非逆向渠道)"))
	overview, err := buildAccountCapabilityOverview(scope, snapshot, nil, "fixture-61", nil)
	require.NoError(t, err)
	require.Len(t, overview.Groups, 1)
	require.Equal(t, 61, overview.Groups[0].Models[0].VerifiedAccountCount)
	require.Equal(t, 61, overview.Totals.VerifiedAccountCount)
	require.Len(t, overview.Groups[0].Models[0].Candidates, 61)
	require.Len(t, overview.Scope.AccountIDs, 61)
}

func TestAccountCapabilityRecommendationMergesPartialSelectionAndAllSuccessfulPlatforms(t *testing.T) {
	const groupName = "claude(非逆向渠道)"
	group := capabilityOverviewFixtureGroup(23, groupName, "claude-sonnet-5")
	one := capabilityOverviewFixtureRow(1, "claude-fable-5-1", groupName, "messages", 101)
	one.AccountPlatform, one.Recommended = PlatformAnthropic, false
	two := capabilityOverviewFixtureRow(2, "claude-fable-5-1", groupName, "chat_completions", 102)
	alt := capabilityOverviewFixtureRow(2, "claude-fable-5-1", groupName, "chat_completions", 103)
	alt.UpstreamModel = "claude-fable-5-1-ssvip"
	unselected := capabilityOverviewFixtureRow(3, "claude-fable-5", groupName, "responses", 0)
	snapshot, scope := capabilityOverviewFixtureSnapshot([]AccountCapabilityCandidate{one, two, two, alt, unselected}, group)
	// Accounts without a candidate still belong to the frozen source selection.
	scope.AccountIDs = append(scope.AccountIDs, 77)
	snapshot.Accounts = append(snapshot.Accounts, AccountCapabilityScopeAccount{ID: 77, FolderID: 8})
	overview, err := buildAccountCapabilityOverview(scope, snapshot, nil, "", nil)
	require.NoError(t, err)
	request := AccountCapabilityRecommendationRequest{Scope: scope, Models: []AccountCapabilityModelSelection{{GroupID: 23, PublicModel: "claude-fable-5-1"}}}
	recommendation, err := buildAccountCapabilityRecommendation(scope, snapshot, overview, request)
	require.NoError(t, err)
	require.NotNil(t, recommendation.PreviewRequest)
	require.Equal(t, "merge", recommendation.PreviewRequest.Operation)
	require.Equal(t, scope, recommendation.PreviewRequest.Scope)
	require.Len(t, recommendation.PreviewRequest.Groups, 1)
	selected := recommendation.PreviewRequest.Groups[0]
	require.Equal(t, group.Platform, selected.Platform)
	require.Equal(t, group.RateMultiplier, selected.RateMultiplier)
	require.Len(t, selected.Models, 1)
	require.Equal(t, []int64{101, 102, 103}, selected.Models[0].EvidenceIDs)
	require.Equal(t, 3, recommendation.Impact.ReusedSuccessCount)
	require.Len(t, recommendation.Impact.AddedModels, 1)
	require.Len(t, recommendation.Impact.AddedAccounts, 2, "the second target on account 2 is not a third account")
	require.Len(t, recommendation.Impact.AddedRoutes, 3)
	require.Equal(t, []AccountCapabilityModelReference{{GroupID: 23, GroupName: groupName, PublicModel: "claude-sonnet-5"}}, recommendation.Impact.RetainedModels)
	require.Empty(t, recommendation.Impact.RemovedModels)
	require.Empty(t, recommendation.Impact.RemovedAccounts)
	require.Nil(t, recommendation.ProbeRequest, "an unselected model must not trigger a request")
	require.Zero(t, recommendation.MaximumRequestCount)
}

func TestAccountCapabilityRecommendationChecksOneNewCompatibleWireAndNeverRetriesSuccess(t *testing.T) {
	const groupName = "claude(非逆向渠道)"
	rows := []AccountCapabilityCandidate{}
	for id := int64(1); id <= 4; id++ {
		responses := capabilityOverviewFixtureRow(id, "claude-fable-5", groupName, "responses", 0)
		chat := responses
		chat.Protocol, chat.ProbeProtocolPriority = "chat_completions", 1
		switch id {
		case 1:
			responses.ProbeProtocolPriority, chat.ProbeProtocolPriority = 1, 0 // configured Chat account
		case 3:
			responses.AlreadyAttempted, responses.ProbeEligible = true, false
			responses.LatestAttempt = &AccountCapabilityAttempt{ItemID: 50, Status: "failed", Classification: "upstream_unavailable"}
		case 4:
			// A previous successful wire forbids widening the test, even when a
			// later definite failure makes that old evidence unpublishable.
			responses.HasCompatibleSuccess, chat.HasCompatibleSuccess = true, true
			responses.ProbeEligible, chat.ProbeEligible = false, false
		}
		rows = append(rows, responses, chat)
	}
	unknown := capabilityOverviewFixtureRow(5, "claude-fable-5-cc", groupName, "responses", 0)
	unknown.Recognized, unknown.NeedsNameConfirmation = false, true
	rows = append(rows, unknown)
	ws := capabilityOverviewFixtureRow(6, "claude-fable-5", groupName, "responses_websocket", 0)
	rows = append(rows, ws)
	snapshot, scope := capabilityOverviewFixtureSnapshot(rows, capabilityOverviewFixtureGroup(23, groupName))
	overview, err := buildAccountCapabilityOverview(scope, snapshot, nil, "", nil)
	require.NoError(t, err)
	recommendation, err := buildAccountCapabilityRecommendation(scope, snapshot, overview, AccountCapabilityRecommendationRequest{Scope: scope})
	require.NoError(t, err)
	require.Nil(t, recommendation.PreviewRequest)
	require.NotNil(t, recommendation.ProbeRequest)
	require.Equal(t, 3, recommendation.MaximumRequestCount)
	probe := recommendation.ProbeRequest
	require.True(t, probe.OnlyUntested)
	require.Equal(t, []int64{1, 2, 3}, probe.AccountIDs)
	require.Len(t, probe.ExpectedConfigFingerprints, 3)
	byAccount := map[int64]AccountCapabilityProbeTarget{}
	for _, target := range probe.Items {
		byAccount[target.AccountID] = target
		require.Equal(t, AccountCapabilityProfileText, target.Profile)
		require.Equal(t, fmt.Sprintf("%064x", target.AccountID), probe.ExpectedConfigFingerprints[target.AccountID])
	}
	require.Equal(t, "chat_completions", byAccount[1].Protocol)
	require.Equal(t, "responses", byAccount[2].Protocol)
	require.Equal(t, "chat_completions", byAccount[3].Protocol)
	_, err = normalizeCapabilityRequest(*probe)
	require.NoError(t, err, "generated jobs satisfy the same strict frozen-config guard as manual requests")
}

func TestAccountCapabilityRecommendationMissingPriceIsNotUnsupportedOrRetested(t *testing.T) {
	row := capabilityOverviewFixtureRow(1, "qwen3.8-max", "Qwen", "responses", 101)
	row.PricingKnown, row.Publishable = false, false
	row.NotPublishableReasons = []string{"pricing_unavailable"}
	snapshot, scope := capabilityOverviewFixtureSnapshot([]AccountCapabilityCandidate{row})
	overview, err := buildAccountCapabilityOverview(scope, snapshot, nil, "", nil)
	require.NoError(t, err)
	model := capabilityOverviewFindModel(t, capabilityOverviewFindGroup(t, overview, "Qwen"), "qwen3.8-max")
	require.Equal(t, 1, model.VerifiedAccountCount)
	require.Equal(t, "set_price", model.Action)
	recommendation, err := buildAccountCapabilityRecommendation(scope, snapshot, overview, AccountCapabilityRecommendationRequest{Scope: scope})
	require.NoError(t, err)
	require.Nil(t, recommendation.PreviewRequest)
	require.Nil(t, recommendation.ProbeRequest)
	require.Equal(t, "pricing_unavailable", recommendation.Exclusions[0].Reason)
	raw, err := json.Marshal(recommendation)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"added_routes":[]`)
	require.Contains(t, string(raw), `"removed_models":[]`)
}

func TestAccountCapabilityRecommendationAlternativeTargetDoesNotInflateAddedAccounts(t *testing.T) {
	group := capabilityOverviewFixtureGroup(24, "gpt-vip", "gpt-6-astra")
	one := capabilityOverviewFixtureRow(1, "gpt-6-astra", "gpt-vip", "responses", 101)
	one.UpstreamModel, one.Published = "gpt-6-astra-ssvip", true
	group.ManagedModelRoutes.Routes[0].Branches = []ManagedModelRouteBranch{{Selector: "s2pub-fixture", TargetPlatform: PlatformOpenAI, UpstreamProtocol: "responses",
		Accounts: []ManagedModelRouteAccount{{AccountID: 1, UpstreamModel: one.UpstreamModel, AccountFingerprint: one.ConfigFingerprint}}}}
	alt := capabilityOverviewFixtureRow(1, "gpt-6-astra", "gpt-vip", "responses", 102)
	alt.UpstreamModel = "gpt-6-astra-vip"
	snapshot, scope := capabilityOverviewFixtureSnapshot([]AccountCapabilityCandidate{one, alt}, group)
	overview, err := buildAccountCapabilityOverview(scope, snapshot, []int64{24}, "", nil)
	require.NoError(t, err)
	recommendation, err := buildAccountCapabilityRecommendation(scope, snapshot, overview, AccountCapabilityRecommendationRequest{Scope: scope})
	require.NoError(t, err)
	require.Empty(t, recommendation.Impact.AddedAccounts)
	require.Len(t, recommendation.Impact.AddedRoutes, 1)
	require.Equal(t, alt.UpstreamModel, recommendation.Impact.AddedRoutes[0].UpstreamModel)
	require.Equal(t, "vip", recommendation.PreviewRequest.Groups[0].Models[0].Tier)
	require.Equal(t, []int64{101, 102}, recommendation.PreviewRequest.Groups[0].Models[0].EvidenceIDs)
}

type capabilityOverviewAdminFixture struct {
	AdminService
	folders     []AccountManagementFolder
	accounts    map[int64]*Account
	folderReads int
}

func (fixture *capabilityOverviewAdminFixture) ListAccountFolders(context.Context) ([]AccountManagementFolder, error) {
	fixture.folderReads++
	return fixture.folders, nil
}

func (fixture *capabilityOverviewAdminFixture) GetAccount(_ context.Context, id int64) (*Account, error) {
	if account := fixture.accounts[id]; account != nil {
		return account, nil
	}
	return nil, ErrAccountCapabilityNotFound
}

func TestAccountCapabilityOverviewScopeUsesNamedDefaultsAndAccountDeepLink(t *testing.T) {
	folder := int64(91)
	admin := &capabilityOverviewAdminFixture{folders: []AccountManagementFolder{{ID: 70, Name: "dmxapi"}, {ID: 80, Name: "白嫖"}, {ID: 8, Name: "not-the-default"}},
		accounts: map[int64]*Account{10: {ID: 10, ManagementFolderID: &folder}}}
	service := &AccountCapabilityCatalogService{admin: admin}
	scope, err := service.normalizeCapabilityOverviewScope(context.Background(), CapabilityPublicationScope{})
	require.NoError(t, err)
	require.Equal(t, []int64{70, 80}, scope.FolderIDs)
	require.Equal(t, 1, admin.folderReads)
	scope, err = service.normalizeCapabilityOverviewScope(context.Background(), CapabilityPublicationScope{AccountIDs: []int64{10}})
	require.NoError(t, err)
	require.Equal(t, []int64{91}, scope.FolderIDs)
	require.Equal(t, []int64{10}, scope.AccountIDs)
	require.Equal(t, 1, admin.folderReads, "explicit account selection must not consult or intersect default folders")
}

func TestAccountCapabilityRecommendationIdempotencyTracksManualConfigurationChanges(t *testing.T) {
	row := capabilityOverviewFixtureRow(1, "claude-fable-5-1", "claude(非逆向渠道)", "messages", 101)
	group := capabilityOverviewFixtureGroup(23, row.GroupName, row.PublicModel)
	snapshot, scope := capabilityOverviewFixtureSnapshot([]AccountCapabilityCandidate{row}, group)
	snapshot.AccountPublicationRevisions = map[int64]string{1: "original-bindings-and-mappings"}
	request := &CapabilityPublicationRequest{Operation: "merge", Scope: scope, Groups: []CapabilityPublicationGroup{{ID: group.ID, Name: group.Name,
		Platform: group.Platform, RateMultiplier: group.RateMultiplier, Models: []CapabilityPublicationModel{{PublicModel: row.PublicModel, EvidenceIDs: []int64{101}}}}}}
	first, err := capabilityRecommendationPreviewKey(request, snapshot)
	require.NoError(t, err)
	request.IdempotencyKey = first
	repeated, err := capabilityRecommendationPreviewKey(request, snapshot)
	require.NoError(t, err)
	require.Equal(t, first, repeated)
	snapshot.AccountPublicationRevisions[1] = "manually-changed-binding-or-selector"
	accountChanged, err := capabilityRecommendationPreviewKey(request, snapshot)
	require.NoError(t, err)
	require.NotEqual(t, first, accountChanged, "routing fingerprint deliberately excludes bindings and mappings; their separate revision must change the plan key")
	group.ManagedModelRoutes.Routes = nil // Browser removes the route, not its old successful evidence.
	changed, err := capabilityRecommendationPreviewKey(request, snapshot)
	require.NoError(t, err)
	require.NotEqual(t, accountChanged, changed)
	require.True(t, strings.HasPrefix(changed, "cap-plan-v2-"))
}

func TestAccountCapabilityOverviewDoesNotOfferUnexecutableProbe(t *testing.T) {
	ws := capabilityOverviewFixtureRow(1, "claude-fable-5", "claude(非逆向渠道)", "responses_websocket", 0)
	grok := capabilityOverviewFixtureRow(2, "grok-4.5", "grok", "responses", 0)
	snapshot, scope := capabilityOverviewFixtureSnapshot([]AccountCapabilityCandidate{ws, grok}, capabilityOverviewFixtureGroup(23, ws.GroupName),
		capabilityOverviewFixtureGroup(35, capabilityUnclassifiedGroup, "unrelated-private-model"))
	overview, err := buildAccountCapabilityOverview(scope, snapshot, nil, "", nil)
	require.NoError(t, err)
	require.Equal(t, "view", capabilityOverviewFindModel(t, capabilityOverviewFindGroup(t, overview, ws.GroupName), ws.PublicModel).Action)
	unassigned := capabilityOverviewFindModel(t, capabilityOverviewFindGroup(t, overview, capabilityUnclassifiedGroup), grok.PublicModel)
	require.Equal(t, "view", unassigned.Action)
	require.Contains(t, unassigned.Reasons, "group_missing")
	recommendation, err := buildAccountCapabilityRecommendation(scope, snapshot, overview, AccountCapabilityRecommendationRequest{Scope: scope})
	require.NoError(t, err)
	require.Nil(t, recommendation.ProbeRequest)
	require.Zero(t, recommendation.MaximumRequestCount)
}
