package policy

import (
	"errors"
	"slices"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func planBatch(request extensionv1.BatchTestPlanningRequest) (extensionv1.BatchTestPlan, error) {
	invalid := errors.New("invalid or mixed batch test selections")
	if request.HasItems && request.HasLegacy {
		return extensionv1.BatchTestPlan{}, invalid
	}
	plan := extensionv1.BatchTestPlan{Models: make(map[int64]string)}
	if request.HasItems || request.Items != nil {
		efforts := map[int64]string{}
		for _, item := range request.Items {
			item.ModelID = strings.TrimSpace(item.ModelID)
			if item.AccountID <= 0 || item.ModelID == "" || len(item.ModelID) > 256 || len(item.ReasoningEffort) > 32 || item.ReasoningEffort != strings.TrimSpace(item.ReasoningEffort) {
				return plan, invalid
			}
			if previous, exists := plan.Models[item.AccountID]; exists {
				if previous != item.ModelID || efforts[item.AccountID] != item.ReasoningEffort {
					return plan, invalid
				}
				continue
			}
			plan.AccountIDs = append(plan.AccountIDs, item.AccountID)
			plan.Models[item.AccountID], efforts[item.AccountID] = item.ModelID, item.ReasoningEffort
			plan.Items = append(plan.Items, item)
		}
	} else {
		plan.ModelID = strings.TrimSpace(request.ModelID)
		if len(plan.ModelID) > 256 {
			return plan, invalid
		}
		for _, id := range request.AccountIDs {
			if id <= 0 {
				return plan, invalid
			}
			plan.Models[id] = plan.ModelID
		}
		for id := range plan.Models {
			plan.AccountIDs = append(plan.AccountIDs, id)
		}
		slices.Sort(plan.AccountIDs)
	}
	if len(plan.AccountIDs) == 0 {
		return plan, invalid
	}
	return plan, nil
}
