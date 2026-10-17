package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *PelicanTestHandler) Gallery(c *gin.Context) {
	pelicanPrivateHeaders(c)
	if id := c.Query("task_id"); id != "" {
		if _, err := uuid.Parse(id); err != nil {
			response.BadRequest(c, "task_id must be a UUID")
			return
		}
	}
	page, size := response.ParsePagination(c)
	account, _ := strconv.ParseInt(c.Query("account_id"), 10, 64)
	rows, err := h.runner.Gallery(c.Request.Context(), service.PelicanResultFilter{Page: page, Size: size, TaskID: c.Query("task_id"), AccountID: account, Model: c.Query("model"), Status: c.Query("status"), Effort: c.Query("effort"), Scene: c.Query("scene"), Verdict: c.Query("verdict")})
	if err != nil {
		pelicanTestError(c, err)
		return
	}
	for _, row := range rows.Items {
		h.preview.DecorateSummary(row)
	}
	response.Success(c, rows)
}

func (h *PelicanTestHandler) Retest(c *gin.Context) {
	id, ok := pelicanTaskID(c)
	if !ok {
		return
	}
	var request service.PelicanRetestRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	var createdBy int64
	if subject, found := middleware.GetAuthSubjectFromContext(c); found {
		createdBy = subject.UserID
	}
	task, err := h.runner.Retest(c.Request.Context(), createdBy, id, request, h.catalog)
	if err != nil {
		pelicanTestError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "pelican_retest", "task_id": task.ID, "source_task_id": id, "replayed": task.Replayed})
	response.Accepted(c, task)
}

func (h *PelicanTestHandler) ServeSVG(c *gin.Context) {
	pelicanPrivateHeaders(c)
	c.Header("Content-Security-Policy", "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; sandbox")
	svg, err := h.preview.ResolveSVG(c.Request.Context(), c.Param("cap"))
	if err != nil {
		c.Status(http.StatusGone)
		return
	}
	c.Data(http.StatusOK, "image/svg+xml; charset=utf-8", []byte(svg))
}
