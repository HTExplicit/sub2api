package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	promptpolicy "github.com/HTExplicit/sub2api-plugins/promptskills/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type accountScopedPromptInvoker struct {
	manager      *PluginManager
	installation *PluginInstallation
	module       *promptpolicy.Module
	other        extensionv1.OperationInvoker
	calls        []extensionv1.Invocation
}

func (p *accountScopedPromptInvoker) InvokeOperation(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Operation != "prompt.plan" && in.Operation != "prompt.availability" {
		if p.other != nil {
			return p.other.InvokeOperation(ctx, platform, kind, in)
		}
		return extensionv1.Result{}, ErrExtensionOperationDisabled
	}
	if in.Operation == "prompt.plan" {
		p.calls = append(p.calls, in)
	}
	// Use the actual host operation ownership/cohort gate, then run only the
	// independent policy module in process. No customer body or model IO exists.
	if _, _, err := p.manager.operationOwner(platform, kind, in); err != nil {
		return extensionv1.Result{}, err
	}
	return p.module.Invoke(ctx, in)
}

func (p *accountScopedPromptInvoker) InvokeDomainOperation(ctx context.Context, in extensionv1.Invocation, _ bool) (extensionv1.Result, error) {
	if in.Operation != "prompt.availability" {
		if other, ok := p.other.(domainOperationInvoker); ok {
			return other.InvokeDomainOperation(ctx, in, false)
		}
		return p.InvokeOperation(ctx, "*", "*", in)
	}
	platform, kind, err := p.manager.domainOperationScope(ctx, in)
	if err != nil {
		return extensionv1.Result{}, err
	}
	return p.InvokeOperation(ctx, platform, kind, in)
}

func (p *accountScopedPromptInvoker) BindDomainOperationContext(ctx context.Context, in extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
	if _, _, err := p.manager.domainOperationScope(ctx, in); err != nil {
		return nil, nil, err
	}
	bound, cancel := context.WithCancel(ctx)
	return bound, cancel, nil
}

func newAccountScopedPromptGateway(t *testing.T, rollout int) (*OpenAIGatewayService, *accountScopedPromptInvoker, *fakeBusinessSystemPromptStore) {
	t.Helper()
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "account-scoped-server"}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	installation := &PluginInstallation{ID: 7, State: PluginStateEnabled,
		Manifest: PluginManifest{Operations: map[string][]string{extensionv1.CapabilityRequest: {"prompt.plan", "prompt.availability"}}},
		Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: rollout}},
	}
	manager := NewPluginManager(nil, nil, nil, PluginHostInfo{}, nil)
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{installation.ID: installation}, runtimes: map[int64]*pluginRuntime{}})
	invoker := &accountScopedPromptInvoker{manager: manager, installation: installation, module: promptpolicy.New()}
	previous := processExtensionOperations.Load()
	if previous != nil {
		invoker.other = previous.invoker
	}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: invoker})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	return &OpenAIGatewayService{businessPromptService: policy}, invoker, store
}

func promptScopeAccounts(t *testing.T) (*Account, *Account) {
	t.Helper()
	var included, excluded *Account
	for id := int64(1); id <= 1000 && (included == nil || excluded == nil); id++ {
		account := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		if stablePluginBucket(id) < 50 && included == nil {
			included = account
		}
		if stablePluginBucket(id) >= 50 && excluded == nil {
			excluded = account
		}
	}
	require.NotNil(t, included)
	require.NotNil(t, excluded)
	return included, excluded
}

