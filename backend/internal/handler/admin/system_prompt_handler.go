package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SystemPromptHandler manages the system prompt library, the global switch,
// the site default and account bindings.
type SystemPromptHandler struct {
	service *service.SystemPromptService
}

func NewSystemPromptHandler(prompts *service.SystemPromptService) *SystemPromptHandler {
	return &SystemPromptHandler{service: prompts}
}

// Get returns the configuration and how accounts are bound.
func (h *SystemPromptHandler) Get(c *gin.Context) {
	state, err := h.service.State(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, state)
}

// Save replaces the whole configuration.
func (h *SystemPromptHandler) Save(c *gin.Context) {
	var request service.SystemPromptConfig
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid system prompt configuration")
		return
	}
	state, err := h.service.Save(c.Request.Context(), request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": state.Enabled, "result": "system_prompts_saved"})
	response.Success(c, state)
}

// SetBindings applies one binding to the selected accounts.
func (h *SystemPromptHandler) SetBindings(c *gin.Context) {
	var request struct {
		AccountIDs []int64 `json:"account_ids" binding:"required"`
		Mode       string  `json:"mode" binding:"required"`
		PromptID   string  `json:"prompt_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid account system prompt binding")
		return
	}
	updated, err := h.service.SetBindings(c.Request.Context(), request.AccountIDs, service.SystemPromptBinding{Mode: request.Mode, PromptID: request.PromptID})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"requested_count": len(request.AccountIDs), "matched_count": updated, "result": "system_prompt_bindings_updated"})
	response.Success(c, gin.H{"updated": updated})
}
