package service

import (
	"encoding/json"
	"testing"
)

func TestCapabilityPublicationNativeDeepseekReusesAllExistingBasicWires(t *testing.T) {
	snap := publicationTestSnapshot()
	account := snap.Accounts[7].Account
	account.Platform, account.WirePlatform = PlatformDeepseek, PlatformDeepseek
	account.Credentials["api_protocol"] = APIProtocolAdaptive
	group := snap.Groups[23].Group
	group.Name, snap.Request.Groups[0].Name = "deepseek", "deepseek"
	group.ModelPricing = nil
	snap.Request.Groups[0].Models = nil
	price := 0.000001
	protocols := []string{AccountCapabilityProtocolResponses, AccountCapabilityProtocolChatCompletions, AccountCapabilityProtocolMessages}
	evidenceID := int64(11)
	for _, model := range []string{"deepseek-v4-pro", "deepseek-v4-flash"} {
		group.ModelPricing = append(group.ModelPricing, ChannelModelPricing{Models: []string{model}, InputPrice: &price, OutputPrice: &price})
		selected := CapabilityPublicationModel{PublicModel: model, EvidenceIDs: []int64{}}
		for _, protocol := range protocols {
			publicationV2Evidence(t, snap, evidenceID, account.ID, model, protocol)
			selected.EvidenceIDs = append(selected.EvidenceIDs, evidenceID)
			evidenceID++
		}
		snap.Request.Groups[0].Models = append(snap.Request.Groups[0].Models, selected)
	}
	before, _ := json.Marshal(account)
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Groups) != 1 || len(plan.Accounts) != 1 || len(plan.Accounts[0].ModelMapping) != 6 {
		t.Fatalf("native account's existing successes were lost: %#v", plan)
	}
	for _, route := range plan.Groups[0].ManagedModelRoutes.Routes {
		branches := ManagedModelRouteBranches(route)
		if len(branches) != 3 {
			t.Fatalf("a verified native wire was filtered: %#v", route)
		}
		seen := map[string]bool{}
		for _, branch := range branches {
			if branch.TargetPlatform != PlatformDeepseek || len(branch.Accounts) != 1 || branch.Accounts[0].AccountID != account.ID ||
				len(branch.Endpoints) != 3 || !publicationHasString(protocols, branch.UpstreamProtocol) {
				t.Fatalf("native branch was classified by model manufacturer instead of supported adapter: %#v", branch)
			}
			seen[branch.UpstreamProtocol] = true
		}
		if len(seen) != 3 {
			t.Fatal("distinct native protocol successes were merged incorrectly")
		}
	}
	after, _ := json.Marshal(account)
	if string(before) != string(after) {
		t.Fatal("publication changed the account's private mappings or configured protocol")
	}
}

func TestCapabilityPublicationNativeCNCompiledRoutesAreUsable(t *testing.T) {
	for _, fixture := range []struct {
		platform, group, model string
		protocols              []string
	}{
		{PlatformKimi, "kimi", "kimi-k3", []string{"responses", "chat_completions", "messages"}},
		{PlatformMiniMax, "MiniMax", "MiniMax-M3", []string{"responses", "chat_completions", "messages"}},
		{PlatformZhipu, "glm", "glm-5.3", []string{"chat_completions", "messages"}},
	} {
		t.Run(fixture.platform, func(t *testing.T) {
			snap := publicationTestSnapshot()
			account := snap.Accounts[7].Account
			account.Platform, account.WirePlatform = fixture.platform, fixture.platform
			account.Credentials["api_protocol"] = APIProtocolAdaptive
			group := snap.Groups[23].Group
			group.Name, snap.Request.Groups[0].Name = fixture.group, fixture.group
			group.ModelPricing[0].Models = []string{fixture.model}
			selected := CapabilityPublicationModel{PublicModel: fixture.model}
			for i, protocol := range fixture.protocols {
				id := int64(11 + i)
				publicationV2Evidence(t, snap, id, account.ID, fixture.model, protocol)
				selected.EvidenceIDs = append(selected.EvidenceIDs, id)
			}
			snap.Request.Groups[0].Models = []CapabilityPublicationModel{selected}
			before, _ := json.Marshal(account)
			plan, err := (&AccountCapabilityPublicationService{}).build(snap)
			if err != nil {
				t.Fatal(err)
			}
			route := publicationV2Route(t, publicationV2Group(t, plan, 23), fixture.model)
			if len(route.Branches) != len(fixture.protocols) {
				t.Fatalf("publication discarded a supported successful CN wire: %#v", route)
			}
			compiled := *group
			compiled.ManagedModelRoutes = plan.Groups[0].ManagedModelRoutes
			for _, endpoint := range []string{"responses", "chat_completions", "messages"} {
				if request, err := ResolveManagedModelRoute(&compiled, fixture.model, endpoint); err != nil || request == nil {
					t.Fatalf("published CN branch is not usable by the shared runtime adapter on %s: %v", endpoint, err)
				}
			}
			after, _ := json.Marshal(account)
			if string(before) != string(after) {
				t.Fatal("publication changed the account's private mappings or configured wire")
			}
		})
	}
}
