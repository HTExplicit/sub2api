package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountEditContextKeepsCoreIndependentAndStoredIdentityBound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: service.Account{ID: 44, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "must-not-leak", "base_url": "https://api.laxarouter.ai"}, Extra: map[string]any{"is_cindy": true}}}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/api/v1/admin/accounts/:id/edit-context", handler.GetEditContext)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/44/edit-context", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var result struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, map[string]any{"schema_version": float64(1), "kind": "core", "account_id": float64(44)}, result.Data)
	require.NotContains(t, response.Body.String(), "must-not-leak")
	// Only the stored canonical identity selects native Cindy editing. A legacy
	// OpenAI base URL/extra marker above was not enough to reclassify the account.
	svc.account.Platform = service.PlatformCindy
	svc.account.WirePlatform = service.WirePlatformOpenAI
	svc.account.ProviderProfile = service.ProviderProfileCindyLaxaV1
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/44/edit-context", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var native struct {
		Data service.AccountEditContext `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &native))
	require.EqualValues(t, 44, native.Data.AccountID)
	require.Equal(t, "provider", native.Data.Kind)
	require.NotNil(t, native.Data.Profile)
	require.True(t, native.Data.Profile.Native)
	require.True(t, native.Data.Profile.Available)
	require.Len(t, native.Data.EditStateSHA256, 64)
	require.NotContains(t, response.Body.String(), "must-not-leak")
	require.NotContains(t, response.Body.String(), "plugin_id")
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/0/edit-context", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "invalid native targets cannot receive an edit context")

}

func TestAccountEditRequestRejectsUnknownAndNullIntent(t *testing.T) {
	for _, raw := range []string{`{"provider_edit":null}`, `{"provider_edit":{"changes":{"extra":{"op":"clear"}}}}`, `{"provider_edit":{"changes":{}}}`} {
		var request UpdateAccountRequest
		require.Error(t, json.Unmarshal([]byte(raw), &request))
	}
	var native UpdateAccountRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"basic","extra":{"openai_responses_mode":null}}`), &native))
	require.Nil(t, native.ProviderEdit)
	require.Contains(t, native.Extra, "openai_responses_mode")
	require.Nil(t, native.Extra["openai_responses_mode"])
}
