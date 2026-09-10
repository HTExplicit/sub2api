package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type capabilityCatalogReaderStub struct {
	overviewFilter  service.AccountCapabilityOverviewFilter
	candidateFilter service.AccountCapabilityCandidateFilter
	planRequest     service.AccountCapabilityRecommendationRequest
	reads           int
	err             error
}

func (s *capabilityCatalogReaderStub) Candidates(_ context.Context, filter service.AccountCapabilityCandidateFilter) (*service.AccountCapabilityCandidatePage, error) {
	s.reads++
	s.candidateFilter = filter
	return &service.AccountCapabilityCandidatePage{Items: []service.AccountCapabilityCandidate{}, Page: filter.Page, PageSize: filter.PageSize}, s.err
}

func (s *capabilityCatalogReaderStub) Overview(_ context.Context, filter service.AccountCapabilityOverviewFilter) (*service.AccountCapabilityOverview, error) {
	s.reads++
	s.overviewFilter = filter
	return &service.AccountCapabilityOverview{Scope: service.CapabilityPublicationScope{FolderIDs: []int64{7, 8}, AccountIDs: []int64{31}}}, s.err
}

func (s *capabilityCatalogReaderStub) Recommend(_ context.Context, request service.AccountCapabilityRecommendationRequest) (*service.AccountCapabilityRecommendation, error) {
	s.reads++
	s.planRequest = request
	return &service.AccountCapabilityRecommendation{Scope: request.Scope, MaximumRequestCount: 0}, s.err
}

func capabilityCatalogTestRouter(reader *capabilityCatalogReaderStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &AccountCapabilityCatalogHandler{catalog: reader}
	router.GET("/overview", h.Overview)
	router.GET("/candidates", h.Candidates)
	router.POST("/plan", h.Plan)
	return router
}

func TestAccountCapabilityCatalogHandlerAdvancedCandidatesKeepGroupContext(t *testing.T) {
	reader := &capabilityCatalogReaderStub{}
	router := capabilityCatalogTestRouter(reader)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/candidates?folder_ids=7,8&account_ids=31&group_ids=23&page=2&page_size=10", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, service.AccountCapabilityCandidateFilter{FolderIDs: []int64{7, 8}, AccountIDs: []int64{31}, GroupIDs: []int64{23}, Page: 2, PageSize: 10}, reader.candidateFilter)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/candidates?folder_ids=7&group_ids=bad", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, 1, reader.reads)
}

func TestAccountCapabilityCatalogHandlerOverviewPreservesContextAndIgnoresPagination(t *testing.T) {
	reader := &capabilityCatalogReaderStub{}
	router := capabilityCatalogTestRouter(reader)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/overview?folder_ids=7,8,7&account_ids=31&group_ids=23&search=fable&page=999&page_size=1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, service.AccountCapabilityOverviewFilter{FolderIDs: []int64{7, 8}, AccountIDs: []int64{31}, GroupIDs: []int64{23}, Search: "fable"}, reader.overviewFilter)
	require.Contains(t, w.Body.String(), `"account_ids":[31]`)
	// Account-only deep links reach the service for exact folder resolution;
	// the handler must not intersect them with invented default folder IDs.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/overview?account_ids=31", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, reader.overviewFilter.FolderIDs)
	require.Equal(t, []int64{31}, reader.overviewFilter.AccountIDs)
}

func TestAccountCapabilityCatalogHandlerPlanIsReadOnlyAndKeepsSelection(t *testing.T) {
	reader := &capabilityCatalogReaderStub{}
	router := capabilityCatalogTestRouter(reader)
	w := httptest.NewRecorder()
	body := `{"scope":{"folder_ids":[7,8],"account_ids":[31]},"group_ids":[23],"models":[{"group_id":23,"public_model":"claude-fable-5"}],"mainstream_only":false}`
	request := httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, request)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, reader.reads)
	require.Equal(t, []int64{31}, reader.planRequest.Scope.AccountIDs)
	require.Equal(t, []int64{23}, reader.planRequest.GroupIDs)
	require.Equal(t, "claude-fable-5", reader.planRequest.Models[0].PublicModel)
	require.NotNil(t, reader.planRequest.MainstreamOnly)
	require.False(t, *reader.planRequest.MainstreamOnly)
	require.Contains(t, w.Body.String(), `"preview_request":null`)
	require.Contains(t, w.Body.String(), `"probe_request":null`)
	require.Contains(t, w.Body.String(), `"maximum_request_count":0`)
}

func TestAccountCapabilityCatalogHandlerRejectsInvalidContextAndSanitizesErrors(t *testing.T) {
	reader := &capabilityCatalogReaderStub{}
	router := capabilityCatalogTestRouter(reader)
	for _, query := range []string{"folder_ids=7,nope", "account_ids=-1", "group_ids=0"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/overview?"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	require.Zero(t, reader.reads)
	for _, test := range []struct {
		err    error
		status int
	}{
		{service.ErrAccountCapabilityScope, http.StatusBadRequest},
		{service.ErrAccountCapabilityConflict, http.StatusConflict},
		{errors.New("internal-secret-fixture-must-not-appear"), http.StatusInternalServerError},
	} {
		reader.err = test.err
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/overview", nil))
		require.Equal(t, test.status, w.Code)
		require.NotContains(t, w.Body.String(), "internal-secret-fixture-must-not-appear")
	}
}
