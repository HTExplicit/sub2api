package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (h *AccountHandler) executeLegacyCodexAccountJob(ctx context.Context, job *service.AccountJob, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	intent, err := service.DecodeLegacyCodexAccountJob(job.Metadata, raw, item)
	if errors.Is(err, service.ErrLegacyAccountJobUnsupported) {
		return service.AccountJobExecutionResult{ItemID: item.ID, Status: service.AccountJobItemStatusFailed,
			Metadata: json.RawMessage(`{}`), ErrorCode: "legacy_operation_unsupported", ErrorMessage: "The stored operation has no native executor"}
	}
	if err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	if h.codexTicketGateway == nil {
		return accountJobFailedWithError(item.ID, "legacy_source_unavailable", errors.New("codex ticket gateway is unavailable"))
	}
	if err := h.codexTicketGateway.ValidateLegacyCodexJobSource(ctx, intent.PluginID); err != nil {
		return accountJobFailedWithError(item.ID, "legacy_source_unavailable", err)
	}
	models, err := h.ticketModels([]string{intent.Model})
	if err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	if len(models) != 1 {
		return accountJobFailedWithError(item.ID, "payload_invalid", fmt.Errorf("stored model %q resolved to %d models", intent.Model, len(models)))
	}
	payload, _ := json.Marshal(codexTicketHarvestRequest{AccountIDs: []int64{intent.AccountID}, Models: models, Force: intent.Force})
	// Only the original frozen target and single saved model reach the native
	// entrypoint; operation_id remains the durable job's own identity.
	item.Metadata, _ = json.Marshal(map[string]any{"account_id": intent.AccountID, "model_id": models[0]})
	if intent.Operation == "stop" {
		return h.executeCodexTicketStop(ctx, payload, item)
	}
	return h.executeCodexTicketHarvest(ctx, job, payload, item)
}
