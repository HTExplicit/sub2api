package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) CodexFingerprint(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account identifier")
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "Codex runtime unavailable")
		return
	}
	view, err := h.codexTicketGateway.CodexFingerprint(c.Request.Context(), id)
	if err != nil {
		response.Error(c, 503, "Codex fingerprint unavailable")
		return
	}
	response.Success(c, view)
}

func (h *AccountHandler) ValidateCodexRouting(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var request service.CodexRoutingValidationRequest
	if err != nil || id <= 0 || c.ShouldBindJSON(&request) != nil {
		response.BadRequest(c, "Invalid Codex validation request")
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "Codex runtime unavailable")
		return
	}
	result, err := h.codexTicketGateway.ValidateCodexRouting(c.Request.Context(), id, request)
	if err != nil {
		response.Error(c, 409, "Codex validation unavailable or budget already spent")
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) SelectCodexProfile(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var choice service.CodexProfileSelection
	if err != nil || id <= 0 || c.ShouldBindJSON(&choice) != nil {
		response.BadRequest(c, "Invalid profile selection")
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "Codex runtime unavailable")
		return
	}
	if err := h.codexTicketGateway.SelectCodexProfile(c.Request.Context(), id, choice); err != nil {
		response.Error(c, 409, "Account identity changed or profile unavailable")
		return
	}
	response.Success(c, gin.H{"applied": true})
}
