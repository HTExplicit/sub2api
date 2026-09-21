package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountViewCoreJSONNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`[1,"two",{"native":true}]`, `{"items":[1,"two",null],"account_ids":"native-not-accounts"}`, `{"unfinished":`, `null`} {
		router := gin.New()
		var plugin *PluginHandler // No registry or plugin runtime exists.
		router.Use(plugin.AccountViewRequest())
		router.POST("/api/v1/admin/native-fixture", func(c *gin.Context) {
			raw, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			c.Data(http.StatusAccepted, "application/octet-stream", raw)
		})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/native-fixture", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusAccepted, response.Code)
		require.Equal(t, body, response.Body.String(), "plain core body bytes and handler response must be untouched")
	}
}

func TestAccountViewInvalidOrUnboundIdentityCannotFallBackToCore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	called := 0
	plugin := &PluginHandler{}
	router.Use(plugin.AccountViewRequest())
	router.GET("/api/v1/admin/accounts", func(c *gin.Context) { called++; c.Status(200) })
	for _, header := range []string{"not/base64?", base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"url":"/admin/accounts"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"plugin_id":1,"plugin_key":"codexrip.cindy-provider","package_sha256":"invalid","view_id":"cindy-accounts","preset_id":"cindy","view_definition_digest":"invalid"}`))} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts", nil)
		request.Header.Set(extensionv1.AccountViewHeader, header)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.GreaterOrEqual(t, response.Code, 400)
	}
	require.Zero(t, called)
}

func TestAccountViewRejectsGenericRPCChannelOnlyForViewOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"/api/v1/admin/plugins/:id/actions", "/api/v1/admin/plugins/:id/jobs"} {
		router := gin.New()
		var plugin *PluginHandler
		router.Use(plugin.AccountViewRequest())
		called := 0
		router.POST(route, func(c *gin.Context) { called++; c.Status(http.StatusAccepted) })
		path := strings.ReplaceAll(route, ":id", "2")
		for _, withView := range []bool{true, false} {
			request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"operation":"fixture","items":[]}`))
			request.Header.Set("Content-Type", "application/json")
			if withView {
				request.Header.Set(extensionv1.AccountViewHeader, base64.RawURLEncoding.EncodeToString([]byte(`{"version":1}`)))
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if withView {
				require.Equal(t, http.StatusBadRequest, response.Code)
				require.Contains(t, response.Body.String(), "ACCOUNT_VIEW_UNSUPPORTED_OPERATION")
			} else {
				require.Equal(t, http.StatusAccepted, response.Code)
			}
		}
		require.Equal(t, 1, called, "only the original no-view channel may reach generic RPC")
	}
}

func TestAccountViewTypedQueryAndBulkCindyPredicates(t *testing.T) {
	var context extensionv1.AccountViewContextV1
	require.Error(t, decodeAccountViewEnvelope([]byte(`{"query":{"extra":{"api_key":"synthetic"}}}`), &context))
	require.Error(t, decodeAccountViewEnvelope([]byte(`{"query":null}`), &context))
	filters, err := toServiceBulkUpdateAccountFilters(&BulkUpdateAccountFilters{CindyOnly: true, CindyHealthStatus: "banned", CindyBalanceStatus: "insufficient"})
	require.NoError(t, err)
	require.NotNil(t, filters.Console)
	require.True(t, filters.Console.CindyOnly)
	require.Equal(t, "banned", filters.Console.CindyHealthStatus)
	require.Equal(t, "insufficient", filters.Console.CindyBalanceStatus)
	_, err = toServiceBulkUpdateAccountFilters(&BulkUpdateAccountFilters{CindyBalanceStatus: "invented"})
	require.Error(t, err)
}

type viewRecoveryAdmin struct {
	*stubAdminService
	account *service.Account
}

func (s *viewRecoveryAdmin) ClearCindyBalanceInsufficient(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}

func TestCindyRecoveryResourceProjection(t *testing.T) {
	stub := &viewRecoveryAdmin{stubAdminService: newStubAdminService(), account: &service.Account{ID: 7, Credentials: map[string]any{"api_key": "synthetic-key"}, Extra: map[string]any{"private_secret": "synthetic-extra"}}}
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	for _, pluginRequest := range []bool{true, false} {
		router := gin.New()
		router.POST("/accounts/:id/recover", func(c *gin.Context) {
			if pluginRequest {
				manager := service.NewPluginManager(nil, nil, nil, service.PluginHostInfo{}, nil)
				c.Request = c.Request.WithContext(manager.WithResourcePolicy(c.Request.Context(), extensionv1.ResourceDescriptor{}))
			}
			handler.ClearCindyBalanceInsufficient(c)
		})
		request := httptest.NewRequest(http.MethodPost, "/accounts/7/recover", nil)
		if pluginRequest {
			request.Header.Set("X-Sub2API-Plugin", "3")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		if pluginRequest {
			var envelope struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Equal(t, map[string]any{"account_id": float64(7), "recovered": true}, envelope.Data)
			require.NotContains(t, response.Body.String(), "credentials")
			require.NotContains(t, response.Body.String(), "synthetic-extra")
		} else {
			require.Contains(t, response.Body.String(), `"credentials"`, "native response shape stays compatible")
		}
	}
}
