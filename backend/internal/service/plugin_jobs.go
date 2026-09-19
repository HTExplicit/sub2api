package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type PluginJobPayload struct {
	PluginID  int64                     `json:"plugin_id"`
	Operation string                    `json:"operation"`
	Items     map[int64]json.RawMessage `json:"items"`
}

// PluginJobExecutor preserves the host's durable job, cancellation, retry and
// billing boundaries. Domain operations execute only in the enabled plugin.
type PluginJobExecutor struct {
	manager *PluginManager
	core    AccountJobExecutor
}

func NewPluginJobExecutor(manager *PluginManager, core AccountJobExecutor) *PluginJobExecutor {
	return &PluginJobExecutor{manager: manager, core: core}
}

func (e *PluginJobExecutor) PrepareAccountJob(ctx context.Context, job *AccountJob, payload json.RawMessage) (context.Context, func(), error) {
	if job.Kind != AccountJobKindExtensionOperation {
		if preparer, ok := e.core.(AccountJobPreparingExecutor); ok {
			return preparer.PrepareAccountJob(ctx, job, payload)
		}
	}
	return ctx, func() {}, nil
}

func (e *PluginJobExecutor) ExecuteAccountJob(ctx context.Context, job *AccountJob, payload json.RawMessage, items []AccountJobItem) ([]AccountJobExecutionResult, error) {
	if job.Kind != AccountJobKindExtensionOperation {
		return e.core.ExecuteAccountJob(ctx, job, payload, items)
	}
	var request PluginJobPayload
	if json.Unmarshal(payload, &request) != nil || request.PluginID <= 0 || request.Operation == "" || len(items) != 1 {
		return nil, errors.New("invalid plugin job")
	}
	item := items[0]
	result := AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusFailed, ErrorCode: "execution_failed", ErrorMessage: "plugin operation failed"}
	if item.TargetAccountID == nil {
		return []AccountJobExecutionResult{result}, nil
	}
	id := *item.TargetAccountID
	var operation map[string]json.RawMessage
	if json.Unmarshal(request.Items[id], &operation) != nil || operation == nil {
		return []AccountJobExecutionResult{result}, nil
	}
	operation["operation_id"], _ = json.Marshal(fmt.Sprintf("job-%d-item-%d", job.ID, item.ID))
	raw, err := json.Marshal(operation)
	if err != nil {
		return nil, err
	}
	out, err := e.manager.InvokeAdminExtension(ctx, request.PluginID, id, request.Operation, raw)
	if ctx.Err() != nil {
		result.Status = AccountJobItemStatusCanceled
		result.ErrorCode = ""
		result.ErrorMessage = ""
		return []AccountJobExecutionResult{result}, nil
	}
	if err != nil {
		return []AccountJobExecutionResult{result}, nil
	}
	if ValidateAccountJobMetadata(out.Payload) != nil {
		return []AccountJobExecutionResult{result}, nil
	}
	result.Metadata = out.Payload
	var outcome struct {
		Success bool `json:"success"`
	}
	if out.Code == "" && json.Unmarshal(out.Payload, &outcome) == nil && outcome.Success {
		result.Status = AccountJobItemStatusSucceeded
		result.ErrorCode = ""
		result.ErrorMessage = ""
	}
	return []AccountJobExecutionResult{result}, nil
}
