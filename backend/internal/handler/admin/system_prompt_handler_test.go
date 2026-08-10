package admin

import (
	"bytes"
	"context"
	"errors"
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

func TestStartSkillSyncPassesRequestedSourceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &serviceTestRemoteSkillStore{
		snapshot: service.RemoteSkillRegistrySnapshot{Revision: 7},
		job:      service.RemoteSkillSyncJob{ID: 9, Status: service.RemoteSkillSyncStatusQueued},
	}
	registry := service.NewRemoteSkillRegistryService(store, nil, &serviceTestRemoteSkillFiles{}, &serviceTestRemoteSkillSource{})
	require.NoError(t, registry.Start(context.Background()))
	t.Cleanup(registry.Stop)
	handler := NewSystemPromptHandler(nil, registry)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/system-prompts/skill-registry/sync", bytes.NewBufferString(`{"source_id":"moxinggang","expected_revision":7}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	handler.StartSkillSync(ctx)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, service.RemoteSkillSourceMoxinggang, store.sourceID)
	require.Equal(t, int64(42), store.actorID)
	require.Equal(t, int64(7), store.expectedRevision)
}

type serviceTestRemoteSkillStore struct {
	snapshot         service.RemoteSkillRegistrySnapshot
	job              service.RemoteSkillSyncJob
	sourceID         string
	actorID          int64
	expectedRevision int64
}

func (s *serviceTestRemoteSkillStore) EnsureRemoteSkillSeed(context.Context, service.RemoteSkillBundleVersion) error {
	return nil
}
func (s *serviceTestRemoteSkillStore) LoadRemoteSkillSnapshot(context.Context) (service.RemoteSkillRegistrySnapshot, error) {
	return s.snapshot, nil
}
func (s *serviceTestRemoteSkillStore) ListRemoteSkillVersions(context.Context) ([]service.RemoteSkillBundleVersion, error) {
	return nil, nil
}
func (s *serviceTestRemoteSkillStore) GetRemoteSkillVersion(context.Context, int64) (service.RemoteSkillBundleVersion, error) {
	return service.RemoteSkillBundleVersion{}, nil
}
func (s *serviceTestRemoteSkillStore) CreateRemoteSkillSyncJob(_ context.Context, sourceID string, actorID, expectedRevision int64) (service.RemoteSkillSyncJob, error) {
	s.sourceID, s.actorID, s.expectedRevision = sourceID, actorID, expectedRevision
	s.job.SourceID = sourceID
	return s.job, nil
}
func (s *serviceTestRemoteSkillStore) UpdateRemoteSkillSyncJobStage(context.Context, int64, string) error {
	return nil
}
func (s *serviceTestRemoteSkillStore) CompleteRemoteSkillSyncJob(context.Context, int64, service.RemoteSkillBundleVersion) (service.RemoteSkillSyncJob, error) {
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

type serviceTestRemoteSkillFiles struct{}

func (*serviceTestRemoteSkillFiles) LoadSeed(context.Context) (service.RemoteSkillBundleVersion, error) {
	return service.RemoteSkillBundleVersion{}, service.ErrRemoteSkillSeedUnavailable
}
func (*serviceTestRemoteSkillFiles) InstallCandidate(context.Context, service.RemoteSkillCandidate) error {
	return nil
}
func (*serviceTestRemoteSkillFiles) ValidateVersion(context.Context, service.RemoteSkillBundleVersion) error {
	return nil
}
func (*serviceTestRemoteSkillFiles) PreparePublic(context.Context, service.RemoteSkillBundleVersion) error {
	return nil
}
func (*serviceTestRemoteSkillFiles) Activate(context.Context, service.RemoteSkillRegistrySnapshot) error {
	return nil
}
func (*serviceTestRemoteSkillFiles) LoadManifest(context.Context, service.RemoteSkillBundleVersion) (service.BusinessSystemPromptBundleManifest, error) {
	return service.BusinessSystemPromptBundleManifest{}, nil
}
func (*serviceTestRemoteSkillFiles) LoadBundle(context.Context, service.RemoteSkillBundleVersion) (*service.BusinessSystemPromptBundle, error) {
	return nil, nil
}

type serviceTestRemoteSkillSource struct{}

func (*serviceTestRemoteSkillSource) Build(context.Context, string, *service.BusinessSystemPromptBundleManifest) (service.RemoteSkillCandidate, error) {
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
