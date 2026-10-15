//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type loginProtectionUsers struct {
	service.UserRepository
	user *service.User
	err  error
}

type loginProtectionHandlerStore struct {
	service.LoginProtectionStore
	failures int
	ban      *service.LoginIPBan
}

func (s *loginProtectionHandlerStore) Check(context.Context, string) (*service.LoginIPBan, error) {
	return s.ban, nil
}
func (s *loginProtectionHandlerStore) RecordFailure(_ context.Context, ip string, settings service.LoginProtectionSettings) (*service.LoginIPBan, bool, error) {
	s.failures++
	if s.failures <= settings.MaxFailures {
		return nil, false, nil
	}
	start := time.Now().UTC()
	s.ban = &service.LoginIPBan{IP: ip, BlockedAt: start, ExpiresAt: start.Add(time.Duration(settings.BanHours) * time.Hour)}
	return s.ban, true, nil
}

func (r *loginProtectionUsers) GetByEmail(context.Context, string) (*service.User, error) {
	return r.user, r.err
}
func (r *loginProtectionUsers) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, r.err
}

type loginProtectionSettingsRepo struct{ service.SettingRepository }

func (*loginProtectionSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == "totp_enabled" {
		return "true", nil
	}
	return "", service.ErrSettingNotFound
}
func (*loginProtectionSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

type loginProtectionTotpCache struct {
	service.TotpCache
	expired  bool
	attempts int
}

func (c *loginProtectionTotpCache) GetLoginSession(context.Context, string) (*service.TotpLoginSession, error) {
	if c.expired {
		return nil, nil
	}
	return &service.TotpLoginSession{UserID: 1}, nil
}
func (*loginProtectionTotpCache) SetLoginSession(context.Context, string, *service.TotpLoginSession, time.Duration) error {
	return nil
}
func (c *loginProtectionTotpCache) GetVerifyAttempts(context.Context, int64) (int, error) {
	return c.attempts, nil
}
func (c *loginProtectionTotpCache) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	c.attempts++
	return c.attempts, nil
}

type loginProtectionEncryptor struct{}

func (loginProtectionEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (loginProtectionEncryptor) Decrypt(string) (string, error)       { return "JBSWY3DPEHPK3PXP", nil }

func TestLoginProtectionPasswordTotpAndSuccessfulLoginShareSlidingCounter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&loginProtectionSettingsRepo{}, &config.Config{})
	store := &loginProtectionHandlerStore{}
	guard := service.NewLoginProtectionService(store, settings, nil)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	require.NoError(t, err)
	secret := "encrypted-secret"
	users := &loginProtectionUsers{user: &service.User{ID: 1, Email: "admin@example.test", Role: service.RoleAdmin, Status: service.StatusActive, PasswordHash: string(hash), TotpEnabled: true, TotpSecretEncrypted: &secret}}
	cache := &loginProtectionTotpCache{}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "unit-test-secret", ExpireHour: 1}}
	auth := service.NewAuthService(nil, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	totp := service.NewTotpService(users, loginProtectionEncryptor{}, cache, settings, nil, nil)
	h := &AuthHandler{authService: auth, settingSvc: settings, totpService: totp, LoginProtection: guard}
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(middleware.LoginProtection(guard))
	router.POST("/api/v1/auth/login", h.Login)
	router.POST("/api/v1/auth/login/2fa", h.Login2FA)
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.8:1234"
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	// Malformed input, expired TOTP sessions and system faults do not count.
	require.Equal(t, 400, post("/api/v1/auth/login", `{}`).Code)
	cache.expired = true
	require.Equal(t, 400, post("/api/v1/auth/login/2fa", `{"temp_token":"expired","totp_code":"badbad"}`).Code)
	cache.expired = false
	users.err = errors.New("test database offline")
	require.Equal(t, 503, post("/api/v1/auth/login", `{"email":"one@example.test","password":"wrong"}`).Code)
	users.err = service.ErrUserNotFound
	require.Equal(t, 401, post("/api/v1/auth/login", `{"email":"one@example.test","password":"wrong"}`).Code)
	users.err = nil
	// Two password errors for different accounts still use this same IP counter.
	for _, email := range []string{"two@example.test", "three@example.test"} {
		require.Equal(t, 401, post("/api/v1/auth/login", `{"email":"`+email+`","password":"wrong"}`).Code)
	}
	// A successful password check issuing a TOTP session must not reset failures.
	require.Equal(t, 200, post("/api/v1/auth/login", `{"email":"admin@example.test","password":"correct-password"}`).Code)
	for range 2 {
		require.Equal(t, 400, post("/api/v1/auth/login/2fa", `{"temp_token":"valid","totp_code":"badbad"}`).Code)
	}
	// A sixth failure already rejected by the existing per-user TOTP limit counts.
	cache.attempts = 5
	result := post("/api/v1/auth/login/2fa", `{"temp_token":"valid","totp_code":"badbad"}`)
	require.Equal(t, 429, result.Code)
	require.Contains(t, result.Body.String(), "LOGIN_IP_BANNED")
	ban, err := store.Check(context.Background(), "203.0.113.8")
	require.NoError(t, err)
	require.NotNil(t, ban)
	require.Equal(t, 24*time.Hour, ban.ExpiresAt.Sub(ban.BlockedAt))
	// Valid credentials are also rejected once the source IP is banned.
	require.Equal(t, 429, post("/api/v1/auth/login", `{"email":"admin@example.test","password":"correct-password"}`).Code)
}
