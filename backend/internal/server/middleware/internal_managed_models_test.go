package middleware

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestManagedModelSelectorIsolationHTTPRejectsRawInternalNamesForEveryGroup(t *testing.T) {
	for _, group := range []*service.Group{
		{ID: 1, Platform: service.PlatformOpenAI, IsExclusive: true},
		{ID: 2, Platform: service.PlatformOpenAI},
		{ID: 3, Platform: service.PlatformCindy, IsExclusive: true},
	} {
		for _, body := range []string{
			`{"model":"s2pub-g23-m123"}`,
			`{"model":"gpt-5.6-sol","Model":" S2PUB-g23-m123 "}`,
			`{"model":"s2pub-g23-m123","model":"gpt-5.6-sol"}`,
			`{"model":"gpt-5.6-sol","session":{"model":"s2pub-g23-m123"}}`,
		} {
			router, calls := newGroupModelAllowlistTestRouter(&service.APIKey{Group: group}, "/v1")
			response := doJSON(t, router, http.MethodPost, "/v1/responses", body)
			require.Equal(t, http.StatusNotFound, response.Code)
			require.Empty(t, *calls, "raw reserved names must never reach the inference handler")
			require.NotContains(t, response.Body.String(), "s2pub-")
		}
	}
}

func TestManagedModelSelectorIsolationHTTPChecksPathQueryAndMultipart(t *testing.T) {
	key := &service.APIKey{Group: &service.Group{ID: 1, Platform: service.PlatformOpenAI, IsExclusive: true}}
	for _, path := range []string{"/v1/models/s2pub-g23-m123", "/v1/realtime?model=normal&model=S2PUB-g23-m123"} {
		router, calls := newGroupModelAllowlistTestRouter(key, "/v1")
		response := doJSON(t, router, http.MethodGet, path, "")
		require.Equal(t, http.StatusNotFound, response.Code)
		require.Empty(t, *calls)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "normal"))
	require.NoError(t, writer.WriteField("model", "s2pub-g23-m123"))
	require.NoError(t, writer.Close())
	router, calls := newGroupModelAllowlistTestRouter(key, "/v1")
	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Empty(t, *calls)
}

func TestManagedModelSelectorIsolationHTTPKeepsLegalPrivateAndCindyBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformCindy} {
		router := gin.New()
		key := &service.APIKey{Group: &service.Group{ID: 1, Platform: platform, IsExclusive: true}}
		router.Use(func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), key); c.Next() }, GroupModelAllowlist())
		original := ` {"model":"ordinary-private-model","input":[{"model":"s2pub-is-application-data"}]} `
		router.POST("/v1/responses", func(c *gin.Context) {
			body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
			require.NoError(t, err)
			require.Equal(t, original, string(body))
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(original))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
	}
}
