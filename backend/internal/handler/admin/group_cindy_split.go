package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type cindyGroupSplitRequest struct {
	SourceKeeps       string  `json:"source_keeps" binding:"required"`
	TargetName        string  `json:"target_name" binding:"required"`
	APIKeyIDs         []int64 `json:"api_key_ids"`
	MemberFingerprint string  `json:"member_fingerprint"`
}

func (h *GroupHandler) cindyGroupAdminService() (service.CindyGroupAdminService, error) {
	if h == nil || h.adminService == nil {
		return nil, service.ErrCindyGroupAdminUnavailable
	}
	svc, ok := h.adminService.(service.CindyGroupAdminService)
	if !ok || svc == nil {
		return nil, service.ErrCindyGroupAdminUnavailable
	}
	return svc, nil
}

// AuditCindyGroups returns anonymous Cindy membership counts by OpenAI group.
// GET /api/v1/admin/cindy/groups/audit
func (h *GroupHandler) AuditCindyGroups(c *gin.Context) {
	svc, err := h.cindyGroupAdminService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result, err := svc.AuditCindyGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// CindyGroupKeyChoices exposes selection metadata, never complete API keys or
// user records, to the independently published group management interface.
func (h *GroupHandler) CindyGroupKeyChoices(c *gin.Context) {
	if h.rejectUnsupportedSimpleModeOperation(c, "api_keys") {
		return
	}
	groupID, ok := parsePositiveCindyGroupID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	keys, total, err := h.adminService.GetGroupAPIKeys(c.Request.Context(), groupID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	type choice struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		DisplayKey string `json:"display_key"`
	}
	out := make([]choice, 0, len(keys))
	for _, key := range keys {
		display := "****"
		if len(key.Key) > 8 {
			display = key.Key[:3] + "****" + key.Key[len(key.Key)-4:]
		}
		out = append(out, choice{ID: key.ID, Name: key.Name, Status: key.Status, DisplayKey: display})
	}
	response.Paginated(c, out, total, page, pageSize)
}

// PreviewCindyGroupSplit validates a split selection without mutating state.
// POST /api/v1/admin/cindy/groups/:id/split-preview
func (h *GroupHandler) PreviewCindyGroupSplit(c *gin.Context) {
	groupID, ok := parsePositiveCindyGroupID(c)
	if !ok {
		return
	}
	var req cindyGroupSplitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	svc, err := h.cindyGroupAdminService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result, err := svc.PreviewCindyGroupSplit(c.Request.Context(), groupID, service.CindyGroupSplitInput{
		SourceKeeps: req.SourceKeeps,
		TargetName:  req.TargetName,
		APIKeyIDs:   req.APIKeyIDs,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// SplitCindyGroup atomically commits a fingerprinted group split.
// POST /api/v1/admin/cindy/groups/:id/split
func (h *GroupHandler) SplitCindyGroup(c *gin.Context) {
	groupID, ok := parsePositiveCindyGroupID(c)
	if !ok {
		return
	}
	var req cindyGroupSplitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	svc, err := h.cindyGroupAdminService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result, err := svc.SplitCindyGroup(c.Request.Context(), groupID, service.CindyGroupSplitInput{
		SourceKeeps:       req.SourceKeeps,
		TargetName:        req.TargetName,
		APIKeyIDs:         req.APIKeyIDs,
		MemberFingerprint: req.MemberFingerprint,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func parsePositiveCindyGroupID(c *gin.Context) (int64, bool) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return 0, false
	}
	return groupID, true
}
