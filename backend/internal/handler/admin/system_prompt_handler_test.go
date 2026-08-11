package admin

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStartSkillSyncAcceptsMultipartPromptCaptureForFixedSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	files := service.NewRemoteSkillRegistryFilesystem(t.TempDir())
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	store := &serviceTestRemoteSkillStore{job: service.RemoteSkillSyncJob{ID: 9, Status: service.RemoteSkillSyncStatusQueued}}
	registry := service.NewRemoteSkillRegistryService(store, nil, files, &serviceTestRemoteSkillSource{})
	require.NoError(t, registry.Start(context.Background()))
	t.Cleanup(registry.Stop)
	handler := NewSystemPromptHandler(nil, registry)

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	require.NoError(t, writer.WriteField("expected_revision", "7"))
	part, err := writer.CreateFormFile("prompt_capture", "capture.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte(seed.Prompt.RawBody))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/system-prompts/skill-registry/syncs", &requestBody)
	ctx.Request.Header.Set("Content-Type", writer.FormDataContentType())
	ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	handler.StartSkillSync(ctx)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, int64(42), store.actorID)
	require.Equal(t, int64(7), store.expectedRevision)
	require.True(t, store.promptProvided)
}

type serviceTestRemoteSkillStore struct {
	snapshot         service.RemoteSkillRegistrySnapshot
	detail           service.RemoteSkillBundleVersionDetail
	job              service.RemoteSkillSyncJob
	actorID          int64
	expectedRevision int64
	promptProvided   bool
}

func (s *serviceTestRemoteSkillStore) EnsureRemoteSkillSeed(_ context.Context, candidate service.RemoteSkillCandidate) (service.RemoteSkillRegistrySnapshot, error) {
	candidate.Version.ID = 1
	candidate.Prompt.ID = 1
	candidate.Version.PromptVersionID = 1
	s.detail = service.RemoteSkillBundleVersionDetail{
		RemoteSkillBundleVersion: candidate.Version,
		Prompt:                   candidate.Prompt,
		FileChanges:              candidate.FileChanges,
	}
	s.snapshot = service.RemoteSkillRegistrySnapshot{
		Revision: 7, Active: &candidate.Version, ActivePrompt: &candidate.Prompt, UpdatedAt: time.Now().UTC(),
	}
	return s.snapshot, nil
}
func (s *serviceTestRemoteSkillStore) LoadRemoteSkillSnapshot(context.Context) (service.RemoteSkillRegistrySnapshot, error) {
	return s.snapshot, nil
}
func (s *serviceTestRemoteSkillStore) ListRemoteSkillVersions(context.Context) ([]service.RemoteSkillBundleVersion, error) {
	return nil, nil
}
func (s *serviceTestRemoteSkillStore) GetRemoteSkillVersion(context.Context, int64) (service.RemoteSkillBundleVersionDetail, error) {
	return s.detail, nil
}
func (s *serviceTestRemoteSkillStore) CreateRemoteSkillSyncJob(_ context.Context, actorID, expectedRevision int64, promptProvided bool) (service.RemoteSkillSyncJob, error) {
	s.actorID, s.expectedRevision, s.promptProvided = actorID, expectedRevision, promptProvided
	return s.job, nil
}
func (s *serviceTestRemoteSkillStore) UpdateRemoteSkillSyncJobStage(context.Context, int64, string) error {
	return nil
}
func (s *serviceTestRemoteSkillStore) CompleteRemoteSkillSyncJob(context.Context, int64, service.RemoteSkillCandidate) (service.RemoteSkillSyncJob, error) {
	return s.job, nil
}
func (s *serviceTestRemoteSkillStore) FailRemoteSkillSyncJob(context.Context, int64, string) error {
	return nil
}
func (s *serviceTestRemoteSkillStore) GetRemoteSkillSyncJob(context.Context, int64) (service.RemoteSkillSyncJob, error) {
	return s.job, nil
}
func (s *serviceTestRemoteSkillStore) PublishRemoteSkillVersion(context.Context, int64, int64, int64) (service.RemoteSkillRegistrySnapshot, error) {
	return s.snapshot, nil
}
func (s *serviceTestRemoteSkillStore) CleanupLegacyRemoteSkillData(context.Context) error {
	return nil
}

