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
	"github.com/tidwall/gjson"
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
	require.Equal(t, "gpt-5.4", cindyAccount.GetMappedModel("gpt-5.4"),
		"account-level mapping must not recognize compatibility aliases")
	require.Equal(t, "gpt-5.4-mini", cindyAccount.GetMappedModel("gpt-5.4-mini"),
		"mixed groups must not inherit compatibility aliases from a selected Cindy account")
	require.Equal(t, "openai/gpt-5.6-luna", cindyAccount.GetMappedModel("openai/gpt-5.6-luna"),
		"the strict group router's resolved target must remain authoritative")
	ordinaryAccount := service.Account{
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://ordinary.example.invalid",
		},
	}
	require.Equal(t, "gpt-5.4-mini", ordinaryAccount.GetMappedModel("gpt-5.4-mini"))
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

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, EndpointModelCapabilities, nil)
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
	modelsHandler.ModelCapabilities(c)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "Model capability catalog is not enabled")
	require.Empty(t, GetUpstreamEndpoint(c, service.PlatformOpenAI))

	for _, tc := range []struct {
		name         string
		request      string
		expected     string
		strict       bool
		ordinary     bool
		modelMapping map[string]any
	}{
		{name: "strict_sol_alias", request: "gpt-5.4", expected: "openai/gpt-5.6-sol", strict: true},
		{name: "strict_luna_alias", request: "gpt-5.4-mini", expected: "openai/gpt-5.6-luna", strict: true},
		{
			name: "configured_stable_sol", request: "gpt-5.6-sol", expected: "openai/gpt-5.6-sol",
			modelMapping: map[string]any{"gpt-5.6-sol": "openai/gpt-5.6-sol"},
		},
		{
			name: "configured_stable_luna", request: "gpt-5.6-luna", expected: "openai/gpt-5.6-luna",
			modelMapping: map[string]any{"gpt-5.6-luna": "openai/gpt-5.6-luna"},
		},
		{name: "mixed_group_does_not_map", request: "gpt-5.4-mini", expected: "gpt-5.4-mini"},
		{name: "ordinary_openai_does_not_map", request: "gpt-5.4-mini", expected: "gpt-5.4-mini", ordinary: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, accountRepo, upstream, handlerGroupID := newCindyBalanceFailoverHandler(t)
			accountRepo.accounts = accountRepo.accounts[1:]
			if tc.modelMapping != nil {
				accountRepo.accounts[0].Credentials["model_mapping"] = tc.modelMapping
				accountRepo.accounts[0].Extra["openai_passthrough"] = false
			}
			if tc.ordinary {
				accountRepo.accounts[0].Credentials["base_url"] = "https://ordinary.example.invalid"
			}
			ctx, recorder := newStrictCindyHandlerContext(t, handlerGroupID, "/v1/responses",
				`{"model":"`+tc.request+`","input":"hi","stream":true}`)
			rawAPIKey, exists := ctx.Get(string(middleware2.ContextKeyAPIKey))
			require.True(t, exists)
			requestAPIKey, ok := rawAPIKey.(*service.APIKey)
			require.True(t, ok)
			requestAPIKey.Group.StrictCindy = tc.strict

			h.Responses(ctx)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, []string{tc.expected}, upstream.models())
		})
	}

	for _, tc := range []struct {
		name           string
		strictGroup    bool
		requestedModel string
		modelMapping   map[string]any
		expectedStatus int
		expectedModels []string
	}{
		{
			name:           "strict_chat_sol_alias",
			strictGroup:    true,
			requestedModel: "gpt-5.4",
			modelMapping:   map[string]any{"gpt-5.6-sol": "openai/gpt-5.6-sol"},
			expectedStatus: http.StatusOK,
			expectedModels: []string{"openai/gpt-5.6-sol"},
		},
		{
			name:           "strict_chat_luna_alias",
			strictGroup:    true,
			requestedModel: "gpt-5.4-mini",
			modelMapping:   map[string]any{"gpt-5.6-luna": "openai/gpt-5.6-luna"},
			expectedStatus: http.StatusOK,
			expectedModels: []string{"openai/gpt-5.6-luna"},
		},
		{
			name:           "mixed_chat_does_not_map",
			strictGroup:    false,
			requestedModel: "gpt-5.4-mini",
			modelMapping:   map[string]any{"gpt-5.4-mini": "gpt-5.4-mini"},
			expectedStatus: http.StatusOK,
			expectedModels: []string{"gpt-5.4-mini"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, accountRepo, upstream, handlerGroupID := newCindyBalanceFailoverHandler(t)
			accountRepo.accounts = accountRepo.accounts[1:]
			accountRepo.accounts[0].Credentials["model_mapping"] = tc.modelMapping
			accountRepo.accounts[0].Extra["openai_passthrough"] = false
			ctx, recorder := newStrictCindyHandlerContext(t, handlerGroupID, "/v1/chat/completions",
				`{"model":"`+tc.requestedModel+`","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":false}`)
			rawAPIKey, exists := ctx.Get(string(middleware2.ContextKeyAPIKey))
			require.True(t, exists)
			requestAPIKey, ok := rawAPIKey.(*service.APIKey)
			require.True(t, ok)
			requestAPIKey.Group.StrictCindy = tc.strictGroup

			h.ChatCompletions(ctx)

			require.Equal(t, tc.expectedStatus, recorder.Code, recorder.Body.String())
			require.Equal(t, tc.expectedModels, upstream.models())
			require.Equal(t, []string{"/v1/responses"}, upstream.paths())
		})
	}

	wsResult := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:     `{"type":"response.create","model":"gpt-5.4-mini","input":"hi","stream":false}`,
		strictCindyGroup: true,
	})
	require.Equal(t, "openai/gpt-5.6-luna",
		gjson.GetBytes(wsResult.upstreamFirstPayload, "model").String(),
		"direct Responses WebSocket ingress must preserve the strict catalog-off alias")
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
