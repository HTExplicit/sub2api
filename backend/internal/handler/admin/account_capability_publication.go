package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AccountCapabilityPublicationHandler belongs exclusively to the authenticated
// administrator route group. Client input names evidence, never its outcome.
type AccountCapabilityPublicationHandler struct {
	service *service.AccountCapabilityPublicationService
}

func NewAccountCapabilityPublicationHandler(s *service.AccountCapabilityPublicationService) *AccountCapabilityPublicationHandler {
	return &AccountCapabilityPublicationHandler{service: s}
}

func (h *AccountCapabilityPublicationHandler) Preview(c *gin.Context) {
	var request service.CapabilityPublicationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "invalid capability publication request")
		return
	}
	result, err := h.service.Preview(c.Request.Context(), request)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *AccountCapabilityPublicationHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid change set id")
		return
	}
	result, err := h.service.Get(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *AccountCapabilityPublicationHandler) Apply(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid change set id")
		return
	}
	result, err := h.service.Apply(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}
