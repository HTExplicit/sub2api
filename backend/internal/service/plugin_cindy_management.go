package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func invokeCindyManagement(ctx context.Context, operation string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > extensionv1.MaxPayloadBytes {
		return ErrCindyGroupAdminUnavailable
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtension(call, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: operation, Payload: raw})
	if err != nil {
		return ErrCindyGroupAdminUnavailable
	}
	switch result.Code {
	case "invalid_input":
		return ErrCindyGroupInvalidInput
	case "invalid_key_selection":
		return ErrCindyGroupAPIKeySelection
	case "not_mixed":
		return ErrCindyGroupNotMixed
	case "":
		if json.Unmarshal(result.Payload, output) == nil {
			return nil
		}
	}
	return ErrCindyGroupAdminUnavailable
}

type CindyGroupPartitionPlan = extensionv1.CindyGroupPartitionPlan

func PlanCindyGroupPartition(ctx context.Context, cindyCount, ordinaryCount int64, sourceKeeps string) (CindyGroupPartitionPlan, error) {
	var plan CindyGroupPartitionPlan
	err := invokeCindyManagement(ctx, "cindy.groups.partition", extensionv1.CindyGroupPartitionRequest{CindyCount: cindyCount, OrdinaryCount: ordinaryCount, SourceKeeps: sourceKeeps}, &plan)
	if err != nil {
		return plan, err
	}
	if plan.Classification != "pure_cindy" && plan.Classification != "mixed" && plan.Classification != "no_cindy" {
		return plan, ErrCindyGroupAdminUnavailable
	}
	if sourceKeeps != "" {
		count := ordinaryCount
		if plan.MoveCindy {
			count = cindyCount
		}
		if plan.SourceCindy == plan.TargetCindy || plan.MoveCindy != plan.TargetCindy || plan.SourceCindy != (sourceKeeps == CindyGroupSourceKeepsCindy) || plan.AccountsToMove != count || count <= 0 {
			return plan, ErrCindyGroupAdminUnavailable
		}
	}
	return plan, nil
}

func normalizeCindyGroupSplitInputContext(ctx context.Context, input CindyGroupSplitInput, requireFingerprint bool) (CindyGroupSplitInput, error) {
	var output CindyGroupSplitInput
	err := invokeCindyManagement(ctx, "cindy.groups.input", extensionv1.CindyGroupInputRequest{Input: input, RequireFingerprint: requireFingerprint}, &output)
	if err != nil {
		return output, err
	}
	// Normalization cannot broaden the administrator's explicit key selection
	// or substitute another target name or membership precondition.
	ids := append([]int64{}, input.APIKeyIDs...)
	slices.Sort(ids)
	if !slices.Equal(ids, output.APIKeyIDs) || output.SourceKeeps != strings.ToLower(strings.TrimSpace(input.SourceKeeps)) || output.TargetName != strings.TrimSpace(input.TargetName) || output.MemberFingerprint != strings.ToLower(strings.TrimSpace(input.MemberFingerprint)) {
		return output, ErrCindyGroupAdminUnavailable
	}
	return output, nil
}
