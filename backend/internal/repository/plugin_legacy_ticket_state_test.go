package repository

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLegacyCodexRoutingImportRetiresStateWithoutGrantingAccess(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for _, length := range []int{292, 312, 332, 356, 780} {
		account := &service.Account{ID: 7, Platform: "openai", Type: "oauth", Credentials: map[string]any{"chatgpt_account_id": "team", "plan_type": "business"}, Extra: map[string]any{"codex_turn_ticket:gpt-6-astra": map[string]any{"state": strings.Repeat("x", length), "expires_at": now.Add(time.Hour)}}}
		for _, phase := range []string{"", "ready", "retry", "stopped", "manual_running"} {
			var legacy *service.CodexTicketLifecycle
			if phase != "" {
				legacy = &service.CodexTicketLifecycle{Phase: phase, JobID: 17}
			}
			request, err := legacyCodexRoutingImport(account, "gpt-6-astra", legacy, now)
			require.NoError(t, err)
			var state map[string]any
			require.NoError(t, json.Unmarshal(request.Value, &state))
			require.NotContains(t, state, "ticket")
			require.NotContains(t, state, "qualification")
			require.NotContains(t, state, "expires_at")
			require.Equal(t, "deny", request.Projection.Scheduling[0].Effect)
			require.Nil(t, request.Projection.Scheduling[0].Until)
			require.Zero(t, request.Projection.Observations[0].Count)
			if phase == "ready" || phase == "retry" {
				require.Equal(t, "needs_cookie_verification", state["phase"])
				require.Equal(t, true, state["enrolled"])
				require.Equal(t, now, *request.NextAt)
			} else {
				require.Equal(t, "stopped", state["phase"])
				require.Equal(t, false, state["enrolled"])
				require.Nil(t, request.NextAt)
			}
			if legacy != nil {
				require.Equal(t, "job-17", state["operation_id"])
			}
		}
	}
}
