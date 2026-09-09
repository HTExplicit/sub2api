package service

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

func publicationV2AddAccount(snap *CapabilityPublicationSnapshot, id int64, platform string, folderID int64) *Account {
	a := &Account{
		ID: id, Name: "publication fixture account", Platform: platform, WirePlatform: platform,
		Type: AccountTypeAPIKey, ManagementFolderID: &folderID, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"api_key": "fixture-only", "base_url": "https://provider.invalid",
			"model_mapping": map[string]any{"private-model": "private-upstream"},
		},
		Extra: map[string]any{},
	}
	if platform == PlatformOpenAI {
		a.Extra["openai_responses_mode"] = "force_chat_completions"
	}
	snap.Accounts[id] = &CapabilityPublicationAccountSnapshot{Account: a, Bindings: map[int64]int{55: 17}}
	return a
}

func publicationV2Evidence(t *testing.T, snap *CapabilityPublicationSnapshot, id, accountID int64, upstream, protocol string) CapabilityPublicationEvidence {
	t.Helper()
	a := snap.Accounts[accountID].Account
	now := time.Now()
	result, err := json.Marshal(publicationProbeResult{
		Status: "alive", Protocol: protocol, Profile: "text", UpstreamModel: upstream, RequestCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := CapabilityPublicationEvidence{
		ID: id, AccountID: accountID, FolderID: a.ManagementFolderID,
		ConfigFingerprint: ManagedModelAccountFingerprint(a), UpstreamModel: upstream, Protocol: protocol,
		Profile: "text", Status: "succeeded", RunKind: "probe", RunFolderIDs: []int64{*a.ManagementFolderID},
		RunAccountIDs: []int64{accountID}, Result: result, FinishedAt: &now,
	}
	snap.Evidence[id] = e
	return e
}

// The omitted model is deliberately a version-1 publication. A first merge
// after the upgrade must retain its route, exact selector and group membership.
func publicationV2MergeSnapshot() *CapabilityPublicationSnapshot {
	snap := publicationTestSnapshot()
	a := publicationV2AddAccount(snap, 8, PlatformOpenAI, 9)
	snap.Request.Scope.AccountIDs = []int64{7, 8}
	oldModel := "gpt-5.6-sol"
	selector := ManagedModelSelector(23, oldModel)
	a.Credentials["model_mapping"].(map[string]any)[selector] = oldModel
	snap.Accounts[8].Bindings[23] = 31
	snap.Groups[23].Bindings[8] = 31
	endpoints := []string{"chat_completions", "messages", "responses"}
	g := snap.Groups[23].Group
	g.ManagedModelRoutes = domain.ManagedModelRoutesConfig{
		Version: 1, Enabled: true,
		Routes: []domain.ManagedModelRoute{{
			PublicModel: oldModel, Aliases: []string{"gpt-5.6"}, Selector: selector,
			TargetPlatform: PlatformOpenAI, Endpoints: endpoints,
			Accounts: []domain.ManagedModelRouteAccount{{
				AccountID: 8, UpstreamModel: oldModel,
				AccountFingerprint: ManagedModelAccountFingerprint(a), Endpoints: endpoints,
			}},
		}},
	}
	g.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{oldModel}}
	g.AllowMessagesDispatch = true
	g.MessagesDispatchModelConfig = OpenAIMessagesDispatchModelConfig{
		ExactModelMappings: map[string]string{oldModel: selector, "gpt-5.6": selector},
	}
	price := 0.02
	g.ModelPricing = append(g.ModelPricing, ChannelModelPricing{
		Models: []string{oldModel}, InputPrice: &price, OutputPrice: &price,
	})
	snap.Groups[23].Channel = &Channel{
		ID: 41, Name: "existing-public-channel", BillingModelSource: BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {oldModel: selector, "gpt-5.6": selector}},
		ModelPricing: []ChannelModelPricing{{Models: []string{oldModel}, InputPrice: &price, OutputPrice: &price}},
		Features:     "[]", FeaturesConfig: map[string]any{"unchanged_fixture_feature": true},
		ApplyPricingToAccountStats: true,
	}
	return snap
}

