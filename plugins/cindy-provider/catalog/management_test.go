package catalog

import (
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestManagementDuplicateOwnerExcludesAllTerminalStates(t *testing.T) {
	facts := []extensionv1.CindyDuplicateCandidate{
		{ID: 1, IdentityHash: "same", Status: "active", CreatedAt: time.Unix(10, 0)},
		{ID: 2, IdentityHash: "same", Status: "active", CreatedAt: time.Unix(20, 0)},
		{ID: 3, IdentityHash: "same", Status: "quarantined", CreatedAt: time.Unix(1, 0)},
	}
	groups := duplicateInventory(facts)
	if len(groups) != 1 || groups[0].ProposedOwnerID != 1 || len(groups[0].OtherAccountIDs) != 2 {
		t.Fatalf("unexpected owner: %+v", groups)
	}
	facts[0].Exhausted, facts[1].Banned = true, true
	groups = duplicateInventory(facts)
	if groups[0].ProposedOwnerID != 0 || len(groups[0].OtherAccountIDs) != 3 {
		t.Fatalf("terminal owner selected: %+v", groups)
	}
}

func TestManagementGroupPlanKeepsExplicitSideAndKeySelection(t *testing.T) {
	for _, side := range []string{"cindy", "ordinary"} {
		plan, code := partitionGroup(extensionv1.CindyGroupPartitionRequest{CindyCount: 2, OrdinaryCount: 3, SourceKeeps: side})
		if code != "" || plan.SourceCindy != (side == "cindy") || plan.TargetCindy == plan.SourceCindy || plan.MoveCindy != plan.TargetCindy {
			t.Fatalf("wrong split side: %+v %s", plan, code)
		}
	}
	_, code := partitionGroup(extensionv1.CindyGroupPartitionRequest{CindyCount: 2, SourceKeeps: "ordinary"})
	if code != "not_mixed" {
		t.Fatalf("split admitted without ordinary members: %s", code)
	}
	input, code := normalizeGroupInput(extensionv1.CindyGroupInputRequest{Input: extensionv1.CindyGroupSplitInput{SourceKeeps: " CINDY ", TargetName: " Target ", APIKeyIDs: []int64{2, 1}}})
	if code != "" || input.SourceKeeps != "cindy" || input.TargetName != "Target" || len(input.APIKeyIDs) != 2 || input.APIKeyIDs[0] != 1 {
		t.Fatalf("input changed intent: %+v %s", input, code)
	}
}
