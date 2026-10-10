package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *PelicanTestHandler) StartTask(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	var request service.CodexGatewayBorrowTestRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	var createdBy int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		createdBy = subject.UserID
	}
	task, err := h.runner.StartBackground(c.Request.Context(), createdBy, request, h.catalog)
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "pelican_background_test", "task_id": task.ID, "replayed": task.Replayed, "targets": task.Total})
	response.Accepted(c, task)
}

func pelicanTaskID(c *gin.Context) (string, bool) {
	codexGatewayBorrowPrivateHeaders(c)
	id, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		response.BadRequest(c, "task ID must be a UUID")
		return "", false
	}
	return id.String(), true
}

func (h *PelicanTestHandler) GetTask(c *gin.Context) {
	id, ok := pelicanTaskID(c)
	if !ok {
		return
	}
	task, err := h.runner.TaskSnapshot(c.Request.Context(), id)
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	response.Success(c, task)
}

func (h *PelicanTestHandler) GetTaskResults(c *gin.Context) {
	id, ok := pelicanTaskID(c)
	if !ok {
		return
	}
	page, size := response.ParsePagination(c)
	accountID, _ := strconv.ParseInt(c.Query("account_id"), 10, 64)
	result, err := h.runner.TaskResults(c.Request.Context(), id, service.PelicanResultFilter{Page: page, Size: size, AccountID: accountID, Model: c.Query("model"), Status: c.Query("status")})
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *PelicanTestHandler) GetTaskResult(c *gin.Context) {
	id, ok := pelicanTaskID(c)
	if !ok {
		return
	}
	resultID, err := uuid.Parse(c.Param("result_id"))
	if err != nil {
		response.BadRequest(c, "result ID must be a UUID")
		return
	}
	result, err := h.runner.TaskResult(c.Request.Context(), id, resultID.String())
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	response.Success(c, h.preview.DecorateResult(result))
}

func (h *PelicanTestHandler) CancelTask(c *gin.Context) {
	id, ok := pelicanTaskID(c)
	if !ok {
		return
	}
	task, err := h.runner.CancelBackground(c.Request.Context(), id)
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "pelican_cancel_requested", "task_id": task.ID})
	response.Success(c, task)
}

// Read-only progress stream. Disconnecting never owns or cancels execution;
// reconnect starts with a durable snapshot, not a POST or model replay.
func (h *PelicanTestHandler) ObserveTask(c *gin.Context) {
	id, ok := pelicanTaskID(c)
	if !ok {
		return
	}
	task, err := h.runner.TaskSnapshot(c.Request.Context(), id)
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		kind := "snapshot"
		if task.Status != "pending" && task.Status != "running" {
			kind = "task_complete"
		}
		payload, err := json.Marshal(struct {
			Type string                       `json:"type"`
			Task *service.PelicanTaskSnapshot `json:"task"`
		}{kind, task})
		if err != nil {
			return
		}
		if _, err = fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
			return
		}
		c.Writer.Flush()
		if kind == "task_complete" {
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
		task, err = h.runner.TaskSnapshot(c.Request.Context(), id)
		if err != nil {
			return
		}
	}
}
