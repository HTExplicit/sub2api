package policy

import extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"

func planImport(in extensionv1.AccountImportPlanningRequest) []extensionv1.AccountImportItemPlan {
	if in.Phase == "finalize" {
		return finalizeImport(in)
	}
	out := make([]extensionv1.AccountImportItemPlan, len(in.Items))
	credentials, devices := map[string][]int{}, map[string][]int{}
	reject := func(plan *extensionv1.AccountImportItemPlan, code string) {
		plan.Action, plan.Code, plan.AccountID = "reject", code, 0
	}
	targetStrict := in.TargetCanonical && (in.TargetStrict || !in.TargetHasMembers)
	for index, facts := range in.Items {
		plan := extensionv1.AccountImportItemPlan{Index: index, CanonicalCindy: facts.CindyCandidate && (!facts.LegacyCindy || facts.APIKeyValid)}
		if facts.CindyCandidate && !facts.APIKeyValid {
			reject(&plan, extensionv1.AccountImportCodeCindyAPIKeyInvalid)
		}
		if plan.CanonicalCindy && plan.Action != "reject" {
			if in.TargetGroupID <= 0 {
				reject(&plan, extensionv1.AccountImportCodeCindyTargetRequired)
			} else if !targetStrict {
				reject(&plan, extensionv1.AccountImportCodeCindyTargetInvalid)
			} else {
				plan.GroupIDs = []int64{in.TargetGroupID}
			}
		}
		if plan.Action != "reject" && !facts.PayloadValid {
			reject(&plan, extensionv1.AccountImportCodePayloadInvalid)
		}
		if plan.CanonicalCindy && plan.Action != "reject" {
			if !facts.DeviceValid || !facts.DeviceSourceValid {
				reject(&plan, extensionv1.AccountImportCodeDeviceInvalid)
			}
			if plan.Action != "reject" {
				if facts.CredentialIdentity != "" {
					plan.TrackCredential = true
					credentials[facts.CredentialIdentity] = append(credentials[facts.CredentialIdentity], index)
				}
				if facts.DeviceIdentity != "" {
					plan.TrackDevice = true
					devices[facts.DeviceIdentity] = append(devices[facts.DeviceIdentity], index)
				}
			}
		}
		if plan.Action != "reject" {
			switch len(facts.Matches) {
			case 0:
				plan.Action, plan.Code = "create", extensionv1.AccountImportCodeCreate
			case 1:
				plan.Action, plan.Code, plan.AccountID = "update", extensionv1.AccountImportCodeUpdate, facts.Matches[0]
			default:
				code := extensionv1.AccountImportCodeIdentityConflict
				if plan.CanonicalCindy {
					code = extensionv1.AccountImportCodeCredentialConflict
				}
				reject(&plan, code)
			}
		}
		out[index] = plan
	}
	if in.Phase == "prepare" {
		return out
	}
	for _, indexes := range credentials {
		if len(indexes) > 1 {
			for _, index := range indexes {
				reject(&out[index], extensionv1.AccountImportCodeCredentialConflict)
			}
		}
	}
	for _, indexes := range devices {
		if len(indexes) > 1 {
			for _, index := range indexes {
				reject(&out[index], extensionv1.AccountImportCodeDeviceConflict)
			}
			continue
		}
		index := indexes[0]
		owners := in.Items[index].DeviceOwners
		if len(owners) > 0 && (len(owners) != 1 || owners[0] != out[index].AccountID) {
			reject(&out[index], extensionv1.AccountImportCodeDeviceConflict)
		}
	}
	return out
}

func finalizeImport(in extensionv1.AccountImportPlanningRequest) []extensionv1.AccountImportItemPlan {
	out := append([]extensionv1.AccountImportItemPlan(nil), in.Prepared...)
	for index, facts := range in.Items {
		plan := &out[index]
		if plan.TrackCredential && facts.CredentialCopies > 1 {
			plan.Action, plan.Code, plan.AccountID = "reject", extensionv1.AccountImportCodeCredentialConflict, 0
		}
		if plan.TrackDevice && (facts.DeviceCopies > 1 || (len(facts.DeviceOwners) > 0 && (len(facts.DeviceOwners) != 1 || facts.DeviceOwners[0] != plan.AccountID))) {
			plan.Action, plan.Code, plan.AccountID = "reject", extensionv1.AccountImportCodeDeviceConflict, 0
		}
	}
	return out
}
