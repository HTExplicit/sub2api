package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type DeepSeekReasoningHandler struct {
	service *service.DeepSeekReasoningService
}

func NewDeepSeekReasoningHandler(s *service.DeepSeekReasoningService) *DeepSeekReasoningHandler {
	return &DeepSeekReasoningHandler{service: s}
}

func (h *DeepSeekReasoningHandler) Get(c *gin.Context) {
	config, err := h.service.Get(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, config)
}

func (h *DeepSeekReasoningHandler) Save(c *gin.Context) {
	var request struct {
		Enabled *bool  `json:"enabled" binding:"required"`
		Effort  string `json:"effort" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid DeepSeek reasoning configuration")
		return
	}
	config := service.DeepSeekReasoningConfig{Enabled: *request.Enabled, Effort: request.Effort}
	if err := h.service.Save(c.Request.Context(), config); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": config.Enabled, "effort": config.Effort, "result": "deepseek_reasoning_saved"})
	response.Success(c, config)
}
