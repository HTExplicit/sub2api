package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

func TestCapabilityPublicationCompositePriceProjectionPreservesExactAmounts(t *testing.T) {
	input, output, cache, multiplier := 0.000002, 0.000010, 0.000001, 2.5
	original := ChannelModelPricing{ID: 11, ChannelID: 41, Platform: PlatformAnthropic, Models: []string{"claude-fable-5-1"},
		InputPrice: &input, OutputPrice: &output, CacheReadPrice: &cache, FastMultiplier: &multiplier,
		Intervals: []PricingInterval{{ID: 31, PricingID: 11, MinTokens: 200000, InputPrice: &output}}}
	unrelated := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"gpt-6-astra"}, InputPrice: &output, OutputPrice: &output}
	gs := &CapabilityPublicationGroupSnapshot{Group: &Group{Platform: PlatformComposite}, Channel: &Channel{ModelPricing: []ChannelModelPricing{original, unrelated}}}
	gp := CapabilityPublicationGroupPatch{Platform: PlatformComposite, ChannelPricing: publicationClone(gs.Channel.ModelPricing),
		ManagedModelRoutes: domain.ManagedModelRoutesConfig{Version: 2, Routes: []domain.ManagedModelRoute{{PublicModel: "claude-fable-5-1", Branches: []domain.ManagedModelRouteBranch{
			{TargetPlatform: PlatformAnthropic}, {TargetPlatform: PlatformOpenAI},
		}}}}}
	before, _ := json.Marshal(gs)
	publicationProjectCompositePrices(&gp, gs)
	if len(gp.ChannelPricing) != 3 || !reflect.DeepEqual(gp.ChannelPricing[:2], gs.Channel.ModelPricing) {
		t.Fatal("projection overwrote an existing platform price or unrelated model")
	}
	projected := gp.ChannelPricing[2]
	if projected.Platform != PlatformOpenAI || !reflect.DeepEqual(projected.Models, []string{"claude-fable-5-1"}) ||
		*projected.InputPrice != input || *projected.OutputPrice != output || *projected.CacheReadPrice != cache || *projected.FastMultiplier != multiplier ||
		projected.Intervals[0].MinTokens != 200000 || *projected.Intervals[0].InputPrice != output {
		t.Fatalf("new adapter branch changed the public product price: %#v", projected)
	}
	publicationProjectCompositePrices(&gp, gs)
	if len(gp.ChannelPricing) != 3 {
		t.Fatal("idempotent projection duplicated the same platform price")
	}
	after, _ := json.Marshal(gs)
	if string(before) != string(after) {
		t.Fatal("projection modified the locked snapshot")
	}
	gp.ManagedModelRoutes.Routes[0].PublicModel = "unknown-unpriced-model"
	publicationProjectCompositePrices(&gp, gs)
	if len(gp.ChannelPricing) != 3 {
		t.Fatal("projection invented a price for an unknown model")
	}
}

func TestCapabilityPublicationMergePreservesManualDispatchAndDefault(t *testing.T) {
	snap := publicationV2MergeSnapshot()
	g := snap.Groups[23].Group
	g.DefaultMappedModel = "private-default"
	g.AllowMessagesDispatch = false
	g.MessagesDispatchModelConfig.ExactModelMappings["manual-alias"] = "manual-target"
	snap.Groups[23].Channel.ModelMapping[PlatformOpenAI]["manual-alias"] = "manual-target"
	snap.Groups[23].Routes = []CompositeModelRoute{{ID: 999, GroupID: g.ID, PublicModel: "manual-*", MatchType: "prefix", TargetPlatform: PlatformAnthropic, UpstreamModel: "manual-target", Endpoint: "messages", Priority: 8, Enabled: false, Notes: "manual rule"}}
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	patch := plan.Groups[0]
	if patch.DefaultMappedModel != "private-default" || patch.AllowMessagesDispatch || patch.MessagesDispatch.ExactModelMappings["manual-alias"] != "manual-target" ||
		patch.ChannelMapping[PlatformOpenAI]["manual-alias"] != "manual-target" || !reflect.DeepEqual(patch.CompositeRoutes, snap.Groups[23].Routes) {
		t.Fatal("managed model merge changed manual routing or feature switches")
	}
}

func TestCapabilityPublicationEmptyLegacyDraftDoesNotEnableManagedRouting(t *testing.T) {
	snap := publicationTestSnapshot()
	snap.Request.Groups[0].Models = nil
	snap.Groups[23].Group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"private-existing-model"}}
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil || len(plan.Groups) != 0 || len(plan.Accounts) != 0 || len(plan.Changes) != 0 {
		t.Fatalf("empty legacy draft was not a no-op: plan=%#v err=%v", plan, err)
	}
}

func TestCapabilityPublicationQuotaAndOwnedProjectionPreserveExistingDecisions(t *testing.T) {
	snap := publicationV2MergeSnapshot()
	group := snap.Groups[23].Group
	group.Platform, group.WirePlatform = PlatformComposite, PlatformComposite
	snap.Request.Groups[0].Platform = PlatformComposite
	// Retain the original quota platform even if a model is later routed by
	// another adapter; the execution branch must not change the user's quota.
	group.ManagedModelRoutes.Routes[0].QuotaPlatform = PlatformAnthropic
	group.MessagesDispatchModelConfig.ExactModelMappings["gpt-5.6-sol"] = "manual-override"
	snap.Groups[23].Channel.ModelMapping[PlatformOpenAI]["gpt-5.6-sol"] = "manual-channel-override"
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	patch := plan.Groups[0]
	old := publicationV2Route(t, patch, "gpt-5.6-sol")
	added := publicationV2Route(t, patch, "gpt-6-astra")
	if old.QuotaPlatform != PlatformAnthropic || added.QuotaPlatform != PlatformOpenAI {
		t.Fatalf("quota identity changed with publication branches: old=%q added=%q", old.QuotaPlatform, added.QuotaPlatform)
	}
	if patch.MessagesDispatch.ExactModelMappings["gpt-5.6-sol"] != "manual-override" || patch.ChannelMapping[PlatformOpenAI]["gpt-5.6-sol"] != "manual-channel-override" {
		t.Fatal("publication projection overwrote manually configured entries it did not own")
	}
}

func TestCapabilityPublicationDoesNotPromoteWebSocketEvidenceIntoHTTPBranch(t *testing.T) {
	snap := publicationTestSnapshot()
	snap.Accounts[7].Account.Extra["openai_responses_mode"] = "force_responses"
	evidence := snap.Evidence[11]
	evidence.Protocol = "responses_websocket"
	evidence.ConfigFingerprint = ManagedModelAccountFingerprint(snap.Accounts[7].Account)
	evidence.Result = json.RawMessage(`{"status":"alive","protocol":"responses_websocket","profile":"text","upstream_model":"gpt-6-astra","request_count":1}`)
	snap.Evidence[11] = evidence
	if _, err := (&AccountCapabilityPublicationService{}).build(snap); err == nil {
		t.Fatal("WebSocket evidence created an unsupported managed HTTP branch")
	}
}