func publicationV2Group(t *testing.T, plan *CapabilityPublicationPlan, groupID int64) CapabilityPublicationGroupPatch {
	t.Helper()
	for _, group := range plan.Groups {
		if group.GroupID == groupID {
			if group.ManagedModelRoutes.Version != 2 || !group.ManagedModelRoutes.Enabled {
				t.Fatalf("publication did not compile an enabled v2 config: %#v", group.ManagedModelRoutes)
			}
			for _, route := range group.ManagedModelRoutes.Routes {
				if route.Selector != "" || route.TargetPlatform != "" || len(route.Accounts) != 0 {
					t.Fatalf("v2 route retained ambiguous legacy selector/platform/members: %#v", route)
				}
			}
			return group
		}
	}
	t.Fatalf("group %d is missing from the plan", groupID)
	return CapabilityPublicationGroupPatch{}
}

func publicationV2Route(t *testing.T, group CapabilityPublicationGroupPatch, publicModel string) ManagedModelRoute {
	t.Helper()
	for _, route := range group.ManagedModelRoutes.Routes {
		if route.PublicModel == publicModel {
			return route
		}
	}
	t.Fatalf("public model %q is missing from the plan", publicModel)
	return ManagedModelRoute{}
}

func publicationV2AccountPatch(plan *CapabilityPublicationPlan, accountID int64) *CapabilityPublicationAccountPatch {
	for i := range plan.Accounts {
		if plan.Accounts[i].AccountID == accountID {
			return &plan.Accounts[i]
		}
	}
	return nil
}

func publicationV2AssertNoRemovals(t *testing.T, plan *CapabilityPublicationPlan) {
	t.Helper()
	for _, patch := range plan.Accounts {
		if len(patch.RemoveSelectors) != 0 || len(patch.RemoveGroupIDs) != 0 {
			t.Fatalf("merge produced an implicit removal: %#v", patch)
		}
	}
}

func TestCapabilityPublicationV2MergePreservesOmittedModelAndMember(t *testing.T) {
	for _, operation := range []string{"", "merge"} {
		t.Run("operation="+operation, func(t *testing.T) {
			snap := publicationV2MergeSnapshot()
			snap.Request.Operation = operation
			before, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateCapabilityPublicationRequest(snap.Request); err != nil {
				t.Fatal(err)
			}
			plan, err := (&AccountCapabilityPublicationService{}).build(snap)
			if err != nil {
				t.Fatal(err)
			}
			group := publicationV2Group(t, plan, 23)
			if len(group.ManagedModelRoutes.Routes) != 2 || len(group.ModelAllowlist.Models) != 2 ||
				!publicationHasString(group.ModelAllowlist.Models, "gpt-5.6-sol") || !publicationHasString(group.ModelAllowlist.Models, "gpt-6-astra") {
				t.Fatalf("adding one model replaced the old model: %#v", group.ModelAllowlist)
			}
			old := publicationV2Route(t, group, "gpt-5.6-sol")
			branches := ManagedModelRouteBranches(old)
			if len(branches) != 1 || len(branches[0].Accounts) != 1 || branches[0].Accounts[0].AccountID != 8 || branches[0].Accounts[0].UpstreamModel != "gpt-5.6-sol" {
				t.Fatalf("the omitted verified member was not retained: %#v", old)
			}
			if branches[0].Selector != ManagedModelSelector(23, "gpt-5.6-sol") || !reflect.DeepEqual(old.Aliases, []string{"gpt-5.6"}) {
				t.Fatalf("retained publication lost its selector or alias: %#v", old)
			}
			publicationV2AssertNoRemovals(t, plan)
			if group.RateMultiplier != 0.2 || !reflect.DeepEqual(group.ChannelPricing, snap.Groups[23].Channel.ModelPricing) ||
				!reflect.DeepEqual(group.ChannelFeaturesConfig, snap.Groups[23].Channel.FeaturesConfig) || !group.ChannelApplyPricingToAccountStats {
				t.Fatal("merge changed an existing rate or channel configuration")
			}
			for _, patch := range plan.Accounts {
				if _, changed := patch.ModelMapping["private-model"]; changed || publicationHasID(patch.AddGroupIDs, 55) {
					t.Fatalf("merge rewrote private state: %#v", patch)
				}
			}
			after, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("building the preview mutated the source snapshot")
			}
		})
	}
}

func TestCapabilityPublicationV2NilModelsDoNotImplyRemoval(t *testing.T) {
	snap := publicationV2MergeSnapshot()
	snap.Request.Groups[0].Models = nil
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	group := publicationV2Group(t, plan, 23)
	if group.Status != StatusActive || len(group.ManagedModelRoutes.Routes) != 1 ||
		!reflect.DeepEqual(group.ModelAllowlist.Models, []string{"gpt-5.6-sol"}) {
		t.Fatalf("an empty selection withdrew an existing publication: %#v", group)
	}
	publicationV2AssertNoRemovals(t, plan)
}

