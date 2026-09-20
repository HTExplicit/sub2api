package service

import (
	"context"
	"slices"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func PlanAccountImport(ctx context.Context, in extensionv1.AccountImportPlanningRequest) ([]extensionv1.AccountImportItemPlan, error) {
	in.Phase, in.Prepared = "", nil
	plans, err := collectAccountImportPlans(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(plans) != len(in.Items) {
		return nil, ErrExtensionOperationUnavailable
	}
	for index, plan := range plans {
		facts := in.Items[index]
		if plan.Index != index || plan.CanonicalCindy != (facts.CindyCandidate && (!facts.LegacyCindy || facts.APIKeyValid)) {
			return nil, ErrExtensionOperationUnavailable
		}
		if len(plan.GroupIDs) > 0 && (!plan.CanonicalCindy || in.TargetGroupID <= 0 || !slices.Equal(plan.GroupIDs, []int64{in.TargetGroupID})) {
			return nil, ErrExtensionOperationUnavailable
		}
		switch plan.Action {
		case "create":
			if plan.AccountID != 0 || len(facts.Matches) != 0 || plan.Code != extensionv1.AccountImportCodeCreate {
				return nil, ErrExtensionOperationUnavailable
			}
		case "update":
			if plan.AccountID <= 0 || len(facts.Matches) != 1 || facts.Matches[0] != plan.AccountID || plan.Code != extensionv1.AccountImportCodeUpdate {
				return nil, ErrExtensionOperationUnavailable
			}
		case "reject":
			if plan.AccountID != 0 || !slices.Contains([]string{extensionv1.AccountImportCodePayloadInvalid, extensionv1.AccountImportCodeIdentityConflict, extensionv1.AccountImportCodeCindyTargetRequired, extensionv1.AccountImportCodeCindyTargetInvalid, extensionv1.AccountImportCodeCindyAPIKeyInvalid, extensionv1.AccountImportCodeCredentialConflict, extensionv1.AccountImportCodeDeviceConflict, extensionv1.AccountImportCodeDeviceInvalid}, plan.Code) {
				return nil, ErrExtensionOperationUnavailable
			}
		default:
			return nil, ErrExtensionOperationUnavailable
		}
		if plan.Action != "reject" {
			if !facts.PayloadValid || (plan.CanonicalCindy && (!facts.APIKeyValid || !facts.DeviceValid || !facts.DeviceSourceValid || !slices.Equal(plan.GroupIDs, []int64{in.TargetGroupID}))) {
				return nil, ErrExtensionOperationUnavailable
			}
		}
	}
	return plans, nil
}

// Large import files still fit the official bounded RPC protocol. The plugin
// identifies which opaque identities participate; the host only aggregates
// those references across chunks before asking the plugin to resolve them.
func collectAccountImportPlans(ctx context.Context, in extensionv1.AccountImportPlanningRequest) ([]extensionv1.AccountImportItemPlan, error) {
	const chunkSize = 1000
	if len(in.Items) <= chunkSize {
		var plans []extensionv1.AccountImportItemPlan
		err := accountToolsOperation(ctx, "*", "*", "import.plan", in, &plans)
		return plans, err
	}
	prepared := make([]extensionv1.AccountImportItemPlan, 0, len(in.Items))
	credentials, devices := map[string]int{}, map[string]int{}
	for start := 0; start < len(in.Items); start += chunkSize {
		end := min(start+chunkSize, len(in.Items))
		chunk := in
		chunk.Phase = "prepare"
		chunk.Items = in.Items[start:end]
		var plans []extensionv1.AccountImportItemPlan
		if err := accountToolsOperation(ctx, "*", "*", "import.plan", chunk, &plans); err != nil {
			return nil, err
		}
		if len(plans) != len(chunk.Items) {
			return nil, ErrExtensionOperationUnavailable
		}
		for index, plan := range plans {
			if plan.Index != index {
				return nil, ErrExtensionOperationUnavailable
			}
			facts := chunk.Items[index]
			if plan.TrackCredential {
				credentials[facts.CredentialIdentity]++
			}
			if plan.TrackDevice {
				devices[facts.DeviceIdentity]++
			}
		}
		prepared = append(prepared, plans...)
	}
	result := make([]extensionv1.AccountImportItemPlan, 0, len(in.Items))
	for start := 0; start < len(in.Items); start += chunkSize {
		end := min(start+chunkSize, len(in.Items))
		chunk := in
		chunk.Phase = "finalize"
		chunk.Prepared = prepared[start:end]
		chunk.Items = append([]extensionv1.AccountImportItemFacts(nil), in.Items[start:end]...)
		for index := range chunk.Items {
			facts := &chunk.Items[index]
			facts.CredentialCopies, facts.DeviceCopies = credentials[facts.CredentialIdentity], devices[facts.DeviceIdentity]
		}
		var plans []extensionv1.AccountImportItemPlan
		if err := accountToolsOperation(ctx, "*", "*", "import.plan", chunk, &plans); err != nil {
			return nil, err
		}
		if len(plans) != len(chunk.Items) {
			return nil, ErrExtensionOperationUnavailable
		}
		for index := range plans {
			if plans[index].Index != index {
				return nil, ErrExtensionOperationUnavailable
			}
			plans[index].Index += start
		}
		result = append(result, plans...)
	}
	return result, nil
}
