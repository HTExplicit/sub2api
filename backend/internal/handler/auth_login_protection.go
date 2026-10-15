package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Keep the existing constructor usable by isolated auth tests; Wire always
// supplies the shared protection instance through this provider.
func ProvideAuthHandler(cfg *config.Config, auth *service.AuthService, users *service.UserService, settings *service.SettingService, promo *service.PromoService, redeem *service.RedeemService, totp *service.TotpService, attributes *service.UserAttributeService, protection *service.LoginProtectionService) *AuthHandler {
	h := NewAuthHandler(cfg, auth, users, settings, promo, redeem, totp, attributes)
	h.LoginProtection = protection
	return h
}

func (h *AuthHandler) recordLoginFailure(c *gin.Context) bool {
	if h.LoginProtection == nil {
		return false
	}
	ban, err := h.LoginProtection.RecordFailure(c.Request.Context(), middleware.SecurityClientIP(c))
	if ban != nil {
		middleware.RejectLoginIPBan(c, ban)
		return true
	}
	if err != nil {
		middleware.RejectLoginProtectionUnavailable(c)
		return true
	}
	return false
}

// Recheck before issuing a login session: another request can trigger a ban
// while this request is validating its password or second factor.
func (h *AuthHandler) rejectProtectedLogin(c *gin.Context) bool {
	if h.LoginProtection == nil {
		return false
	}
	ban, err := h.LoginProtection.Check(c.Request.Context(), middleware.SecurityClientIP(c))
	if ban != nil {
		middleware.RejectLoginIPBan(c, ban)
		return true
	}
	if err != nil {
		middleware.RejectLoginProtectionUnavailable(c)
		return true
	}
	return false
}
