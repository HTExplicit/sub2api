package policy

import extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"

func planImport(in extensionv1.AccountImportPlanningRequest) []extensionv1.AccountImportItemPlan {
	if in.Phase == "finalize" {
		// Items are planned independently; the prepared chunk plans are final.
		return append([]extensionv1.AccountImportItemPlan(nil), in.Prepared...)
	}
	out := make([]extensionv1.AccountImportItemPlan, len(in.Items))
	for index, facts := range in.Items {
		plan := extensionv1.AccountImportItemPlan{Index: index}
		switch {
		case !facts.PayloadValid:
			plan.Action, plan.Code = "reject", extensionv1.AccountImportCodePayloadInvalid
		case len(facts.Matches) == 0:
			plan.Action, plan.Code = "create", extensionv1.AccountImportCodeCreate
		case len(facts.Matches) == 1:
			plan.Action, plan.Code, plan.AccountID = "update", extensionv1.AccountImportCodeUpdate, facts.Matches[0]
		default:
			plan.Action, plan.Code = "reject", extensionv1.AccountImportCodeIdentityConflict
		}
		out[index] = plan
	}
	return out
}
