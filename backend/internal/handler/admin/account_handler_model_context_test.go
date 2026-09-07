package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type contextCapacityRowResponse struct {
	Data struct {
		Rows []struct {
			ID        string   `json:"upstream_model_id"`
			Aliases   []string `json:"aliases"`
			Editable  bool     `json:"editable"`
			Effective int64    `json:"effective_context_window"`
			Source    string   `json:"effective_source"`
			Upstream  *struct {
				ContextWindow int64 `json:"context_window"`
			} `json:"upstream"`
		} `json:"capacity_rows"`
	} `json:"data"`
}

func setupModelContextCapacityRouter(adminSvc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/accounts/:id/models/context-capacities", handler.GetModelContextCapacities)
	router.POST("/accounts/models/context-capacities-preview", handler.PreviewModelContextCapacities)
	return router
}

func TestAccountHandlerModelContextCapacityGetUsesOnlyLocalState(t *testing.T) {
	stub := newStubAdminService()
	stub.getAccountResult = &service.Account{ID: 18, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "never-return-secret", "base_url": "https://private-upstream.example", "model_mapping": map[string]any{"public-model": "real-model"}},
		Extra:       map[string]any{service.ModelContextOverridesExtraKey: map[string]int64{"real-model": 1050000}},
	}
	stub.getAccountResult.SetUpstreamModelContextCapacitySnapshot(service.UpstreamModelContextCapacitySnapshot{
		ObservedAt: "2026-09-07T00:00:00Z", Models: map[string]service.ModelContextCapacity{"real-model": {ContextWindow: 500000}},
	})
	router := setupModelContextCapacityRouter(stub) // No accountTestService or upstream clients.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/accounts/18/models/context-capacities?model_ids=draft-model", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var payload contextCapacityRowResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Rows, 2)
	for _, row := range payload.Data.Rows {
		if row.ID == "real-model" {
			require.Equal(t, []string{"public-model"}, row.Aliases)
			require.Equal(t, int64(1050000), row.Effective)
			require.Equal(t, "custom", row.Source)
			require.True(t, row.Editable)
			require.NotNil(t, row.Upstream)
			require.Equal(t, int64(500000), row.Upstream.ContextWindow)
		}
	}
	require.NotContains(t, rec.Body.String(), "never-return-secret")
	require.NotContains(t, rec.Body.String(), "private-upstream.example")
	require.Zero(t, stub.updateAccountCalls)
	require.Zero(t, stub.updateAccountExtraCalls)
}

func TestAccountHandlerModelContextCapacityPreviewNeedsNoAPIKeyAndDoesNotPersist(t *testing.T) {
	stub := newStubAdminService()
	router := setupModelContextCapacityRouter(stub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts/models/context-capacities-preview", strings.NewReader(`{"platform":"openai","type":"apikey","base_url":"https://private.example/v1","model_ids":["unknown-new-model"],"model_mapping":{"friendly-name":"unknown-new-model"}}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var payload contextCapacityRowResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Rows, 1)
	require.Equal(t, "unknown-new-model", payload.Data.Rows[0].ID)
	require.Equal(t, int64(258000), payload.Data.Rows[0].Effective)
	require.Equal(t, []string{"friendly-name"}, payload.Data.Rows[0].Aliases)
	require.Empty(t, stub.createdAccounts)
	require.Zero(t, stub.updateAccountCalls)
	require.Zero(t, stub.updateAccountExtraCalls)
}

func TestAccountHandlerModelContextCapacityPreviewCannotReclassifyProtectedAccount(t *testing.T) {
	stub := newStubAdminService()
	stub.getAccountResult = &service.Account{ID: 19, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"base_url": "https://chatgpt.com", "model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
	}
	router := setupModelContextCapacityRouter(stub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts/models/context-capacities-preview", strings.NewReader(`{"account_id":19,"platform":"openai","type":"apikey","base_url":"https://unprotected.example","model_ids":["gpt-5.4"]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var payload contextCapacityRowResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.Data.Rows)
	for _, row := range payload.Data.Rows {
		require.False(t, row.Editable)
	}
	require.Equal(t, "https://chatgpt.com", stub.getAccountResult.Credentials["base_url"])
	require.Equal(t, service.AccountTypeOAuth, stub.getAccountResult.Type)
}

func TestAccountHandlerModelContextCapacityPreviewNewEndpointDoesNotReuseOldObservation(t *testing.T) {
	stub := newStubAdminService()
	stub.getAccountResult = &service.Account{ID: 20, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://old-source.example/v1"},
	}
	stub.getAccountResult.SetUpstreamModelContextCapacitySnapshot(service.UpstreamModelContextCapacitySnapshot{
		ObservedAt: "2026-09-01T00:00:00Z", Models: map[string]service.ModelContextCapacity{"unknown-model": {ContextWindow: 900000}},
	})
	router := setupModelContextCapacityRouter(stub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts/models/context-capacities-preview", strings.NewReader(`{"account_id":20,"base_url":"https://new-source.example/v1","model_ids":["unknown-model"]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var payload contextCapacityRowResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Rows, 1)
	require.Nil(t, payload.Data.Rows[0].Upstream)
	require.Equal(t, int64(258000), payload.Data.Rows[0].Effective)
	require.Equal(t, "https://old-source.example/v1", stub.getAccountResult.Credentials["base_url"])
	require.NotNil(t, stub.getAccountResult.GetUpstreamModelContextCapacitySnapshot(), "preview must not mutate saved observations")
}

func TestAccountHandlerModelContextOverrideTypedRequestMapping(t *testing.T) {
	stub := newStubAdminService()
	router, _, _ := setupAccountMixedChannelRouter(stub)
	for _, tt := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/admin/accounts", `{"name":"new","platform":"openai","type":"apikey","credentials":{"api_key":"test"},"model_context_overrides":{"real-model":1050000}}`},
		{http.MethodPut, "/api/v1/admin/accounts/19", `{"model_context_overrides":{"real-model":1050000,"remove-model":null}}`},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	require.Len(t, stub.createdAccounts, 1)
	require.Equal(t, int64(1050000), *stub.createdAccounts[0].ModelContextOverrides["real-model"])
	require.Equal(t, int64(1050000), *stub.lastUpdateAccountInput.ModelContextOverrides["real-model"])
	value, present := stub.lastUpdateAccountInput.ModelContextOverrides["remove-model"]
	require.True(t, present)
	require.Nil(t, value)
}

func TestAccountHandlerModelContextOverridesRejectNonIntegerJSON(t *testing.T) {
	for _, invalid := range []string{`1.5`, `"258K"`, `true`, `90071992547409999999999`} {
		stub := newStubAdminService()
		router, _, _ := setupAccountMixedChannelRouter(stub)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/19", strings.NewReader(`{"model_context_overrides":{"real-model":`+invalid+`}}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, invalid)
		require.Zero(t, stub.updateAccountCalls)
	}
}
