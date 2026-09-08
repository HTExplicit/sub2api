package service

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestAccountAvailableModelsProjectsDiscoveryWithoutChangingCache(t *testing.T) {
	fixtureBytes, err := os.ReadFile("testdata/account_available_models_contract.json")
	require.NoError(t, err)
	var fixture struct {
		Upstream json.RawMessage `json:"upstream"`
		Expected []openai.Model  `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	var calls atomic.Int32
	upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		calls.Add(1)
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/v1/models", req.URL.Path)
		return ordinaryModelsUpstreamResponse(string(fixture.Upstream)), nil
	}}
	gateway := newCodexModelsAPIKeyTestService(upstream)
	svc := NewAccountTestService(nil, nil, nil, nil, nil, upstream, gateway.cfg, nil)
	svc.SetOpenAIGatewayService(gateway)
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	account.Credentials["model_mapping"] = map[string]any{"public-alias": "Case/Only-ID"}
	credentialsBefore, err := json.Marshal(account.Credentials)
	require.NoError(t, err)
	catalog, err := gateway.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	originalBody := append([]byte(nil), catalog.Body...)
	originalETag := catalog.ETag

	models, err := svc.FetchOpenAIAccountModels(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, fixture.Expected, models)
	for _, model := range models {
		require.NotEmpty(t, strings.TrimSpace(model.DisplayName))
		require.NotEmpty(t, strings.TrimSpace(model.Type))
	}
	models[0].DisplayName = "caller-only change"
	again, err := svc.FetchOpenAIAccountModels(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, fixture.Expected, again)
	stillRaw, err := gateway.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, originalBody, stillRaw.Body)
	require.Equal(t, originalETag, stillRaw.ETag)
	credentialsAfter, err := json.Marshal(account.Credentials)
	require.NoError(t, err)
	require.Equal(t, credentialsBefore, credentialsAfter)
	require.EqualValues(t, 1, calls.Load(), "display projection must use the existing discovery cache")
}

func TestAccountAvailableModelsProjectsOAuthDiscovery(t *testing.T) {
	_, calls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"Case/OAuth-A","display_name":"upstream descriptor"},{"slug":"oauth-second"}]}`)
	gateway := &OpenAIGatewayService{}
	svc := &AccountTestService{}
	svc.SetOpenAIGatewayService(gateway)
	account := newCodexModelsTestAccount()
	before, err := gateway.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	models, err := svc.FetchOpenAIAccountModels(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"Case/OAuth-A", "oauth-second"}, []string{models[0].ID, models[1].ID})
	for _, model := range models {
		require.Equal(t, model.ID, model.DisplayName)
		require.Equal(t, "model", model.Type)
	}
	after, err := gateway.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, before.Body, after.Body, "do not add UI fields to the public OAuth representation")
	require.Equal(t, before.ETag, after.ETag)
	require.EqualValues(t, 1, calls.Load())
}

func TestAccountAvailableModelsPreservesEmptyAndInvalidCatalogSemantics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		invalid bool
	}{
		{"empty", `{"data":[]}`, false},
		{"missing", `{}`, true},
		{"null", `{"data":null}`, true},
		{"error", `{"error":{"message":"failure"}}`, true},
		{"empty_id", `{"data":[{"id":""}]}`, true},
		{"blank_id", `{"data":[{"id":"  "}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, "/v1/models", req.URL.Path)
				return ordinaryModelsUpstreamResponse(tc.body), nil
			}}
			gateway := newCodexModelsAPIKeyTestService(upstream)
			svc := &AccountTestService{}
			svc.SetOpenAIGatewayService(gateway)
			account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
			models, err := svc.FetchOpenAIAccountModels(context.Background(), account)
			if tc.invalid {
				require.Error(t, err, "shared discovery must reject invalid catalogs before display conversion")
				require.Nil(t, models)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, models)
			require.Empty(t, models)
		})
	}
}
