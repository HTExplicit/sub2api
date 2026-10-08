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

// PelicanTestHandler owns the independent administrator entry point. Borrowing
// configuration and preparation remain in CodexGatewayBorrowHandler; this entry
// selects only an account, a model, an effort, and a generation budget.
type PelicanTestHandler struct {
	catalog *service.PelicanTestCatalog
	runner  *service.CodexGatewayBorrowTestRunner
	preview *service.CodexGatewayBorrowPreviewService
}

func NewPelicanTestHandler(catalog *service.PelicanTestCatalog, runner *service.CodexGatewayBorrowTestRunner, preview *service.CodexGatewayBorrowPreviewService) *PelicanTestHandler {
	return &PelicanTestHandler{catalog: catalog, runner: runner, preview: preview}
}

func (h *PelicanTestHandler) GetOptions(c *gin.Context) {
	h.options(c, nil)
}

func (h *PelicanTestHandler) GetAccountOptions(c *gin.Context) {
	codexGatewayBorrowPrivateHeaders(c)
	var request struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	h.options(c, request.AccountIDs)
}

func (h *PelicanTestHandler) options(c *gin.Context, accountIDs []int64) {
	codexGatewayBorrowPrivateHeaders(c)
	options, err := h.catalog.Options(c.Request.Context(), accountIDs)
	if err != nil {
		if errors.Is(err, service.ErrPelicanTestOptionsInvalidRequest) {
			response.BadRequest(c, err.Error())
		} else {
			response.Error(c, http.StatusInternalServerError, err.Error())
		}
		return
	}
	response.Success(c, options)
}

func (h *PelicanTestHandler) StartTests(c *gin.Context) {
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
	// Only this server-side entry point can select account execution. A JSON
	// field cannot change the legacy borrowing handler's existing contract.
	request.Standalone = true
	var createdBy int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		createdBy = subject.UserID
	}
	task, replayed, err := h.runner.Create(c.Request.Context(), createdBy, request)
	if err != nil {
		codexGatewayBorrowTestError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "pelican_manual_test", "task_id": task.ID, "replayed": replayed, "targets": task.Total})
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
				if ctx.Err() == nil {
					if _, writeErr := fmt.Fprint(c.Writer, ": keep-alive\n\n"); writeErr != nil {
						cancel()
					} else {
						c.Writer.Flush()
					}
				}
				writeMu.Unlock()
			}
		}
	}()
	runErr := h.runner.Run(ctx, task, emit)
	close(keepAliveDone)
	<-keepAliveStopped
	if runErr != nil && ctx.Err() == nil {
		task.Status, task.Error = "incomplete", runErr.Error()
		emit(service.CodexGatewayBorrowTestEvent{Type: "task_complete", Task: task})
	}
}

func (h *PelicanTestHandler) ListTests(c *gin.Context) {
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

func (h *PelicanTestHandler) GetTest(c *gin.Context) {
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

func (h *PelicanTestHandler) ServePreview(c *gin.Context) {
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
