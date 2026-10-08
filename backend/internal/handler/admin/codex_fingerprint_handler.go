package admin

import (
	"strconv"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetCodexFingerprintSettings(c *gin.Context) {
	view, err := h.settingService.GetCodexFingerprintSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, view)
}

func (h *SettingHandler) UpdateCodexFingerprintSettings(c *gin.Context) {
	raw, err := c.GetRawData()
	var config service.CodexFingerprintSettings
	if err == nil {
		config, err = service.DecodeCodexFingerprintSettings(raw)
	}
	if err != nil {
		response.BadRequest(c, "Invalid Codex fingerprint settings: "+err.Error())
		return
	}
	view, err := h.settingService.UpdateCodexFingerprintSettings(c.Request.Context(), config)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": config.Enabled, "client_version": config.ClientVersion, "version_auto_sync_enabled": config.VersionAutoSyncEnabled})
	response.Success(c, view)
}

func (h *AccountHandler) codexFingerprintAccount(c *gin.Context) (*service.Account, *service.Account, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return nil, nil, infraerrors.BadRequest("INVALID_ACCOUNT_ID", "invalid account ID")
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	if !account.IsOpenAIOAuthLike() {
		return nil, nil, infraerrors.BadRequest("CODEX_FINGERPRINT_UNSUPPORTED_ACCOUNT", "Codex fingerprint settings require an OpenAI OAuth or setup-token account")
	}
	source := account
	if account.IsShadow() {
		source, err = h.adminService.GetAccount(c.Request.Context(), *account.ParentAccountID)
		if err != nil {
			return nil, nil, err
		}
	}
	return account, source, nil
}

func (h *AccountHandler) GetCodexFingerprint(c *gin.Context) {
	account, source, err := h.codexFingerprintAccount(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, service.DescribeCodexFingerprintAccount(account, source))
}

func (h *AccountHandler) UpdateCodexFingerprint(c *gin.Context) {
	raw, err := c.GetRawData()
	var mode string
	if err == nil {
		mode, err = service.DecodeCodexFingerprintMode(raw)
	}
	if err != nil {
		response.BadRequest(c, "Invalid Codex fingerprint mode: "+err.Error())
		return
	}
	account, _, err := h.codexFingerprintAccount(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.adminService.UpdateAccountExtra(c.Request.Context(), account.ID, map[string]any{"codex_fingerprint_mode": mode}); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"account_id": account.ID, "mode": mode})
	h.GetCodexFingerprint(c)
}
