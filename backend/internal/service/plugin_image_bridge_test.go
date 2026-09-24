package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestImageBridgeUsesOnlyBoundedFacts(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	body := []byte(`{"model":"gpt-5.6-luna","input":"private-prompt","reasoning":{"encrypted_content":"private-cipher"},"tools":[{"type":"image_generation","model":"gpt-image-2","mask":"private-image","quality":{"text":"private-object"}}]}`)
	account := cindyHTTPToWSV2TestAccount()
	account.ID = 37
	_, err := ResolveCindyResponsesImageToolsForAccount(context.Background(), account, body)
	require.ErrorIs(t, err, ErrCindyResponsesImageToolModelNotFound)
	var request map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"model":"gpt-5.6-luna","tools":[{"type":"image_generation"}]}`), &request))
	tools, ok := request["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	tool["model"] = strings.Repeat("private-model", 1000)
	facts, err := imageBridgeFacts(request, "validate")
	require.NoError(t, err)
	require.True(t, facts.Tools[0].InvalidModel)
	require.Empty(t, facts.Tools[0].Model)
	processExtensionOperations.Store(nil)
	_, err = ResolveCindyResponsesImageTools(body)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	text := []byte(`{"model":"gpt-5.6-luna", "input":"keep text byte stable"}`)
	resolved, err := ResolveCindyResponsesImageTools(text)
	require.NoError(t, err)
	require.Equal(t, text, resolved)
}

func TestImageBridgeAccountScopeIsCheckedBeforeCachedPlansAndLeases(t *testing.T) {
	calls := 0
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		calls++
		return extensionv1.Result{Payload: []byte(`{}`)}, nil
	})
	installation := manager.extensions.Load().installations[1]
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityRequest: {"image.responses.plan"}}
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: PlatformCindy, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 100}}
	in := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "image.responses.plan", AccountID: 37, Payload: []byte(`{}`)}
	_, err := manager.InvokeCachedOperation(context.Background(), PlatformCindy, AccountTypeAPIKey, in)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	installation.Bindings[0].RolloutPercent = int(stablePluginBucket(37))
	_, err = manager.InvokeCachedOperation(context.Background(), PlatformCindy, AccountTypeAPIKey, in)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	_, _, err = manager.BindOperationContext(context.Background(), PlatformCindy, AccountTypeAPIKey, in)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	require.Equal(t, 1, calls)
}

func TestImageBridgePlanCannotChangeBillingIdentityOrUnrelatedTools(t *testing.T) {
	request := extensionv1.ImageBridgeRequest{Model: "luna", Capabilities: []extensionv1.CindyCapability{
		{PublicID: "luna", LiveUpstreamID: "openai/luna", PublicModel: true, Kind: CindyModelKindText},
		{PublicID: "image", LiveUpstreamID: "openai/image", PublicModel: true, Kind: CindyModelKindImage},
	}, Tools: []extensionv1.ImageBridgeTool{{Index: 1, Model: "image"}}}
	plan := extensionv1.ImageBridgePlan{Model: "openai/luna", Tools: []extensionv1.ImageBridgeModelPlan{{Index: 1, Model: "openai/image"}}}
	require.True(t, validImageBridgePlan(request, plan))
	plan.Model = "openai/image"
	require.False(t, validImageBridgePlan(request, plan))
	plan.Model = "openai/luna"
	plan.Tools[0].Index = 0
	require.False(t, validImageBridgePlan(request, plan))
	plan.Tools[0].Index = 1
	plan.Tools = append(plan.Tools, plan.Tools[0])
	require.False(t, validImageBridgePlan(request, plan))
}

func TestImageBridgePlanRejectsInvalidTargetsBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		index  int
		target any
	}{
		{name: "negative index", index: -1, target: map[string]any{}},
		{name: "out of bounds", index: 1, target: map[string]any{}},
		{name: "non-object target", target: "invalid"},
		{name: "nil object", target: map[string]any(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{"model": "original", "n": 2, "tools": []any{test.target}}
			before, err := json.Marshal(body)
			require.NoError(t, err)
			changed, err := applyImageBridgePlan(body, extensionv1.ImageBridgePlan{
				Model: "replacement", StripCount: true,
				Tools: []extensionv1.ImageBridgeModelPlan{{Index: test.index, Model: "replacement"}},
			})
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
			require.False(t, changed)
			after, err := json.Marshal(body)
			require.NoError(t, err)
			require.Equal(t, before, after, "an invalid target must leave the complete request untouched")
		})
	}
}
