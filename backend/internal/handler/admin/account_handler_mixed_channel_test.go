package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupAccountMixedChannelRouter(adminSvc *stubAdminService) (*gin.Engine, *AccountHandler, *accountJobSubmitRepository) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	accountHandler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	jobs := attachAccountJobSubmitter(router, accountHandler)
	router.POST("/api/v1/admin/accounts/check-mixed-channel", accountHandler.CheckMixedChannel)
	router.POST("/api/v1/admin/accounts", accountHandler.Create)
	router.PUT("/api/v1/admin/accounts/:id", accountHandler.Update)
	router.POST("/api/v1/admin/accounts/bulk-update", accountHandler.BulkUpdate)
	return router, accountHandler, jobs
}

func TestAccountHandlerCheckMixedChannelNoRisk(t *testing.T) {
	adminSvc := newStubAdminService()
	router, _, _ := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"platform":  "antigravity",
		"group_ids": []int64{27},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/check-mixed-channel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, float64(0), resp["code"])
	data, ok := resp["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, data["has_risk"])
	require.Equal(t, int64(0), adminSvc.lastMixedCheck.accountID)
	require.Equal(t, "antigravity", adminSvc.lastMixedCheck.platform)
	require.Equal(t, []int64{27}, adminSvc.lastMixedCheck.groupIDs)
}

func TestAccountHandlerCheckMixedChannelWithRisk(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.checkMixedErr = &service.MixedChannelError{
		GroupID:         27,
		GroupName:       "claude-max",
		CurrentPlatform: "Antigravity",
		OtherPlatform:   "Anthropic",
	}
	router, _, _ := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"platform":   "antigravity",
		"group_ids":  []int64{27},
		"account_id": 99,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/check-mixed-channel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, float64(0), resp["code"])
	data, ok := resp["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, data["has_risk"])
	require.Equal(t, "mixed_channel_warning", data["error"])
	details, ok := data["details"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(27), details["group_id"])
	require.Equal(t, "claude-max", details["group_name"])
	require.Equal(t, "Antigravity", details["current_platform"])
	require.Equal(t, "Anthropic", details["other_platform"])
	require.Equal(t, int64(99), adminSvc.lastMixedCheck.accountID)
}

func TestAccountHandlerCreateMixedChannelConflictSimplifiedResponse(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.createAccountErr = &service.MixedChannelError{
		GroupID:         27,
		GroupName:       "claude-max",
		CurrentPlatform: "Antigravity",
		OtherPlatform:   "Anthropic",
	}
	router, _, _ := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"name":        "ag-oauth-1",
		"platform":    "antigravity",
		"type":        "oauth",
		"credentials": map[string]any{"refresh_token": "rt"},
		"group_ids":   []int64{27},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "mixed_channel_warning", resp["error"])
	require.Contains(t, resp["message"], "mixed_channel_warning")
	_, hasDetails := resp["details"]
	_, hasRequireConfirmation := resp["require_confirmation"]
	require.False(t, hasDetails)
	require.False(t, hasRequireConfirmation)
}

func TestAccountHandlerUpdateMixedChannelConflictSimplifiedResponse(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.updateAccountErr = &service.MixedChannelError{
		GroupID:         27,
		GroupName:       "claude-max",
		CurrentPlatform: "Antigravity",
		OtherPlatform:   "Anthropic",
	}
	router, _, _ := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"group_ids": []int64{27},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/3", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "mixed_channel_warning", resp["error"])
	require.Contains(t, resp["message"], "mixed_channel_warning")
	_, hasDetails := resp["details"]
	_, hasRequireConfirmation := resp["require_confirmation"]
	require.False(t, hasDetails)
	require.False(t, hasRequireConfirmation)
}

func TestAccountHandlerUpdateMapsUpstreamBillingRateSyncSettings(t *testing.T) {
	adminSvc := newStubAdminService()
	router, _, _ := setupAccountMixedChannelRouter(adminSvc)
	body, _ := json.Marshal(map[string]any{
		"name":                               "gemini-key",
		"upstream_billing_probe_enabled":     true,
		"upstream_billing_rate_sync_enabled": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/42", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, adminSvc.lastUpdateAccountInput)
	require.NotNil(t, adminSvc.lastUpdateAccountInput.ProbeEnabled)
	require.True(t, *adminSvc.lastUpdateAccountInput.ProbeEnabled)
	require.NotNil(t, adminSvc.lastUpdateAccountInput.RateSyncEnabled)
	require.True(t, *adminSvc.lastUpdateAccountInput.RateSyncEnabled)
}

func TestAccountHandlerBulkUpdateMixedChannelConflictFailsJobItems(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.bulkUpdateAccountErr = &service.MixedChannelError{
		GroupID:         27,
		GroupName:       "claude-max",
		CurrentPlatform: "Antigravity",
		OtherPlatform:   "Anthropic",
	}
	router, handler, jobs := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1, 2, 3},
		"group_ids":   []int64{27},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/bulk-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var payload BulkUpdateAccountsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBulkUpdate, &payload)
	require.Equal(t, []int64{1, 2, 3}, payload.AccountIDs)
	results := executeSubmittedAccountJobItems(handler, params)
	require.Len(t, results, 3)
	for _, result := range results {
		require.Equal(t, service.AccountJobItemStatusFailed, result.Status)
		require.Equal(t, "bulk_update_failed", result.ErrorCode)
	}
}

