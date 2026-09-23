package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func newPromptRulesGatewayPolicy(delivery, position string) *BusinessSystemPromptService {
	rule := extensionv1.PromptRule{ID: "site", Name: "Site rule", Enabled: true, TemplateID: 1, VersionID: 2, Order: 100, Delivery: delivery, Position: position, ModelMatch: "upstream", Models: []string{}}
	hash, _, _ := extensionv1.ValidateTextDocument("site-rule-content", 100)
	snapshot := BusinessSystemPromptSnapshot{Revision: 8, Enabled: true, RulePolicy: &extensionv1.PromptRulePolicy{Version: 1, Rules: []extensionv1.PromptRule{rule}, DefaultRuleIDs: []string{"site"}}, ResolvedRules: []extensionv1.ResolvedPromptRule{{Rule: rule, Body: "site-rule-content", SHA256: hash}}}
	service := NewBusinessSystemPromptService(nil, nil)
	service.snapshot.Store(&snapshot)
	return service
}

func TestPromptRulesFinalHTTPPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"responses", "passthrough", "raw-chat", "responses-to-chat", "chat-to-responses", "messages-to-responses", "oauth"} {
		t.Run(path, func(t *testing.T) {
			account := businessSystemPromptAPIKeyAccount(true)
			body := []byte(`{"model":"gpt-5.4","instructions":"client","stream":false,"input":[{"role":"user","content":"hello"}]}`)
			delivery, position := "developer", "conversation_tail"
			if path == "oauth" {
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "fixture", "chatgpt_account_id": "fixture"}
				delivery, position = "native_control", "control_prepend"
			}
			if path == "raw-chat" || path == "chat-to-responses" {
				body = []byte(`{"model":"gpt-5.4","instructions":"unusual-client-field","stream":false,"messages":[{"role":"system","content":"client control"},{"role":"user","content":"hello"}]}`)
			}
			if path == "messages-to-responses" {
				body = []byte(`{"model":"gpt-5.4","max_tokens":32,"stream":false,"system":"client control","messages":[{"role":"user","content":"hello"}]}`)
			}
			if path == "raw-chat" || path == "responses-to-chat" {
				account.Extra["openai_responses_supported"] = false
			}
			c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
			upstream := businessSystemPromptErrorUpstream()
			gateway := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newPromptRulesGatewayPolicy(delivery, position)}
			var err error
			switch path {
			case "passthrough":
				_, err = gateway.forwardOpenAIPassthrough(context.Background(), c, account, body, body, "gpt-5.4", false, false, time.Now())
			case "raw-chat":
				_, err = gateway.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
			case "responses-to-chat":
				_, err = gateway.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body, false)
			case "chat-to-responses":
				_, err = gateway.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			case "messages-to-responses":
				_, err = gateway.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
			default:
				_, err = gateway.Forward(context.Background(), c, account, body)
			}
			require.Error(t, err, "the offline upstream intentionally returns a capture-complete error")
			require.NotNil(t, upstream.lastReq, "%v", err)
			require.Equal(t, 1, strings.Count(string(upstream.lastBody), "site-rule-content"))
			if path == "oauth" {
				require.Equal(t, "site-rule-content\n\nclient", gjson.GetBytes(upstream.lastBody, "instructions").String())
				return
			}
			field := "input"
			if path == "raw-chat" || path == "responses-to-chat" {
				field = "messages"
			}
			items := gjson.GetBytes(upstream.lastBody, field).Array()
			require.NotEmpty(t, items)
			require.Equal(t, "developer", items[len(items)-1].Get("role").String())
			require.Equal(t, "site-rule-content", items[len(items)-1].Get("content").String())
			if path == "raw-chat" {
				require.Equal(t, "unusual-client-field", gjson.GetBytes(upstream.lastBody, "instructions").String())
			}
		})
	}
}

