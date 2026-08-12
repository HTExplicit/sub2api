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

func setupCindyAccountHandlerRouter(adminSvc *cindyAdminServiceStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts/cindy/insufficient-delete-preview", handler.PreviewCindyInsufficientDeletion)
	router.POST("/api/v1/admin/accounts/cindy/delete-insufficient", handler.DeleteCindyInsufficient)
	return router
}

func TestCindyInsufficientDeletePreviewAndDeleteDoNotAcceptAccountIDs(t *testing.T) {
	adminSvc := &cindyAdminServiceStub{
		stubAdminService: newStubAdminService(),
		preview:          &service.CindyInsufficientDeletePreview{Count: 2, Fingerprint: "abc123"},
		deleteResult:     &service.CindyInsufficientDeleteResult{DeletedCount: 2},
	}
	router := setupCindyAccountHandlerRouter(adminSvc)

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
	router.ServeHTTP(deleteRecorder, request)

	require.Equal(t, http.StatusOK, deleteRecorder.Code, deleteRecorder.Body.String())
	require.Equal(t, 1, adminSvc.deleteInvocationCnt)
	require.Equal(t, 2, adminSvc.lastExpectedCount)
	require.Equal(t, "abc123", adminSvc.lastFingerprint)
	require.NotContains(t, deleteRecorder.Body.String(), "account_ids")
}

func TestCindyInsufficientDeleteReturnsConflictWhenCandidateSetChanged(t *testing.T) {
	adminSvc := &cindyAdminServiceStub{
		stubAdminService: newStubAdminService(),
		deleteErr:        service.ErrCindyInsufficientDeleteChanged,
	}
	body := bytes.NewBufferString(`{"expected_count":1,"fingerprint":"stale"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/cindy/delete-insufficient", body)
	request.Header.Set("Content-Type", "application/json")
	setupCindyAccountHandlerRouter(adminSvc).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
}
