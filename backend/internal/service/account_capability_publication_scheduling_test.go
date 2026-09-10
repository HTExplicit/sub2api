package service

import (
	"strings"
	"testing"
	"time"
)

func TestCapabilityPublicationMergePreservesPausedAccountScheduling(t *testing.T) {
	snapshot := publicationTestSnapshot()
	old := time.Now().Add(-90 * 24 * time.Hour)
	evidence := snapshot.Evidence[11]
	evidence.FinishedAt = &old
	snapshot.Evidence[11] = evidence
	plan, err := (&AccountCapabilityPublicationService{}).build(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	patch := publicationV2AccountPatch(plan, 7)
	if patch == nil || patch.Schedulable != nil || snapshot.Accounts[7].Account.Schedulable || snapshot.Accounts[7].Bindings[55] != 17 {
		t.Fatalf("an incremental model addition changed a user-paused account or its private binding: %#v", patch)
	}
	route := publicationV2Route(t, publicationV2Group(t, plan, 23), "gpt-6-astra")
	if len(route.Branches) != 1 || len(route.Branches[0].Accounts) != 1 || route.Branches[0].Accounts[0].AccountID != 7 {
		t.Fatal("a paused account's successful evidence was confused with an unpublished model")
	}
	warning := false
	for _, value := range plan.Warnings {
		warning = warning || strings.Contains(value, "remains paused for scheduling")
	}
	if !warning {
		t.Fatal("the preview did not explain why a published successful account is not ready to take requests")
	}
	for _, change := range plan.Changes {
		if change.Kind == "scheduling" {
			t.Fatal("default merge proposed a global scheduling change")
		}
	}
}

func TestCapabilityPublicationSchedulingEnableIsExplicitAndEvidenceScoped(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		change  func(*CapabilityPublicationSnapshot)
		allowed bool
	}{
		{"current successful account explicitly enabled", func(*CapabilityPublicationSnapshot) {}, true},
		{"no selected basic success", func(s *CapabilityPublicationSnapshot) { s.Request.Groups[0].Models = nil }, false},
		{"successful evidence belongs to old configuration", func(s *CapabilityPublicationSnapshot) {
			s.Accounts[7].Account.Credentials["api_key"] = "browser-changed-fixture-key"
		}, false},
		{"success superseded by terminal evidence", func(s *CapabilityPublicationSnapshot) { e := s.Evidence[11]; e.Superseded = true; s.Evidence[11] = e }, false},
		{"different account outside frozen scope", func(s *CapabilityPublicationSnapshot) { s.Request.EnableAccountIDs = []int64{8} }, false},
		{"same account explicitly detached", func(s *CapabilityPublicationSnapshot) { s.Request.DetachAccountIDs = []int64{7} }, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			snapshot := publicationTestSnapshot()
			snapshot.Request.EnableAccountIDs = []int64{7}
			fixture.change(snapshot)
			plan, err := (&AccountCapabilityPublicationService{}).build(snapshot)
			if !fixture.allowed {
				if err == nil {
					t.Fatal("scheduling was enabled without the exact authorized account and current successful evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			patch := publicationV2AccountPatch(plan, 7)
			if patch == nil || patch.Schedulable == nil || !*patch.Schedulable || snapshot.Accounts[7].Account.Schedulable {
				t.Fatalf("explicit enable was not isolated to the approved patch: %#v", patch)
			}
		})
	}
}
