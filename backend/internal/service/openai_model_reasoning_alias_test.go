package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMappedForwardReasoningAliasAppliesPolicyOnce(t *testing.T) {
	ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "high", []ReasoningEffortMapping{{From: "xhigh", To: "high"}, {From: "high", To: "medium"}}, "downgrade")
	body := []byte(`{"model":"public-sol","input":"hi"}`)
	got, changed, err := materializeOpenAIForwardReasoningEffort(ctx, body, "gpt-5.6-sol-max")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "high", gjson.GetBytes(got, "reasoning.effort").String())
	second, changed, err := materializeOpenAIForwardReasoningEffort(ctx, got, "gpt-5.6-sol-max")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(got), string(second), "an ingress-governed value cannot pass a second non-idempotent mapping")
	deny := WithOpenAIReasoningEffortPolicy(context.Background(), "high", nil, "deny")
	_, _, err = materializeOpenAIForwardReasoningEffort(deny, body, "gpt-5.6-sol-max")
	require.Error(t, err)
}

func TestResolveOpenAIModelReasoningAliasIsExplicit(t *testing.T) {
	for _, tt := range []struct {
		model, base, effort string
	}{
		{"gpt-5-minimal", "gpt-5", "minimal"},
		{"gpt-5.4-none", "gpt-5.4", "none"},
		{"openai/gpt-5.3-codex-xhigh", "gpt-5.3-codex", "xhigh"},
		{"gpt-5.6-sol-max", "gpt-5.6-sol", "max"},
		{"gpt-5.4-extrahigh", "gpt-5.4", "xhigh"},
		{"gpt-6-astra-max", "gpt-6-astra", "max"},
		{"gpt 5.4 high", "gpt-5.4", "high"},
		{"gpt-5.1-codex-max", "", ""},
		{"custom-model-high", "", ""},
		{"vendor/gpt-5.6-sol-high", "", ""},
		{"router/openai/gpt-5.6-sol-high", "", ""},
		{"gpt-5.5-custom-high", "", ""},
		{"gpt-5.4-2026-01-01", "", ""},
		{"gpt-5.4-high-low", "", ""},
	} {
		t.Run(tt.model, func(t *testing.T) {
			base, effort, ok := resolveOpenAIModelReasoningAlias(tt.model)
			require.Equal(t, tt.base != "", ok)
			require.Equal(t, tt.base, base)
			require.Equal(t, tt.effort, effort)
		})
	}
}

func TestNormalizeOpenAIModelForUpstreamReasoningAlias(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, tt := range []struct{ model, expected string }{
		{"gpt-5-minimal", "gpt-5"},
		{"gpt-5.6-sol-xhigh", "gpt-5.6-sol"},
		{"gpt-6-astra-max", "gpt-6-astra"},
		{"gpt-5.1-codex-max", "gpt-5.1-codex-max"},
		{"gpt-5.5-custom-high", "gpt-5.5-custom-high"},
		{"vendor/gpt-6-astra-max", "vendor/gpt-6-astra-max"},
	} {
		require.Equal(t, tt.expected, normalizeOpenAIModelForUpstream(account, tt.model))
	}
}

func TestReasoningAliasSchedulingMatchesPassthroughWire(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_passthrough": true}}
	for _, tc := range []struct{ requested, expected string }{
		{"gpt-5.6-sol-xhigh", "gpt-5.6-sol"},
		{"gpt-6-astra-max", "gpt-6-astra"},
		{"gpt-5.1-codex-max", "gpt-5.1-codex-max"},
		{"vendor/gpt-6-astra-max", "vendor/gpt-6-astra-max"},
	} {
		require.Equal(t, tc.expected, resolveOpenAIAccountUpstreamModelForRequest(account, tc.requested, false))
	}
}

func TestMaterializeOpenAIModelReasoningEffortPreservesNativeIntent(t *testing.T) {
	for _, tt := range []struct {
		name, body, effort string
		changed            bool
	}{
		{"known suffix", `{"model":"gpt-5.6-sol-xhigh","reasoning":{"mode":"pro","context":"all_turns","future":{"nonce":9007199254740993}}}`, "xhigh", true},
		{"explicit wins", `{"model":"gpt-5.6-sol-max","reasoning":{"effort":"minimal","summary":"auto"}}`, "minimal", false},
		{"explicit none wins", `{"model":"gpt-5.6-sol-max","reasoning":{"effort":"none"}}`, "none", false},
		{"explicit unknown wins", `{"model":"gpt-5.6-sol-max","reasoning":{"effort":"future"}}`, "future", false},
		{"explicit null wins", `{"model":"gpt-5.6-sol-max","reasoning":{"effort":null}}`, "", false},
		{"flat wins", `{"model":"gpt-5.6-sol-max","reasoning_effort":"low"}`, "", false},
		{"malformed reasoning preserved", `{"model":"gpt-5.6-sol-max","reasoning":null}`, "", false},
		{"custom name unchanged", `{"model":"custom-gpt-5.6-sol-high"}`, "", false},
		{"no defaults injected", `{"model":"gpt-6-astra","input":"hi"}`, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := materializeOpenAIModelReasoningEffort([]byte(tt.body))
			require.NoError(t, err)
			require.Equal(t, tt.changed, changed)
			require.Equal(t, tt.effort, gjson.GetBytes(got, "reasoning.effort").String())
			require.Equal(t, gjson.Get(tt.body, "model").String(), gjson.GetBytes(got, "model").String(), "billing sees original alias")
			if !tt.changed {
				require.Equal(t, tt.body, string(got))
			}
			if field := gjson.Get(tt.body, "reasoning.future"); field.Exists() {
				require.Equal(t, field.Raw, gjson.GetBytes(got, "reasoning.future").Raw)
				require.Equal(t, "pro", gjson.GetBytes(got, "reasoning.mode").String())
				require.Equal(t, "all_turns", gjson.GetBytes(got, "reasoning.context").String())
			}
			second, again, err := materializeOpenAIModelReasoningEffort(got)
			require.NoError(t, err)
			require.False(t, again)
			require.Equal(t, string(got), string(second))
		})
	}
}

