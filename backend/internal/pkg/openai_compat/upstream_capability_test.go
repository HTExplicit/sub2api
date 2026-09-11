package openai_compat

import "testing"

func TestResolveResponsesSupport(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  AccountResponsesSupport
	}{
		{"nil extra", nil, ResponsesSupportUnknown},
		{"empty extra", map[string]any{}, ResponsesSupportUnknown},
		{"key missing", map[string]any{"other": "value"}, ResponsesSupportUnknown},
		{"value true", map[string]any{ExtraKeyResponsesSupported: true}, ResponsesSupportYes},
		{"value false", map[string]any{ExtraKeyResponsesSupported: false}, ResponsesSupportNo},
		{"value wrong type string", map[string]any{ExtraKeyResponsesSupported: "true"}, ResponsesSupportUnknown},
		{"value wrong type number", map[string]any{ExtraKeyResponsesSupported: 1}, ResponsesSupportUnknown},
		{"value nil", map[string]any{ExtraKeyResponsesSupported: nil}, ResponsesSupportUnknown},
		{"force responses", map[string]any{ExtraKeyResponsesMode: string(ResponsesSupportModeForceResponses)}, ResponsesSupportYes},
		{"force chat completions", map[string]any{ExtraKeyResponsesMode: string(ResponsesSupportModeForceChatCompletions)}, ResponsesSupportNo},
		{"auto follows probe", map[string]any{ExtraKeyResponsesMode: string(ResponsesSupportModeAuto), ExtraKeyResponsesSupported: false}, ResponsesSupportNo},
		{"invalid mode follows probe", map[string]any{ExtraKeyResponsesMode: "bogus", ExtraKeyResponsesSupported: true}, ResponsesSupportYes},
		{"force responses overrides probe false", map[string]any{ExtraKeyResponsesMode: string(ResponsesSupportModeForceResponses), ExtraKeyResponsesSupported: false}, ResponsesSupportYes},
		{"force chat completions overrides probe true", map[string]any{ExtraKeyResponsesMode: string(ResponsesSupportModeForceChatCompletions), ExtraKeyResponsesSupported: true}, ResponsesSupportNo},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveResponsesSupport(tc.extra)
			if got != tc.want {
				t.Errorf("ResolveResponsesSupport(%v) = %v, want %v", tc.extra, got, tc.want)
			}
		})
	}
}

func TestShouldUseResponsesAPI(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  bool
	}{
		// 关键不变量：未探测必须返回 true（保留旧行为）
		{"unknown defaults to true (preserve old behavior)", nil, true},
		{"unknown empty defaults to true", map[string]any{}, true},
		{"unknown wrong type defaults to true", map[string]any{ExtraKeyResponsesSupported: "yes"}, true},

		// 已探测：标记决定
		{"explicitly supported", map[string]any{ExtraKeyResponsesSupported: true}, true},
		{"explicitly unsupported", map[string]any{ExtraKeyResponsesSupported: false}, false},

		// 手动覆盖：覆盖自动探测结果
		{"force responses overrides unsupported probe", map[string]any{ExtraKeyResponsesMode: string(ResponsesSupportModeForceResponses), ExtraKeyResponsesSupported: false}, true},
		{"force chat completions overrides supported probe", map[string]any{ExtraKeyResponsesMode: string(ResponsesSupportModeForceChatCompletions), ExtraKeyResponsesSupported: true}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldUseResponsesAPI(tc.extra)
			if got != tc.want {
				t.Errorf("ShouldUseResponsesAPI(%v) = %v, want %v", tc.extra, got, tc.want)
			}
		})
	}
}

func TestNormalizeResponsesSupportMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want ResponsesSupportMode
	}{
		{"empty", "", ResponsesSupportModeAuto},
		{"auto", "auto", ResponsesSupportModeAuto},
		{"force responses", "force_responses", ResponsesSupportModeForceResponses},
		{"force chat completions", "force_chat_completions", ResponsesSupportModeForceChatCompletions},
		{"invalid", "enabled", ResponsesSupportModeAuto},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeResponsesSupportMode(tc.mode)
			if got != tc.want {
				t.Errorf("NormalizeResponsesSupportMode(%q) = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestResolveReasoningSummaryMode(t *testing.T) {
	tests := []struct {
		name     string
		extra    map[string]any
		official bool
		want     ReasoningSummaryMode
	}{
		{"official default passthrough", nil, true, ReasoningSummaryModePassthrough},
		{"compatible default auto", nil, false, ReasoningSummaryModeAuto},
		{"empty extra official", map[string]any{}, true, ReasoningSummaryModePassthrough},
		{"empty extra compatible", map[string]any{}, false, ReasoningSummaryModeAuto},
		{"explicit passthrough on compatible", map[string]any{ExtraKeyReasoningSummaryMode: string(ReasoningSummaryModePassthrough)}, false, ReasoningSummaryModePassthrough},
		{"explicit auto on official", map[string]any{ExtraKeyReasoningSummaryMode: string(ReasoningSummaryModeAuto)}, true, ReasoningSummaryModeAuto},
		{"explicit omit", map[string]any{ExtraKeyReasoningSummaryMode: string(ReasoningSummaryModeOmit)}, false, ReasoningSummaryModeOmit},
		{"invalid extra falls back official", map[string]any{ExtraKeyReasoningSummaryMode: "bogus"}, true, ReasoningSummaryModePassthrough},
		{"invalid extra falls back compatible", map[string]any{ExtraKeyReasoningSummaryMode: "bogus"}, false, ReasoningSummaryModeAuto},
		{"wrong type falls back compatible", map[string]any{ExtraKeyReasoningSummaryMode: true}, false, ReasoningSummaryModeAuto},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveReasoningSummaryMode(tc.extra, tc.official)
			if got != tc.want {
				t.Errorf("ResolveReasoningSummaryMode(%v, %v) = %q, want %q", tc.extra, tc.official, got, tc.want)
			}
		})
	}
}

func TestParseReasoningSummaryMode(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		want  ReasoningSummaryMode
		valid bool
	}{
		{"passthrough", "passthrough", ReasoningSummaryModePassthrough, true},
		{"auto", "auto", ReasoningSummaryModeAuto, true},
		{"omit", "omit", ReasoningSummaryModeOmit, true},
		{"empty", "", "", false},
		{"invalid", "detailed", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := ParseReasoningSummaryMode(tc.mode)
			if got != tc.want || valid != tc.valid {
				t.Errorf("ParseReasoningSummaryMode(%q) = (%q, %v), want (%q, %v)", tc.mode, got, valid, tc.want, tc.valid)
			}
		})
	}
}
