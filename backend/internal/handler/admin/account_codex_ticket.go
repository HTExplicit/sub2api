package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type codexTicketHarvestRequest struct {
	AccountIDs []int64  `json:"account_ids"`
	Models     []string `json:"models"`
	Force      bool     `json:"force"`
}

func (h *AccountHandler) SetCodexTicketGateway(gateway *service.OpenAIGatewayService) {
	h.codexTicketGateway = gateway
}

func (h *AccountHandler) CodexTicketPolicy(c *gin.Context) {
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "票据服务不可用")
		return
	}
	response.Success(c, gin.H{"models": h.codexTicketGateway.CodexTicketModels(), "enabled": h.codexTicketGateway.CodexTicketsEnabled(c.Request.Context())})
}

func (h *AccountHandler) ticketModels(models []string) ([]string, error) {
	if h.codexTicketGateway == nil {
		return nil, fmt.Errorf("票据服务不可用")
	}
	allowed := map[string]bool{}
	for _, m := range h.codexTicketGateway.CodexTicketModels() {
		allowed[m] = true
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("请选择至少一个模型")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, m := range models {
		m = strings.TrimSpace(m)
		if !allowed[m] {
			return nil, fmt.Errorf("模型不在票据配置范围内")
		}
		if !seen[m] {
			out = append(out, m)
			seen[m] = true
		}
	}
	return out, nil
}

func (h *AccountHandler) HarvestCodexTicket(c *gin.Context) { h.submitCodexTicketHarvest(c, true) }
func (h *AccountHandler) BatchHarvestCodexTickets(c *gin.Context) {
	h.submitCodexTicketHarvest(c, false)
}

func (h *AccountHandler) submitCodexTicketHarvest(c *gin.Context, single bool) {
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
	req.Models = models
	if !h.codexTicketGateway.CodexTicketsEnabled(c.Request.Context()) {
		response.BadRequest(c, "请先开启票据总开关")
		return
	}
	seeds := []service.AccountJobItemSeed{}
	for _, id := range req.AccountIDs {
		for _, model := range req.Models {
			target := id
			metadata, _ := json.Marshal(map[string]any{"account_id": id, "model_id": model})
			seeds = append(seeds, service.AccountJobItemSeed{Ordinal: len(seeds) + 1, TargetAccountID: &target, Metadata: metadata})
		}
	}
	h.submitAccountJob(c, service.AccountJobKindCodexTicketHarvest, req, seeds)
}

func (h *AccountHandler) StopCodexTicketRenewal(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "账号ID不正确")
		return
	}
	var req codexTicketHarvestRequest
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "请求格式不正确")
		return
	}
	models, err := h.ticketModels(req.Models)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err = h.codexTicketGateway.StopCodexTicketRenewal(c.Request.Context(), id, models); err != nil {
		response.InternalError(c, "停止续期失败")
		return
	}
	response.Success(c, gin.H{"stopped": true})
}

func (h *AccountHandler) executeCodexTicketHarvest(ctx context.Context, job *service.AccountJob, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req codexTicketHarvestRequest
	var meta struct {
		Model string `json:"model_id"`
	}
	if json.Unmarshal(raw, &req) != nil || json.Unmarshal(item.Metadata, &meta) != nil || meta.Model == "" {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	id, ok := accountJobTarget(item)
	if !ok || h.codexTicketGateway == nil {
		return accountJobFailed(item.ID, "target_missing")
	}
	result := h.codexTicketGateway.HarvestCodexTicket(ctx, id, meta.Model, fmt.Sprintf("job:%d:item:%d", job.ID, item.ID), job.ID, req.Force)
	metadata := map[string]any{"account_id": id, "model_id": meta.Model, "ticket_result": result}
	if result.Success {
		return accountJobSucceeded(item.ID, metadata)
	}
	failure := accountJobFailed(item.ID, result.Code)
	failure.Metadata, _ = json.Marshal(metadata)
	if ctx.Err() != nil {
		failure.Status = service.AccountJobItemStatusCanceled
	}
	return failure
}
