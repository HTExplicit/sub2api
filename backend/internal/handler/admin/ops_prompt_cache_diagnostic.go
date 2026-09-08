package admin

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// These routes are registered only under the existing admin-authenticated group.
// Diagnostics are temporary process memory, never a persistent account setting.
func (h *OpsHandler) promptCacheDiagnosticAvailable(c *gin.Context) bool {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return false
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	return true
}

func (h *OpsHandler) StartPromptCacheDiagnostic(c *gin.Context) {
	if !h.promptCacheDiagnosticAvailable(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var cfg service.PromptCacheDiagnosticConfig
	if err := decoder.Decode(&cfg); err != nil {
		response.BadRequest(c, "Invalid diagnostic configuration")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid diagnostic configuration")
		return
	}
	view, err := service.StartOpenAIPromptCacheDiagnostic(cfg)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, view)
}

func (h *OpsHandler) GetPromptCacheDiagnostic(c *gin.Context) {
	if !h.promptCacheDiagnosticAvailable(c) {
		return
	}
	h.promptCacheDiagnosticResult(c, false)
}

func (h *OpsHandler) StopPromptCacheDiagnostic(c *gin.Context) {
	if !h.promptCacheDiagnosticAvailable(c) {
		return
	}
	h.promptCacheDiagnosticResult(c, true)
}

func (h *OpsHandler) promptCacheDiagnosticResult(c *gin.Context, stop bool) {
	view := service.GetOpenAIPromptCacheDiagnostic(c.Param("id"), stop)
	if view == nil {
		response.NotFound(c, "Diagnostic not found or retention expired")
		return
	}
	response.Success(c, view)
}
