package admin

import (
	"context"
	"encoding/json"
	"fmt"
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
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式不正确："+err.Error())
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
		response.BadRequest(c, fmt.Sprintf("所选账号已发生变化：请求 %d 个，找到 %d 个", len(req.AccountIDs), len(accounts)))
		return
	}
	for _, account := range accounts {
		if account.Platform != service.PlatformOpenAI || (account.Type != service.AccountTypeOAuth && account.Type != service.AccountTypeSetupToken) || account.IsShadow() {
			response.BadRequest(c, fmt.Sprintf("全部所选账号必须支持 Codex 路由续期：账号 %d（%s）是 %s/%s，影子账号=%t", account.ID, account.Name, account.Platform, account.Type, account.IsShadow()))
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
	if err := json.Unmarshal(raw, &req); err != nil {
		return codexTicketJobFailure(item.ID, "payload_invalid", "decode the job payload: "+err.Error(), nil, false)
	}
	if err := json.Unmarshal(item.Metadata, &metadata); err != nil {
		return codexTicketJobFailure(item.ID, "payload_invalid", "decode the item metadata: "+err.Error(), nil, false)
	}
	if metadata.Model == "" {
		return codexTicketJobFailure(item.ID, "payload_invalid", "the item metadata has no model_id", nil, false)
	}
	id, ok := accountJobTarget(item)
	if !ok {
		return codexTicketJobFailure(item.ID, "target_missing", "the job item has no target account", map[string]any{"model_id": metadata.Model}, false)
	}
	details := map[string]any{"account_id": id, "model_id": metadata.Model}
	if h.codexTicketGateway == nil {
		return codexTicketJobFailure(item.ID, "ticket_plugin_unavailable", "the Codex runtime gateway is not configured", details, false)
	}
	account, err := h.adminService.GetAccount(ctx, id)
	switch {
	case err != nil:
		return codexTicketJobFailure(item.ID, "account_ineligible", fmt.Sprintf("read account %d: %v", id, err), details, false)
	case account == nil:
		return codexTicketJobFailure(item.ID, "account_ineligible", fmt.Sprintf("account %d not found", id), details, false)
	case account.Platform != service.PlatformOpenAI || (account.Type != service.AccountTypeOAuth && account.Type != service.AccountTypeSetupToken) || account.IsShadow():
		return codexTicketJobFailure(item.ID, "account_ineligible", fmt.Sprintf("account %d is %s/%s (shadow=%t); only OpenAI OAuth/setup-token accounts renew Codex routes", id, account.Platform, account.Type, account.IsShadow()), details, false)
	}
	if err := h.codexTicketGateway.StopCodexTicketRenewal(ctx, id, []string{metadata.Model}); err != nil {
		details["error"] = err.Error()
		return codexTicketJobFailure(item.ID, "stop_failed", err.Error(), details, ctx.Err() != nil)
	}
	details["code"], details["message"] = "ticket_stopped", service.CodexTicketFailure("ticket_stopped").Message
	return accountJobSucceeded(item.ID, details)
}
