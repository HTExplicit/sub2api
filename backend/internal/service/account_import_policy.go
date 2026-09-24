package service

import (
	"context"
	"slices"

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
		if plan.Index != index || plan.CanonicalCindy != (facts.CindyCandidate && (!facts.LegacyCindy || facts.APIKeyValid)) {
			return nil, ErrExtensionOperationUnavailable
		}
		if err := validateImportTrackingHints(in, plan, facts); err != nil {
			return nil, err
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
			if !facts.PayloadValid || (facts.CindyCandidate && !facts.APIKeyValid) ||
				(plan.CanonicalCindy && (!importTrackingCandidate(in, facts) || !slices.Equal(plan.GroupIDs, []int64{in.TargetGroupID}))) {
				return nil, ErrExtensionOperationUnavailable
			}
		}
	}
	if err := validateImportIdentitySafety(in, plans); err != nil {
		return nil, err
	}
	return plans, nil
}

// validateImportTrackingHints checks only immutable host-side invariants. The
// plugin remains the owner of create/update/reject policy; the host merely
// prevents a plan from changing the identity set used for cross-item safety.
func validateImportTrackingHints(in extensionv1.AccountImportPlanningRequest, plan extensionv1.AccountImportItemPlan, facts extensionv1.AccountImportItemFacts) error {
	track := importTrackingCandidate(in, facts)
	if plan.TrackCredential != (track && facts.CredentialIdentity != "") ||
		plan.TrackDevice != (track && facts.DeviceIdentity != "") {
		return ErrExtensionOperationUnavailable
	}
	return nil
}

// importTrackingCandidate is deliberately narrower than the complete import
// policy. It describes only the items that the plugin is allowed to put in
// the cross-item credential/device execution set. Invalid/early-rejected
// inputs stay out of that set so one bad item remains an item-level reject.
func importTrackingCandidate(in extensionv1.AccountImportPlanningRequest, facts extensionv1.AccountImportItemFacts) bool {
	canonical := facts.CindyCandidate && (!facts.LegacyCindy || facts.APIKeyValid)
	targetUsable := in.TargetGroupID > 0 && in.TargetCanonical && (in.TargetStrict || !in.TargetHasMembers)
	return canonical && targetUsable && facts.APIKeyValid && facts.PayloadValid && facts.DeviceValid && facts.DeviceSourceValid
}

func importIdentityCopies(in extensionv1.AccountImportPlanningRequest) (map[string]int, map[string]int) {
	credentials, devices := map[string]int{}, map[string]int{}
	for _, facts := range in.Items {
		if !importTrackingCandidate(in, facts) {
			continue
		}
		if facts.CredentialIdentity != "" {
			credentials[facts.CredentialIdentity]++
		}
		if facts.DeviceIdentity != "" {
			devices[facts.DeviceIdentity]++
		}
	}
	return credentials, devices
}

// validateImportIdentitySafety prevents writes with duplicate or already-owned
// identities. The plugin still owns the rejection code and its precedence:
// device ownership can supersede credential conflict even without a duplicate
// device in this request.
func validateImportIdentitySafety(in extensionv1.AccountImportPlanningRequest, plans []extensionv1.AccountImportItemPlan) error {
	credentials, devices := importIdentityCopies(in)
	for index, plan := range plans {
		facts := in.Items[index]
		if plan.Action == "reject" || !importTrackingCandidate(in, facts) {
			continue
		}
		if credentials[facts.CredentialIdentity] > 1 || devices[facts.DeviceIdentity] > 1 {
			return ErrExtensionOperationUnavailable
		}
		if facts.DeviceIdentity != "" && len(facts.DeviceOwners) > 0 &&
			(len(facts.DeviceOwners) != 1 || facts.DeviceOwners[0] != plan.AccountID) {
			return ErrExtensionOperationUnavailable
		}
	}
	return nil
}

// Large import files still fit the official bounded RPC protocol. The host
// aggregates opaque identities from immutable eligibility facts across chunks;
// the plugin owns the prepare/finalize decisions, not the safety set.
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
			facts := chunk.Items[index]
			if plan.CanonicalCindy != (facts.CindyCandidate && (!facts.LegacyCindy || facts.APIKeyValid)) {
				return nil, ErrExtensionOperationUnavailable
			}
			if err := validateImportTrackingHints(in, plan, facts); err != nil {
				return nil, err
			}
		}
		prepared = append(prepared, plans...)
	}
	credentials, devices := importIdentityCopies(in)
	result := make([]extensionv1.AccountImportItemPlan, 0, len(in.Items))
	for start := 0; start < len(in.Items); start += chunkSize {
		end := min(start+chunkSize, len(in.Items))
		chunk := in
		chunk.Phase = "finalize"
		chunk.Prepared = prepared[start:end]
		chunk.Items = append([]extensionv1.AccountImportItemFacts(nil), in.Items[start:end]...)
		for index := range chunk.Items {
			facts := &chunk.Items[index]
			facts.CredentialCopies, facts.DeviceCopies = 0, 0
			if importTrackingCandidate(in, *facts) {
				facts.CredentialCopies, facts.DeviceCopies = credentials[facts.CredentialIdentity], devices[facts.DeviceIdentity]
			}
		}
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
			globalIndex := start + index
			facts := in.Items[globalIndex]
			if err := validateImportTrackingHints(in, plans[index], facts); err != nil {
				return nil, err
			}
			plans[index].Index += start
		}
		result = append(result, plans...)
	}
	return result, nil
}
