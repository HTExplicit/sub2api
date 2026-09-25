package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRetiredPromptManagementHasNoExecutionPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/system-prompts/skill-registry/syncs", nil)
	NewSystemPromptHandler(nil).Retired(ctx)
	require.Equal(t, http.StatusGone, recorder.Code)
	require.Equal(t, "system_prompt_management_retired", gjson.Get(recorder.Body.String(), "reason").String())
}

func TestWriteBusinessSystemPromptErrorUsesStableProtocolCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, testCase := range map[string]struct {
		err         error
		wantStatus  int
		wantReason  string
		wantMessage string
	}{
		"unsupported rule delivery": {err: service.ErrPromptDeliveryUnsupported, wantStatus: http.StatusUnprocessableEntity, wantReason: "prompt_delivery_unsupported", wantMessage: "The selected prompt delivery or position is unsupported by this destination"},
		"referenced rule":           {err: service.ErrPromptRuleReferenced, wantStatus: http.StatusConflict, wantReason: "prompt_rule_referenced", wantMessage: "An account still references this rule"},
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
			message := testCase.wantMessage
			if message == "" {
				message = testCase.wantReason
			}
			require.Equal(t, message, gjson.Get(recorder.Body.String(), "message").String())
		})
	}
}
