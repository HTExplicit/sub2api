package routes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pinnedModelsRoutesRepository struct {
	service.AccountRepository
	account           service.Account
	identityCalls     int
	availabilityCalls int
}

func (r *pinnedModelsRoutesRepository) ListByGroup(_ context.Context, groupID int64) ([]service.Account, error) {
	for _, boundGroupID := range r.account.GroupIDs {
		if boundGroupID == groupID {
			return []service.Account{r.account}, nil
		}
	}
	return nil, nil
}

func (*pinnedModelsRoutesRepository) CindyGroupIdentityReaderMarker() {}

func (r *pinnedModelsRoutesRepository) ListCindyGroupIdentityMembers(ctx context.Context, groupID int64) ([]service.Account, error) {
	r.identityCalls++
	return r.ListByGroup(ctx, groupID)
}

func (*pinnedModelsRoutesRepository) CindyCodexModelsAccountReaderMarker() {}

func (r *pinnedModelsRoutesRepository) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	if r.account.Platform == platform && r.account.IsSchedulable() {
		return []service.Account{r.account}, nil
	}
	return nil, nil
}

func (r *pinnedModelsRoutesRepository) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]service.Account, error) {
	members, err := r.ListByGroup(ctx, groupID)
	if err != nil || len(members) == 0 {
		return nil, err
	}
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *pinnedModelsRoutesRepository) ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	r.availabilityCalls++
	if r.account.Status != service.StatusActive || !r.account.Schedulable {
		return nil, nil
	}
	if groupID != nil {
		members, err := r.ListByGroup(ctx, *groupID)
		if err != nil || len(members) == 0 {
			return nil, err
		}
	} else if !includeGrouped && len(r.account.GroupIDs) != 0 {
		return nil, nil
	}
	for _, platform := range platforms {
		if platform == r.account.Platform {
			return []service.Account{r.account}, nil
		}
	}
	return nil, nil
}

type pinnedModelsRoutesUpstream struct {
	service.HTTPUpstream
	ordinaryCalls atomic.Int32
	codexCalls    atomic.Int32
}

func (u *pinnedModelsRoutesUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body := `{"data":[{"id":"ordinary-upstream-model"}]}`
	if req.URL.Query().Has("client_version") {
		u.codexCalls.Add(1)
		body = `{"models":[{"slug":"gpt-5.5"}]}`
	} else {
		u.ordinaryCalls.Add(1)
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestGatewayRoutesPinnedModelsDispatchesOrdinaryAndCodexRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &pinnedModelsRoutesRepository{account: service.Account{
		ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		GroupIDs:    []int64{1},
		Credentials: map[string]any{"api_key": "test-models-key", "base_url": "https://models.example/v1"},
	}}
	upstream := &pinnedModelsRoutesUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	s := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	gateway := service.NewGatewayService(repo, nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &handler.Handlers{
		Gateway:       handler.NewGatewayHandler(gateway, s, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil),
		OpenAIGateway: handler.NewOpenAIGatewayHandler(s, nil, nil, nil, nil, nil, nil, nil, cfg),
		AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
	}
	group := &service.Group{ID: 1, Platform: service.PlatformOpenAI,
		CodexModelsManifestConfig: service.GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{7}}}
	router := gin.New()
	RegisterGatewayRoutes(router, h, servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
		c.Next()
	}), nil, nil, nil, nil, nil, cfg)
	for _, path := range []string{"/v1/models", "/models", "/v1/models?client_version=", "/models?client_version="} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", path, w.Body.String())
		var response struct {
			Object string `json:"object"`
			Data   []struct {
				ID            string `json:"id"`
				ContextWindow int64  `json:"context_window"`
				Source        string `json:"context_capacity_source"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, "list", response.Object)
		require.Len(t, response.Data, 1)
		require.Equal(t, "ordinary-upstream-model", response.Data[0].ID)
		require.Equal(t, service.DefaultModelContextWindow, response.Data[0].ContextWindow)
		require.Equal(t, "default", response.Data[0].Source)
	}
	for _, path := range []string{"/v1/models?client_version=" + service.CodexCanonicalClientVersion(), "/models?client_version=" + service.CodexCanonicalClientVersion(), "/backend-api/codex/models"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", path, w.Body.String())
		require.Contains(t, w.Body.String(), `"slug":"gpt-5.5"`)
		require.Contains(t, w.Body.String(), `"context_capacity_source":"official"`)
		require.NotContains(t, w.Body.String(), `"data"`)
	}
	require.EqualValues(t, 1, upstream.ordinaryCalls.Load())
	require.EqualValues(t, 1, upstream.codexCalls.Load())
	require.Positive(t, repo.identityCalls, "the fixture must exercise the strict identity guard")
	require.Positive(t, repo.availabilityCalls, "the fixture must exercise persistent capacity candidates")
}
