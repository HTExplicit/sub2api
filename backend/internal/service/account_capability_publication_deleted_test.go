package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

func publicationDeletedTestSnapshot(missing bool) *CapabilityPublicationSnapshot {
	snap := publicationV2MergeSnapshot()
	a := snap.Accounts[8]
	deletedAt := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	d := &CapabilityPublicationDeletedAccountSnapshot{
		Missing: missing, FolderID: a.Account.ManagementFolderID,
		Platform: a.Account.Platform, ProviderProfile: a.Account.ProviderProfile,
		Bindings: publicationClone(a.Bindings),
	}
	if missing {
		d.Evidence = &CapabilityPublicationDeletedAccountEvidence{
			ID: 91, FolderID: a.Account.ManagementFolderID, ConfigFingerprint: ManagedModelAccountFingerprint(a.Account),
			RunFolderIDs: []int64{9}, RunAccountIDs: []int64{8},
		}
	} else {
		d.DeletedAt = &deletedAt
	}
	snap.DeletedAccounts = map[int64]*CapabilityPublicationDeletedAccountSnapshot{8: d}
	delete(snap.Accounts, 8)
	return snap
}

func TestCapabilityPublicationDeletedExplicitRemovalPreservesLiveAndPrivateState(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, wholeModel := range []bool{false, true} {
			name := map[bool]string{false: "soft_deleted", true: "physically_missing"}[missing] + "/" + map[bool]string{false: "line", true: "model"}[wholeModel]
			t.Run(name, func(t *testing.T) {
				snap := publicationDeletedTestSnapshot(missing)
				if wholeModel {
					snap.Request.Groups[0].RemoveModels = []string{"gpt-5.6-sol"}
				} else {
					snap.Request.Groups[0].RemoveLines = []CapabilityPublicationLineRemoval{{PublicModel: "gpt-5.6-sol", AccountID: 8, UpstreamModel: "gpt-5.6-sol", Protocol: ""}}
				}
				before, _ := json.Marshal(snap)
				if err := validateCapabilityPublicationRequest(snap.Request); err != nil {
					t.Fatal(err)
				}
				plan, err := (&AccountCapabilityPublicationService{}).build(snap)
				if err != nil {
					t.Fatal(err)
				}
				group := publicationV2Group(t, plan, 23)
				if len(group.ManagedModelRoutes.Routes) != 1 || !reflect.DeepEqual(group.ModelAllowlist.Models, []string{"gpt-6-astra"}) || group.Status != StatusActive || group.RateMultiplier != 0.2 {
					t.Fatalf("deleted cleanup affected another model, status or rate: %#v", group)
				}
				if !reflect.DeepEqual(group.ChannelPricing, snap.Groups[23].Channel.ModelPricing) {
					t.Fatal("deleted cleanup changed existing prices")
				}
				dead := publicationV2AccountPatch(plan, 8)
				if dead == nil || !reflect.DeepEqual(dead.RemoveGroupIDs, []int64{23}) || len(dead.RemoveSelectors) != 0 || len(dead.ModelMapping) != 0 || len(dead.AddGroupIDs) != 0 || dead.Schedulable != nil {
					t.Fatalf("deleted account patch exceeded its surviving public binding: %#v", dead)
				}
				live := publicationV2AccountPatch(plan, 7)
				if live == nil || len(live.RemoveSelectors) != 0 || len(live.RemoveGroupIDs) != 0 {
					t.Fatalf("cleanup escaped into the live publication: %#v", live)
				}
				after, _ := json.Marshal(snap)
				if string(before) != string(after) || snap.DeletedAccounts[8].Bindings[55] != 17 {
					t.Fatal("preview mutated its original snapshot or a private binding")
				}
			})
		}
	}
}

func TestCapabilityPublicationDeletedLineRemovalKeepsOtherMembers(t *testing.T) {
	snap := publicationDeletedTestSnapshot(true)
	other := publicationV2AddAccount(snap, 9, PlatformOpenAI, 99)
	snap.Accounts[9].Bindings[23] = 41
	snap.Groups[23].Bindings[9] = 41
	old := &snap.Groups[23].Group.ManagedModelRoutes.Routes[0]
	old.Accounts = append(old.Accounts, domain.ManagedModelRouteAccount{
		AccountID: 9, UpstreamModel: "gpt-5.6-sol", AccountFingerprint: ManagedModelAccountFingerprint(other), Endpoints: old.Endpoints,
	})
	snap.Request.Groups[0].RemoveLines = []CapabilityPublicationLineRemoval{{PublicModel: old.PublicModel, AccountID: 8, UpstreamModel: "gpt-5.6-sol"}}
	// Account deletion may already have removed its public binding. The route
	// subtraction must then succeed without any patch to the absent account.
	delete(snap.Groups[23].Bindings, 8)
	delete(snap.DeletedAccounts[8].Bindings, 23)
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	retained := publicationV2Route(t, publicationV2Group(t, plan, 23), old.PublicModel)
	branches := ManagedModelRouteBranches(retained)
	if len(branches) != 1 || len(branches[0].Accounts) != 1 || branches[0].Accounts[0].AccountID != 9 || !reflect.DeepEqual(retained.Aliases, old.Aliases) {
		t.Fatalf("deleting a stale line erased a retained outside-scope member or alias: %#v", retained)
	}
	if publicationV2AccountPatch(plan, 8) != nil || publicationV2AccountPatch(plan, 9) != nil {
		t.Fatal("route-only cleanup modified an absent account or the retained outside account")
	}
}