func TestCapabilityPublicationV2OutOfScopeMembersRemainUntouched(t *testing.T) {
	snap := publicationV2MergeSnapshot()
	snap.Request.Scope.AccountIDs = []int64{7}
	outsideFolder := int64(99)
	snap.Accounts[8].Account.ManagementFolderID = &outsideFolder
	// This bound account has no managed route at all. Its unknown purpose must
	// not turn it into an implicit detachment just because it is absent in UI.
	publicationV2AddAccount(snap, 99, PlatformOpenAI, outsideFolder)
	snap.Accounts[99].Bindings[23] = 7
	snap.Accounts[99].Bindings[88] = 12
	snap.Groups[23].Bindings[99] = 7
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	group := publicationV2Group(t, plan, 23)
	publicationV2Route(t, group, "gpt-5.6-sol")
	publicationV2AssertNoRemovals(t, plan)
	for _, accountID := range []int64{8, 99} {
		if patch := publicationV2AccountPatch(plan, accountID); patch != nil &&
			(len(patch.ModelMapping) != 0 || len(patch.AddGroupIDs) != 0 || patch.Schedulable != nil) {
			t.Fatalf("an unselected outside account was modified: %#v", patch)
		}
	}
	if snap.Accounts[8].Bindings[23] != 31 || snap.Accounts[99].Bindings[88] != 12 {
		t.Fatal("preview mutated retained group priorities")
	}
}

func TestCapabilityPublicationV2WholeModelRemovalIsExplicit(t *testing.T) {
	snap := publicationV2MergeSnapshot()
	snap.Request.Groups[0].RemoveModels = []string{"gpt-5.6-sol"}
	if err := validateCapabilityPublicationRequest(snap.Request); err != nil {
		t.Fatal(err)
	}
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	group := publicationV2Group(t, plan, 23)
	if len(group.ManagedModelRoutes.Routes) != 1 || !reflect.DeepEqual(group.ModelAllowlist.Models, []string{"gpt-6-astra"}) {
		t.Fatalf("explicit removal did not coexist with an additive merge: %#v", group.ModelAllowlist)
	}
	removed := publicationV2AccountPatch(plan, 8)
	if removed == nil || !publicationHasString(removed.RemoveSelectors, ManagedModelSelector(23, "gpt-5.6-sol")) {
		t.Fatal("explicitly removed model retained its owned selector")
	}
	if publicationHasID(removed.RemoveGroupIDs, 55) || removed.Schedulable != nil {
		t.Fatalf("model removal altered a private binding or account scheduling: %#v", removed)
	}
	added := publicationV2AccountPatch(plan, 7)
	if added == nil || len(added.RemoveSelectors) != 0 || len(added.RemoveGroupIDs) != 0 {
		t.Fatalf("explicit model removal escaped into the newly added model: %#v", added)
	}
}

