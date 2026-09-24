package admin

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Stop remains a persisted account operation after the first-party iframe and
// extension executor retire. The older synchronous endpoint stays available.
func (h *AccountHandler) StopCodexTicketRenewalJob(c *gin.Context) {
	h.submitCodexTicketStop(c, true)
}

func (h *AccountHandler) BatchStopCodexTicketRenewal(c *gin.Context) {
	h.submitCodexTicketStop(c, false)
}

func (h *AccountHandler) submitCodexTicketStop(c *gin.Context, single bool) {
	var req codexTicketHarvestRequest
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "请求格式不正确")
		return
	}
	if single {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "账号ID不正确")
			return
		}
		req.AccountIDs = []int64{id}
	}
	req.AccountIDs = normalizeInt64IDList(req.AccountIDs)
	if len(req.AccountIDs) == 0 || len(req.AccountIDs) > 100 {
		response.BadRequest(c, "每次请选择1至100个账号")
		return
	}
	models, err := h.ticketModels(req.Models)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.Models, req.Force = models, false
	accounts, err := h.adminService.GetAccountsByIDs(c.Request.Context(), req.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if len(accounts) != len(req.AccountIDs) {
		response.BadRequest(c, "所选账号已发生变化")
		return
	}
	for _, account := range accounts {
		if account.Platform != service.PlatformOpenAI || (account.Type != service.AccountTypeOAuth && account.Type != service.AccountTypeSetupToken) || account.IsShadow() {
			response.BadRequest(c, "全部所选账号必须支持 Codex 路由续期")
			return
		}
	}
	seeds := make([]service.AccountJobItemSeed, 0, len(req.AccountIDs)*len(models))
	for _, id := range req.AccountIDs {
		for _, model := range models {
			target := id
			metadata, _ := json.Marshal(map[string]any{"account_id": id, "model_id": model})
			seeds = append(seeds, service.AccountJobItemSeed{Ordinal: len(seeds) + 1, TargetAccountID: &target, Metadata: metadata})
		}
	}
	h.submitAccountJob(c, service.AccountJobKindCodexTicketStop, req, seeds)
}

func (h *AccountHandler) executeCodexTicketStop(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req codexTicketHarvestRequest
	var metadata struct {
		Model string `json:"model_id"`
	}
	if json.Unmarshal(raw, &req) != nil || json.Unmarshal(item.Metadata, &metadata) != nil || metadata.Model == "" {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	id, ok := accountJobTarget(item)
	if !ok || h.codexTicketGateway == nil {
		return accountJobFailed(item.ID, "target_missing")
	}
	account, err := h.adminService.GetAccount(ctx, id)
	if err != nil || account == nil || account.Platform != service.PlatformOpenAI || (account.Type != service.AccountTypeOAuth && account.Type != service.AccountTypeSetupToken) || account.IsShadow() {
		return accountJobFailed(item.ID, "account_ineligible")
	}
	if err := h.codexTicketGateway.StopCodexTicketRenewal(ctx, id, []string{metadata.Model}); err != nil {
		failure := accountJobFailed(item.ID, "stop_failed")
		if ctx.Err() != nil {
			failure.Status = service.AccountJobItemStatusCanceled
		}
		return failure
	}
	return accountJobSucceeded(item.ID, map[string]any{"account_id": id, "model_id": metadata.Model})
}
