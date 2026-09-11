package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const testAPIKeyRevealPassword = "reveal-test-password"

type apiKeyRevealAdminStub struct {
	stubAdminService
}

func (s *apiKeyRevealAdminStub) UpdateAccount(ctx context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	if s.getAccountResult != nil {
		acc := *s.getAccountResult
		if input != nil && input.Name != "" {
			acc.Name = input.Name
		}
		return &acc, nil
	}
	return s.stubAdminService.UpdateAccount(ctx, id, input)
}

func setupAPIKeyVisibilityRouter(t *testing.T, password string) (*gin.Engine, *AccountHandler, *apiKeyRevealAdminStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	adminSvc := &apiKeyRevealAdminStub{stubAdminService: *newStubAdminService()}
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cfg = &config.Config{Security: config.SecurityConfig{AccountAPIKeyRevealPassword: password}}
	adminSvc.getAccountResult = &service.Account{
		ID:       42,
		Name:     "api-account",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"api_key":       "sk-secret",
			"refresh_token": "rt-secret",
			"base_url":      "https://api.openai.com",
		},
	}
	adminSvc.accounts = []service.Account{*adminSvc.getAccountResult}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	router.GET("/api/v1/admin/accounts/api-key-visibility", handler.GetAPIKeyVisibility)
	router.PUT("/api/v1/admin/accounts/api-key-visibility", handler.SetAPIKeyVisibility)
	router.GET("/api/v1/admin/accounts", handler.List)
	router.GET("/api/v1/admin/accounts/:id", handler.GetByID)
	router.PUT("/api/v1/admin/accounts/:id", handler.Update)
	return router, handler, adminSvc
}

func doAPIKeyVisibilityJSON(t *testing.T, router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	return payload.Data
}

func TestAPIKeyVisibilityWrongPasswordDoesNotEnable(t *testing.T) {
	router, _, _ := setupAPIKeyVisibilityRouter(t, testAPIKeyRevealPassword)
	rec := doAPIKeyVisibilityJSON(t, router, http.MethodPut, "/api/v1/admin/accounts/api-key-visibility", map[string]any{
		"enabled":  true,
		"password": "wrong-password",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	status := doAPIKeyVisibilityJSON(t, router, http.MethodGet, "/api/v1/admin/accounts/api-key-visibility", nil)
	require.Equal(t, http.StatusOK, status.Code)
	require.Equal(t, false, decodeData(t, status)["enabled"])
}

func TestAPIKeyVisibilityUnconfiguredDoesNotEnable(t *testing.T) {
	router, _, _ := setupAPIKeyVisibilityRouter(t, "")
	rec := doAPIKeyVisibilityJSON(t, router, http.MethodPut, "/api/v1/admin/accounts/api-key-visibility", map[string]any{
		"enabled":  true,
		"password": testAPIKeyRevealPassword,
	})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAPIKeyVisibilityRevealOnGetAndUpdateNotList(t *testing.T) {
	router, _, _ := setupAPIKeyVisibilityRouter(t, testAPIKeyRevealPassword)

	before := doAPIKeyVisibilityJSON(t, router, http.MethodGet, "/api/v1/admin/accounts/42", nil)
	require.Equal(t, http.StatusOK, before.Code)
	beforeCreds, _ := decodeData(t, before)["credentials"].(map[string]any)
	require.NotContains(t, beforeCreds, "api_key")
	require.Equal(t, "https://api.openai.com", beforeCreds["base_url"])

	enable := doAPIKeyVisibilityJSON(t, router, http.MethodPut, "/api/v1/admin/accounts/api-key-visibility", map[string]any{
		"enabled":  true,
		"password": testAPIKeyRevealPassword,
	})
	require.Equal(t, http.StatusOK, enable.Code)
	require.Equal(t, true, decodeData(t, enable)["enabled"])

	detail := doAPIKeyVisibilityJSON(t, router, http.MethodGet, "/api/v1/admin/accounts/42", nil)
	require.Equal(t, http.StatusOK, detail.Code)
	detailCreds, _ := decodeData(t, detail)["credentials"].(map[string]any)
	require.Equal(t, "sk-secret", detailCreds["api_key"])
	require.NotContains(t, detailCreds, "refresh_token")

	update := doAPIKeyVisibilityJSON(t, router, http.MethodPut, "/api/v1/admin/accounts/42", map[string]any{
		"name": "api-account",
	})
	require.Equal(t, http.StatusOK, update.Code)
	updateCreds, _ := decodeData(t, update)["credentials"].(map[string]any)
	require.Equal(t, "sk-secret", updateCreds["api_key"])
	require.NotContains(t, updateCreds, "refresh_token")

	list := doAPIKeyVisibilityJSON(t, router, http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20", nil)
	require.Equal(t, http.StatusOK, list.Code)
	var listPayload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &listPayload))
	require.Len(t, listPayload.Data.Items, 1)
	listCreds, _ := listPayload.Data.Items[0]["credentials"].(map[string]any)
	require.NotContains(t, listCreds, "api_key")
	require.NotContains(t, listCreds, "refresh_token")
}

func TestAPIKeyVisibilityDisableClearsReveal(t *testing.T) {
	router, _, _ := setupAPIKeyVisibilityRouter(t, testAPIKeyRevealPassword)
	enable := doAPIKeyVisibilityJSON(t, router, http.MethodPut, "/api/v1/admin/accounts/api-key-visibility", map[string]any{
		"enabled":  true,
		"password": testAPIKeyRevealPassword,
	})
	require.Equal(t, http.StatusOK, enable.Code)

	disable := doAPIKeyVisibilityJSON(t, router, http.MethodPut, "/api/v1/admin/accounts/api-key-visibility", map[string]any{
		"enabled": false,
	})
	require.Equal(t, http.StatusOK, disable.Code)
	require.Equal(t, false, decodeData(t, disable)["enabled"])

	detail := doAPIKeyVisibilityJSON(t, router, http.MethodGet, "/api/v1/admin/accounts/42", nil)
	disabledCreds, ok := decodeData(t, detail)["credentials"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, disabledCreds, "api_key")
}