func TestCapabilityPublicationV2LineRemovalKeepsOtherTargetsAndMembers(t *testing.T) {
	snap := publicationTestSnapshot()
	snap.Request.Groups[0].Models = nil
	a := snap.Accounts[7].Account
	a.Schedulable = true
	snap.Accounts[7].Bindings[23] = 19
	snap.Groups[23].Bindings[7] = 19
	publicationV2AddAccount(snap, 8, PlatformOpenAI, 9)
	snap.Accounts[8].Bindings[23] = 31
	snap.Groups[23].Bindings[8] = 31
	snap.Request.Scope.AccountIDs = []int64{7, 8}
	endpoints := []string{"chat_completions", "messages", "responses"}
	route := domain.ManagedModelRoute{PublicModel: "gpt-6-astra", Endpoints: endpoints}
	for _, upstream := range []string{"gpt-6-astra", "openai/gpt-6-astra"} {
		selector := ManagedModelBranchSelector(23, route.PublicModel, PlatformOpenAI, "chat_completions", upstream)
		branch := domain.ManagedModelRouteBranch{
			Selector: selector, TargetPlatform: PlatformOpenAI, UpstreamProtocol: "chat_completions", Endpoints: endpoints,
		}
		for _, accountID := range []int64{7, 8} {
			account := snap.Accounts[accountID].Account
			account.Credentials["model_mapping"].(map[string]any)[selector] = upstream
			branch.Accounts = append(branch.Accounts, domain.ManagedModelRouteAccount{
				AccountID: accountID, UpstreamModel: upstream, AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints,
			})
		}
		route.Branches = append(route.Branches, branch)
	}
	snap.Groups[23].Group.ManagedModelRoutes = domain.ManagedModelRoutesConfig{Version: 2, Enabled: true, Routes: []domain.ManagedModelRoute{route}}
	snap.Groups[23].Group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{route.PublicModel}}
	snap.Request.Groups[0].RemoveLines = []CapabilityPublicationLineRemoval{{
		PublicModel: route.PublicModel, AccountID: 7, UpstreamModel: "openai/gpt-6-astra", Protocol: "chat_completions",
	}}
	if err := validateCapabilityPublicationRequest(snap.Request); err != nil {
		t.Fatal(err)
	}
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	group := publicationV2Group(t, plan, 23)
	retained := publicationV2Route(t, group, route.PublicModel)
	branches := ManagedModelRouteBranches(retained)
	if len(branches) != 2 {
		t.Fatalf("removing one account erased another target or account: %#v", retained)
	}
	memberCount := 0
	for _, branch := range branches {
		for _, member := range branch.Accounts {
			memberCount++
			if member.AccountID == 7 && member.UpstreamModel == "openai/gpt-6-astra" {
				t.Fatal("explicitly removed line is still a routing candidate")
			}
		}
	}
	if memberCount != 3 {
		t.Fatalf("removal changed more than the one selected line: %d members remain", memberCount)
	}
	patch := publicationV2AccountPatch(plan, 7)
	selector := ManagedModelBranchSelector(23, route.PublicModel, PlatformOpenAI, "chat_completions", "openai/gpt-6-astra")
	if patch == nil || !reflect.DeepEqual(patch.RemoveSelectors, []string{selector}) || len(patch.RemoveGroupIDs) != 0 || patch.Schedulable != nil {
		t.Fatalf("line removal changed the retained account's membership or other selectors: %#v", patch)
	}
	if other := publicationV2AccountPatch(plan, 8); other != nil && (len(other.RemoveSelectors) != 0 || len(other.RemoveGroupIDs) != 0) {
		t.Fatalf("line removal affected another verified member: %#v", other)
	}
}

func TestCapabilityPublicationV2CrossPlatformSuccessesAreAllRetained(t *testing.T) {
	for _, groupPlatform := range []string{PlatformOpenAI, PlatformComposite} {
		t.Run(groupPlatform, func(t *testing.T) {
			snap := publicationTestSnapshot()
			publicationV2AddAccount(snap, 8, PlatformAnthropic, 9)
			snap.Request.Scope.AccountIDs = []int64{7, 8}
			snap.Request.Groups[0].Name = "claude(非逆向渠道)"
			snap.Request.Groups[0].Platform = groupPlatform
			snap.Request.Groups[0].Models = []CapabilityPublicationModel{{PublicModel: "claude-fable-5-1", EvidenceIDs: []int64{11, 12}}}
			group := snap.Groups[23].Group
			group.Name, group.Platform, group.WirePlatform = snap.Request.Groups[0].Name, groupPlatform, groupPlatform
			group.ModelPricing[0].Models = []string{"claude-fable-5-1"}
			publicationV2Evidence(t, snap, 11, 7, "claude-fable-5-1", "chat_completions")
			publicationV2Evidence(t, snap, 12, 8, "claude-fable-5-1", "messages")
			plan, err := (&AccountCapabilityPublicationService{}).build(snap)
			if err != nil {
				t.Fatal(err)
			}
			route := publicationV2Route(t, publicationV2Group(t, plan, 23), "claude-fable-5-1")
			branches := ManagedModelRouteBranches(route)
			got := map[string]int64{}
			for _, branch := range branches {
				if len(branch.Accounts) != 1 || !publicationHasString(branch.Endpoints, "messages") {
					t.Fatalf("successful compatible wire was not compiled into ingress: %#v", branch)
				}
				got[branch.TargetPlatform+"/"+branch.UpstreamProtocol] = branch.Accounts[0].AccountID
			}
			want := map[string]int64{PlatformOpenAI + "/chat_completions": 7, PlatformAnthropic + "/messages": 8}
			if len(branches) != 2 || !reflect.DeepEqual(got, want) {
				t.Fatalf("platform preference discarded successful fallback evidence: got %#v, want %#v", got, want)
			}
			for _, accountID := range []int64{7, 8} {
				patch := publicationV2AccountPatch(plan, accountID)
				if patch == nil || len(patch.ModelMapping) != 1 || !publicationHasID(patch.AddGroupIDs, 23) {
					t.Fatalf("a successful account is absent from effective publication: %#v", patch)
				}
			}
		})
	}
}

