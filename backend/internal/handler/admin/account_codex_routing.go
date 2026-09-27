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
		response.Error(c, 503, "Codex fingerprint unavailable: "+err.Error())
		return
	}
	response.Success(c, view)
}

func (h *AccountHandler) ValidateCodexRouting(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var request service.CodexRoutingValidationRequest
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid Codex validation request: account id "+strconv.Quote(c.Param("id")))
		return
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid Codex validation request: "+err.Error())
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "Codex runtime unavailable")
		return
	}
	result, err := h.codexTicketGateway.ValidateCodexRouting(c.Request.Context(), id, request)
	if err != nil {
		response.Error(c, 409, "Codex validation unavailable: "+err.Error())
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) SelectCodexProfile(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var choice service.CodexProfileSelection
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid profile selection: account id "+strconv.Quote(c.Param("id")))
		return
	}
	if err := c.ShouldBindJSON(&choice); err != nil {
		response.BadRequest(c, "Invalid profile selection: "+err.Error())
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "Codex runtime unavailable")
		return
	}
	if err := h.codexTicketGateway.SelectCodexProfile(c.Request.Context(), id, choice); err != nil {
		response.Error(c, 409, "Profile not applied: "+err.Error())
		return
	}
	response.Success(c, gin.H{"applied": true})
}
