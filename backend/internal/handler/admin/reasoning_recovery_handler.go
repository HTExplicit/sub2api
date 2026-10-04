package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ReasoningRecoveryHandler manages the global reasoning recovery switch.
type ReasoningRecoveryHandler struct {
	service *service.ReasoningRecoveryService
}

func NewReasoningRecoveryHandler(recovery *service.ReasoningRecoveryService) *ReasoningRecoveryHandler {
	return &ReasoningRecoveryHandler{service: recovery}
}

// Get returns the stored switch.
// GET /api/v1/admin/reasoning-recovery
func (h *ReasoningRecoveryHandler) Get(c *gin.Context) {
	config, err := h.service.Get(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, config)
}

// Save stores the switch and returns what was saved.
// PUT /api/v1/admin/reasoning-recovery
func (h *ReasoningRecoveryHandler) Save(c *gin.Context) {
	raw, err := c.GetRawData()
	var request service.ReasoningRecoveryConfig
	if err == nil {
		request, err = service.DecodeReasoningRecoveryConfig(raw)
	}
	if err != nil {
		response.BadRequest(c, "Invalid reasoning recovery settings: "+err.Error())
		return
	}
	config, err := h.service.Save(c.Request.Context(), request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": config.Enabled, "result": "reasoning_recovery_saved"})
	response.Success(c, config)
}
