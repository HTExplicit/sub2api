package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CodexErrorDiagnostics is installed behind the existing administrator
// middleware and returns every continuation diagnostic stored on the error.
func (h *AccountHandler) CodexErrorDiagnostics(ops *OpsHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		if h == nil || ops == nil || ops.opsService == nil {
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
		response.Success(c, service.ProjectCodexErrorDiagnostics(detail))
	}
}
