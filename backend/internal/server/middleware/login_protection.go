package middleware

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func RejectLoginIPBan(c *gin.Context, ban *service.LoginIPBan) {
	seconds := max(1, int(math.Ceil(time.Until(ban.ExpiresAt).Seconds())))
	c.Header("Retry-After", strconv.Itoa(seconds))
	c.Header("Cache-Control", "no-store")
	response.ErrorWithDetails(c, http.StatusTooManyRequests, "This IP is temporarily banned after repeated failed logins", "LOGIN_IP_BANNED",
		map[string]string{"expires_at": ban.ExpiresAt.Format(time.RFC3339Nano)})
	c.Abort()
}

func RejectLoginProtectionUnavailable(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Login protection is temporarily unavailable; retry later", "LOGIN_PROTECTION_UNAVAILABLE", nil)
	c.Abort()
}

// LoginProtection checks every new request before routing, including the SPA
// fallback and WebSocket upgrade. It never cancels an already admitted stream.
func LoginProtection(protection *service.LoginProtectionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if protection == nil {
			c.Next()
			return
		}
		ban, err := protection.Check(c.Request.Context(), SecurityClientIP(c))
		if ban != nil {
			RejectLoginIPBan(c, ban)
			return
		}
		if err != nil && c.Request.Method == http.MethodPost && (c.Request.URL.Path == "/api/v1/auth/login" || c.Request.URL.Path == "/api/v1/auth/login/2fa") {
			RejectLoginProtectionUnavailable(c)
			return
		}
		c.Next()
	}
}
