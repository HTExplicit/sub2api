package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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
