package handler

import (
	"encoding/json"
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

const cindyCatalogRollbackHandlerHelperEnv = "SUB2API_CINDY_CATALOG_ROLLBACK_HANDLER_HELPER"

func TestCindyCatalogRollbackRestoresLegacyHandlerRouting(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCindyCatalogRollbackHandlerHelper$")
	cmd.Env = append(withoutCindyCatalogHandlerEnv(os.Environ()),
		service.CindyCapabilityCatalogEnabledEnv+"=false",
		cindyCatalogRollbackHandlerHelperEnv+"=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated catalog rollback handler test failed: %v\n%s", err, output)
	}
}

func TestCindyCatalogRollbackHandlerHelper(t *testing.T) {
	if os.Getenv(cindyCatalogRollbackHandlerHelperEnv) == "" {
		t.Skip("subprocess helper")
	}
	if service.CindyCapabilityCatalogFeatureEnabled() {
		t.Fatal("catalog rollback helper started with the catalog enabled")
	}

	gin.SetMode(gin.TestMode)
	groupID := int64(99201)
	group := &service.Group{
		ID:                    groupID,
		Platform:              service.PlatformOpenAI,
		AllowMessagesDispatch: false,
		StrictCindyKnown:      true,
		StrictCindy:           true,
	}
	apiKey := &service.APIKey{GroupID: &groupID, Group: group}
	gatewayService := &service.OpenAIGatewayService{}
	openAIHandler := &OpenAIGatewayHandler{gatewayService: gatewayService}

	for _, endpoint := range []service.CindyEndpoint{
		service.CindyEndpointResponses,
		service.CindyEndpointChatCompletions,
	} {
		t.Run(string(endpoint), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/legacy", strings.NewReader(`{"model":"legacy-model"}`))
			allowed, err := openAIHandler.strictCindyModelAllowed(c, apiKey, "legacy-model", endpoint)
			require.NoError(t, err)
			require.True(t, allowed, "catalog rollback must bypass the strict endpoint matrix")
		})
	}

	strictMessages, err := gatewayService.ClassifyStrictCindyGroup(t.Context(), group)
	require.NoError(t, err)
	require.False(t, strictMessages)
	require.False(t, allowOpenAICompatibleMessagesDispatch(apiKey),
		"catalog rollback must restore the legacy Messages dispatch policy")
	cindyAccount := cindyGatewayModelAccountForTest(99202)
	require.Equal(t, "legacy-mapped-model",
		openAIMessagesForwardModelForAccount(&cindyAccount, "catalog-native-model", "legacy-mapped-model"))

	cindyAccount.Credentials["model_mapping"] = map[string]any{
		"legacy-model": "legacy-upstream-model",
	}
	repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		groupID: {cindyAccount},
	}}
	modelsHandler := newGatewayModelsHandlerForTest(repo)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	modelsHandler.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var response gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Equal(t, []string{"legacy-model"}, modelIDsForTest(response.Data))
}

func withoutCindyCatalogHandlerEnv(environment []string) []string {
	blocked := map[string]struct{}{
		strings.ToUpper(service.CindyCapabilityCatalogEnabledEnv): {},
		strings.ToUpper(cindyCatalogRollbackHandlerHelperEnv):     {},
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[strings.ToUpper(key)]; found {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
