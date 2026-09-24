package service

import (
	"context"
	"encoding/json"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func accountToolsOperation(ctx context.Context, operation string, input, output any) error {
	return accountToolsOperationScoped(ctx, 0, operation, input, output)
}

func accountToolsOperationForAccount(ctx context.Context, account *Account, operation string, input, output any) error {
	if account == nil {
		return ErrExtensionOperationUnavailable
	}
	return accountToolsOperationScoped(ctx, account.ID, operation, input, output)
}

func accountToolsOperationScoped(ctx context.Context, accountID int64, operation string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := invokeAccountTools(call, extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: operation, AccountID: accountID, Payload: raw})
	if err != nil {
		return infraerrors.New(503, "ACCOUNT_TOOLS_UNAVAILABLE", "account tools are unavailable")
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
	return nil
}

func PlanBatchAccountTests(ctx context.Context, request extensionv1.BatchTestPlanningRequest) (extensionv1.BatchTestPlan, error) {
	var plan extensionv1.BatchTestPlan
	err := accountToolsOperation(ctx, "test.batch", request, &plan)
	return plan, err
}

func ValidateAccountTaxonomyPlan(ctx context.Context, plan extensionv1.TaxonomyBulkPlan) error {
	return accountToolsOperation(ctx, "taxonomy.bulk", plan, nil)
}

func normalizeAccountTaxonomyNameContext(ctx context.Context, value string) (string, string, error) {
	var normalized extensionv1.TaxonomyName
	err := accountToolsOperation(ctx, "taxonomy.name", extensionv1.TaxonomyName{Name: value}, &normalized)
	return normalized.Name, normalized.Normalized, err
}

func validateTaxonomyOrderIDsContext(ctx context.Context, actual, ordered []int64) error {
	return accountToolsOperation(ctx, "taxonomy.order", extensionv1.TaxonomyOrder{Actual: actual, Ordered: ordered}, nil)
}
