package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
)

func (h *SystemPromptHandler) Rules(c *gin.Context) {
	state, err := h.service.PromptRulesState()
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, state)
}

func (h *SystemPromptHandler) UpdateRules(c *gin.Context) {
	var request struct {
		ExpectedRevision int64                        `json:"expected_revision" binding:"required"`
		Policy           extensionv1.PromptRulePolicy `json:"policy" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid prompt rule policy")
		return
	}
	actor, ok := h.actorID(c)
	if !ok {
		return
	}
	state, err := h.service.UpdatePromptRules(c.Request.Context(), request.Policy, request.ExpectedRevision, actor)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"revision": state.Revision, "rule_count": len(state.Policy.Rules), "result": "rules_updated"})
	response.Success(c, state)
}

func (h *SystemPromptHandler) AccountBindings(c *gin.Context) {
	var request struct {
		AccountIDs []int64 `json:"account_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid account selection")
		return
	}
	views, err := h.service.PromptAccountBindings(c.Request.Context(), request.AccountIDs)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, views)
}

func (h *SystemPromptHandler) UpdateAccountBindings(c *gin.Context) {
	var request struct {
		ExpectedRevision int64                         `json:"expected_revision" binding:"required"`
		Updates          []service.PromptBindingUpdate `json:"updates" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid account prompt binding")
		return
	}
	results, err := h.service.UpdatePromptAccountBindings(c.Request.Context(), request.Updates, request.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"account_count": len(results), "result": "bindings_updated"})
	response.Success(c, results)
}

func (h *SystemPromptHandler) PreviewRules(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("account_id"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "Invalid account")
		return
	}
	var request service.PromptRulesPreviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid prompt preview")
		return
	}
	result, err := h.service.PreviewPromptRules(c.Request.Context(), id, request)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, result)
}
