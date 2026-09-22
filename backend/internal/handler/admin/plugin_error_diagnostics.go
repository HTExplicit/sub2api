package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ErrorDiagnostics is installed behind the existing administrator middleware
// and a named resource grant. It never exposes the underlying log document.
func (h *PluginHandler) ErrorDiagnostics(ops *OpsHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h == nil || h.manager == nil || ops == nil || ops.opsService == nil {
			response.Error(c, http.StatusServiceUnavailable, "Diagnostics unavailable")
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid error id")
			return
		}
		detail, err := ops.opsService.GetErrorLogByID(c.Request.Context(), id)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, h.manager.ProjectErrorDiagnostics(c.Request.Context(), detail))
	}
}
