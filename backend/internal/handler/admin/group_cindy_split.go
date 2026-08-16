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