func TestMaterializeOpenAIModelReasoningEffortMappedAlias(t *testing.T) {
	body := []byte(`{"model":"public-sol","reasoning":{"summary":"auto"}}`)
	got, changed, err := materializeOpenAIModelReasoningEffort(body, "gpt-5.6-sol-xhigh")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "public-sol", gjson.GetBytes(got, "model").String())
	require.Equal(t, "xhigh", gjson.GetBytes(got, "reasoning.effort").String())
	require.Equal(t, "auto", gjson.GetBytes(got, "reasoning.summary").String())
}

func TestMaterializeOpenAIModelReasoningEffortUsesActualProtocolField(t *testing.T) {
	chat := []byte(`{"model":"gpt-5.6-sol-xhigh","messages":[{"role":"user","content":"hi"}]}`)
	normalized, changed, err := materializeOpenAIModelReasoningEffort(chat)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "xhigh", gjson.GetBytes(normalized, "reasoning_effort").String())
	require.False(t, gjson.GetBytes(normalized, "reasoning.effort").Exists())
	policyBody, _, err := ApplyOpenAIReasoningEffortPolicy(chat, "high", nil, "")
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(policyBody, "reasoning_effort").String())
	responses := []byte(`{"model":"gpt-5.6-sol-xhigh","input":"native","messages":[]}`)
	normalized, changed, err = materializeOpenAIModelReasoningEffort(responses)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "xhigh", gjson.GetBytes(normalized, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(normalized, "reasoning_effort").Exists())
}

func TestOpenAIReasoningAliasRespectsExplicitGroupPolicy(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol-max","reasoning":{"context":"all_turns","mode":"pro","future":123}}`)
	got, changed, err := ApplyOpenAIReasoningEffortPolicy(body, "high", []ReasoningEffortMapping{{From: "max", To: "xhigh"}}, "downgrade")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "high", gjson.GetBytes(got, "reasoning.effort").String())
	require.Equal(t, "all_turns", gjson.GetBytes(got, "reasoning.context").String())
	require.Equal(t, "pro", gjson.GetBytes(got, "reasoning.mode").String())
	require.Equal(t, int64(123), gjson.GetBytes(got, "reasoning.future").Int())
	rejected, changed, err := ApplyOpenAIReasoningEffortPolicy(body, "high", nil, "deny")
	require.Error(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(rejected))
}

func TestApplyCodexOAuthTransformReasoningAliasMaterializesWire(t *testing.T) {
	for _, tt := range []struct {
		model, effort, expected string
	}{
		{"gpt-5.6-sol-xhigh", "", "xhigh"},
		{"gpt-5.6-sol-max", "minimal", "minimal"},
		{"gpt-6-astra-max", "max", "max"},
	} {
		t.Run(tt.model+tt.effort, func(t *testing.T) {
			reasoning := map[string]any{"context": "all_turns", "future": json.Number("9007199254740993")}
			if tt.effort != "" {
				reasoning["effort"] = tt.effort
			}
			req := map[string]any{"model": tt.model, "reasoning": reasoning, "instructions": "test", "input": "hello"}
			result := applyCodexOAuthTransform(req, true, false)
			require.NoError(t, result.Error)
			base, _, _ := resolveOpenAIModelReasoningAlias(tt.model)
			require.Equal(t, base, req["model"])
			next := req["reasoning"].(map[string]any)
			require.Equal(t, tt.expected, next["effort"])
			require.Equal(t, "all_turns", next["context"])
			require.Equal(t, json.Number("9007199254740993"), next["future"])
			if tt.effort == "" {
				require.NotContains(t, reasoning, "effort", "nested source map remains immutable")
			}
		})
	}
}

func TestEffectiveReasoningMetadataPreservesWireValues(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "xhigh", "max"} {
		body := []byte(`{"model":"custom-high","reasoning":{"effort":"` + effort + `"}}`)
		got := extractOpenAIReasoningEffortFromBody(body, "gpt-5.4")
		require.NotNil(t, got)
		require.Equal(t, effort, *got)
	}
	require.Nil(t, extractOpenAIReasoningEffortFromBody([]byte(`{"model":"gpt-5.4-xhigh"}`), "gpt-5.4-xhigh"))
	require.Nil(t, CanonicalRequestedReasoningEffort([]byte(`{"model":"custom-high"}`)))
	require.Nil(t, CanonicalRequestedReasoningEffort([]byte(`{"model":"gpt-5.4-high","reasoning":{"effort":null}}`)))
}
