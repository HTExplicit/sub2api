package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CodexGatewayBorrowHandler struct {
	core    *service.CodexGatewayBorrowService
	runner  *service.CodexGatewayBorrowTestRunner
	preview *service.CodexGatewayBorrowPreviewService
}

func NewCodexGatewayBorrowHandler(core *service.CodexGatewayBorrowService, runner *service.CodexGatewayBorrowTestRunner, preview *service.CodexGatewayBorrowPreviewService) *CodexGatewayBorrowHandler {
	return &CodexGatewayBorrowHandler{core: core, runner: runner, preview: preview}
}

func codexGatewayBorrowPrivateHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
}

func (h *CodexGatewayBorrowHandler) GetConfig(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	config, err := h.core.GetConfig(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, config)
}

func (h *CodexGatewayBorrowHandler) SaveConfig(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	var request service.CodexGatewayBorrowConfig
	raw, err := c.GetRawData()
	if err == nil {
		request, err = service.DecodeCodexGatewayBorrowConfig(raw)
	}
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	config, err := h.core.SaveConfig(c.Request.Context(), request)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "codex_gateway_borrow_config_saved", "enabled": config.Enabled})
	response.Success(c, config)
}

func (h *CodexGatewayBorrowHandler) Status(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	response.Success(c, h.core.CurrentStatus(c.Request.Context()))
}

func (h *CodexGatewayBorrowHandler) Prepare(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	status, err := h.core.Prepare(c.Request.Context())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, status)
}

func (h *CodexGatewayBorrowHandler) Verify(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	var request struct {
		AccountID int64  `json:"account_id"`
		Model     string `json:"model"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if request.AccountID <= 0 || strings.TrimSpace(request.Model) == "" {
		response.BadRequest(c, "account_id and model are required")
		return
	}
	result, err := h.core.Verify(c.Request.Context(), request.AccountID, strings.TrimSpace(request.Model))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, result)
}

func codexGatewayBorrowTestError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrCodexGatewayBorrowTestReplayConflict):
		status = http.StatusConflict
	case errors.Is(err, service.ErrCodexGatewayBorrowTestInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, service.ErrCodexGatewayBorrowTestExpired):
		status = http.StatusGone
	case errors.Is(err, service.ErrCodexGatewayBorrowTestNotFound):
		status = http.StatusNotFound
	}
	response.Error(c, status, err.Error())
}

func (h *CodexGatewayBorrowHandler) StartTests(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	var request service.CodexGatewayBorrowTestRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(request.ClientTaskID)); err != nil {
		response.BadRequest(c, "client_task_id must be a UUID")
		return
	}
	if len(request.Targets) == 0 {
		response.BadRequest(c, "targets is required")
		return
	}
	var createdBy int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		createdBy = subject.UserID
	}
	task, replayed, err := h.runner.Create(c.Request.Context(), createdBy, request)
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "codex_gateway_borrow_manual_test", "task_id": task.ID, "replayed": replayed, "targets": task.Total})
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	var writeMu sync.Mutex
	emit := func(event service.CodexGatewayBorrowTestEvent) {
		if ctx.Err() != nil {
			return
		}
		if event.Task != nil {
			event.Task = h.preview.DecorateTask(event.Task)
		}
		if event.Result != nil {
			event.Result = h.preview.DecorateResult(event.Result)
		}
		payload, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			cancel()
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if _, writeErr := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); writeErr != nil {
			cancel()
			return
		}
		c.Writer.Flush()
	}
	emit(service.CodexGatewayBorrowTestEvent{Type: "task_start", Task: task})
	if replayed {
		emit(service.CodexGatewayBorrowTestEvent{Type: "task_complete", Task: task})
		return
	}
	keepAliveDone, keepAliveStopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(keepAliveStopped)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-keepAliveDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				writeMu.Lock()
				if _, writeErr := fmt.Fprint(c.Writer, ": keep-alive\n\n"); writeErr != nil {
					cancel()
				} else {
					c.Writer.Flush()
				}
				writeMu.Unlock()
			}
		}
	}()
	runErr := h.runner.Run(ctx, task, emit)
	close(keepAliveDone)
	<-keepAliveStopped
	if runErr != nil && ctx.Err() == nil {
		// An unexpected persistence failure remains fully visible on the live
		// stream even if the database cannot save that final failure observation.
		task.Status, task.Error = "incomplete", runErr.Error()
		emit(service.CodexGatewayBorrowTestEvent{Type: "task_complete", Task: task})
	}
}

func (h *CodexGatewayBorrowHandler) ListTests(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	page, size := response.ParsePagination(c)
	if rawSize := c.Query("size"); rawSize != "" {
		if parsed, err := strconv.Atoi(rawSize); err == nil && parsed > 0 {
			size = parsed
		}
	}
	list, err := h.runner.List(c.Request.Context(), page, size)
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	response.Success(c, list)
}

func (h *CodexGatewayBorrowHandler) GetTest(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	id := strings.TrimSpace(c.Param("id"))
	if id == "last" {
		list, err := h.runner.List(c.Request.Context(), 1, 1)
		if err != nil {
			codexGatewayBorrowTestError(c, err)
			return
		}
		if len(list.Items) == 0 {
			response.NotFound(c, "no unexpired pelican test")
			return
		}
		id = list.Items[0].ID
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		response.BadRequest(c, "test ID must be a UUID or last")
		return
	}
	task, err := h.runner.Get(c.Request.Context(), parsed.String())
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	response.Success(c, h.preview.DecorateTask(task))
}

func (h *CodexGatewayBorrowHandler) ServePreview(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	c.Header("Content-Security-Policy", service.CodexGatewayBorrowPreviewCSP)
	c.Header("X-Frame-Options", "SAMEORIGIN")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	html, err := h.preview.Resolve(c.Request.Context(), strings.TrimSpace(c.Param("cap")))
	if err != nil {
		c.Status(http.StatusGone)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// Diagnose streams explicit administrator observations; opening a page never invokes it.
func (h *CodexGatewayBorrowHandler) Diagnose(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	var request service.CodexBorrowDiagnosticRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "codex_gateway_borrow_diagnostic", "account_id": request.AccountID, "model": request.Model, "transport": request.Transport})
	c.Header("Content-Type", "text/event-stream")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	var emission sync.Mutex
	var observedRequests int32
	emit := func(event service.CodexBorrowDiagnosticEvent) {
		emission.Lock()
		defer emission.Unlock()
		if event.Requests > observedRequests {
			observedRequests = event.Requests
		}
		if event.Type == "error" {
			event.Requests = observedRequests
		}
		if c.Request.Context().Err() != nil {
			return
		}
		raw, err := json.Marshal(event)
		if err == nil {
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", raw)
			c.Writer.Flush()
		}
	}
	if err := h.core.Diagnose(c.Request.Context(), request, emit); err != nil {
		emit(service.CodexBorrowDiagnosticEvent{Type: "error", Error: err.Error(), Limit: service.CodexBorrowDiagnosticMaxRequests})
	}
}
