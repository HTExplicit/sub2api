//go:build unit

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

type cindyGroupAdminServiceStub struct {
	service.AdminService
	auditResult   *service.CindyGroupAuditResult
	previewResult *service.CindyGroupSplitPreview
	splitResult   *service.CindyGroupSplitResult
	splitErr      error
	previewInput  service.CindyGroupSplitInput
	splitInput    service.CindyGroupSplitInput
}

func (s *cindyGroupAdminServiceStub) AuditCindyGroups(context.Context) (*service.CindyGroupAuditResult, error) {
	return s.auditResult, nil
}

func (s *cindyGroupAdminServiceStub) PreviewCindyGroupSplit(_ context.Context, _ int64, input service.CindyGroupSplitInput) (*service.CindyGroupSplitPreview, error) {
	s.previewInput = input
	return s.previewResult, nil
}

func (s *cindyGroupAdminServiceStub) SplitCindyGroup(_ context.Context, _ int64, input service.CindyGroupSplitInput) (*service.CindyGroupSplitResult, error) {
	s.splitInput = input
	return s.splitResult, s.splitErr
}

func setupCindyGroupRouter(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewGroupHandler(svc, nil, nil)
	router.GET("/api/v1/admin/cindy/groups/audit", handler.AuditCindyGroups)
	router.POST("/api/v1/admin/cindy/groups/:id/split-preview", handler.PreviewCindyGroupSplit)
	router.POST("/api/v1/admin/cindy/groups/:id/split", handler.SplitCindyGroup)
	return router
}

func TestCindyGroupHandlerAuditReturnsAnonymousCounts(t *testing.T) {
	svc := &cindyGroupAdminServiceStub{auditResult: &service.CindyGroupAuditResult{
		Summary: service.CindyGroupAuditSummary{MixedGroups: 1},
		Groups: []service.CindyGroupAuditEntry{{
			GroupID:              7,
			Classification:       service.CindyGroupClassificationMixed,
			CindyAccountCount:    2,
			OrdinaryAccountCount: 1,
		}},
	}}
	recorder := httptest.NewRecorder()
	setupCindyGroupRouter(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/cindy/groups/audit", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "credentials")
	require.NotContains(t, recorder.Body.String(), "account_id")
	require.Contains(t, recorder.Body.String(), `"mixed_groups":1`)
}

func TestCindyGroupHandlerPreviewDefaultsToNoAPIKeyRebind(t *testing.T) {
	svc := &cindyGroupAdminServiceStub{previewResult: &service.CindyGroupSplitPreview{
		SourceGroupID:     7,
		MemberFingerprint: "abc",
	}}
	body := []byte(`{"source_keeps":"cindy","target_name":"Ordinary"}`)
	recorder := httptest.NewRecorder()
	setupCindyGroupRouter(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/cindy/groups/7/split-preview", bytes.NewReader(body)))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, svc.previewInput.APIKeyIDs)
	require.Equal(t, "cindy", svc.previewInput.SourceKeeps)
	require.Equal(t, "Ordinary", svc.previewInput.TargetName)
}

func TestCindyGroupHandlerSplitPropagatesFingerprintDriftAsConflict(t *testing.T) {
	svc := &cindyGroupAdminServiceStub{splitErr: service.ErrCindyGroupSplitDrift}
	body := []byte(`{
		"source_keeps":"ordinary",
		"target_name":"Cindy",
		"api_key_ids":[11],
		"member_fingerprint":"b55e34d35e40f425b885f8293c7f0c8a9f61f505fa0f2a1258a6c68f3d63998a"
	}`)
	recorder := httptest.NewRecorder()
	setupCindyGroupRouter(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/cindy/groups/7/split", bytes.NewReader(body)))

	require.Equal(t, http.StatusConflict, recorder.Code)
	var envelope struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, "CINDY_GROUP_SPLIT_DRIFT", envelope.Reason)
	require.Equal(t, []int64{11}, svc.splitInput.APIKeyIDs)
}

func TestCindyGroupHandlerRejectsInvalidGroupID(t *testing.T) {
	recorder := httptest.NewRecorder()
	setupCindyGroupRouter(&cindyGroupAdminServiceStub{}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/cindy/groups/0/split-preview", bytes.NewReader([]byte(`{}`))),
	)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