func TestPromptRealAccountZeroRolloutRejected(t *testing.T) {
	gateway, invoker, _ := newAccountScopedPromptGateway(t, 0)
	account, _ := promptScopeAccounts(t)
	input := []byte(`{"instructions":"client","input":"customer history"}`)
	output, application, err := gateway.applyBusinessSystemPrompt(input, account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.False(t, application.Applied)
	require.Equal(t, input, output)
	require.Len(t, invoker.calls, 1)
	require.Equal(t, account.ID, invoker.calls[0].AccountID)
}

func TestPromptAccountFailoverCannotReusePreviousDecision(t *testing.T) {
	gateway, invoker, _ := newAccountScopedPromptGateway(t, 50)
	included, excluded := promptScopeAccounts(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	input := []byte(`{"instructions":"client","input":"customer history"}`)
	_, first, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, included, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.True(t, first.Applied)
	output, second, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, excluded, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.False(t, second.Applied, "the previous account's input-hash cache hit must not grant the new account")
	require.Equal(t, "client", gjson.GetBytes(output, "instructions").String())
	require.Len(t, invoker.calls, 2)
	require.Equal(t, included.ID, invoker.calls[0].AccountID)
	require.Equal(t, excluded.ID, invoker.calls[1].AccountID)
}

func TestPromptRealAccountRolloutAndAccountlessPreview(t *testing.T) {
	for _, rollout := range []int{50, 100} {
		t.Run(fmt.Sprint(rollout), func(t *testing.T) {
			gateway, invoker, _ := newAccountScopedPromptGateway(t, rollout)
			included, excluded := promptScopeAccounts(t)
			for _, account := range []*Account{included, excluded} {
				input := []byte(`{"instructions":"client","input":"private-customer-history"}`)
				_, application, err := gateway.applyBusinessSystemPrompt(input, account, BusinessSystemPromptProtocolResponses, false)
				require.NoError(t, err)
				require.Equal(t, int(stablePluginBucket(account.ID)) < rollout, application.Applied)
				call := invoker.calls[len(invoker.calls)-1]
				require.Equal(t, account.ID, call.AccountID)
				require.Equal(t, account.ID, gjson.GetBytes(call.Payload, "target.account_id").Int())
				require.NotContains(t, string(call.Payload), "private-customer-history")
			}
		})
	}
	t.Run("accountless_preview", func(t *testing.T) {
		_, invoker, _ := newAccountScopedPromptGateway(t, 0)
		input := []byte(`{"instructions":"client","input":"preview"}`)
		_, application, err := ApplyBusinessSystemPromptToJSON(input, BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "preview-server"}, BusinessSystemPromptTarget{Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Protocol: BusinessSystemPromptProtocolResponses})
		require.NoError(t, err)
		require.True(t, application.Applied, "global preview keeps its accountless semantics")
		require.Zero(t, invoker.calls[0].AccountID)
		require.False(t, gjson.GetBytes(invoker.calls[0].Payload, "target.account_id").Exists())
	})
}

func TestPromptAccountChangeRestoresOnlyOwnedCarrier(t *testing.T) {
	for _, carrier := range []string{"responses", "chat", "chat_instructions"} {
		for _, form := range []string{"original", "output", "wire_rewrite"} {
			t.Run(carrier+"/"+form, func(t *testing.T) {
				gateway, invoker, store := newAccountScopedPromptGateway(t, 50)
				included, excluded := promptScopeAccounts(t)
				protocol := BusinessSystemPromptProtocolResponses
				input := []byte(`{"instructions":"  client  ","input":"private-history","model":"before"}`)
				if carrier != "responses" {
					protocol = BusinessSystemPromptProtocolChat
					// This customer system message deliberately equals our policy.
					input = []byte(`{"messages":[{"role":"system","content":"account-scoped-server"},{"role":"developer","content":"client-control"},{"role":"user","content":"private-history"}],"model":"before"}`)
					if carrier == "chat_instructions" {
						var err error
						input, err = sjson.SetBytes(input, "instructions", "  client  ")
						require.NoError(t, err)
					}
				}
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				first, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, included, protocol, false)
				require.NoError(t, err)
				require.True(t, application.Applied)
				store.loaded = BusinessSystemPromptSnapshot{Revision: 2, Enabled: true, Body: "new-unfrozen-server"}
				require.NoError(t, gateway.businessPromptService.Reload(context.Background()))
				retry := first
				if form == "original" {
					retry = input
				} else if form == "wire_rewrite" {
					retry, err = sjson.SetBytes(retry, "model", "after")
					require.NoError(t, err)
				}
				clean, rejected, err := gateway.applyBusinessSystemPromptForRequest(ctx, retry, excluded, protocol, false)
				require.NoError(t, err)
				require.False(t, rejected.Applied)
				if carrier == "chat" {
					require.JSONEq(t, gjson.GetBytes(input, "messages").Raw, gjson.GetBytes(clean, "messages").Raw)
					require.Equal(t, "account-scoped-server", gjson.GetBytes(clean, "messages.0.content").String())
				} else {
					require.Equal(t, gjson.GetBytes(input, "instructions").Raw, gjson.GetBytes(clean, "instructions").Raw)
				}
				if form == "wire_rewrite" {
					require.Equal(t, "after", gjson.GetBytes(clean, "model").String())
				}
				_, frozen, err := gateway.applyBusinessSystemPromptForRequest(ctx, clean, included, protocol, false)
				require.NoError(t, err)
				require.True(t, frozen.Applied)
				require.Equal(t, int64(1), frozen.Revision)
				require.Equal(t, "account-scoped-server", frozen.ServerInstructions)
				require.Len(t, invoker.calls, 3)
				for _, call := range invoker.calls {
					require.Equal(t, int64(1), gjson.GetBytes(call.Payload, "snapshot.revision").Int())
					require.NotContains(t, string(call.Payload), "private-history")
				}
			})
		}
	}
}