func TestPromptRulesRetryScopeRestoresOnlyOwnedInsertions(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("system", "control_prepend")
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	account := businessSystemPromptAPIKeyAccount(true)
	input := []byte(`{"model":"model-a","prompt_cache_key":"client-cache","instructions":"client","input":[{"role":"system","content":"site-rule-content"},{"role":"user","content":"hello"}],"previous_response_id":"resp_keep"}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
	first, err := gateway.finalizeBusinessPromptForSend(c, account, input, "responses", false)
	require.NoError(t, err)
	retry, err := gateway.finalizeBusinessPromptForSend(c, account, first, "responses", false)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	require.Equal(t, 2, strings.Count(string(retry), "site-rule-content"), "the identical customer message must remain")
	account.Extra[PromptAccountBindingExtraKey] = extensionv1.PromptAccountBinding{Mode: "off"}
	clean, err := gateway.finalizeBusinessPromptForSend(c, account, retry, "responses", false)
	require.NoError(t, err)
	require.JSONEq(t, string(input), string(clean))
	account.Extra[PromptAccountBindingExtraKey] = extensionv1.PromptAccountBinding{Mode: "inherit"}
	policy.snapshot.Store(&BusinessSystemPromptSnapshot{Revision: 9, Enabled: false})
	previousSnapshot, _, err := gateway.businessSystemPromptSnapshotForRequest(c, account)
	require.NoError(t, err)
	require.Equal(t, int64(8), previousSnapshot.Revision)
	reapplied, err := gateway.finalizeBusinessPromptForSend(c, account, clean, "responses", false)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(reapplied), "site-rule-content"))
	require.Equal(t, "resp_keep", gjson.GetBytes(reapplied, "previous_response_id").String())
	convertedInput, err := restoreBusinessSystemPromptBeforeConversion(c, reapplied, "responses")
	require.NoError(t, err)
	require.JSONEq(t, gjson.GetBytes(input, "input").Raw, gjson.GetBytes(convertedInput, "input").Raw)
}

func TestPromptRulesPassthroughFirstAndSecondTurnWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := newStagedPassthroughConn()
	defer func() { _ = upstream.Close() }()
	gateway := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	gateway.businessPromptService = newPromptRulesGatewayPolicy("system", "conversation_tail")
	server, _ := startPassthroughLifecycleServer(t, ctx, gateway, passthroughLifecycleAccount())
	defer server.Close()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	for turn := 1; turn <= 2; turn++ {
		body := []byte(`{"type":"response.create","model":"gpt-5.4","input":[{"role":"user","content":"hello"}],"instructions":"client"}`)
		if turn == 2 {
			body, err = sjson.SetBytes(body, "previous_response_id", "resp_one")
			require.NoError(t, err)
		}
		writeCtx, writeCancel := context.WithTimeout(ctx, 3*time.Second)
		err = client.Write(writeCtx, coderws.MessageText, body)
		writeCancel()
		require.NoError(t, err)
		select {
		case sent := <-upstream.writes:
			require.Equal(t, "system", gjson.GetBytes(sent, "input.1.role").String())
			require.Equal(t, "site-rule-content", gjson.GetBytes(sent, "input.1.content").String())
			require.Equal(t, 1, strings.Count(string(sent), "site-rule-content"))
			if turn == 2 {
				require.Equal(t, "resp_one", gjson.GetBytes(sent, "previous_response_id").String())
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no captured websocket request")
		}
		upstream.Send(`{"type":"response.completed","response":{"id":"resp_one","model":"gpt-5.4","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
		readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
		_, payload, err := client.Read(readCtx)
		readCancel()
		require.NoError(t, err)
		require.Equal(t, "response.completed", gjson.GetBytes(payload, "type").String())
	}
}

type promptRulesPreviewAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *promptRulesPreviewAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestPromptRulesPreviewMatchesFinalHTTPControlBytes(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("developer", "conversation_tail")
	account := businessSystemPromptAPIKeyAccount(true)
	policy.accountRepo = &promptRulesPreviewAccountRepo{account: account}
	body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"client","input":[{"role":"user","content":"hello"}]}`)
	preview, err := policy.PreviewPromptRules(context.Background(), account.ID, PromptRulesPreviewRequest{Protocol: "responses", Body: json.RawMessage(body)})
	require.NoError(t, err)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	upstream := businessSystemPromptErrorUpstream()
	gateway := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
	_, err = gateway.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.NotNil(t, upstream.lastReq)
	require.JSONEq(t, gjson.GetBytes(preview.Body, "input").Raw, gjson.GetBytes(upstream.lastBody, "input").Raw)
	require.Equal(t, gjson.GetBytes(preview.Body, "instructions").String(), gjson.GetBytes(upstream.lastBody, "instructions").String())
}

func TestPromptRulesNativeWSReplayKeepsCleanAccumulatorAndFrozenPolicy(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("developer", "conversation_tail")
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	account := businessSystemPromptAPIKeyAccount(true)
	first := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"first"}],"previous_response_id":"resp_anchor"}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", first)
	beginBusinessSystemPromptRequestTurn(c)
	parsed, plan, err := gateway.prepareBusinessPromptWSIngress(c, first, account, "responses", false)
	require.NoError(t, err)
	require.True(t, plan.Applied)
	require.Equal(t, first, parsed, "the accumulator must never receive the site's messages")
	wire, err := gateway.finalizeBusinessPromptWSIngress(c, account, parsed)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(wire), "site-rule-content"))
	policy.snapshot.Store(&BusinessSystemPromptSnapshot{Revision: 9, Enabled: false})
	replay := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"earlier"},{"role":"assistant","content":"answer"},{"role":"user","content":"first"}],"previous_response_id":"resp_anchor"}`)
	wire, err = gateway.finalizeBusinessPromptWSIngress(c, account, replay)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(wire), "site-rule-content"))
	require.Equal(t, "developer", gjson.GetBytes(wire, "input.3.role").String())
	require.Equal(t, "resp_anchor", gjson.GetBytes(wire, "previous_response_id").String())
	require.NotContains(t, string(replay), "site-rule-content")
	application, ok := businessSystemPromptApplicationFromRequest(c, "responses")
	require.True(t, ok)
	require.Equal(t, int64(8), application.Revision)
}

