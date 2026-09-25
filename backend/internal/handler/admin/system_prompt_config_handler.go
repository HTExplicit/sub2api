package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SystemPromptHandler) Config(c *gin.Context) {
	state, err := h.service.PromptConfig(c.Request.Context())
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, state)
}

func (h *SystemPromptHandler) SaveConfig(c *gin.Context) {
	var request service.PromptConfigUpdate
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid prompt configuration")
		return
	}
	actor, ok := h.actorID(c)
	if !ok {
		return
	}
	state, err := h.service.SavePromptConfig(c.Request.Context(), request, actor)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"revision": state.Revision, "rule_count": len(state.Policy.Rules), "result": "prompt_config_saved"})
	response.Success(c, state)
}