func TestPromptSameAccountCacheRechecksRevocation(t *testing.T) {
	for _, revoke := range []string{"zero_rollout", "disabled"} {
		for _, form := range []string{"input", "output", "cache_key_rewrite"} {
			t.Run(revoke+"/"+form, func(t *testing.T) {
				gateway, invoker, _ := newAccountScopedPromptGateway(t, 100)
				account, _ := promptScopeAccounts(t)
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				input := []byte(`{"instructions":"client","input":"history","prompt_cache_key":"client-key"}`)
				body, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, account, BusinessSystemPromptProtocolResponses, false)
				require.NoError(t, err)
				require.True(t, application.Applied)
				if form == "input" {
					body = input
				} else if form == "cache_key_rewrite" {
					body, err = rewriteBusinessSystemPromptCacheKey(ctx, body, application)
					require.NoError(t, err)
				}
				if revoke == "disabled" {
					invoker.installation.State = PluginStateDisabled
					invoker.installation.Bindings[0].Enabled = false
				} else {
					invoker.installation.Bindings[0].RolloutPercent = 0
				}
				clean, denied, err := gateway.applyBusinessSystemPromptForRequest(ctx, body, account, BusinessSystemPromptProtocolResponses, false)
				require.NoError(t, err)
				require.False(t, denied.Applied)
				require.Equal(t, "client", gjson.GetBytes(clean, "instructions").String())
				require.Len(t, invoker.calls, 2)
				require.Equal(t, account.ID, invoker.calls[1].AccountID)
			})
		}
	}
}

func TestPromptUnknownCarrierFailsClosedWithoutDeletingClientMessages(t *testing.T) {
	for _, protocol := range []string{BusinessSystemPromptProtocolResponses, BusinessSystemPromptProtocolChat} {
		t.Run(protocol, func(t *testing.T) {
			gateway, _, _ := newAccountScopedPromptGateway(t, 50)
			included, excluded := promptScopeAccounts(t)
			input := []byte(`{"instructions":"client","input":"history"}`)
			if protocol == BusinessSystemPromptProtocolChat {
				input = []byte(`{"messages":[{"role":"system","content":"account-scoped-server"},{"role":"user","content":"history"}]}`)
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			body, _, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, included, protocol, false)
			require.NoError(t, err)
			if protocol == BusinessSystemPromptProtocolResponses {
				body, err = sjson.SetBytes(body, "instructions", "client\n\naccount-scoped-server\nunknown-transform")
			} else {
				body, err = sjson.SetBytes(body, "messages.-1", map[string]string{"role": "user", "content": "unknown-transform"})
			}
			require.NoError(t, err)
			before := append([]byte(nil), body...)
			output, _, err := gateway.applyBusinessSystemPromptForRequest(ctx, body, excluded, protocol, false)
			require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
			require.Nil(t, output)
			require.Equal(t, before, body)
		})
	}
}

