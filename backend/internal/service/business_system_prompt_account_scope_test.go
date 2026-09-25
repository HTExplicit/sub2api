package service

import (
	"context"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type promptSendBindingRepository struct {
	AccountRepository
	accounts map[int64]*Account
	reads    []int64
}

func (r *promptSendBindingRepository) GetByID(_ context.Context, id int64) (*Account, error) {
	r.reads = append(r.reads, id)
	return r.accounts[id], nil
}

func TestPromptAccountSelectionUsesFreshBindingAndFrozenContent(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("system", "control_prepend")
	included := businessSystemPromptAPIKeyAccount(true)
	included.ID = 1
	excluded := businessSystemPromptAPIKeyAccount(true)
	excluded.ID = 2
	excluded.Extra[PromptAccountBindingExtraKey] = extensionv1.PromptAccountBinding{Mode: "off"}
	repo := &promptSendBindingRepository{accounts: map[int64]*Account{1: included, 2: excluded}}
	policy.SetAccountRepository(repo)
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	original := []byte(`{"model":"gpt-5.4","input":[{"role":"system","content":"site-rule-content"},{"role":"user","content":"customer"}]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", original)
	sent, first, err := policy.ApplyForSend(c, included, original, "responses", false)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(sent), "site-rule-content"))
	require.Equal(t, int64(8), first.Revision)

	changed := promptRulesSnapshotForTest(BusinessSystemPromptSnapshot{Revision: 9, Enabled: true, Body: "next-revision"})
	policy.snapshot.Store(&changed)
	staleSelection := *excluded
	staleSelection.Extra = nil
	clean, off, err := policy.ApplyForSend(c, &staleSelection, original, "responses", false)
	require.NoError(t, err)
	require.False(t, off.Applied)
	require.Equal(t, original, clean, "failover starts from the untouched customer sequence")
	require.Equal(t, int64(8), off.Revision)
	_, oldResponse := businessSystemPromptApplicationFromRequest(c, "chat")
	require.False(t, oldResponse)

	excluded.Extra[PromptAccountBindingExtraKey] = extensionv1.PromptAccountBinding{Mode: "custom", RuleIDs: []string{"site"}}
	sent, again, err := gateway.applyBusinessSystemPromptForRequest(c, original, &staleSelection, "responses", false)
	require.NoError(t, err)
	require.True(t, again.Applied)
	require.Equal(t, first.Revision, again.Revision)
	require.NotContains(t, string(sent), "next-revision")
	require.Equal(t, []int64{1, 2, 2}, repo.reads)
	require.Equal(t, 1, strings.Count(string(original), "site-rule-content"))
}

func TestPromptRulePlatformUsesProviderRatherThanWireProtocol(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("auto", "control_append")
	body := []byte(`{"model":"gpt-5.4","instructions":"client","input":"history"}`)
	for _, platform := range []string{PlatformCindy, PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			account := businessSystemPromptAPIKeyAccount(true)
			account.Platform, account.WirePlatform = platform, WirePlatformOpenAI
			c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
			wire, app, err := policy.ApplyForSend(c, account, body, "responses", false)
			require.NoError(t, err)
			require.Equal(t, platform == PlatformCindy, app.Applied)
			if platform == PlatformGrok {
				require.Equal(t, body, wire)
				require.Equal(t, "platform_scope", app.RulesPlan.Skipped[0].Reason)
			}
		})
	}
}

func TestPromptCleanSourceFallbackDoesNotInvokePluginOrCarryPreviousOutput(t *testing.T) {
	previous := invokePromptSkills
	invokePromptSkills = func(context.Context, extensionv1.Invocation) (extensionv1.Result, error) {
		t.Fatal("ordinary inline prompt reached plugin runtime")
		return extensionv1.Result{}, nil
	}
	t.Cleanup(func() { invokePromptSkills = previous })
	policy := newPromptRulesGatewayPolicy("auto", "control_append")
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	account := businessSystemPromptAPIKeyAccount(true)
	body := []byte(`{"model":"gpt-5.4","instructions":"client","input":"history"}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	wire, app, err := policy.ApplyForSend(c, account, body, "responses", false)
	require.NoError(t, err)
	require.True(t, app.Applied)
	again, _, err := policy.ApplyForSend(c, account, body, "responses", false)
	require.NoError(t, err)
	require.Equal(t, wire, again)
	chat := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"client"},{"role":"user","content":"history"}]}`)
	converted, applied, err := policy.ApplyForSend(c, account, chat, "chat", false)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(converted), "site-rule-content"))
	require.Equal(t, app.Revision, applied.Revision)
	require.Equal(t, "client", gjson.GetBytes(converted, "messages.0.content").String())
	require.Equal(t, "site-rule-content", gjson.GetBytes(converted, "messages.1.content").String())
	require.Equal(t, body, gateway.rewriteBusinessSystemPromptJSONForRequest(c, body, "responses"))
}

func TestPromptEchoProofPreservesIdenticalClientMessageAtNewPositions(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("developer", "before_last_user")
	account := businessSystemPromptAPIKeyAccount(true)
	clientText := strings.Repeat("customer history ", 10000)
	body := []byte(`{"model":"gpt-5.4","input":[{"role":"developer","content":"site-rule-content"},{"role":"user","content":"` + clientText + `"}]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	wire, _, err := policy.ApplyForSend(c, account, body, "responses", false)
	require.NoError(t, err)
	stateRaw, ok := businessSystemPromptRequestGet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, "responses"))
	require.True(t, ok)
	state, ok := stateRaw.(businessSystemPromptRequestState)
	require.True(t, ok)
	require.Len(t, state.rulesUndo, 1)
	require.Empty(t, state.rulesUndo[0].scalar, "large client histories are not copied into echo metadata")
	require.Equal(t, []int{1}, state.rulesUndo[0].indices)
	restored := gateway.rewriteBusinessSystemPromptJSONForRequest(c, wire, "responses")
	require.JSONEq(t, string(body), string(restored))
	require.Equal(t, 1, strings.Count(string(restored), "site-rule-content"))
}
