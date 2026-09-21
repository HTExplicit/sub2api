package service

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestPluginRequestInjectionUsesActualAccountRollout(t *testing.T) {
	for _, percent := range []int{0, 50, 100} {
		t.Run(strconv.Itoa(percent), func(t *testing.T) {
			var calls []extensionv1.Invocation
			manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
				calls = append(calls, in)
				return extensionv1.Result{Payload: []byte(`{"headers":{"X-Scoped-Policy":"applied"}}`)}, nil
			})
			for index := range manager.extensions.Load().installations[1].Bindings {
				manager.extensions.Load().installations[1].Bindings[index].RolloutPercent = percent
			}
			for _, id := range []int64{2, 3} {
				before := len(calls)
				headers := http.Header{"Authorization": {"Bearer synthetic-host-token"}}
				require.NoError(t, manager.ApplyRequestHeaders(context.Background(), ticketTestAccount(id), "gpt-6-astra", headers))
				require.Equal(t, "Bearer synthetic-host-token", headers.Get("Authorization"))
				if int(stablePluginBucket(id)) < percent {
					require.Len(t, calls, before+1)
					require.Equal(t, id, calls[before].AccountID, "payload metadata does not replace the invocation scope")
					require.Equal(t, "applied", headers.Get("X-Scoped-Policy"))
				} else {
					require.Len(t, calls, before, "outside-rollout account must not enter the request policy")
					require.Empty(t, headers.Get("X-Scoped-Policy"))
				}
			}
		})
	}
}

func TestPluginSchedulingUsesTheSameAccountRolloutAsRequestInjection(t *testing.T) {
	for _, percent := range []int{0, 50, 100} {
		t.Run(strconv.Itoa(percent), func(t *testing.T) {
			manager := ticketTestManager(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
			registry := manager.extensions.Load()
			for index := range registry.installations[1].Bindings {
				registry.installations[1].Bindings[index].RolloutPercent = percent
			}
			for _, id := range []int64{2, 3} {
				inScope := int(stablePluginBucket(id)) < percent
				decision := manager.SchedulingDecision(ticketTestAccount(id), "gpt-6-astra", time.Now())
				require.Equal(t, !inScope, decision.Allowed, "only in-scope accounts are subject to this plugin's missing-ticket policy")
				if inScope {
					require.Equal(t, "ticket_missing", decision.Reason)
				}
			}
			registry.runtimes = map[int64]*pluginRuntime{}
			for _, id := range []int64{2, 3} {
				decision := manager.SchedulingDecision(ticketTestAccount(id), "gpt-6-astra", time.Now())
				if int(stablePluginBucket(id)) < percent {
					require.False(t, decision.Allowed)
					require.Equal(t, "plugin_unavailable", decision.Reason)
				} else {
					require.True(t, decision.Allowed, "a failed plugin cannot block an account outside its binding")
				}
			}
		})
	}
}
