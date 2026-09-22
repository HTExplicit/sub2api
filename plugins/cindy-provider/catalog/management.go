package catalog

import (
	"encoding/hex"
	"sort"
	"strings"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func duplicateInventory(accounts []extensionv1.CindyDuplicateCandidate) []extensionv1.CindyDuplicateIdentityGroup {
	groups := make(map[string][]extensionv1.CindyDuplicateCandidate)
	for _, account := range accounts {
		groups[account.IdentityHash] = append(groups[account.IdentityHash], account)
	}
	result := make([]extensionv1.CindyDuplicateIdentityGroup, 0, len(groups))
	for hash, members := range groups {
		if len(members) < 2 {
			continue
		}
		sort.Slice(members, func(i, j int) bool {
			left, right := members[i], members[j]
			leftTerminal, rightTerminal := duplicateTerminal(left), duplicateTerminal(right)
			if leftTerminal != rightTerminal {
				return !leftTerminal
			}
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.Before(right.UpdatedAt)
			}
			return left.ID < right.ID
		})
		owner := int64(0)
		if !duplicateTerminal(members[0]) {
			owner = members[0].ID
		}
		others := make([]int64, 0, len(members))
		for _, member := range members {
			if member.ID != owner {
				others = append(others, member.ID)
			}
		}
		sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
		result = append(result, extensionv1.CindyDuplicateIdentityGroup{IdentityHash: hash, ProposedOwnerID: owner, OtherAccountIDs: others})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].IdentityHash < result[j].IdentityHash })
	return result
}

func duplicateTerminal(account extensionv1.CindyDuplicateCandidate) bool {
	return account.Banned || account.Exhausted || strings.EqualFold(strings.TrimSpace(account.Status), "disabled") || strings.EqualFold(strings.TrimSpace(account.Status), "quarantined")
}

func normalizeGroupInput(request extensionv1.CindyGroupInputRequest) (extensionv1.CindyGroupSplitInput, string) {
	input := request.Input
	input.SourceKeeps = strings.ToLower(strings.TrimSpace(input.SourceKeeps))
	input.TargetName = strings.TrimSpace(input.TargetName)
	if (input.SourceKeeps != "cindy" && input.SourceKeeps != "ordinary") || input.TargetName == "" || utf8.RuneCountInString(input.TargetName) > 100 {
		return input, "invalid_input"
	}
	seen := make(map[int64]bool, len(input.APIKeyIDs))
	ids := make([]int64, 0, len(input.APIKeyIDs))
	for _, id := range input.APIKeyIDs {
		if id <= 0 || seen[id] {
			return input, "invalid_key_selection"
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	input.APIKeyIDs = ids
	input.MemberFingerprint = strings.ToLower(strings.TrimSpace(input.MemberFingerprint))
	if request.RequireFingerprint {
		decoded, err := hex.DecodeString(input.MemberFingerprint)
		if err != nil || len(decoded) != 32 {
			return input, "invalid_input"
		}
	}
	return input, ""
}

func partitionGroup(request extensionv1.CindyGroupPartitionRequest) (extensionv1.CindyGroupPartitionPlan, string) {
	plan := extensionv1.CindyGroupPartitionPlan{Classification: "no_cindy"}
	if request.CindyCount < 0 || request.OrdinaryCount < 0 {
		return plan, "invalid_input"
	}
	if request.CindyCount > 0 {
		plan.Classification = "pure_cindy"
		if request.OrdinaryCount > 0 {
			plan.Classification = "mixed"
		}
	}
	if request.SourceKeeps == "" {
		return plan, ""
	}
	if request.SourceKeeps != "cindy" && request.SourceKeeps != "ordinary" {
		return plan, "invalid_input"
	}
	if plan.Classification != "mixed" {
		return plan, "not_mixed"
	}
	plan.SourceCindy = request.SourceKeeps == "cindy"
	plan.TargetCindy, plan.MoveCindy = !plan.SourceCindy, !plan.SourceCindy
	plan.TargetClassification, plan.AccountsToMove = "no_cindy", request.OrdinaryCount
	if plan.TargetCindy {
		plan.TargetClassification, plan.AccountsToMove = "pure_cindy", request.CindyCount
	}
	return plan, ""
}
