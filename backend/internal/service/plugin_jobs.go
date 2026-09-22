package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type PluginJobPayload struct {
	PluginID  int64                      `json:"plugin_id"`
	Operation string                     `json:"operation"`
	Items     map[string]json.RawMessage `json:"items"`
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
	if job == nil {
		return ctx, nil, ErrAccountJobPluginUnavailable
	}
	if err := validateAccountViewJobKind(ctx, job.Kind, job.Metadata); err != nil {
		return ctx, nil, err
	}
	owner, err := AccountJobPluginExecution(job.Metadata)
	if err != nil {
		return ctx, nil, err
	}
	ctx, releaseView, err := e.manager.BindAccountJobView(ctx, job.Metadata, payload, false)
	if err != nil {
		return ctx, nil, err
	}
	releasePrimary := func() {}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { releasePrimary(); releaseView() }) }
	preparedOK := false
	defer func() {
		if !preparedOK {
			release()
		}
	}()
	if owner.ID > 0 {
		if owner.Generation <= 0 {
			return ctx, nil, ErrAccountJobPluginUnavailable
		}
		ctx, releasePrimary, err = e.manager.BindAccountJobExecution(ctx, owner.ID, owner.Generation, job.Kind)
		if err != nil {
			releasePrimary = func() {}
			return ctx, nil, err
		}
	} else {
		// Pre-plugin pending jobs keep their records, but use the current
		// domain policy and lifetime once their execution path has migrated.
		operation := ""
		switch job.Kind {
		case AccountJobKindImportData:
			operation = "import.plan"
		case AccountJobKindBatchTest:
			operation = "test.batch"
		case AccountJobKindBulkTaxonomy:
			operation = "taxonomy.bulk"
		}
		if isCindyCleanupAccountJob(job.Kind) {
			if e.manager == nil {
				return ctx, nil, ErrAccountJobPluginUnavailable
			}
			id, lookupErr := e.manager.PinnedPluginOwner(ctx, CindyAccountViewPluginKey)
			if lookupErr != nil {
				return ctx, nil, ErrAccountJobPluginUnavailable
			}
			ctx, releasePrimary, err = e.manager.BindAccountJobExecution(ctx, id, 0, job.Kind)
			if err != nil {
				releasePrimary = func() {}
				return ctx, nil, err
			}
		} else if operation != "" {
			if e.manager == nil {
				return ctx, nil, ErrAccountJobPluginUnavailable
			}
			id, _, lookupErr := e.manager.operationOwner("*", "*", extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: operation})
			if lookupErr != nil {
				return ctx, nil, ErrAccountJobPluginUnavailable
			}
			ctx, releasePrimary, err = e.manager.BindAccountJobExecution(ctx, id, 0, job.Kind)
			if err != nil {
				releasePrimary = func() {}
				return ctx, nil, err
			}
		} else if job.Kind == AccountJobKindExtensionOperation {
			return ctx, nil, ErrAccountJobPluginUnavailable
		}
	}
	if job.Kind != AccountJobKindExtensionOperation {
		if preparer, ok := e.core.(AccountJobPreparingExecutor); ok {
			prepared, cleanup, err := preparer.PrepareAccountJob(ctx, job, payload)
			if err != nil {
				if cleanup != nil {
					cleanup()
				}
				release()
				return ctx, nil, err
			}
			if prepared == nil {
				prepared = ctx
			}
			preparedOK = true
			return prepared, func() {
				if cleanup != nil {
					cleanup()
				}
				release()
			}, nil
		}
	}
	preparedOK = true
	return ctx, release, nil
}

