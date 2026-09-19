package service

import (
	"context"
	"encoding/json"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func accountToolsOperation(ctx context.Context, platform, accountType, operation string, input, output any, owner ...*int64) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := invokeProcessExtension(call, platform, accountType, extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: operation, Payload: raw})
	if err != nil {
		return infraerrors.New(503, "ACCOUNT_TOOLS_UNAVAILABLE", "account tools plugin is disabled or unavailable")
	}
	if result.Code != "" {
		status := result.HTTPStatus
		if status < 400 || status > 499 {
			status = 503
		}
		return infraerrors.New(status, result.Code, result.Message)
	}
	if output != nil {
		if err := json.Unmarshal(result.Payload, output); err != nil {
			return err
		}
	}
	if len(owner) > 0 && owner[0] != nil {
		*owner[0] = result.PluginID
	}
	return nil
}

func PlanBatchAccountTests(ctx context.Context, request extensionv1.BatchTestPlanningRequest) (extensionv1.BatchTestPlan, int64, error) {
	var plan extensionv1.BatchTestPlan
	var owner int64
	err := accountToolsOperation(ctx, "*", "*", "test.batch", request, &plan, &owner)
	return plan, owner, err
}

func ValidateAccountTaxonomyPlan(ctx context.Context, plan extensionv1.TaxonomyBulkPlan) (int64, error) {
	var owner int64
	err := accountToolsOperation(ctx, "*", "*", "taxonomy.bulk", plan, nil, &owner)
	return owner, err
}

func normalizeAccountTaxonomyNameContext(ctx context.Context, value string) (string, string, error) {
	var normalized extensionv1.TaxonomyName
	err := accountToolsOperation(ctx, "*", "*", "taxonomy.name", extensionv1.TaxonomyName{Name: value}, &normalized)
	return normalized.Name, normalized.Normalized, err
}

func validateTaxonomyOrderIDsContext(ctx context.Context, actual, ordered []int64) error {
	return accountToolsOperation(ctx, "*", "*", "taxonomy.order", extensionv1.TaxonomyOrder{Actual: actual, Ordered: ordered}, nil)
}
