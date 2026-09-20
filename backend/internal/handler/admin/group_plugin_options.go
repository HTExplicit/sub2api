package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// PluginOptions supplies only the labels and identifiers needed by a group
// picker. It preserves the host's simple-mode restrictions and omits settings.
func (h *GroupHandler) PluginOptions(c *gin.Context) {
	if h == nil || h.adminService == nil {
		response.Error(c, 503, "Group options unavailable")
		return
	}
	if h.rejectUnsupportedSimpleModeOperation(c, simpleModeGroupGetAll) {
		return
	}
	groups, err := h.adminService.GetAllGroupsIncludingInactive(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	options := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		if h.isSimpleMode() && !service.IsGroupBindableInSimpleMode(&group) {
			continue
		}
		options = append(options, gin.H{"id": group.ID, "name": group.Name, "platform": group.Platform, "status": group.Status})
	}
	response.Success(c, options)
}
