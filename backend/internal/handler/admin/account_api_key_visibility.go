package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type apiKeyVisibilityRequest struct {
	Enabled  bool   `json:"enabled"`
	Password string `json:"password"`
}

type apiKeyVisibilityState struct {
	Enabled bool `json:"enabled"`
}

// GetAPIKeyVisibility reports whether the current admin may see account API keys.
// GET /api/v1/admin/accounts/api-key-visibility
func (h *AccountHandler) GetAPIKeyVisibility(c *gin.Context) {
	response.Success(c, apiKeyVisibilityState{Enabled: h.canRevealAPIKey(c)})
}

// SetAPIKeyVisibility enables or disables API key reveal for the current admin.
// Enabling requires the configured reveal password. PUT /api/v1/admin/accounts/api-key-visibility
func (h *AccountHandler) SetAPIKeyVisibility(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authorization required")
		return
	}

	var req apiKeyVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if !req.Enabled {
		h.setAPIKeyReveal(subject.UserID, false)
		response.Success(c, apiKeyVisibilityState{Enabled: false})
		return
	}

	configured := h.apiKeyRevealPassword()
	if configured == "" {
		response.Error(c, http.StatusServiceUnavailable, "API key visibility password is not configured")
		return
	}
	if !constantTimePasswordEqual(strings.TrimSpace(req.Password), configured) {
		response.BadRequest(c, "incorrect password")
		return
	}

	h.setAPIKeyReveal(subject.UserID, true)
	response.Success(c, apiKeyVisibilityState{Enabled: true})
}

func (h *AccountHandler) buildAccountResponseWithRevealedAPIKey(c *gin.Context, account *service.Account) AccountWithConcurrency {
	item := h.buildAccountResponseWithRuntime(c.Request.Context(), account)
	if h.canRevealAPIKey(c) {
		dto.RestoreAccountAPIKey(item.Account, account)
	}
	return item
}

func (h *AccountHandler) canRevealAPIKey(c *gin.Context) bool {
	if h == nil {
		return false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		return false
	}
	h.apiKeyRevealMu.RLock()
	defer h.apiKeyRevealMu.RUnlock()
	_, enabled := h.apiKeyRevealUsers[subject.UserID]
	return enabled
}

func (h *AccountHandler) setAPIKeyReveal(userID int64, enabled bool) {
	if h == nil || userID <= 0 {
		return
	}
	h.apiKeyRevealMu.Lock()
	defer h.apiKeyRevealMu.Unlock()
	if h.apiKeyRevealUsers == nil {
		h.apiKeyRevealUsers = make(map[int64]struct{})
	}
	if enabled {
		h.apiKeyRevealUsers[userID] = struct{}{}
		return
	}
	delete(h.apiKeyRevealUsers, userID)
}

func (h *AccountHandler) apiKeyRevealPassword() string {
	if h == nil || h.cfg == nil {
		return ""
	}
	return h.cfg.Security.AccountAPIKeyRevealPassword
}

func constantTimePasswordEqual(provided, configured string) bool {
	providedSum := sha256.Sum256([]byte(provided))
	configuredSum := sha256.Sum256([]byte(configured))
	return subtle.ConstantTimeCompare(providedSum[:], configuredSum[:]) == 1
}
