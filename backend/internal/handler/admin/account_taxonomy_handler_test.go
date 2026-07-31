package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountTaxonomyHandlerStub struct {
	*stubAdminService
	facets        *service.AccountConsoleFacets
	lastBulk      service.BulkAccountTaxonomyInput
	bulkResult    *service.BulkAccountTaxonomyResult
	folders       []service.AccountManagementFolder
	tags          []service.AccountManagementTag
	lastFolderIDs []int64
	lastTagIDs    []int64
}

func newAccountTaxonomyHandlerStub() *accountTaxonomyHandlerStub {
	return &accountTaxonomyHandlerStub{stubAdminService: newStubAdminService()}
}

func (s *accountTaxonomyHandlerStub) ListAccountFolders(context.Context) ([]service.AccountManagementFolder, error) {
	return s.folders, nil
}

func (s *accountTaxonomyHandlerStub) CreateAccountFolder(context.Context, service.AccountTaxonomyInput) (*service.AccountManagementFolder, error) {
	return &service.AccountManagementFolder{}, nil
}

func (s *accountTaxonomyHandlerStub) UpdateAccountFolder(context.Context, int64, service.AccountTaxonomyInput) (*service.AccountManagementFolder, error) {
	return &service.AccountManagementFolder{}, nil
}

func (s *accountTaxonomyHandlerStub) DeleteAccountFolder(context.Context, int64, bool) error {
	return nil
}

func (s *accountTaxonomyHandlerStub) ListAccountTags(context.Context) ([]service.AccountManagementTag, error) {
	return s.tags, nil
}

func (s *accountTaxonomyHandlerStub) CreateAccountTag(context.Context, service.AccountTaxonomyInput) (*service.AccountManagementTag, error) {
	return &service.AccountManagementTag{}, nil
}

func (s *accountTaxonomyHandlerStub) UpdateAccountTag(context.Context, int64, service.AccountTaxonomyInput) (*service.AccountManagementTag, error) {
	return &service.AccountManagementTag{}, nil
}

func (s *accountTaxonomyHandlerStub) DeleteAccountTag(context.Context, int64) error { return nil }

func (s *accountTaxonomyHandlerStub) SetAccountTaxonomy(context.Context, int64, service.AccountTaxonomyAssignment) (*service.Account, error) {
	return &service.Account{}, nil
}

func (s *accountTaxonomyHandlerStub) ListAccountsConsole(context.Context, int, int, service.AccountConsoleFilters) ([]service.Account, int64, error) {
	return nil, 0, nil
}

func (s *accountTaxonomyHandlerStub) GetAccountConsoleFacets(context.Context, service.AccountConsoleFilters) (*service.AccountConsoleFacets, error) {
	return s.facets, nil
}

func (s *accountTaxonomyHandlerStub) ReorderAccountFolders(_ context.Context, orderedIDs []int64) ([]service.AccountManagementFolder, error) {
	s.lastFolderIDs = append([]int64(nil), orderedIDs...)
	return s.folders, nil
}

func (s *accountTaxonomyHandlerStub) ReorderAccountTags(_ context.Context, orderedIDs []int64) ([]service.AccountManagementTag, error) {
	s.lastTagIDs = append([]int64(nil), orderedIDs...)
	return s.tags, nil
}

func (s *accountTaxonomyHandlerStub) BulkUpdateAccountTaxonomy(_ context.Context, input service.BulkAccountTaxonomyInput) (*service.BulkAccountTaxonomyResult, error) {
	s.lastBulk = input
	return s.bulkResult, nil
}

func setupAccountTaxonomyHandlerRouter(adminSvc *accountTaxonomyHandlerStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts/facets", handler.GetAccountFacets)
	router.PUT("/api/v1/admin/accounts/folders/order", handler.ReorderAccountFolders)
	router.PUT("/api/v1/admin/accounts/tags/order", handler.ReorderAccountTags)
	router.POST("/api/v1/admin/accounts/bulk-taxonomy", handler.BulkUpdateAccountTaxonomy)
	return router
}

func TestAccountFacetsUsesStableSnakeCaseTaxonomyDTO(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	adminSvc := newAccountTaxonomyHandlerStub()
	adminSvc.facets = &service.AccountConsoleFacets{
		Total: 4, UncategorizedCount: 1,
		Platforms: []service.AccountFacetOption{{Value: "openai", Label: "OpenAI", Count: 4}},
		Folders:   []service.AccountManagementFolder{{ID: 7, Name: "Production", SortOrder: 2, AccountCount: 3, CreatedAt: now, UpdatedAt: now}},
		Tags:      []service.AccountManagementTag{{ID: 9, Name: "Paid", SortOrder: 1, AccountCount: 2, CreatedAt: now, UpdatedAt: now}},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/facets", nil)
	setupAccountTaxonomyHandlerRouter(adminSvc).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var responseBody map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
	data, ok := responseBody["data"].(map[string]any)
	require.True(t, ok)
	folderItems, ok := data["folders"].([]any)
	require.True(t, ok)
	require.Len(t, folderItems, 1)
	folder, ok := folderItems[0].(map[string]any)
	require.True(t, ok)
	tagItems, ok := data["tags"].([]any)
	require.True(t, ok)
	require.Len(t, tagItems, 1)
	tag, ok := tagItems[0].(map[string]any)
	require.True(t, ok)
	for _, item := range []map[string]any{folder, tag} {
		require.Len(t, item, 6)
		require.Contains(t, item, "id")
		require.Contains(t, item, "name")
		require.Contains(t, item, "sort_order")
		require.Contains(t, item, "account_count")
		require.Contains(t, item, "created_at")
		require.Contains(t, item, "updated_at")
	}
	require.Equal(t, float64(4), data["total"])
	require.Equal(t, float64(1), data["uncategorized_count"])
}

func TestBulkAccountTaxonomyMapsFilteredTarget(t *testing.T) {
	adminSvc := newAccountTaxonomyHandlerStub()
	adminSvc.bulkResult = &service.BulkAccountTaxonomyResult{MatchedCount: 2, UpdatedCount: 2}
	body, err := json.Marshal(map[string]any{
		"filters":              map[string]any{"platforms": "openai", "folder": "uncategorized", "tags": "3"},
		"expected_match_count": 2,
		"folder_action":        "set", "folder_id": 7,
		"tag_add_ids": []int64{4}, "tag_remove_ids": []int64{3},
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/bulk-taxonomy", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	setupAccountTaxonomyHandlerRouter(adminSvc).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, adminSvc.lastBulk.Filters)
	require.NotNil(t, adminSvc.lastBulk.Filters.Console)
	require.Equal(t, []string{"openai"}, adminSvc.lastBulk.Filters.Console.Platforms)
	require.True(t, adminSvc.lastBulk.Filters.Console.IncludeUncategorized)
	require.Equal(t, []int64{3}, adminSvc.lastBulk.Filters.Console.TagIDs)
	require.Equal(t, "set", adminSvc.lastBulk.FolderAction)
	require.Equal(t, int64(7), *adminSvc.lastBulk.FolderID)
	require.Equal(t, []int64{4}, adminSvc.lastBulk.TagAddIDs)
	require.Equal(t, []int64{3}, adminSvc.lastBulk.TagRemoveIDs)
}
