package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAICompatAnthropicReasoningFidelity(t *testing.T) {
	for _, tc := range []struct {
		name, model, requested, converted, want string
	}{
		{"astra_max", "gpt-6-astra", "max", "xhigh", "max"},
		{"sol_max", "gpt-5.6-sol", "max", "xhigh", "max"},
		{"sol_alias_max", "gpt-5.6", "max", "xhigh", "max"},
		{"sol_xhigh", "gpt-5.6-sol", "xhigh", "xhigh", "xhigh"},
		{"legacy_scale", "gpt-5.2", "max", "xhigh", "xhigh"},
		{"unknown_destination", "custom-model", "max", "xhigh", "xhigh"},
		{"resolved_destination_controls_scale", "gpt-5.2", "max", "xhigh", "xhigh"},
		{"default_not_promoted", "gpt-6-astra", "", "medium", "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &apicompat.AnthropicRequest{
				Model:        "client-alias",
				OutputConfig: &apicompat.AnthropicOutputConfig{Effort: tc.requested},
			}
			require.Equal(t, tc.want, openAICompatAnthropicReasoningEffort(req, tc.model, tc.converted))
			require.Equal(t, tc.requested, req.OutputConfig.Effort)
		})
	}
}

func TestOpenAICompatAnthropicReasoningFidelity_Aliases(t *testing.T) {
	for _, tc := range []struct {
		name, model, explicit, wantModel, wantEffort string
	}{
		{"sol_xhigh_stays_xhigh", "gpt-5.6-sol-xhigh", "", "gpt-5.6-sol", "xhigh"},
		{"astra_max_stays_max", "gpt-6-astra-max", "", "gpt-6-astra", "max"},
		{"sol_none_is_not_discarded", "gpt-5.6-sol-none", "", "gpt-5.6-sol", "none"},
		{"minimal_is_not_none", "gpt-5-minimal", "", "gpt-5", "minimal"},
		{"explicit_wins", "gpt-5.6-sol-xhigh", "low", "gpt-5.6-sol", "low"},
		{"explicit_modern_max", "gpt-5.6-sol-xhigh", "max", "gpt-5.6-sol", "max"},
		{"legacy_xhigh", "gpt-5.4-xhigh", "", "gpt-5.4", "xhigh"},
		{"legacy_extrahigh", "gpt-5.4-extrahigh", "", "gpt-5.4", "xhigh"},
		{"native_model_max_is_not_effort", "gpt-5.1-codex-max", "", "gpt-5.1-codex-max", "medium"},
		{"unknown_model_not_rewritten", "gpt-custom-high", "", "gpt-custom-high", "medium"},
		{"non_openai_not_rewritten", "custom-high", "", "custom-high", "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &apicompat.AnthropicRequest{
				Model: tc.model,
				Messages: []apicompat.AnthropicMessage{
					{Role: "user", Content: json.RawMessage(`"hello"`)},
				},
			}
			if tc.explicit != "" {
				req.OutputConfig = &apicompat.AnthropicOutputConfig{Effort: tc.explicit}
			}
			applyOpenAICompatModelNormalization(req)
			require.Equal(t, tc.wantModel, req.Model)
			converted, err := apicompat.AnthropicToResponses(req)
			require.NoError(t, err)
			converted.Reasoning.Effort = openAICompatAnthropicReasoningEffort(req, tc.wantModel, converted.Reasoning.Effort)
			wire, err := json.Marshal(converted)
			require.NoError(t, err)
			require.Equal(t, tc.wantModel, gjson.GetBytes(wire, "model").String())
			require.Equal(t, tc.wantEffort, gjson.GetBytes(wire, "reasoning.effort").String())
		})
	}
}
