package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func codexQualityAdmin(c *gin.Context) (int64, int64, bool) {
	c.Header("Cache-Control", "no-store")
	actor, ok := middleware.GetAuthSubjectFromContext(c)
	role, roleOK := middleware.GetUserRoleFromContext(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if !ok || !roleOK || role != "admin" || actor.UserID <= 0 || err != nil || id <= 0 {
		response.Error(c, 403, "Codex quality run unavailable")
		return 0, 0, false
	}
	return actor.UserID, id, true
}

func writeCodexQualityResult(c *gin.Context, result *service.CodexQualityRunView, err error) {
	if err != nil || result == nil {
		response.Error(c, 409, "Codex quality run unavailable")
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) CreateCodexQualityRun(c *gin.Context) {
	actor, id, ok := codexQualityAdmin(c)
	if !ok {
		return
	}
	var req service.CodexQualityCreateRequest
	if h.codexTicketGateway == nil || c.ShouldBindJSON(&req) != nil || req.APIKeyID <= 0 {
		response.Error(c, 409, "Codex quality run unavailable")
		return
	}
	// A global integration admin key resolves to a real administrator too. Only
	// that administrator's own existing downstream key can authorize this run.
	keys, _, err := h.adminService.GetUserAPIKeys(c.Request.Context(), actor, 1, 1000, "", "")
	if err != nil {
		response.Error(c, 409, "Codex quality run unavailable")
		return
	}
	var key *service.APIKey
	for index := range keys {
		if keys[index].ID == req.APIKeyID && keys[index].UserID == actor {
			key = &keys[index]
			break
		}
	}
	result, err := h.codexTicketGateway.CreateCodexQualityRun(c.Request.Context(), actor, id, key, req)
	writeCodexQualityResult(c, result, err)
}

func (h *AccountHandler) ReadCodexQualityRun(c *gin.Context) {
	actor, id, ok := codexQualityAdmin(c)
	if !ok {
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 409, "Codex quality run unavailable")
		return
	}
	result, err := h.codexTicketGateway.ReadCodexQualityRun(c.Request.Context(), actor, id, c.Param("run_id"))
	writeCodexQualityResult(c, result, err)
}

func (h *AccountHandler) RenewCodexQualityRoute(c *gin.Context) {
	actor, id, ok := codexQualityAdmin(c)
	if !ok {
		return
	}
	var req struct {
		OperationID string `json:"operation_id"`
	}
	if h.codexTicketGateway == nil || c.ShouldBindJSON(&req) != nil {
		response.Error(c, 409, "Codex quality run unavailable")
		return
	}
	lookup := func(ctx context.Context, keyID int64) (*service.APIKey, error) {
		keys, _, err := h.adminService.GetUserAPIKeys(ctx, actor, 1, 1000, "", "")
		if err != nil {
			return nil, service.ErrCodexQualityUnavailable
		}
		for index := range keys {
			if keys[index].ID == keyID && keys[index].UserID == actor {
				return &keys[index], nil
			}
		}
		return nil, service.ErrCodexQualityUnavailable
	}
	result, err := h.codexTicketGateway.RenewCodexQualityRoute(c.Request.Context(), actor, id, c.Param("run_id"), req.OperationID, lookup)
	writeCodexQualityResult(c, result, err)
}

func (h *AccountHandler) CloseCodexQualityRun(c *gin.Context) {
	actor, id, ok := codexQualityAdmin(c)
	if !ok {
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 409, "Codex quality run unavailable")
		return
	}
	result, err := h.codexTicketGateway.CloseCodexQualityRun(c.Request.Context(), actor, id, c.Param("run_id"))
	writeCodexQualityResult(c, result, err)
}
