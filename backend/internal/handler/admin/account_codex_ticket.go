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
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式不正确："+err.Error())
		return
	}
	models, err := h.ticketModels(req.Models)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err = h.codexTicketGateway.StopCodexTicketRenewal(c.Request.Context(), id, models); err != nil {
		response.InternalError(c, "停止续期失败："+err.Error())
		return
	}
	response.Success(c, gin.H{"stopped": true})
}

// codexTicketJobFailure builds a failed item directly: ErrorCode keeps the
// result code and ErrorMessage the verbatim error text (shared job contract).
func codexTicketJobFailure(itemID int64, code, message string, metadata map[string]any, canceled bool) service.AccountJobExecutionResult {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if _, exists := metadata["message"]; !exists {
		metadata["message"] = message
	}
	raw, _ := json.Marshal(metadata)
	result := service.AccountJobExecutionResult{ItemID: itemID, Status: service.AccountJobItemStatusFailed, Metadata: raw, ErrorCode: code, ErrorMessage: message}
	if canceled {
		result.Status = service.AccountJobItemStatusCanceled
	}
	return result
}

// codexTicketJobMetadata keeps the complete harvest outcome. message and facts
// stay at the top level, where the operation views render them; the upstream
// body, STATE and response headers are stored once, in those facts.
func codexTicketJobMetadata(accountID int64, model string, result service.CodexTicketResult) map[string]any {
	stored := result
	stored.Facts = nil
	if result.Observation != nil {
		observation := *result.Observation
		observation.UpstreamBody, observation.State, observation.ResponseHeaders, observation.ResponseHeadersOmitted = "", "", nil, nil
		stored.Observation = &observation
	}
	message := result.Message
	if detail := result.Detail(); detail != "" && detail != message {
		if message == "" {
			message = detail
		} else {
			message += "：" + detail
		}
	}
	metadata := map[string]any{"account_id": accountID, "model_id": model, "code": result.Code, "message": message, "facts": result.Facts, "ticket_result": stored}
	if result.HTTPStatus > 0 {
		metadata["http_status"] = result.HTTPStatus
	}
	if result.ResponseModel != "" {
		metadata["response_model"] = result.ResponseModel
	}
	if detail := result.Detail(); detail != "" {
		metadata["error"] = detail
	}
	return metadata
}

func (h *AccountHandler) executeCodexTicketHarvest(ctx context.Context, job *service.AccountJob, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req codexTicketHarvestRequest
	var meta struct {
		Model string `json:"model_id"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return codexTicketJobFailure(item.ID, "payload_invalid", "decode the job payload: "+err.Error(), nil, false)
	}
	if err := json.Unmarshal(item.Metadata, &meta); err != nil {
		return codexTicketJobFailure(item.ID, "payload_invalid", "decode the item metadata: "+err.Error(), nil, false)
	}
	if meta.Model == "" {
		return codexTicketJobFailure(item.ID, "payload_invalid", "the item metadata has no model_id", nil, false)
	}
	id, ok := accountJobTarget(item)
	if !ok {
		return codexTicketJobFailure(item.ID, "target_missing", "the job item has no target account", map[string]any{"model_id": meta.Model}, false)
	}
	if h.codexTicketGateway == nil {
		return codexTicketJobFailure(item.ID, "ticket_plugin_unavailable", "the Codex runtime gateway is not configured", map[string]any{"account_id": id, "model_id": meta.Model}, false)
	}
	result := h.codexTicketGateway.HarvestCodexTicket(ctx, id, meta.Model, fmt.Sprintf("job:%d:item:%d", job.ID, item.ID), job.ID, req.Force)
	metadata := codexTicketJobMetadata(id, meta.Model, result)
	if result.Success {
		return accountJobSucceeded(item.ID, metadata)
	}
	message := result.Detail()
	if message == "" {
		message = result.Message
	}
	return codexTicketJobFailure(item.ID, result.Code, message, metadata, ctx.Err() != nil)
}
