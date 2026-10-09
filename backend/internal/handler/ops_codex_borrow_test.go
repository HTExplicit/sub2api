package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsCodexBorrowPreparationIsNotAnUpstream503(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	err := &service.CodexGatewayBorrowFailure{Cause: service.ErrCodexGatewayBorrowUnavailable, Stage: "target_validation", Reason: "target_state_changed", Detail: "full diagnostic detail"}
	service.RecordCodexGatewayBorrowPreparationFailure(c, &service.Account{ID: 2, Platform: "openai"}, err)
	phase, limited, owner, source := classifyOpsErrorLog(c, "upstream_error", "no qualified Codex gateway borrow route is available", "CODEX_GATEWAY_BORROW_UNAVAILABLE", 503)
	require.Equal(t, "routing", phase)
	require.False(t, limited, "a platform failure still counts as a failed client request")
	require.Equal(t, "platform", owner)
	require.Equal(t, "gateway", source)
	entry := &service.OpsInsertErrorLogInput{}
	applyOpsUpstreamFieldsFromContext(c, entry)
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Zero(t, *entry.UpstreamStatusCode)
	require.Len(t, entry.UpstreamErrors, 1)
	require.Equal(t, "target_state_changed", entry.UpstreamErrors[0].Reason)
	require.Contains(t, *entry.UpstreamErrorDetail, "full diagnostic detail")
}