type serviceTestRemoteSkillSource struct{}

func (*serviceTestRemoteSkillSource) Build(context.Context, service.RemoteSkillPromptCapture, *service.RemoteSkillCandidate) (service.RemoteSkillCandidate, error) {
	return service.RemoteSkillCandidate{}, errors.New("stop after handler contract")
}

func TestWriteBusinessSystemPromptErrorUsesStableProtocolCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, testCase := range map[string]struct {
		err        error
		wantStatus int
		wantReason string
	}{
		"revision conflict": {
			err: service.ErrBusinessSystemPromptRevisionConflict, wantStatus: http.StatusConflict,
			wantReason: "system_prompt_revision_conflict",
		},
		"unavailable": {
			err: service.ErrBusinessSystemPromptUnavailable, wantStatus: http.StatusServiceUnavailable,
			wantReason: "system_prompt_unavailable",
		},
		"source unavailable": {
			err: service.ErrBusinessSystemPromptSourceUnavailable, wantStatus: http.StatusServiceUnavailable,
			wantReason: "system_prompt_source_unavailable",
		},
		"source invalid": {
			err: service.ErrBusinessSystemPromptSourceInvalid, wantStatus: http.StatusUnprocessableEntity,
			wantReason: "system_prompt_source_invalid",
		},
		"license changed": {
			err: service.ErrBusinessSystemPromptSourceLicenseChanged, wantStatus: http.StatusUnprocessableEntity,
			wantReason: "system_prompt_source_license_changed",
		},
		"source not managed": {
			err: service.ErrBusinessSystemPromptSourceNotManaged, wantStatus: http.StatusConflict,
			wantReason: "system_prompt_source_not_managed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/system-prompts", nil)
			writeBusinessSystemPromptError(ctx, testCase.err)
			require.Equal(t, testCase.wantStatus, recorder.Code)
			require.Equal(t, testCase.wantReason, gjson.Get(recorder.Body.String(), "reason").String())
			require.Equal(t, testCase.wantReason, gjson.Get(recorder.Body.String(), "message").String())
		})
	}
}

func TestBusinessSystemPromptRuntimeResponseIncludesActiveVersionAndStatus(t *testing.T) {
	updatedAt := time.Date(2026, 8, 6, 5, 4, 3, 0, time.UTC)
	got := businessSystemPromptRuntimeResponse(service.BusinessSystemPromptSnapshot{
		Enabled: true, ExposeServerPrompt: false, CompactEnabled: true,
		TemplateID: 11, VersionID: 22, TemplateVersion: 3, Revision: 9,
		SHA256: "ABCDEF", ByteLength: 123, Degraded: true, UpdatedAt: updatedAt,
	})
	require.Equal(t, int64(3), got.TemplateVersion)
	require.Equal(t, int64(9), got.Revision)
	require.Equal(t, "abcdef", got.SHA256)
	require.Equal(t, 123, got.ByteLength)
	require.True(t, got.Degraded)
	require.Equal(t, updatedAt, got.UpdatedAt)
}

func TestSelectBusinessSystemPromptVersionUsesLatestOrExplicitID(t *testing.T) {
	detail := service.BusinessSystemPromptTemplateDetail{Versions: []service.BusinessSystemPromptVersion{
		{ID: 8, Version: 2, Body: "latest"},
		{ID: 4, Version: 1, Body: "old"},
	}}
	latest, err := selectVersion(detail, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), latest.Version)
	explicit, err := selectVersion(detail, 4)
	require.NoError(t, err)
	require.Equal(t, "old", explicit.Body)
	_, err = selectVersion(detail, 99)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptVersionNotFound)
}