func TestPromptRulesOAuthPreviewMatchesControlAdapters(t *testing.T) {
	for _, protocol := range []string{"responses", "chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			account := &Account{ID: 62, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "fixture", "chatgpt_account_id": "fixture"}}
			body := []byte(`{"model":"gpt-6-astra","stream":false,"input":[{"role":"system","content":"client system"},{"role":"user","content":"hello"}]}`)
			if protocol == "chat" {
				body = []byte(`{"model":"gpt-6-astra","stream":false,"messages":[{"role":"system","content":"client system"},{"role":"user","content":"hello"}]}`)
			}
			if protocol == "messages" {
				body = []byte(`{"model":"gpt-6-astra","stream":false,"max_tokens":32,"system":"client system","messages":[{"role":"user","content":"hello"}]}`)
			}
			policy := newPromptRulesGatewayPolicy("native_control", "control_append")
			policy.accountRepo = &promptRulesPreviewAccountRepo{account: account}
			cfg := businessSystemPromptTestConfig()
			cfg.Gateway.ForcedCodexInstructionsTemplate = "base {{.ExistingInstructions}}"
			policy.previewConfig = cfg
			preview, err := policy.PreviewPromptRules(context.Background(), account.ID, PromptRulesPreviewRequest{Protocol: protocol, Body: json.RawMessage(body)})
			require.NoError(t, err)
			c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
			upstream := businessSystemPromptErrorUpstream()
			gateway := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, businessPromptService: policy}
			switch protocol {
			case "chat":
				_, err = gateway.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			case "messages":
				_, err = gateway.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
			default:
				_, err = gateway.Forward(context.Background(), c, account, body)
			}
			require.Error(t, err)
			require.NotNil(t, upstream.lastReq, "%v", err)
			require.Equal(t, gjson.GetBytes(upstream.lastBody, "instructions").String(), gjson.GetBytes(preview.Body, "instructions").String())
			require.JSONEq(t, gjson.GetBytes(upstream.lastBody, "input").Raw, gjson.GetBytes(preview.Body, "input").Raw)
		})
	}
}

