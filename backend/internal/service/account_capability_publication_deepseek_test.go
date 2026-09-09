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
