package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayCapacityRepo struct {
	*gatewayModelsAccountRepoStub
	availabilityErr   error
	availabilityCalls int
}

func (r *gatewayCapacityRepo) ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	r.availabilityCalls++
	if r.availabilityErr != nil {
		return nil, r.availabilityErr
	}
	return r.gatewayModelsAccountRepoStub.ListModelAvailabilityCandidates(ctx, groupID, platforms, includeGrouped)
}

func TestGatewayModelsContextCapacityPreservesShapeAliasesAndOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 991, Platform: service.PlatformOpenAI, ModelsListConfig: service.GroupModelsListConfig{Enabled: true, Models: []string{"alias-b", "alias-a"}}}
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://capacity.example/v1", "model_mapping": map[string]any{"alias-a": "native-model", "alias-b": "native-model"}},
		Extra:       map[string]any{service.ModelContextOverridesExtraKey: map[string]int64{"native-model": 650001}},
	}
	repo := &gatewayCapacityRepo{gatewayModelsAccountRepoStub: &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{group.ID: {account}}}}
	handler := newGatewayModelsHandlerForTest(repo)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	handler.Models(ctx)
	require.Equal(t, http.StatusOK, response.Code)
	var envelope struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "list", envelope.Object)
	require.Len(t, envelope.Data, 2)
	for i, model := range []string{"alias-b", "alias-a"} {
		require.Equal(t, model, envelope.Data[i]["id"])
		require.Equal(t, "model", envelope.Data[i]["object"])
		require.Equal(t, "openai", envelope.Data[i]["owned_by"])
		require.Equal(t, float64(650001), envelope.Data[i]["context_window"])
		require.Equal(t, "custom", envelope.Data[i]["context_capacity_source"])
		require.NotContains(t, envelope.Data[i], "created_at")
		require.NotContains(t, envelope.Data[i], "account_id")
	}
	require.Equal(t, 1, repo.availabilityCalls)
}

func TestGatewayModelsContextCapacityQueryFailureKeepsList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 992, Platform: service.PlatformGemini}
	account := service.Account{ID: 1, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"unlisted": "unlisted"}}}
	repo := &gatewayCapacityRepo{gatewayModelsAccountRepoStub: &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{group.ID: {account}}}, availabilityErr: errors.New("sensitive internal failure")}
	handler := newGatewayModelsHandlerForTest(repo)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	handler.Models(ctx)
	require.Equal(t, http.StatusOK, response.Code)
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "unlisted", envelope.Data[0]["id"])
	require.Equal(t, float64(258000), envelope.Data[0]["context_window"])
	require.Equal(t, "account_query_failed", envelope.Data[0]["context_capacity_reason"])
	require.NotContains(t, response.Body.String(), "sensitive internal failure")
	require.Equal(t, 1, repo.availabilityCalls)
}