func TestCapabilityPublicationV2SameAccountDistinctTargetsUseIndependentSelectors(t *testing.T) {
	for _, fixture := range []struct {
		name, tier string
		targets    []string
	}{
		{name: "recognized provider namespace", targets: []string{"gpt-6-astra", "openai/gpt-6-astra"}},
		{name: "distinct vip upstream products", tier: "vip", targets: []string{"gpt-6-astra-vip", "gpt-6-astra-ssvip"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			snap := publicationTestSnapshot()
			snap.Request.Groups[0].Models[0].Tier = fixture.tier
			snap.Request.Groups[0].Models[0].EvidenceIDs = []int64{11, 12}
			if fixture.tier == "vip" {
				snap.Request.Groups[0].Name, snap.Groups[23].Group.Name = "gpt-vip", "gpt-vip"
			}
			for i, target := range fixture.targets {
				publicationV2Evidence(t, snap, int64(11+i), 7, target, "chat_completions")
			}
			plan, err := (&AccountCapabilityPublicationService{}).build(snap)
			if err != nil {
				t.Fatal(err)
			}
			route := publicationV2Route(t, publicationV2Group(t, plan, 23), "gpt-6-astra")
			branches := ManagedModelRouteBranches(route)
			if len(branches) != 2 {
				t.Fatalf("same-account targets were overwritten or discarded: %#v", route)
			}
			patch := publicationV2AccountPatch(plan, 7)
			if patch == nil || len(patch.ModelMapping) != 2 {
				t.Fatalf("independent actual targets did not receive separate mappings: %#v", patch)
			}
			seen := map[string]bool{}
			for _, branch := range branches {
				if len(branch.Accounts) != 1 || branch.Accounts[0].AccountID != 7 {
					t.Fatalf("unexpected branch membership: %#v", branch)
				}
				target := branch.Accounts[0].UpstreamModel
				wantSelector := ManagedModelBranchSelector(23, route.PublicModel, PlatformOpenAI, "chat_completions", target)
				if !publicationHasString(fixture.targets, target) || branch.Selector != wantSelector || seen[branch.Selector] || patch.ModelMapping[branch.Selector] != target {
					t.Fatalf("branch selector collided or did not preserve the actual target: %#v", branch)
				}
				seen[branch.Selector] = true
			}
		})
	}
}

func TestCapabilityPublicationV2HistoricalSameConfigSuccessDoesNotExpire(t *testing.T) {
	snap := publicationTestSnapshot()
	evidence := snap.Evidence[11]
	old := time.Now().Add(-90 * 24 * time.Hour)
	evidence.FinishedAt = &old
	snap.Evidence[11] = evidence
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatalf("unchanged configuration's successful basic probe was expired: %v", err)
	}
	route := publicationV2Route(t, publicationV2Group(t, plan, 23), "gpt-6-astra")
	branches := ManagedModelRouteBranches(route)
	if len(branches) != 1 || len(branches[0].Accounts) != 1 || branches[0].Accounts[0].AccountFingerprint != evidence.ConfigFingerprint {
		t.Fatal("historical success was not reused with its original configuration identity")
	}
}

func TestCapabilityPublicationV2DuplicateActualTargetAndProtocolIsOneLine(t *testing.T) {
	snap := publicationTestSnapshot()
	publicationV2Evidence(t, snap, 12, 7, "gpt-6-astra", "chat_completions")
	snap.Request.Groups[0].Models[0].EvidenceIDs = []int64{11, 12}
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	route := publicationV2Route(t, publicationV2Group(t, plan, 23), "gpt-6-astra")
	branches := ManagedModelRouteBranches(route)
	patch := publicationV2AccountPatch(plan, 7)
	if len(branches) != 1 || len(branches[0].Accounts) != 1 || patch == nil || len(patch.ModelMapping) != 1 {
		t.Fatalf("duplicate evidence created duplicate lines or selector mappings: route=%#v, patch=%#v", route, patch)
	}
}

func TestCapabilityPublicationV2RejectsLegacyWholeGroupReplace(t *testing.T) {
	req := publicationTestSnapshot().Request
	req.Operation = "replace"
	if err := validateCapabilityPublicationRequest(req); err == nil {
		t.Fatal("the legacy replace operation can still silently remove unselected models")
	}
}
