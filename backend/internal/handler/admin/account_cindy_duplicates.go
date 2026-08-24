package admin

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type cindyDuplicateInventoryAdminService interface {
	BuildCindyDuplicateIdentityInventory(context.Context) ([]service.CindyDuplicateIdentityGroup, error)
}

// GetCindyDuplicateIdentityInventory is informational only. It never merges,
// deletes, or mutates accounts and never exposes raw credentials.
func (h *AccountHandler) GetCindyDuplicateIdentityInventory(c *gin.Context) {
	svc, ok := h.adminService.(cindyDuplicateInventoryAdminService)
	if !ok {
		response.Error(c, 501, "cindy duplicate inventory is unavailable")
		return
	}
	inventory, err := svc.BuildCindyDuplicateIdentityInventory(c.Request.Context())
	if err != nil {
		response.Error(c, 500, "failed to build cindy duplicate inventory")
		return
	}
	response.Success(c, inventory)
}
