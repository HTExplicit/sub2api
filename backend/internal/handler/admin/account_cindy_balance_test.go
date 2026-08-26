package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cindyAdminServiceStub struct {
	*stubAdminService
	preview             *service.CindyInsufficientDeletePreview
	deleteResult        *service.CindyInsufficientDeleteResult
	deleteErr           error
	lastExpectedCount   int
	lastFingerprint     string
	deleteInvocationCnt int
	bannedPreview       *service.CindyInsufficientDeletePreview
	bannedDeleteResult  *service.CindyInsufficientDeleteResult
	bannedDeleteErr     error
}

func (s *cindyAdminServiceStub) PreviewCindyInsufficientDeletion(context.Context) (*service.CindyInsufficientDeletePreview, error) {
	return s.preview, nil
}

func (s *cindyAdminServiceStub) DeleteCindyInsufficient(_ context.Context, expectedCount int, fingerprint string) (*service.CindyInsufficientDeleteResult, error) {
	s.deleteInvocationCnt++
	s.lastExpectedCount = expectedCount
	s.lastFingerprint = fingerprint
	return s.deleteResult, s.deleteErr
}

func (s *cindyAdminServiceStub) PreviewCindyBannedDeletion(context.Context) (*service.CindyInsufficientDeletePreview, error) {
	return s.bannedPreview, nil
}

func (s *cindyAdminServiceStub) DeleteCindyBanned(context.Context, int, string) (*service.CindyInsufficientDeleteResult, error) {
	return s.bannedDeleteResult, s.bannedDeleteErr
}

func setupCindyAccountHandlerRouter(adminSvc *cindyAdminServiceStub) (*gin.Engine, *AccountHandler, *accountJobSubmitRepository) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	jobs := attachAccountJobSubmitter(router, handler)
	router.GET("/api/v1/admin/accounts/cindy/insufficient-delete-preview", handler.PreviewCindyInsufficientDeletion)
	router.POST("/api/v1/admin/accounts/cindy/delete-insufficient", handler.DeleteCindyInsufficient)
	router.GET("/api/v1/admin/accounts/cindy/banned-delete-preview", handler.PreviewCindyBannedDeletion)
	router.POST("/api/v1/admin/accounts/cindy/delete-banned", handler.DeleteCindyBanned)
	return router, handler, jobs
}

func TestCindyBannedCleanupUsesSeparateJobWithoutClientAccountIDs(t *testing.T) {
	adminSvc := &cindyAdminServiceStub{
		stubAdminService:   newStubAdminService(),
		bannedPreview:      &service.CindyInsufficientDeletePreview{Count: 1, Fingerprint: "banned-fingerprint"},
		bannedDeleteResult: &service.CindyInsufficientDeleteResult{DeletedCount: 1},
	}
	router, handler, jobs := setupCindyAccountHandlerRouter(adminSvc)

	preview := httptest.NewRecorder()
	router.ServeHTTP(preview, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/cindy/banned-delete-preview", nil))
	require.Equal(t, http.StatusOK, preview.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/cindy/delete-banned",
		bytes.NewBufferString(`{"expected_count":1,"fingerprint":"banned-fingerprint","account_ids":[999]}`))
	request.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(request)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	var payload map[string]any
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindCindyBannedCleanup, &payload)
	require.NotContains(t, params.PayloadCipher, "account_ids")
	results := executeSubmittedAccountJobItems(handler, params)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
}

func TestCindyInsufficientDeletePreviewAndDeleteDoNotAcceptAccountIDs(t *testing.T) {
	adminSvc := &cindyAdminServiceStub{
		stubAdminService: newStubAdminService(),
		preview:          &service.CindyInsufficientDeletePreview{Count: 2, Fingerprint: "abc123"},
		deleteResult:     &service.CindyInsufficientDeleteResult{DeletedCount: 2},
	}
	router, handler, jobs := setupCindyAccountHandlerRouter(adminSvc)

	previewRecorder := httptest.NewRecorder()
	router.ServeHTTP(previewRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/cindy/insufficient-delete-preview", nil))
	require.Equal(t, http.StatusOK, previewRecorder.Code, previewRecorder.Body.String())
	require.JSONEq(t, `{"code":0,"message":"success","data":{"count":2,"fingerprint":"abc123"}}`, previewRecorder.Body.String())

	body, err := json.Marshal(map[string]any{
		"expected_count": 2,
		"fingerprint":    "abc123",
		"account_ids":    []int64{1, 2, 999999},
	})
	require.NoError(t, err)
	deleteRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/cindy/delete-insufficient", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(request)
	router.ServeHTTP(deleteRecorder, request)

	require.Equal(t, http.StatusAccepted, deleteRecorder.Code, deleteRecorder.Body.String())
	var payload struct {
		ExpectedCount int    `json:"expected_count"`
		Fingerprint   string `json:"fingerprint"`
	}
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindCindyConfirmedCleanup, &payload)
	require.Equal(t, 2, payload.ExpectedCount)
	require.Equal(t, "abc123", payload.Fingerprint)
	require.NotContains(t, params.PayloadCipher, "account_ids")
	require.Zero(t, adminSvc.deleteInvocationCnt, "HTTP submission must not perform cleanup synchronously")
	results := executeSubmittedAccountJobItems(handler, params)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
	require.Equal(t, 1, adminSvc.deleteInvocationCnt)
	require.Equal(t, 2, adminSvc.lastExpectedCount)
	require.Equal(t, "abc123", adminSvc.lastFingerprint)
}

func TestCindyInsufficientDeleteJobFailsWhenCandidateSetChanged(t *testing.T) {
	adminSvc := &cindyAdminServiceStub{
		stubAdminService: newStubAdminService(),
		deleteErr:        service.ErrCindyInsufficientDeleteChanged,
	}
	body := bytes.NewBufferString(`{"expected_count":1,"fingerprint":"stale"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/cindy/delete-insufficient", body)
	request.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(request)
	router, handler, jobs := setupCindyAccountHandlerRouter(adminSvc)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
	var payload map[string]any
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindCindyConfirmedCleanup, &payload)
	results := executeSubmittedAccountJobItems(handler, params)
	require.Equal(t, service.AccountJobItemStatusFailed, results[0].Status)
	require.Equal(t, "cindy_cleanup_target_changed", results[0].ErrorCode)
}
