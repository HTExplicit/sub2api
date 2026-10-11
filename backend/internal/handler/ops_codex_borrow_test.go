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

func TestOpsCodexBorrowPreparationUsesSelectedNode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stage        string
		verification *service.CodexGatewayBorrowVerification
		proxyID      int64
		proxyName    string
	}{
		{"legacy binding", "target_validation", nil, 47, "saved binding"},
		{"exhausted pool retains last probe", "node_selection", &service.CodexGatewayBorrowVerification{ProxyID: 43, ProxyName: "selected node", NodeIdentity: "selected-identity"}, 43, "selected node"},
		{"explicit direct", "target_validation", &service.CodexGatewayBorrowVerification{ProxyName: "Server direct", NodeIdentity: "direct-identity"}, 0, "direct/no_proxy"},
		{"no selected node", "node_selection", nil, 0, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			binding := int64(47)
			account := &service.Account{ID: 2, Platform: "openai", ProxyID: &binding, Proxy: &service.Proxy{ID: 47, Name: "saved binding"}}
			failure := &service.CodexGatewayBorrowFailure{Cause: service.ErrCodexGatewayBorrowUnavailable, Stage: tc.stage, Reason: "node_candidates_exhausted", Verification: tc.verification}
			service.RecordCodexGatewayBorrowPreparationFailure(c, account, failure)
			entry := &service.OpsInsertErrorLogInput{}
			applyOpsUpstreamFieldsFromContext(c, entry)
			require.Len(t, entry.UpstreamErrors, 1)
			event := entry.UpstreamErrors[0]
			if tc.proxyID == 0 {
				require.Nil(t, event.ProxyID)
			} else {
				require.NotNil(t, event.ProxyID)
				require.Equal(t, tc.proxyID, *event.ProxyID)
			}
			require.Equal(t, tc.proxyName, event.ProxyName)
			require.NotNil(t, entry.UpstreamStatusCode)
			require.Zero(t, *entry.UpstreamStatusCode, "local qualification failure is not a business upstream 503")
			require.Equal(t, int64(47), *account.ProxyID, "diagnostics cannot rewrite account bindings")
		})
	}
}
