package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type configuredModelsUpstream struct {
	service.HTTPUpstream
	body   string
	status int
	calls  atomic.Int32
}

func (u *configuredModelsUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	if req.Method != http.MethodGet || req.URL.Path != "/v1/models" {
		return nil, fmt.Errorf("unexpected non-catalog request")
	}
	return &http.Response{StatusCode: u.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

func configuredModelsRouter(account service.Account, upstream *configuredModelsUpstream) (*gin.Engine, *availableModelsAdminService, *service.OpenAIGatewayService) {
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	accountTestSvc := service.NewAccountTestService(nil, nil, nil, nil, nil, upstream, cfg, nil)
	accountTestSvc.SetOpenAIGatewayService(gateway)
	adminSvc := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: account}
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, accountTestSvc, nil, nil, nil, nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/admin/accounts/:id/models", handler.GetAvailableModels)
	return router, adminSvc, gateway
}

func getConfiguredTestModels(t *testing.T, router *gin.Engine) []openai.Model {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/601/models", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data []openai.Model `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.NotNil(t, payload.Data, "a valid empty selection must remain an array")
	for _, model := range payload.Data {
		require.NotEmpty(t, strings.TrimSpace(model.DisplayName))
		require.NotEmpty(t, strings.TrimSpace(model.Type))
	}
	return payload.Data
}

func configuredTestModelIDs(models []openai.Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestAccountHandlerGetAvailableModels_ConfiguredModels(t *testing.T) {
	for _, kind := range []string{"apikey", "oauth", "shadow"} {
		t.Run(kind, func(t *testing.T) {
			account := service.Account{
				ID: 601, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key": "mock-key", "base_url": "https://models.example/v1",
					"model_mapping": map[string]any{"Case/B": "missing-upstream-target", "Case/A": "Case/A"},
				},
			}
			if kind != "apikey" {
				// No OAuth token is supplied: even a regression must not contact a real backend.
				account.Type = service.AccountTypeOAuth
			}
			if kind == "shadow" {
				parentID := int64(600)
				account.ParentAccountID = &parentID
				account.QuotaDimension = service.QuotaDimensionSpark
			}
			upstream := &configuredModelsUpstream{status: http.StatusOK, body: `{"data":[{"id":"Case/A"},{"id":"Case/C"}]}`}
			router, adminSvc, gateway := configuredModelsRouter(account, upstream)
			before, err := json.Marshal(adminSvc.account.Credentials)
			require.NoError(t, err)
			models := getConfiguredTestModels(t, router)
			require.Equal(t, []string{"Case/A", "Case/B"}, configuredTestModelIDs(models))
			require.Equal(t, "Case/B", models[1].DisplayName)
			require.Equal(t, "model", models[1].Type)
			require.Zero(t, models[1].Created, "new aliases must not acquire a fabricated creation time")
			after, err := json.Marshal(adminSvc.account.Credentials)
			require.NoError(t, err)
			require.Equal(t, before, after, "building the picker must not rewrite saved configuration")
			require.Zero(t, upstream.calls.Load(), "concrete configuration does not require discovery")

			// Model the next saved account snapshot without invoking unrelated persistence code.
			adminSvc.account.Credentials["model_mapping"] = map[string]any{
				"Case/D": "Case/D", "Case/B": "missing-upstream-target", "Case/A": "Case/A",
			}
			require.Equal(t, []string{"Case/A", "Case/B", "Case/D"}, configuredTestModelIDs(getConfiguredTestModels(t, router)))
			require.Zero(t, upstream.calls.Load())
			if kind == "apikey" {
				cached, fetchErr := gateway.FetchOpenAIModelsList(context.Background(), &adminSvc.account)
				require.NoError(t, fetchErr)
				require.Equal(t, []string{"Case/A", "Case/B", "Case/D"}, configuredTestModelIDs(getConfiguredTestModels(t, router)))
				stillRaw, fetchErr := gateway.FetchOpenAIModelsList(context.Background(), &adminSvc.account)
				require.NoError(t, fetchErr)
				require.Equal(t, cached.Body, stillRaw.Body)
				require.Equal(t, cached.ETag, stillRaw.ETag)
				require.NotContains(t, string(stillRaw.Body), "Case/B")
				require.EqualValues(t, 1, upstream.calls.Load(), "the saved list must remain authoritative with a warm raw catalog")
			}
		})
	}
}

func TestAccountHandlerGetAvailableModels_ConfiguredModelWildcards(t *testing.T) {
	fallbackIDs := []string{"Public/A"}
	for _, model := range openai.DefaultModels {
		if strings.HasPrefix(model.ID, "gpt-image-") {
			fallbackIDs = append(fallbackIDs, model.ID)
		}
	}
	for _, tc := range []struct {
		name    string
		mapping map[string]any
		body    string
		status  int
		want    []string
	}{
		{
			name: "live",
			mapping: map[string]any{
				"z-explicit": "absent-target", "case/*": "broad-target",
				"case/special-*": "specific-target", "case/special-one": "exact-target",
			},
			body: `{"data":[
				{"id":"case/two","display_name":"Provider Two","type":"provider-model","object":"model","created":123,"owned_by":"provider","description":"Provider description","context_window":321000,"max_output_tokens":1234},
				{"id":"not-permitted"},{"id":"case/special-two"},{"id":"case/special-one"},{"id":"case/two"}
			]}`,
			status: http.StatusOK,
			want:   []string{"case/special-one", "z-explicit", "case/two", "case/special-two"},
		},
		{
			name: "failed_discovery", mapping: map[string]any{"Public/A": "absent-target", "gpt-image-*": "image-target"},
			body: `{"error":"unavailable"}`, status: http.StatusBadGateway, want: fallbackIDs,
		},
		{
			name: "empty_catalog", mapping: map[string]any{"Public/A": "absent-target", "gpt-*": "target"},
			body: `{"data":[]}`, status: http.StatusOK, want: []string{"Public/A"},
		},
		{
			name: "empty_wildcard_only", mapping: map[string]any{"gpt-*": "target"},
			body: `{"data":[]}`, status: http.StatusOK, want: []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := service.Account{ID: 601, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "mock-key", "base_url": "https://models.example/v1", "model_mapping": tc.mapping}}
			before, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			upstream := &configuredModelsUpstream{body: tc.body, status: tc.status}
			router, adminSvc, gateway := configuredModelsRouter(account, upstream)
			models := getConfiguredTestModels(t, router)
			require.Equal(t, tc.want, configuredTestModelIDs(models))
			require.EqualValues(t, 1, upstream.calls.Load())
			after, err := json.Marshal(adminSvc.account.Credentials)
			require.NoError(t, err)
			require.Equal(t, before, after)
			if tc.name == "live" {
				require.Equal(t, openai.Model{ID: "case/two", DisplayName: "Provider Two", Type: "provider-model", Object: "model", Created: 123, OwnedBy: "provider", Description: "Provider description", ContextWindow: 321000, MaxOutputTokens: 1234}, models[2])
				for id, target := range map[string]string{"case/special-one": "exact-target", "case/special-two": "specific-target", "case/two": "broad-target"} {
					mapped, matched := adminSvc.account.ResolveMappedModel(id)
					require.True(t, matched)
					require.Equal(t, target, mapped, "picker IDs must preserve exact and longest-wildcard routing")
				}
				cached, fetchErr := gateway.FetchOpenAIModelsList(context.Background(), &adminSvc.account)
				require.NoError(t, fetchErr)
				require.Contains(t, string(cached.Body), "not-permitted", "picker filtering must not narrow the shared catalog")
				require.NotContains(t, string(cached.Body), "z-explicit")
				require.Equal(t, models, getConfiguredTestModels(t, router))
				stillRaw, fetchErr := gateway.FetchOpenAIModelsList(context.Background(), &adminSvc.account)
				require.NoError(t, fetchErr)
				require.Equal(t, cached.Body, stillRaw.Body)
				require.Equal(t, cached.ETag, stillRaw.ETag)
				require.EqualValues(t, 1, upstream.calls.Load())
			}
		})
	}
}

func TestAccountHandlerGetAvailableModels_UnconfiguredModels(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/failed=%t", passthrough, failed), func(t *testing.T) {
				account := service.Account{ID: 601, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "mock-key", "base_url": "https://models.example/v1"}}
				if passthrough {
					account.Credentials["model_mapping"] = map[string]any{"stale-alias": "old-target"}
					account.Extra = map[string]any{"openai_passthrough": true}
				}
				upstream := &configuredModelsUpstream{status: http.StatusOK, body: `{"data":[{"id":"upstream-only","display_name":"Live model"}]}`}
				if failed {
					upstream.status = http.StatusBadGateway
				}
				router, _, _ := configuredModelsRouter(account, upstream)
				models := getConfiguredTestModels(t, router)
				if failed {
					require.Equal(t, openai.DefaultModels, models)
				} else {
					require.Equal(t, []string{"upstream-only"}, configuredTestModelIDs(models))
					require.Equal(t, "Live model", models[0].DisplayName)
				}
				require.EqualValues(t, 1, upstream.calls.Load())
			})
		}
	}
}