func TestCapabilityPublicationDeletedMergeRetainsUnselectedUnprovenReferences(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "soft_deleted", true: "physically_missing"}[missing], func(t *testing.T) {
			snap := publicationDeletedTestSnapshot(missing)
			snap.Request.Scope.AccountIDs = []int64{7}
			snap.DeletedAccounts[8].FolderID = nil
			snap.DeletedAccounts[8].Evidence = nil
			plan, err := (&AccountCapabilityPublicationService{}).build(snap)
			if err != nil {
				t.Fatal(err)
			}
			old := publicationV2Route(t, publicationV2Group(t, plan, 23), "gpt-5.6-sol")
			members := ManagedModelRouteBranches(old)[0].Accounts
			if len(members) != 1 || members[0].AccountID != 8 || publicationV2AccountPatch(plan, 8) != nil {
				t.Fatal("a merge implicitly pruned an unselected, unproven deleted reference")
			}
			publicationV2AssertNoRemovals(t, plan)
		})
	}
}

func TestCapabilityPublicationDeletedRemovalRejectsUnprovenScope(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		missing bool
		change  func(*CapabilityPublicationSnapshot)
	}{
		{"soft unknown folder", false, func(s *CapabilityPublicationSnapshot) { s.DeletedAccounts[8].FolderID = nil }},
		{"soft outside folder", false, func(s *CapabilityPublicationSnapshot) { id := int64(99); s.DeletedAccounts[8].FolderID = &id }},
		{"soft missing deletion marker", false, func(s *CapabilityPublicationSnapshot) { s.DeletedAccounts[8].DeletedAt = nil }},
		{"soft derived account", false, func(s *CapabilityPublicationSnapshot) { id := int64(3); s.DeletedAccounts[8].ParentAccountID = &id }},
		{"soft protected platform", false, func(s *CapabilityPublicationSnapshot) { s.DeletedAccounts[8].Platform = PlatformCindy }},
		{"missing provenance", true, func(s *CapabilityPublicationSnapshot) { s.DeletedAccounts[8].Evidence = nil }},
		{"missing latest scope outside", true, func(s *CapabilityPublicationSnapshot) {
			id := int64(99)
			d := s.DeletedAccounts[8]
			d.FolderID, d.Evidence.FolderID = &id, &id
		}},
		{"missing ledger run account", true, func(s *CapabilityPublicationSnapshot) { s.DeletedAccounts[8].Evidence.RunAccountIDs = []int64{7} }},
		{"missing ledger run folder", true, func(s *CapabilityPublicationSnapshot) { s.DeletedAccounts[8].Evidence.RunFolderIDs = []int64{99} }},
		{"missing changed configuration", true, func(s *CapabilityPublicationSnapshot) {
			s.DeletedAccounts[8].Evidence.ConfigFingerprint = "different-configuration"
		}},
		{"missing ambiguous reference", true, func(s *CapabilityPublicationSnapshot) {
			route := publicationClone(s.Groups[23].Group.ManagedModelRoutes.Routes[0])
			route.Accounts[0].AccountFingerprint = "different-configuration"
			s.Groups[23].Group.ManagedModelRoutes.Routes = append(s.Groups[23].Group.ManagedModelRoutes.Routes, route)
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			snap := publicationDeletedTestSnapshot(fixture.missing)
			snap.Request.Groups[0].RemoveModels = []string{"gpt-5.6-sol"}
			fixture.change(snap)
			if _, err := (&AccountCapabilityPublicationService{}).build(snap); !errors.Is(err, ErrCapabilityPublicationConflict) {
				t.Fatalf("deleted account with unproven origin was accepted: %v", err)
			}
			// The live-account explicit-detachment exception must not let a
			// tombstone with unknown origin bypass the folder check.
			snap.Request.Scope.AccountIDs = []int64{7}
			snap.Request.DetachAccountIDs = []int64{8}
			if _, err := (&AccountCapabilityPublicationService{}).build(snap); !errors.Is(err, ErrCapabilityPublicationConflict) {
				t.Fatalf("explicit detachment bypassed deleted provenance: %v", err)
			}
		})
	}
}

func TestCapabilityPublicationDeletedLegacyRemovalDoesNotGuessProtocol(t *testing.T) {
	snap := publicationDeletedTestSnapshot(true)
	snap.Request.Groups[0].RemoveLines = []CapabilityPublicationLineRemoval{{PublicModel: "gpt-5.6-sol", AccountID: 8, UpstreamModel: "gpt-5.6-sol", Protocol: "chat_completions"}}
	if _, err := (&AccountCapabilityPublicationService{}).build(snap); err == nil {
		t.Fatal("a missing legacy account's unspecified wire was guessed from its old upstream name")
	}
}
