package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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
		if plan.Index != index {
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
			if plan.AccountID != 0 || (plan.Code != extensionv1.AccountImportCodePayloadInvalid && plan.Code != extensionv1.AccountImportCodeIdentityConflict) {
				return nil, ErrExtensionOperationUnavailable
			}
		default:
			return nil, ErrExtensionOperationUnavailable
		}
		if plan.Action != "reject" && !facts.PayloadValid {
			return nil, ErrExtensionOperationUnavailable
		}
	}
	return plans, nil
}

// Large import files still fit the official bounded RPC protocol: they are
// planned in chunks, then each chunk's prepared plans are finalized.
func collectAccountImportPlans(ctx context.Context, in extensionv1.AccountImportPlanningRequest) ([]extensionv1.AccountImportItemPlan, error) {
	const chunkSize = 1000
	if len(in.Items) <= chunkSize {
		var plans []extensionv1.AccountImportItemPlan
		err := accountToolsOperation(ctx, "import.plan", in, &plans)
		return plans, err
	}
	prepared := make([]extensionv1.AccountImportItemPlan, 0, len(in.Items))
	for start := 0; start < len(in.Items); start += chunkSize {
		end := min(start+chunkSize, len(in.Items))
		chunk := in
		chunk.Phase = "prepare"
		chunk.Items = in.Items[start:end]
		var plans []extensionv1.AccountImportItemPlan
		if err := accountToolsOperation(ctx, "import.plan", chunk, &plans); err != nil {
			return nil, err
		}
		if len(plans) != len(chunk.Items) {
			return nil, ErrExtensionOperationUnavailable
		}
		for index, plan := range plans {
			if plan.Index != index {
				return nil, ErrExtensionOperationUnavailable
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
		chunk.Items = in.Items[start:end]
		var plans []extensionv1.AccountImportItemPlan
		if err := accountToolsOperation(ctx, "import.plan", chunk, &plans); err != nil {
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