func TestAccountHandlerBulkUpdateMixedChannelConfirmSkips(t *testing.T) {
	adminSvc := newStubAdminService()
	router, handler, jobs := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"account_ids":                []int64{1, 2},
		"group_ids":                  []int64{27},
		"confirm_mixed_channel_risk": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/bulk-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var payload BulkUpdateAccountsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBulkUpdate, &payload)
	require.NotNil(t, payload.ConfirmMixedChannelRisk)
	require.True(t, *payload.ConfirmMixedChannelRisk)
	results := executeSubmittedAccountJobItems(handler, params)
	require.Len(t, results, 2)
	for _, result := range results {
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	}
	require.NotNil(t, adminSvc.lastBulkUpdateAccountInput)
	require.True(t, adminSvc.lastBulkUpdateAccountInput.SkipMixedChannelCheck)
}

func TestBulkUpdateAcceptsFilterTargetRequest(t *testing.T) {
	adminSvc := newStubAdminService()
	router, _, jobs := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"filters": map[string]any{
			"platform":     "openai",
			"type":         "oauth",
			"status":       "active",
			"group":        "12",
			"privacy_mode": "blocked",
			"search":       "bulk-target",
		},
		"schedulable": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/bulk-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var payload BulkUpdateAccountsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBulkUpdate, &payload)
	require.Len(t, params.Items, 1, "filter-targeted jobs resolve their concrete targets in the worker")
	filters, err := toServiceBulkUpdateAccountFilters(payload.Filters)
	require.NoError(t, err)
	require.NotNil(t, filters)
	require.Nil(t, filters.Console)
}

func TestBulkUpdateAcceptsCockpitConsoleFilterTargetRequest(t *testing.T) {
	adminSvc := newStubAdminService()
	router, _, jobs := setupAccountMixedChannelRouter(adminSvc)
	body, _ := json.Marshal(map[string]any{
		"filters": map[string]any{
			"platforms": "openai,grok", "types": "oauth", "statuses": "active,error",
			"plans": "team,pro", "proxies": "direct,5", "folders": "9",
			"folder": "uncategorized", "tags": "3,4", "account_ids": "7,8",
			"group": "12", "privacy_mode": "blocked", "search": "bulk-target",
		},
		"schedulable": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/bulk-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var payload BulkUpdateAccountsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBulkUpdate, &payload)
	require.Len(t, params.Items, 1, "filter-targeted jobs resolve their concrete targets in the worker")
	converted, err := toServiceBulkUpdateAccountFilters(payload.Filters)
	require.NoError(t, err)
	require.NotNil(t, converted)
	filters := converted.Console
	require.NotNil(t, filters)
	require.Equal(t, []string{"openai", "grok"}, filters.Platforms)
	require.Equal(t, []string{"oauth"}, filters.Types)
	require.Equal(t, []string{"active", "error"}, filters.Statuses)
	require.Equal(t, []string{"team", "pro"}, filters.Plans)
	require.Equal(t, []int64{5}, filters.ProxyIDs)
	require.True(t, filters.IncludeDirect)
	require.Equal(t, []int64{9}, filters.FolderIDs)
	require.True(t, filters.IncludeUncategorized)
	require.Equal(t, []int64{3, 4}, filters.TagIDs)
	require.Equal(t, []int64{7, 8}, filters.AccountIDs)
	require.Equal(t, int64(12), filters.GroupID)
	require.Equal(t, "blocked", filters.PrivacyMode)
	require.Equal(t, "bulk-target", filters.Search)
}

func TestBulkUpdateAcceptsDedicatedUpstreamBillingProbeSetting(t *testing.T) {
	adminSvc := newStubAdminService()
	router, _, jobs := setupAccountMixedChannelRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{
		"account_ids":                    []int64{1, 2},
		"upstream_billing_probe_enabled": false,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/bulk-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var payload BulkUpdateAccountsRequest
	requireSubmittedAccountJob(t, jobs, service.AccountJobKindBulkUpdate, &payload)
	require.NotNil(t, payload.ProbeEnabled)
	require.False(t, *payload.ProbeEnabled)
}
