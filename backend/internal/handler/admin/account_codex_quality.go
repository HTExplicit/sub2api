package admin

import (
	"context"
	"fmt"
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
	switch {
	case !ok || actor.UserID <= 0:
		response.Error(c, 403, "Codex quality run unavailable: no authenticated administrator")
	case !roleOK || role != "admin":
		response.Error(c, 403, fmt.Sprintf("Codex quality run unavailable: role %q is not admin", role))
	case err != nil || id <= 0:
		response.Error(c, 403, "Codex quality run unavailable: invalid account id "+strconv.Quote(c.Param("id")))
	default:
		return actor.UserID, id, true
	}
	return 0, 0, false
}

func writeCodexQualityResult(c *gin.Context, result *service.CodexQualityRunView, err error) {
	if err != nil {
		response.Error(c, 409, "Codex quality run unavailable: "+err.Error())
		return
	}
	if result == nil {
		response.Error(c, 409, "Codex quality run unavailable: the runtime returned no run")
		return
	}
	response.Success(c, result)
}

func codexQualityKeyLookup(h *AccountHandler, actor int64) service.CodexQualityKeyLookup {
	return func(ctx context.Context, keyID int64) (*service.APIKey, error) {
		keys, _, err := h.adminService.GetUserAPIKeys(ctx, actor, 1, 1000, "", "")
		if err != nil {
			return nil, fmt.Errorf("list the API keys of administrator %d: %w", actor, err)
		}
		for index := range keys {
			if keys[index].ID == keyID && keys[index].UserID == actor {
				return &keys[index], nil
			}
		}
		return nil, fmt.Errorf("API key %d is not one of the %d API keys of administrator %d", keyID, len(keys), actor)
	}
}

func (h *AccountHandler) CreateCodexQualityRun(c *gin.Context) {
	actor, id, ok := codexQualityAdmin(c)
	if !ok {
		return
	}
	var req service.CodexQualityCreateRequest
	if h.codexGateway == nil {
		response.Error(c, 409, "Codex quality run unavailable: Codex runtime unavailable")
		return
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 409, "Codex quality run unavailable: invalid request: "+err.Error())
		return
	}
	if req.APIKeyID <= 0 {
		response.Error(c, 409, "Codex quality run unavailable: api_key_id is required")
		return
	}
	// A global integration admin key resolves to a real administrator too. Only
	// that administrator's own existing downstream key can authorize this run.
	key, err := codexQualityKeyLookup(h, actor)(c.Request.Context(), req.APIKeyID)
	if err != nil {
		response.Error(c, 409, "Codex quality run unavailable: "+err.Error())
		return
	}
	result, err := h.codexGateway.CreateCodexQualityRun(c.Request.Context(), actor, id, key, req)
	writeCodexQualityResult(c, result, err)
}

func (h *AccountHandler) ReadCodexQualityRun(c *gin.Context) {
	actor, id, ok := codexQualityAdmin(c)
	if !ok {
		return
	}
	if h.codexGateway == nil {
		response.Error(c, 409, "Codex quality run unavailable: Codex runtime unavailable")
		return
	}
	result, err := h.codexGateway.ReadCodexQualityRun(c.Request.Context(), actor, id, c.Param("run_id"))
	writeCodexQualityResult(c, result, err)
}

func (h *AccountHandler) CloseCodexQualityRun(c *gin.Context) {
	actor, id, ok := codexQualityAdmin(c)
	if !ok {
		return
	}
	if h.codexGateway == nil {
		response.Error(c, 409, "Codex quality run unavailable: Codex runtime unavailable")
		return
	}
	result, err := h.codexGateway.CloseCodexQualityRun(c.Request.Context(), actor, id, c.Param("run_id"))
	writeCodexQualityResult(c, result, err)
}
