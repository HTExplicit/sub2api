package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
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