func TestPromptUndoMetadataIsBoundedAndHistoryStaysOutOfRPC(t *testing.T) {
	gateway, invoker, _ := newAccountScopedPromptGateway(t, 50)
	included, excluded := promptScopeAccounts(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	client := strings.Repeat("private-client-control", businessSystemPromptRestoreMaxBytes/10)
	history := strings.Repeat("private-history", extensionv1.MaxPayloadBytes/10)
	input, err := json.Marshal(map[string]string{"instructions": client, "input": history})
	require.NoError(t, err)
	output, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, included, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.True(t, application.Applied)
	value, _ := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, BusinessSystemPromptProtocolResponses))
	state := value.(businessSystemPromptRequestState)
	require.Empty(t, state.undo.instructions)
	require.False(t, state.undo.restorable)
	rejected, _, err := gateway.applyBusinessSystemPromptForRequest(ctx, output, excluded, BusinessSystemPromptProtocolResponses, false)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
	require.Nil(t, rejected)
	// Passing the known original remains safe without retaining a second copy.
	clean, denied, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, excluded, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.False(t, denied.Applied)
	require.Equal(t, input, clean)
	for _, call := range invoker.calls {
		require.Less(t, len(call.Payload), extensionv1.MaxPayloadBytes)
		require.NotContains(t, string(call.Payload), "private-client-control")
		require.NotContains(t, string(call.Payload), "private-history")
	}
}

