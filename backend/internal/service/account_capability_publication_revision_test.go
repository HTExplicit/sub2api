package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func publicationRevisionFixture() *CapabilityPublicationSnapshot {
	snapshot := publicationV2MergeSnapshot()
	snapshot.Request.ExpectedConfigRevisions = &CapabilityPublicationInputRevisions{Accounts: map[int64]string{}, Groups: map[int64]string{}}
	for _, id := range snapshot.Request.Scope.AccountIDs {
		snapshot.Request.ExpectedConfigRevisions.Accounts[id] = capabilityPublicationSnapshotAccountRevision(snapshot.Accounts[id])
	}
	for id, group := range snapshot.Groups {
		snapshot.Request.ExpectedConfigRevisions.Groups[id] = CapabilityPublicationGroupInputRevision(group.Group, group.Channel)
	}
	return snapshot
}

func TestCapabilityPublicationInputRevisionRejectsOrganizerDrift(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		change func(*CapabilityPublicationSnapshot)
	}{
		{"account scheduling", func(s *CapabilityPublicationSnapshot) {
			s.Accounts[7].Account.Schedulable = !s.Accounts[7].Account.Schedulable
		}},
		{"private mapping", func(s *CapabilityPublicationSnapshot) {
			s.Accounts[7].Account.Credentials["model_mapping"] = map[string]any{"private-model": "browser-edited-target"}
		}},
		{"private binding priority", func(s *CapabilityPublicationSnapshot) { s.Accounts[7].Bindings[55] = 99 }},
		{"retained model removed in browser", func(s *CapabilityPublicationSnapshot) { s.Groups[23].Group.ManagedModelRoutes.Routes = nil }},
		{"group pricing mode", func(s *CapabilityPublicationSnapshot) {
			s.Groups[23].Group.LongContextPricingEnabled = !s.Groups[23].Group.LongContextPricingEnabled
		}},
		{"channel mapping", func(s *CapabilityPublicationSnapshot) {
			s.Groups[23].Channel.ModelMapping[PlatformOpenAI]["private-channel-model"] = "browser-edited-target"
		}},
		{"incomplete account guard", func(s *CapabilityPublicationSnapshot) { delete(s.Request.ExpectedConfigRevisions.Accounts, 8) }},
		{"incomplete group guard", func(s *CapabilityPublicationSnapshot) { delete(s.Request.ExpectedConfigRevisions.Groups, 23) }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			snapshot := publicationRevisionFixture()
			fixture.change(snapshot)
			if _, err := (&AccountCapabilityPublicationService{}).build(snapshot); !errors.Is(err, ErrCapabilityPublicationConflict) {
				t.Fatalf("organizer impact was accepted after its input changed: %v", err)
			}
		})
	}
}

func TestCapabilityPublicationInputRevisionAcceptsUnchangedAndTemporaryRuntimeState(t *testing.T) {
	snapshot := publicationRevisionFixture()
	later := time.Now().Add(time.Hour)
	snapshot.Accounts[7].Account.LastUsedAt = &later
	snapshot.Accounts[7].Account.OverloadUntil = &later
	snapshot.Accounts[7].Account.RateLimitResetAt = &later
	if _, err := (&AccountCapabilityPublicationService{}).build(snapshot); err != nil {
		t.Fatalf("temporary runtime state invalidated unchanged publication inputs: %v", err)
	}
	// Old hand-written previews remain compatible; Apply still protects their
	// independently frozen repository snapshot and never overwrites new state.
	snapshot.Request.ExpectedConfigRevisions = nil
	if _, err := (&AccountCapabilityPublicationService{}).build(snapshot); err != nil {
		t.Fatalf("an optional organizer guard became mandatory for legacy previews: %v", err)
	}
}

func TestCapabilityPublicationInputRevisionMatchesCatalogAndLockedBindings(t *testing.T) {
	snapshot := publicationV2MergeSnapshot()
	account := *snapshot.Accounts[8].Account
	account.GroupIDs = []int64{55, 23}
	account.AccountGroups = []AccountGroup{{AccountID: account.ID, GroupID: 55, Priority: 17}, {AccountID: account.ID, GroupID: 23, Priority: 31}}
	if got, want := CapabilityPublicationAccountInputRevision(&account), capabilityPublicationSnapshotAccountRevision(snapshot.Accounts[8]); got != want {
		t.Fatalf("catalogue and locked SQL account views produced different guards: %q != %q", got, want)
	}
}

func TestCapabilityPublicationInputRevisionNormalizesRepositoryChannelShapes(t *testing.T) {
	group := &Group{ID: 23, Name: "fixture", Platform: PlatformOpenAI, WirePlatform: PlatformOpenAI, RateMultiplier: 0.2}
	zero := 0.0
	observed := time.Now()
	catalog := &Channel{ID: 41, Status: StatusActive, BillingModelSource: BillingModelSourceChannelMapped,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {}},
		ModelPricing: []ChannelModelPricing{{ID: 17, ChannelID: 41, Models: []string{"fixture-model"},
			InputPrice: &zero, OutputPrice: &zero, TimePricing: &ChannelTimePricing{}, CreatedAt: observed, UpdatedAt: observed}},
		AccountStatsPricingRules: []AccountStatsPricingRule{{ID: 31, ChannelID: 41, Name: "existing rule", CreatedAt: observed, UpdatedAt: observed}},
	}
	locked := publicationClone(catalog)
	locked.BillingModelSource = ""
	locked.ModelMapping[PlatformOpenAI] = nil
	locked.ModelPricing[0].TimePricing = nil
	locked.ModelPricing[0].Intervals = []PricingInterval{}
	locked.AccountStatsPricingRules[0].CreatedAt, locked.AccountStatsPricingRules[0].UpdatedAt = time.Time{}, time.Time{}
	locked.AccountStatsPricingRules[0].Pricing = []ChannelModelPricing{}
	before, _ := json.Marshal([]any{catalog, locked})
	expected := CapabilityPublicationGroupInputRevision(group, catalog)
	if got := CapabilityPublicationGroupInputRevision(group, locked); got != expected {
		t.Fatalf("same channel settings read by JSON aggregate and ordinary SQL produced false conflict: %q != %q", got, expected)
	}
	after, _ := json.Marshal([]any{catalog, locked})
	if string(before) != string(after) {
		t.Fatal("revision normalization changed the actual channel settings")
	}
	locked.Status = "inactive"
	if CapabilityPublicationGroupInputRevision(group, locked) == expected {
		t.Fatal("changing channel activation did not invalidate the organizer input")
	}
	locked.Status = StatusActive
	locked.ModelPricing[0].InputPrice = nil
	if CapabilityPublicationGroupInputRevision(group, locked) == expected {
		t.Fatal("an explicit zero price was conflated with no identified input price")
	}
}
