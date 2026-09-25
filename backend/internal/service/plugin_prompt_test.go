package service

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPromptNativePlanDoesNotInvokeRPC(t *testing.T) {
	history := strings.Repeat("history", extensionv1.MaxPayloadBytes/7+1)
	body := []byte(`{"instructions":"client","input":"` + history + `"}`)
	snapshot := unifiedPromptSnapshot(t, "openai", []string{"auto"}, []string{"control_append"}, []string{"server"})
	invoked := 0
	updated, application, err := applyBusinessSystemPromptWithInvoker(context.Background(), body, snapshot, BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses}, func(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
		invoked++
		t.Fatal("native planning invoked a plugin RPC")
		return extensionv1.Result{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 0, invoked)
	require.True(t, application.Applied)
	require.Equal(t, "client\n\nserver", gjson.GetBytes(updated, "instructions").String())
	require.Equal(t, history, gjson.GetBytes(updated, "input").String())
}

func TestPromptNativeCancellationUsesIncomingContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := applyBusinessSystemPromptWithInvoker(ctx, []byte(`{"input":"test"}`), BusinessSystemPromptSnapshot{Enabled: true, Body: "server"}, BusinessSystemPromptTarget{Platform: PlatformOpenAI}, func(call context.Context, _, _ string, _ extensionv1.Invocation) (extensionv1.Result, error) {
		t.Fatal("canceled native planning invoked a plugin RPC")
		return extensionv1.Result{}, nil
	})
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}

func TestPromptSendRequiresValidatedRulesInsteadOfDerivedApplicationMetadata(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		name := "published snapshot missing"
		if initialized {
			name = "published rules available"
		}
		t.Run(name, func(t *testing.T) {
			store := businessSystemPromptStoreWithV2Rules(t, BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "current-server"})
			store.loaded.RulePolicy.Rules[0].Role = extensionv1.PromptRoleDeveloper
			store.loaded.RulePolicy.Rules[0].Position = extensionv1.PromptPositionBeforeLastUser
			policy := NewBusinessSystemPromptService(store, nil)
			if initialized {
				require.NoError(t, policy.Initialize(context.Background()))
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, BusinessSystemPromptProtocolResponses), businessSystemPromptRequestState{
				application: BusinessSystemPromptApplication{Applied: true, Carrier: BusinessSystemPromptCarrierInstructions, ServerInstructions: "derived-server", Revision: 1},
			})
			clean := []byte(`{"instructions":"  exact client\n","input":[{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`)
			original := bytes.Clone(clean)
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			body, application, err := policy.ApplyForSend(c, account, clean, BusinessSystemPromptProtocolResponses, false)
			if !initialized {
				require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable, "application metadata cannot provide a missing validated rule snapshot")
				require.Equal(t, original, clean)
				return
			}
			require.NoError(t, err)
			require.True(t, application.Applied)
			require.Len(t, application.RulesPlan.Placements, 1)
			require.Equal(t, "input", application.RulesPlan.Placements[0].Carrier)
			require.Equal(t, "developer", application.RulesPlan.Placements[0].Role)
			require.Equal(t, "  exact client\n", gjson.GetBytes(body, "instructions").String())
			require.Equal(t, "developer", gjson.GetBytes(body, "input.0.role").String())
			require.Equal(t, "current-server", gjson.GetBytes(body, "input.0.content").String())
			require.Equal(t, int64(2), gjson.GetBytes(body, "input.#").Int())
			require.JSONEq(t, gjson.GetBytes(clean, "input.0").Raw, gjson.GetBytes(body, "input.1").Raw)
			require.NotContains(t, string(body), "derived-server")
			require.Equal(t, original, clean)

			next := businessSystemPromptStoreWithV2Rules(t, BusinessSystemPromptSnapshot{Revision: 2, VersionID: 2, Enabled: true, Body: "replacement-server"})
			next.loaded.RulePolicy.Rules[0].Role = extensionv1.PromptRoleDeveloper
			next.loaded.RulePolicy.Rules[0].Position = extensionv1.PromptPositionBeforeLastUser
			store.loaded, store.detail = next.loaded, next.detail
			require.NoError(t, policy.Reload(context.Background()))
			retry, retried, err := policy.ApplyForSend(c, account, clean, BusinessSystemPromptProtocolResponses, false)
			require.NoError(t, err)
			require.Equal(t, body, retry)
			require.Equal(t, int64(1), retried.Revision)
			require.Equal(t, application.RulesPlan.SHA256, retried.RulesPlan.SHA256)

			disabled := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{
				PromptAccountBindingExtraKey: map[string]any{"mode": "off", "rule_ids": []string{}},
			}}
			unmodified, skipped, err := policy.ApplyForSend(c, disabled, clean, BusinessSystemPromptProtocolResponses, false)
			require.NoError(t, err)
			require.False(t, skipped.Applied)
			require.Equal(t, original, unmodified)
			require.Equal(t, []extensionv1.PromptRuleDecision{{RuleID: "fixture", Reason: "account_scope"}}, skipped.RulesPlan.Skipped)
		})
	}
}
