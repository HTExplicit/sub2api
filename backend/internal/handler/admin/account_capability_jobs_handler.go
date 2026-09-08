package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountCapabilityHandler struct {
	capabilities *service.AccountCapabilityService
}

func NewAccountCapabilityHandler(capabilities *service.AccountCapabilityService) *AccountCapabilityHandler {
	return &AccountCapabilityHandler{capabilities: capabilities}
}

func (h *AccountCapabilityHandler) Inventory(c *gin.Context) {
	filter, ok := accountCapabilityQuery(c)
	if !ok {
		return
	}
	page, err := h.capabilities.Inventory(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, accountCapabilityHTTPError(err))
		return
	}
	response.Success(c, page)
}

func (h *AccountCapabilityHandler) ListRuns(c *gin.Context) {
	filter, ok := accountCapabilityQuery(c)
	if !ok {
		return
	}
	page, err := h.capabilities.ListRuns(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, accountCapabilityHTTPError(err))
		return
	}
	response.Success(c, page)
}

func (h *AccountCapabilityHandler) CreateRun(c *gin.Context) {
	actor, ok := accountJobActorID(c)
	if !ok {
		return
	}
	var request service.AccountCapabilityCreateRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.ErrorFrom(c, accountCapabilityHTTPError(service.ErrAccountCapabilityInvalid))
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		response.ErrorFrom(c, accountCapabilityHTTPError(service.ErrAccountCapabilityInvalid))
		return
	}
	run, replayed, err := h.capabilities.Create(c.Request.Context(), actor, c.GetHeader("Idempotency-Key"), request)
	if err != nil {
		response.ErrorFrom(c, accountCapabilityHTTPError(err))
		return
	}
	if replayed {
		c.Header("Idempotency-Replayed", "true")
	}
	response.Accepted(c, run)
}

func (h *AccountCapabilityHandler) GetRun(c *gin.Context) {
	id, ok := accountCapabilityPathID(c)
	if !ok {
		return
	}
	run, err := h.capabilities.GetRun(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, accountCapabilityHTTPError(err))
		return
	}
	response.Success(c, run)
}

func (h *AccountCapabilityHandler) ListItems(c *gin.Context) {
	id, ok := accountCapabilityPathID(c)
	if !ok {
		return
	}
	filter, ok := accountCapabilityQuery(c)
	if !ok {
		return
	}
	page, err := h.capabilities.ListItems(c.Request.Context(), id, filter)
	if err != nil {
		response.ErrorFrom(c, accountCapabilityHTTPError(err))
		return
	}
	response.Success(c, page)
}

func (h *AccountCapabilityHandler) Pause(c *gin.Context)  { h.control(c, "pause") }
func (h *AccountCapabilityHandler) Resume(c *gin.Context) { h.control(c, "resume") }
func (h *AccountCapabilityHandler) Cancel(c *gin.Context) { h.control(c, "cancel") }

func (h *AccountCapabilityHandler) control(c *gin.Context, action string) {
	if _, ok := accountJobActorID(c); !ok {
		return
	}
	id, ok := accountCapabilityPathID(c)
	if !ok {
		return
	}
	run, err := h.capabilities.Control(c.Request.Context(), id, action)
	if err != nil {
		response.ErrorFrom(c, accountCapabilityHTTPError(err))
		return
	}
	response.Success(c, run)
}

func accountCapabilityPathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, accountCapabilityHTTPError(service.ErrAccountCapabilityInvalid))
		return 0, false
	}
	return id, true
}

func accountCapabilityQuery(c *gin.Context) (service.AccountCapabilityFilter, bool) {
	filter := service.AccountCapabilityFilter{Kind: c.Query("kind"), Status: c.Query("status"), Model: c.Query("model"), Page: positiveAccountJobQuery(c.Query("page"), 1), PageSize: positiveAccountJobQuery(c.Query("page_size"), 50)}
	if raw := c.Query("account_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.ErrorFrom(c, accountCapabilityHTTPError(service.ErrAccountCapabilityInvalid))
			return filter, false
		}
		filter.AccountID = id
	}
	if raw := c.Query("folder_ids"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || id <= 0 {
				response.ErrorFrom(c, accountCapabilityHTTPError(service.ErrAccountCapabilityInvalid))
				return filter, false
			}
			filter.FolderIDs = append(filter.FolderIDs, id)
		}
	}
	if raw := c.Query("account_ids"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || id <= 0 {
				response.ErrorFrom(c, accountCapabilityHTTPError(service.ErrAccountCapabilityInvalid))
				return filter, false
			}
			filter.AccountIDs = append(filter.AccountIDs, id)
		}
	}
	return filter, true
}

func accountCapabilityHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrAccountCapabilityInvalid):
		return infraerrors.BadRequest("ACCOUNT_CAPABILITY_INVALID", "Invalid capability request")
	case errors.Is(err, service.ErrAccountCapabilityScope):
		return infraerrors.Conflict("ACCOUNT_CAPABILITY_SCOPE_CHANGED", "The account folder or outbound configuration changed; create a new preview")
	case errors.Is(err, service.ErrAccountCapabilityNotFound):
		return infraerrors.NotFound("ACCOUNT_CAPABILITY_NOT_FOUND", "Capability record not found")
	case errors.Is(err, service.ErrAccountCapabilityConflict):
		return infraerrors.Conflict("ACCOUNT_CAPABILITY_CONFLICT", "The run cannot perform this action in its current state")
	case errors.Is(err, service.ErrAccountCapabilityIdempotencyRequired):
		return infraerrors.BadRequest("ACCOUNT_CAPABILITY_IDEMPOTENCY_REQUIRED", "Idempotency-Key is required")
	case errors.Is(err, service.ErrAccountCapabilityIdempotencyConflict):
		return infraerrors.Conflict("ACCOUNT_CAPABILITY_IDEMPOTENCY_CONFLICT", "Idempotency-Key was reused with a different request")
	default:
		return infraerrors.New(http.StatusInternalServerError, "ACCOUNT_CAPABILITY_INTERNAL", "Capability operation failed")
	}
}
