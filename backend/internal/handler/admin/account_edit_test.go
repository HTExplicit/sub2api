package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountEditContextKeepsCoreIndependentAndViewBound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: service.Account{ID: 44, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "must-not-leak", "base_url": "https://api.laxarouter.ai"}, Extra: map[string]any{"is_cindy": true}}}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	plugin := &PluginHandler{}
	router := gin.New()
	router.Use(plugin.AccountViewRequest())
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
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/44/edit-context", nil)
	request.Header.Set(extensionv1.AccountViewHeader, base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"plugin_id":999}`)))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.GreaterOrEqual(t, response.Code, 400, "a bound origin must not fall back to core")
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
