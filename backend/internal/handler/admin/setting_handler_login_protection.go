package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) SetLoginProtection(protection *service.LoginProtectionService) {
	h.loginProtection = protection
}

func (h *SettingHandler) GetLoginProtection(c *gin.Context) {
	settings, err := h.settingService.GetLoginProtectionSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateLoginProtection(c *gin.Context) {
	var req struct {
		Enabled       *bool `json:"enabled" binding:"required"`
		WindowMinutes int   `json:"window_minutes" binding:"required,min=1,max=1440"`
		MaxFailures   int   `json:"max_failures" binding:"required,min=1,max=1000"`
		BanHours      int   `json:"ban_hours" binding:"required,min=1,max=720"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid login protection settings: "+err.Error())
		return
	}
	settings := service.LoginProtectionSettings{Enabled: *req.Enabled, WindowMinutes: req.WindowMinutes, MaxFailures: req.MaxFailures, BanHours: req.BanHours}
	if err := h.settingService.SetLoginProtectionSettings(c.Request.Context(), settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) ListLoginProtectionBans(c *gin.Context) {
	page, size := response.ParsePagination(c)
	if size > 100 {
		size = 100
	}
	items, total, err := h.loginProtection.List(c.Request.Context(), page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}

func (h *SettingHandler) UnbanLoginProtectionIP(c *gin.Context) {
	var req struct {
		IP string `json:"ip" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid IP: "+err.Error())
		return
	}
	ip, err := service.NormalizeLoginProtectionIP(req.IP)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.loginProtection.Unban(c.Request.Context(), ip); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"target_ip": ip})
	response.Success(c, gin.H{"ip": ip})
}