func (e *PluginJobExecutor) ExecuteAccountJob(ctx context.Context, job *AccountJob, payload json.RawMessage, items []AccountJobItem) ([]AccountJobExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrAccountJobPluginUnavailable
	}
	if err := validateAccountJobViewExecution(ctx, job, payload, items); err != nil {
		return nil, err
	}
	owner, ownerErr := AccountJobPluginExecution(job.Metadata)
	if ownerErr != nil {
		return nil, ownerErr
	}
	execution, bound := PluginExecutionFromContext(ctx)
	if owner.ID == 0 && bound {
		owner = execution
	}
	if owner.ID > 0 {
		ok := bound
		if !ok || execution != owner {
			return nil, ErrAccountJobPluginUnavailable
		}
		var ids []int64
		for _, item := range items {
			if item.TargetAccountID != nil {
				ids = append(ids, *item.TargetAccountID)
			}
		}
		if isCindyCleanupAccountJob(job.Kind) {
			if err := e.manager.ValidateResourcePolicy(ctx, CindyCleanupResourcePolicy(), nil, true); err != nil {
				return nil, ErrAccountJobPluginUnavailable
			}
		} else if _, viewBound := AccountViewFromContext(ctx); viewBound {
			targets := make([]*int64, 0, len(items))
			for _, item := range items {
				targets = append(targets, item.TargetAccountID)
			}
			viewIDs, err := accountViewJobTargets(job.Kind, payload, targets)
			if err != nil {
				return nil, err
			}
			if err := e.manager.ValidateResourcePolicy(ctx, extensionv1.ResourceDescriptor{ResourceGrant: extensionv1.ResourceGrant{Capability: extensionv1.CapabilityAdmin}}, viewIDs, false); err != nil {
				return nil, ErrAccountJobPluginUnavailable
			}
		} else if err := e.manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityAdmin, ids, len(ids) != len(items)); err != nil {
			return nil, ErrAccountJobPluginUnavailable
		}
	}
	if job.Kind != AccountJobKindExtensionOperation {
		return e.core.ExecuteAccountJob(ctx, job, payload, items)
	}
	var request PluginJobPayload
	if json.Unmarshal(payload, &request) != nil || request.PluginID <= 0 || request.Operation == "" || len(items) != 1 {
		return nil, errors.New("invalid plugin job")
	}
	if owner.ID != request.PluginID {
		return nil, ErrAccountJobPluginUnavailable
	}
	item := items[0]
	result := AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusFailed, ErrorCode: "execution_failed", ErrorMessage: "plugin operation failed"}
	if item.TargetAccountID == nil {
		return []AccountJobExecutionResult{result}, nil
	}
	id := *item.TargetAccountID
	var operation map[string]json.RawMessage
	if json.Unmarshal(request.Items[item.Action], &operation) != nil || operation == nil {
		return []AccountJobExecutionResult{result}, nil
	}
	operation["operation_id"], _ = json.Marshal(fmt.Sprintf("job-%d", job.ID))
	raw, err := json.Marshal(operation)
	if err != nil {
		return nil, err
	}
	out, err := e.manager.InvokeAdminExtension(ctx, request.PluginID, id, request.Operation, raw)
	if err != nil {
		if ctx.Err() != nil {
			result.Status = AccountJobItemStatusCanceled
			result.ErrorCode = ""
			result.ErrorMessage = ""
		}
		return []AccountJobExecutionResult{result}, nil
	}
	if ValidateAccountJobMetadata(out.Payload) != nil {
		return []AccountJobExecutionResult{result}, nil
	}
	metadata := map[string]any{}
	_ = json.Unmarshal(item.Metadata, &metadata)
	var details map[string]any
	if json.Unmarshal(out.Payload, &details) == nil {
		for key, value := range details {
			metadata[key] = value
		}
	}
	result.Metadata, _ = json.Marshal(metadata)
	var outcome struct {
		Success bool `json:"success"`
	}
	if out.Code == "" && json.Unmarshal(out.Payload, &outcome) == nil && outcome.Success {
		result.Status = AccountJobItemStatusSucceeded
		result.ErrorCode = ""
		result.ErrorMessage = ""
	} else if ctx.Err() != nil {
		result.Status = AccountJobItemStatusCanceled
		result.ErrorCode = ""
		result.ErrorMessage = ""
	}
	return []AccountJobExecutionResult{result}, nil
}