type promptRulesBindingRepo struct {
	AccountRepository
	accounts []*Account
	updates  []PromptBindingUpdate
}

func (r *promptRulesBindingRepo) GetByIDs(context.Context, []int64) ([]*Account, error) {
	return r.accounts, nil
}
func (r *promptRulesBindingRepo) UpdatePromptBindingIfRevision(_ context.Context, id int64, expected time.Time, _ int64, binding extensionv1.PromptAccountBinding) (bool, error) {
	r.updates = append(r.updates, PromptBindingUpdate{AccountID: id, ExpectedUpdatedAt: expected, Binding: binding})
	return id != 2, nil
}

func TestPromptRulesAccountBindingsCASAndShadowIndependence(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("native_control", "control_append")
	parentID := int64(1)
	updated := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, UpdatedAt: updated, Extra: map[string]any{PromptAccountBindingExtraKey: extensionv1.PromptAccountBinding{Mode: "off"}}}
	shadow := &Account{ID: 2, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, UpdatedAt: updated}
	repo := &promptRulesBindingRepo{accounts: []*Account{parent, shadow}}
	policy.accountRepo = repo
	views, err := policy.PromptAccountBindings(context.Background(), []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, "off", views[0].Binding.Mode)
	require.Equal(t, "inherit", views[1].Binding.Mode)
	require.Equal(t, []string{"site"}, views[1].EffectiveRuleIDs)
	updates := []PromptBindingUpdate{{AccountID: 1, ExpectedUpdatedAt: updated, Binding: extensionv1.PromptAccountBinding{Mode: "custom", RuleIDs: []string{"site"}}}, {AccountID: 2, ExpectedUpdatedAt: updated, Binding: extensionv1.PromptAccountBinding{Mode: "off"}}}
	_, err = policy.UpdatePromptAccountBindings(context.Background(), updates, 7)
	require.ErrorIs(t, err, ErrBusinessSystemPromptRevisionConflict)
	require.Empty(t, repo.updates)
	results, err := policy.UpdatePromptAccountBindings(context.Background(), updates, 8)
	require.NoError(t, err)
	require.True(t, results[0].Applied)
	require.Equal(t, "account_revision_conflict", results[1].Code)
	require.Len(t, repo.updates, 2)
	encoded, _ := json.Marshal(repo.updates)
	require.NotContains(t, string(encoded), "site-rule-content")
}

func TestPromptRulesExecutionScopeRevocationRestoresWireAndCacheKey(t *testing.T) {
	gateway, invoker, _ := newAccountScopedPromptGateway(t, 100)
	gateway.businessPromptService = newPromptRulesGatewayPolicy("developer", "conversation_tail")
	account := businessSystemPromptAPIKeyAccount(true)
	input := []byte(`{"model":"gpt-5.4","prompt_cache_key":"original","input":[{"role":"user","content":"hello"}]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
	wire, err := gateway.finalizeBusinessPromptForSend(c, account, input, "responses", false)
	require.NoError(t, err)
	require.Contains(t, string(wire), "site-rule-content")
	invoker.installation.Bindings[0].RolloutPercent = 0
	clean, err := gateway.finalizeBusinessPromptForSend(c, account, wire, "responses", false)
	require.NoError(t, err)
	require.JSONEq(t, string(input), string(clean))
	application, ok := businessSystemPromptApplicationFromRequest(c, "responses")
	require.True(t, ok)
	require.False(t, application.Applied)
	require.Equal(t, "plugin_scope", application.RulesPlan.Skipped[0].Reason)
}