func TestPromptProtocolConversionRestoresCarrierBeforeAccountFailover(t *testing.T) {
	for _, direction := range []string{"responses_to_chat", "chat_to_responses"} {
		t.Run(direction, func(t *testing.T) {
			gateway, invoker, store := newAccountScopedPromptGateway(t, 50)
			included, excluded := promptScopeAccounts(t)
			includedID, excludedID := included.ID, excluded.ID
			included, excluded = businessSystemPromptAPIKeyAccount(true), businessSystemPromptAPIKeyAccount(true)
			included.ID, excluded.ID = includedID, excludedID
			protocol, path := BusinessSystemPromptProtocolResponses, "/v1/responses"
			input := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"client-control","input":"private-adapter-history"}`)
			if direction == "chat_to_responses" {
				protocol, path = BusinessSystemPromptProtocolChat, "/v1/chat/completions"
				input = []byte(`{"model":"gpt-5.4","stream":false,"messages":[{"role":"system","content":"client-control"},{"role":"user","content":"private-adapter-history"}]}`)
			}
			ctx, _ := newBusinessSystemPromptGinContext(path, input)
			body, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, included, protocol, false)
			require.NoError(t, err)
			require.True(t, application.Applied)
			store.loaded = BusinessSystemPromptSnapshot{Revision: 2, Enabled: true, Body: "new-unfrozen-server"}
			require.NoError(t, gateway.businessPromptService.Reload(context.Background()))
			upstream := &businessCacheStrictUpstream{}
			gateway.cfg, gateway.httpUpstream = businessSystemPromptTestConfig(), upstream
			if direction == "responses_to_chat" {
				_, err = gateway.forwardResponsesViaRawChatCompletions(context.Background(), ctx, excluded, body, false)
			} else {
				_, err = gateway.ForwardAsChatCompletions(context.Background(), ctx, excluded, body, "", "")
			}
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 1)
			require.NotContains(t, string(upstream.lastBody), "account-scoped-server")
			require.NotContains(t, string(upstream.lastBody), "new-unfrozen-server")
			require.Contains(t, string(upstream.lastBody), "client-control")
			require.Contains(t, string(upstream.lastBody), "private-adapter-history")
			last := invoker.calls[len(invoker.calls)-1]
			require.Equal(t, excluded.ID, last.AccountID)
			require.Equal(t, int64(1), gjson.GetBytes(last.Payload, "snapshot.revision").Int())
			require.NotContains(t, string(last.Payload), "private-adapter-history")
		})
	}
}

func TestPromptDuplicateCarrierCannotAuthorizeUndo(t *testing.T) {
	for _, protocol := range []string{BusinessSystemPromptProtocolResponses, BusinessSystemPromptProtocolChat} {
		t.Run(protocol, func(t *testing.T) {
			gateway, _, _ := newAccountScopedPromptGateway(t, 50)
			included, excluded := promptScopeAccounts(t)
			input := []byte(`{"instructions":"first-client","instructions":"last-client","input":"history"}`)
			if protocol == BusinessSystemPromptProtocolChat {
				input = []byte(`{"messages":[{"role":"system","content":"first-client-control"},{"role":"user","content":"first-history"}],"messages":[{"role":"user","content":"last-history"}]}`)
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			output, first, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, included, protocol, false)
			require.NoError(t, err)
			require.True(t, first.Applied)
			before := append([]byte(nil), output...)
			denied, _, err := gateway.applyBusinessSystemPromptForRequest(ctx, output, excluded, protocol, false)
			require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
			require.Nil(t, denied)
			require.Equal(t, before, output, "ambiguous duplicate fields must not delete a customer message")
			original, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, excluded, protocol, false)
			require.NoError(t, err)
			require.False(t, application.Applied)
			require.Equal(t, input, original)
		})
	}
}

func TestPromptCachedTargetSeparatesAccountTypeAndCompact(t *testing.T) {
	for _, change := range []string{"account_type", "compact"} {
		t.Run(change, func(t *testing.T) {
			gateway, invoker, _ := newAccountScopedPromptGateway(t, 100)
			account, _ := promptScopeAccounts(t)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			input := []byte(`{"instructions":"client","input":"history"}`)
			output, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, account, BusinessSystemPromptProtocolResponses, false)
			require.NoError(t, err)
			require.True(t, application.Applied)
			next := *account
			compact := change == "compact"
			if !compact {
				next.Type = AccountTypeOAuth
			}
			clean, current, err := gateway.applyBusinessSystemPromptForRequest(ctx, output, &next, BusinessSystemPromptProtocolResponses, compact)
			require.NoError(t, err)
			require.False(t, current.Applied)
			require.Equal(t, "client", gjson.GetBytes(clean, "instructions").String())
			require.Len(t, invoker.calls, 2)
			require.Equal(t, next.Type, gjson.GetBytes(invoker.calls[1].Payload, "target.account_type").String())
			require.Equal(t, compact, gjson.GetBytes(invoker.calls[1].Payload, "target.compact").Bool())
		})
	}
}

func TestPromptAccountChangeDoesNotReuseOtherProtocolResponseMetadata(t *testing.T) {
	gateway, _, _ := newAccountScopedPromptGateway(t, 50)
	included, excluded := promptScopeAccounts(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, []byte(`{"instructions":"client","input":"history"}`), included, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.True(t, application.Applied)
	_, denied, err := gateway.applyBusinessSystemPromptForRequest(ctx, []byte(`{"messages":[{"role":"user","content":"history"}]}`), excluded, BusinessSystemPromptProtocolChat, false)
	require.NoError(t, err)
	require.False(t, denied.Applied)
	_, present := businessSystemPromptApplicationFromRequest(ctx, BusinessSystemPromptProtocolResponses)
	require.False(t, present)
	echo := []byte(`{"instructions":"client\n\naccount-scoped-server","output":[]}`)
	require.Equal(t, echo, gateway.rewriteBusinessSystemPromptJSONForAnyRequest(ctx, echo), "an excluded account's response must not be scrubbed using an older account's application")
}

func TestPromptDeniedRetriesStillRecognizeEarlierOwnedOutput(t *testing.T) {
	for _, protocol := range []string{BusinessSystemPromptProtocolResponses, BusinessSystemPromptProtocolChat} {
		t.Run(protocol, func(t *testing.T) {
			gateway, _, _ := newAccountScopedPromptGateway(t, 50)
			included, excluded := promptScopeAccounts(t)
			input := []byte(`{"instructions":"client","input":"history"}`)
			field := "instructions"
			if protocol == BusinessSystemPromptProtocolChat {
				field = "messages"
				input = []byte(`{"messages":[{"role":"system","content":"account-scoped-server"},{"role":"user","content":"history"}]}`)
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			earlier, applied, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, included, protocol, false)
			require.NoError(t, err)
			require.True(t, applied.Applied)
			_, denied, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, excluded, protocol, false)
			require.NoError(t, err)
			require.False(t, denied.Applied)
			clean, stillDenied, err := gateway.applyBusinessSystemPromptForRequest(ctx, earlier, excluded, protocol, false)
			require.NoError(t, err)
			require.False(t, stillDenied.Applied)
			require.JSONEq(t, gjson.GetBytes(input, field).Raw, gjson.GetBytes(clean, field).Raw, "a denied cache entry must not forget previously injected output")
		})
	}
}

func TestPromptPlatformChangeUsesBoundedNativeCarrierProof(t *testing.T) {
	for _, protocol := range []string{BusinessSystemPromptProtocolResponses, BusinessSystemPromptProtocolChat} {
		for _, form := range []string{"owned_output", "clean_changed", "unproven_server", "duplicate", "non_string", "oversized_unknown"} {
			t.Run(protocol+"/"+form, func(t *testing.T) {
				gateway, invoker, _ := newAccountScopedPromptGateway(t, 100)
				account, _ := promptScopeAccounts(t)
				input := []byte(`{"instructions":"client-control","input":"history"}`)
				field := "instructions"
				if protocol == BusinessSystemPromptProtocolChat {
					field = "messages"
					input = []byte(`{"messages":[{"role":"system","content":"client-control"},{"role":"user","content":"history"}]}`)
				}
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				output, applied, err := gateway.applyBusinessSystemPromptForRequest(ctx, input, account, protocol, false)
				require.NoError(t, err)
				require.True(t, applied.Applied)
				body := output
				if protocol == BusinessSystemPromptProtocolResponses {
					switch form {
					case "clean_changed":
						body = []byte(`{"instructions":"grok-client","input":"account-scoped-server"}`)
					case "unproven_server":
						body = []byte(`{"instructions":"grok-client\n\naccount-scoped-server","input":"history"}`)
					case "duplicate":
						body = []byte(`{"instructions":"grok-client","instructions":"account-scoped-server","input":"history"}`)
					case "non_string":
						body = []byte(`{"instructions":[{"type":"text","text":"grok-client"}],"input":"history"}`)
					case "oversized_unknown":
						body, err = json.Marshal(map[string]string{"instructions": strings.Repeat("other-client", businessSystemPromptRestoreMaxBytes), "input": "history"})
						require.NoError(t, err)
					}
				} else {
					switch form {
					case "clean_changed":
						body = []byte(`{"messages":[{"role":"system","content":"grok-client"},{"role":"user","content":"account-scoped-server"}]}`)
					case "unproven_server":
						body = []byte(`{"messages":[{"role":"system","content":"account-scoped-server"},{"role":"user","content":"other-client-history"}]}`)
					case "duplicate":
						body = []byte(`{"messages":[{"role":"system","content":"grok-client"}],"messages":[{"role":"system","content":"account-scoped-server"}]}`)
					case "non_string":
						body = []byte(`{"messages":[{"role":"system","content":[{"type":"text","text":"grok-client"}]}]}`)
					case "oversized_unknown":
						body, err = json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": strings.Repeat("other-history", businessSystemPromptRestoreMaxBytes)}}})
						require.NoError(t, err)
					}
				}
				before := append([]byte(nil), body...)
				grok := &Account{ID: account.ID + 1, Platform: PlatformGrok, Type: AccountTypeAPIKey}
				clean, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, body, grok, protocol, false)
				if form == "owned_output" {
					require.NoError(t, err)
					require.JSONEq(t, gjson.GetBytes(input, field).Raw, gjson.GetBytes(clean, field).Raw)
				} else if form == "clean_changed" {
					require.NoError(t, err)
					require.Equal(t, before, clean, "independent customer control and user history remain unchanged")
				} else {
					require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
					require.Nil(t, clean)
				}
				require.False(t, application.Applied)
				require.Equal(t, before, body)
				require.Len(t, invoker.calls, 1, "an excluded platform performs no prompt-policy RPC")
			})
		}
	}
}

func TestPromptUndoTracksTrimmedTextActuallyInserted(t *testing.T) {
	for _, protocol := range []string{BusinessSystemPromptProtocolResponses, BusinessSystemPromptProtocolChat} {
		t.Run(protocol, func(t *testing.T) {
			carrier := BusinessSystemPromptCarrierInstructions
			input := []byte(`{"instructions":"client","input":"history"}`)
			unknown := []byte(`{"instructions":"different-client\n\napproved-server","input":"history"}`)
			if protocol == BusinessSystemPromptProtocolChat {
				carrier = BusinessSystemPromptCarrierSystemMessage
				input = []byte(`{"messages":[{"role":"user","content":"history"}]}`)
				unknown = []byte(`{"messages":[{"role":"system","content":"approved-server"},{"role":"user","content":"different-history"}]}`)
			}
			// The public Application contract does not require trimmed text.
			// Both real local appliers nevertheless trim before insertion.
			snapshot := BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: " \tapproved-server\n "}
			application := BusinessSystemPromptApplication{Applied: true, Carrier: carrier, ServerInstructions: snapshot.Body, Revision: 1}
			output, application, err := applyBusinessSystemPromptApplication(input, application)
			require.NoError(t, err)
			if protocol == BusinessSystemPromptProtocolResponses {
				require.Equal(t, "client\n\napproved-server", gjson.GetBytes(output, "instructions").String())
			} else {
				require.Equal(t, "approved-server", gjson.GetBytes(output, "messages.0.content").String())
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			target := BusinessSystemPromptTarget{AccountID: 1, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Protocol: protocol}
			ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol), cacheBusinessSystemPromptState(input, output, snapshot, target, application))
			before := append([]byte(nil), unknown...)
			clean, err := restoreBusinessSystemPromptForExcludedTarget(ctx, unknown, protocol)
			require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
			require.Nil(t, clean)
			require.Equal(t, before, unknown)
		})
	}
}

func TestPromptCarrierTransitionCannotForgetEarlierAppliedOutput(t *testing.T) {
	for _, direction := range []string{"instructions_to_messages", "messages_to_instructions"} {
		for _, destination := range []string{"grok", "denied"} {
			t.Run(direction+"/"+destination, func(t *testing.T) {
				gateway, invoker, _ := newAccountScopedPromptGateway(t, 100)
				firstAccount, secondAccount := promptScopeAccounts(t)
				withInstructions := []byte(`{"instructions":"client-control","messages":[{"role":"user","content":"history"}]}`)
				withoutInstructions := []byte(`{"messages":[{"role":"user","content":"history"}]}`)
				firstInput, secondInput := withInstructions, withoutInstructions
				if direction == "messages_to_instructions" {
					firstInput, secondInput = withoutInstructions, withInstructions
				}
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				olderOutput, first, err := gateway.applyBusinessSystemPromptForRequest(ctx, firstInput, firstAccount, BusinessSystemPromptProtocolChat, false)
				require.NoError(t, err)
				require.True(t, first.Applied)
				_, second, err := gateway.applyBusinessSystemPromptForRequest(ctx, secondInput, secondAccount, BusinessSystemPromptProtocolChat, false)
				require.NoError(t, err)
				require.True(t, second.Applied)
				require.NotEqual(t, first.Carrier, second.Carrier, "the official planner selects by HasInstructions")
				require.Equal(t, first.ServerInstructions, second.ServerInstructions, "no arbitrary change to approved content is assumed")
				require.Equal(t, first.Revision, second.Revision)
				cached, _ := ctx.Get(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, BusinessSystemPromptProtocolChat))
				state := cached.(businessSystemPromptRequestState)
				require.True(t, state.undo.present && state.otherUndo.present)
				require.NotEqual(t, state.undo.carrier, state.otherUndo.carrier)
				require.LessOrEqual(t, len(state.undo.instructions)+len(state.otherUndo.instructions), businessSystemPromptRestoreMaxBytes)
				require.False(t, state.historyUncertain, "official frozen text is identical across the two native carriers")
				other := &Account{ID: secondAccount.ID + 10000, Platform: PlatformGrok, Type: AccountTypeAPIKey}
				if destination == "denied" {
					other.Platform = PlatformOpenAI
					invoker.installation.Bindings[0].RolloutPercent = 0
				}
				clean, application, err := gateway.applyBusinessSystemPromptForRequest(ctx, olderOutput, other, BusinessSystemPromptProtocolChat, false)
				if err != nil {
					require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
					require.Nil(t, clean)
				} else {
					require.Equal(t, gjson.GetBytes(firstInput, "instructions").Raw, gjson.GetBytes(clean, "instructions").Raw)
					require.JSONEq(t, gjson.GetBytes(firstInput, "messages").Raw, gjson.GetBytes(clean, "messages").Raw)
				}
				require.False(t, application.Applied)
			})
		}
	}
}

func TestPromptChangedServerHistoryRejectsOnlyUnprovenBodies(t *testing.T) {
	for _, protocol := range []string{BusinessSystemPromptProtocolResponses, BusinessSystemPromptProtocolChat} {
		t.Run(protocol, func(t *testing.T) {
			carrier := BusinessSystemPromptCarrierInstructions
			input := []byte(`{"instructions":"client-control","input":"history"}`)
			unknownClean := []byte(`{"instructions":"grok-client","input":"other-history"}`)
			if protocol == BusinessSystemPromptProtocolChat {
				carrier = BusinessSystemPromptCarrierSystemMessage
				input = []byte(`{"messages":[{"role":"user","content":"history"}]}`)
				unknownClean = []byte(`{"messages":[{"role":"system","content":"grok-client"},{"role":"user","content":"other-history"}]}`)
			}
			snapshot := BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "approved-server"}
			target := BusinessSystemPromptTarget{AccountID: 1, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey, Protocol: protocol}
			firstPlan := BusinessSystemPromptApplication{Applied: true, Carrier: carrier, ServerInstructions: "approved-server", Revision: 1}
			olderOutput, first, err := applyBusinessSystemPromptApplication(input, firstPlan)
			require.NoError(t, err)
			previous := cacheBusinessSystemPromptState(input, olderOutput, snapshot, target, first)
			// Defensive SDK-result test, not an assertion that the official
			// planner changes approved text. The public result structure and
			// host decoder do not impose equality with Snapshot.Body.
			nextPlan := firstPlan
			nextPlan.ServerInstructions = "[approved-server]"
			latestOutput, latest, err := applyBusinessSystemPromptApplication(input, nextPlan)
			require.NoError(t, err)
			next := inheritBusinessSystemPromptProvenance(cacheBusinessSystemPromptState(input, latestOutput, snapshot, target, latest), previous)
			require.True(t, next.historyUncertain)
			require.False(t, next.otherUndo.present, "same-carrier text revisions do not grow a history list")
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, protocol), next)
			for _, unproven := range [][]byte{olderOutput, unknownClean} {
				clean, err := restoreBusinessSystemPromptForExcludedTarget(ctx, unproven, protocol)
				require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
				require.Nil(t, clean)
			}
			original, err := restoreBusinessSystemPromptForExcludedTarget(ctx, input, protocol)
			require.NoError(t, err)
			require.Equal(t, input, original)
			restored, err := restoreBusinessSystemPromptForExcludedTarget(ctx, latestOutput, protocol)
			require.NoError(t, err)
			require.JSONEq(t, string(input), string(restored))
		})
	}
}

func TestPromptAppliedMetadataWithoutUndoIsNotCleanProof(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(businessSystemPromptContextKey(ctx, businessSystemPromptRequestApplicationKey, BusinessSystemPromptProtocolResponses), businessSystemPromptRequestState{
		snapshot:    BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "approved-server"},
		application: BusinessSystemPromptApplication{Applied: true, Carrier: BusinessSystemPromptCarrierInstructions, ServerInstructions: "approved-server", Revision: 1},
	})
	input := []byte(`{"instructions":"client\n\napproved-server","input":"history"}`)
	output, err := restoreBusinessSystemPromptForExcludedTarget(ctx, input, BusinessSystemPromptProtocolResponses)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
	require.Nil(t, output)
}
