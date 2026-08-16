package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cindyBalanceProbeListRepositoryStub struct {
	service.CindyBalanceProbeRepository
	limit int
}

type cindyBalanceProbeCreateRepositoryStub struct {
	service.CindyBalanceProbeRepository
	createCalls int
}

type cindyBalanceProbeRoundTripRepositoryStub struct {
	service.CindyBalanceProbeRepository
	createdScope service.CindyBalanceProbeScope
}

func (s *cindyBalanceProbeRoundTripRepositoryStub) Preview(
	_ context.Context,
	scope service.CindyBalanceProbeScope,
	rateRPS float64,
) (*service.CindyBalanceProbePreview, error) {
	return &service.CindyBalanceProbePreview{
		Scope: scope, CandidateCount: 2, CandidateFingerprint: strings.Repeat("b", 64),
		MinimumCalls: 2, MaximumCalls: 4, RateRPS: rateRPS,
	}, nil
}

func (s *cindyBalanceProbeRoundTripRepositoryStub) CreateJob(
	_ context.Context,
	_ *int64,
	scope service.CindyBalanceProbeScope,
	rateRPS float64,
	expectedCount int,
	expectedFingerprint string,
) (*service.CindyBalanceProbeJob, error) {
	s.createdScope = scope
	return &service.CindyBalanceProbeJob{
		ID: 31, Status: "queued", Scope: scope, RateRPS: rateRPS,
		CandidateCount: expectedCount, CandidateFingerprint: expectedFingerprint,
	}, nil
}

func (s *cindyBalanceProbeCreateRepositoryStub) CreateJob(
	context.Context,
	*int64,
	service.CindyBalanceProbeScope,
	float64,
	int,
	string,
) (*service.CindyBalanceProbeJob, error) {
	s.createCalls++
	return nil, service.ErrCindyBalanceProbeChanged
}

func (s *cindyBalanceProbeListRepositoryStub) ListJobs(_ context.Context, limit int) (*service.CindyBalanceProbeJobList, error) {
	s.limit = limit
	return &service.CindyBalanceProbeJobList{
		Items: []service.CindyBalanceProbeJob{{ID: 17, Status: "running"}},
		Total: 1,
	}, nil
}

func TestCindyBalanceProbeHandlerListUsesDefaultLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cindyBalanceProbeListRepositoryStub{}
	probeService := service.NewCindyBalanceProbeService(repo, nil, nil, nil)
	t.Cleanup(probeService.Stop)
	handler := NewCindyBalanceProbeHandler(probeService)
	router := gin.New()
	router.GET("/admin/cindy/balance-probe-jobs", handler.List)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/cindy/balance-probe-jobs", nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 10, repo.limit)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"items":[{"id":17,"status":"running","scope":{"mode":"","filters":{}},"rate_rps":0,"candidate_count":0,"candidate_fingerprint":"","request_count":0,"consecutive_upstream_failures":0,"created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","counts":{"pending":0,"running":0,"healthy":0,"recovered":0,"exhausted":0,"inconclusive":0,"skipped":0}}],"total":1}}`, recorder.Body.String())
}

func TestCindyBalanceProbeHandlerCreateReturnsConflictOnTransactionalCandidateDrift(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cindyBalanceProbeCreateRepositoryStub{}
	probeService := service.NewCindyBalanceProbeService(repo, nil, nil, nil)
	t.Cleanup(probeService.Stop)
	handler := NewCindyBalanceProbeHandler(probeService)
	router := gin.New()
	router.POST("/admin/cindy/balance-probe-jobs", func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 77})
		handler.Create(c)
	})

	body := `{"scope":{"mode":"selected","account_ids":[17]},"rate_rps":0.5,"expected_count":1,"candidate_fingerprint":"` + strings.Repeat("a", 64) + `"}`
	recorder := httptest.NewRecorder()
	router.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/admin/cindy/balance-probe-jobs", bytes.NewBufferString(body)),
	)

	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "CINDY_BALANCE_PROBE_CANDIDATES_CHANGED")
	require.Equal(t, 1, repo.createCalls)
}

func TestCindyBalanceProbeHandlerSelectedPreviewCreateRoundTripCanonicalizesLegacyScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cindyBalanceProbeRoundTripRepositoryStub{}
	probeService := service.NewCindyBalanceProbeService(repo, nil, nil, nil)
	t.Cleanup(probeService.Stop)
	handler := NewCindyBalanceProbeHandler(probeService)
	router := gin.New()
	router.POST("/admin/cindy/balance-probe-jobs/preview", handler.Preview)
	router.POST("/admin/cindy/balance-probe-jobs", func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 77})
		handler.Create(c)
	})

	previewRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		previewRecorder,
		httptest.NewRequest(
			http.MethodPost,
			"/admin/cindy/balance-probe-jobs/preview",
			bytes.NewBufferString(`{"scope":{"mode":"selected","filters":{"account_ids":[19,17,19]}} ,"rate_rps":0.5}`),
		),
	)
	require.Equal(t, http.StatusOK, previewRecorder.Code, previewRecorder.Body.String())

	var previewEnvelope struct {
		Data service.CindyBalanceProbePreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(previewRecorder.Body.Bytes(), &previewEnvelope))
	require.Equal(t, []int64{17, 19}, previewEnvelope.Data.Scope.AccountIDs)
	require.Empty(t, previewEnvelope.Data.Scope.Filters.AccountIDs)

	createBody, err := json.Marshal(map[string]any{
		"scope":                 previewEnvelope.Data.Scope,
		"rate_rps":              previewEnvelope.Data.RateRPS,
		"expected_count":        previewEnvelope.Data.CandidateCount,
		"candidate_fingerprint": previewEnvelope.Data.CandidateFingerprint,
	})
	require.NoError(t, err)
	createRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		createRecorder,
		httptest.NewRequest(http.MethodPost, "/admin/cindy/balance-probe-jobs", bytes.NewReader(createBody)),
	)
	require.Equal(t, http.StatusCreated, createRecorder.Code, createRecorder.Body.String())
	require.Equal(t, []int64{17, 19}, repo.createdScope.AccountIDs)
	require.Empty(t, repo.createdScope.Filters.AccountIDs)
}
