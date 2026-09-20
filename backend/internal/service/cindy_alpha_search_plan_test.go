package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type cindyAlphaSearchPlanInvoker struct {
	invocation extensionv1.Invocation
	plan       CindyAlphaSearchPlan
}

func (f *cindyAlphaSearchPlanInvoker) InvokeOperation(_ context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	f.invocation = in
	raw, err := json.Marshal(f.plan)
	return extensionv1.Result{Payload: raw}, err
}

func TestResolveCindyAlphaSearchPlanSendsOnlyRequestedModel(t *testing.T) {
	fixture := &cindyAlphaSearchPlanInvoker{plan: CindyAlphaSearchPlan{
		Allowed:                         true,
		RequestedModel:                  "gpt-5.6-luna",
		UpstreamModel:                   "openai/gpt-5.6-luna",
		PrimaryProtocol:                 "responses",
		FallbackProtocol:                "messages",
		FallbackOnCapabilityMiss:        true,
		FallbackOnMissingSearchEvidence: true,
		ResponsesToolType:               "web_search",
		NativeMessagesModel:             CindyWebSearchModel,
		MaxSearchUses:                   1,
	}}
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })

	plan, err := resolveCindyAlphaSearchPlanForAccount(context.Background(), "gpt-5.6-luna", 42)
	require.NoError(t, err)
	require.Equal(t, fixture.plan, plan)
	require.Equal(t, extensionv1.CapabilityProvider, fixture.invocation.Capability)
	require.Equal(t, "cindy.search.plan", fixture.invocation.Operation)
	require.Equal(t, int64(42), fixture.invocation.AccountID)
	require.JSONEq(t, `{"model":"gpt-5.6-luna"}`, string(fixture.invocation.Payload))
}

func TestValidateCindyAlphaSearchPlanRejectsUnboundedOrUnexpectedFallback(t *testing.T) {
	base := CindyAlphaSearchPlan{
		Allowed:           true,
		RequestedModel:    "gpt-5.6-luna",
		UpstreamModel:     "openai/gpt-5.6-luna",
		PrimaryProtocol:   "responses",
		ResponsesToolType: "web_search",
		MaxSearchUses:     1,
	}
	require.NoError(t, validateCindyAlphaSearchPlan("gpt-5.6-luna", base))

	tooMany := base
	tooMany.MaxSearchUses = 2
	require.Error(t, validateCindyAlphaSearchPlan("gpt-5.6-luna", tooMany))

	unexpectedFallback := base
	unexpectedFallback.NativeMessagesModel = CindyWebSearchModel
	require.Error(t, validateCindyAlphaSearchPlan("gpt-5.6-luna", unexpectedFallback))
}

func TestCindyAlphaSearchBodiesUseProviderPlanFields(t *testing.T) {
	plan := &CindyAlphaSearchPlan{
		ResponsesToolType:   "web_search",
		NativeMessagesModel: CindyWebSearchModel,
		MaxSearchUses:       1,
	}
	responses, err := buildOpenAIAlphaSearchResponsesWebSearchBody([]byte(`{"commands":{}}`), "openai/gpt-5.6-luna", plan)
	require.NoError(t, err)
	require.Equal(t, "web_search", gjson.GetBytes(responses, "tools.0.type").String())
	require.False(t, gjson.GetBytes(responses, "tools.0.max_uses").Exists())

	native, err := buildCindyAlphaSearchMessagesBody([]byte(`{"commands":{}}`), plan)
	require.NoError(t, err)
	require.Equal(t, CindyWebSearchModel, gjson.GetBytes(native, "model").String())
	require.Equal(t, float64(1), gjson.GetBytes(native, "tools.0.max_uses").Float())
}
