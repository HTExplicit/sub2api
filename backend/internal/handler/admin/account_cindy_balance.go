package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) ClearCindyBalanceInsufficient(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.adminService.ClearCindyBalanceInsufficient(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.buildAccountResponseWithRuntime(c.Request.Context(), account))
}

func (h *AccountHandler) PreviewCindyInsufficientDeletion(c *gin.Context) {
	preview, err := h.adminService.PreviewCindyInsufficientDeletion(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preview)
}

func (h *AccountHandler) DeleteCindyInsufficient(c *gin.Context) {
	var req struct {
		ExpectedCount int    `json:"expected_count"`
		Fingerprint   string `json:"fingerprint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.ExpectedCount < 0 || strings.TrimSpace(req.Fingerprint) == "" {
		response.BadRequest(c, "expected_count and fingerprint are required")
		return
	}
	result, err := h.adminService.DeleteCindyInsufficient(c.Request.Context(), req.ExpectedCount, req.Fingerprint)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
