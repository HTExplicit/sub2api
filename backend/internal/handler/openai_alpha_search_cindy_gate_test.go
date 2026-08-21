//go:build unit

package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const cindySearchDisabledHandlerHelperEnv = "SUB2API_CINDY_SEARCH_DISABLED_HANDLER_HELPER"

func TestCindyAlphaSearchDisabledReturns404BeforeRequestDependencies(t *testing.T) {
	if os.Getenv(cindySearchDisabledHandlerHelperEnv) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCindyAlphaSearchDisabledReturns404BeforeRequestDependencies$")
		cmd.Env = append(withoutHandlerEnvironmentKeys(os.Environ(),
			service.CindyCapabilityCatalogEnabledEnv,
			service.CindySearchEnabledEnv,
			cindySearchDisabledHandlerHelperEnv,
		),
			service.CindyCapabilityCatalogEnabledEnv+"=true",
			service.CindySearchEnabledEnv+"=false",
			cindySearchDisabledHandlerHelperEnv+"=1",
		)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		return
	}

	c, recorder := newCindyAlphaSearchGateContext(`{"model":"gpt-5.6-sol"}`)
	handler := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}

	handler.AlphaSearch(c)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"type":"model_not_found"`)
}

func TestCindyAlphaSearchRejectsHiddenAndMessagesOnlyModelsBeforeSelection(t *testing.T) {
	for _, model := range []string{service.CindyWebSearchModel, "claude-opus-5"} {
		t.Run(model, func(t *testing.T) {
			c, recorder := newCindyAlphaSearchGateContext(`{"model":"` + model + `"}`)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 92})
			handler := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}

			handler.AlphaSearch(c)

			require.Equal(t, http.StatusNotFound, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"type":"model_not_found"`)
		})
	}
}

func newCindyAlphaSearchGateContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	groupID := int64(61010)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      61011,
		GroupID: &groupID,
		Group: &service.Group{
			ID:              groupID,
			Platform:        service.PlatformCindy,
			WirePlatform:    service.WirePlatformOpenAI,
			ProviderProfile: service.ProviderProfileCindyLaxaV1,
		},
		User: &service.User{ID: 92},
	})
	return c, recorder
}

func withoutHandlerEnvironmentKeys(environment []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[strings.ToUpper(key)] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, remove := blocked[strings.ToUpper(name)]; !remove {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
