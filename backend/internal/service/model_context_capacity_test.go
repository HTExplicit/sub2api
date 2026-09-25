package service

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestModelContextCapacityPriorityAndDistinctLimits(t *testing.T) {
	registry := ModelContextCapacity{ContextWindow: 2000000, MaxOutputTokens: 100000}
	upstream := ModelContextCapacity{ContextWindow: 128000, MaxContextWindow: 256000, MaxInputTokens: 120000, MaxOutputTokens: 8000}
	accountFor := func(baseURL string, custom map[string]int64, observed, registryValue *ModelContextCapacity) *Account {
		models := make(map[string]UpstreamModelMetadata)
		if observed != nil {
			models["gpt-5.5"] = UpstreamModelMetadata{ID: "gpt-5.5", ContextWindow: observed.ContextWindow, MaxContextWindow: observed.MaxContextWindow,
				MaxInputTokens: observed.MaxInputTokens, MaxOutputTokens: observed.MaxOutputTokens, CapacitySource: ModelContextSourceUpstream}
		}
		if registryValue != nil {
			models["gpt-5.5-unlisted"] = UpstreamModelMetadata{ID: "gpt-5.5-unlisted", ContextWindow: registryValue.ContextWindow,
				MaxOutputTokens: registryValue.MaxOutputTokens, CapacitySource: ModelContextSourceRegistry}
		}
		return &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": baseURL}, Extra: map[string]any{
			ModelContextOverridesExtraKey: custom,
			UpstreamModelMetadataExtraKey: UpstreamModelMetadataSnapshot{Source: "models.dev", Models: models},
		}}
	}
	// The same order applies on the vendor's host and on a relay.
	for _, baseURL := range []string{"https://api.openai.com/v1", "https://relay.example/v1"} {
		cases := []struct {
			name, model, source string
			account             *Account
			window, maximum     int64
			output              int64
		}{
			{"custom over upstream", "gpt-5.5", "custom", accountFor(baseURL, map[string]int64{"gpt-5.5": 768000}, &upstream, nil), 768000, 768000, 8000},
			{"upstream over catalog", "gpt-5.5", "upstream", accountFor(baseURL, nil, &upstream, nil), 128000, 256000, 8000},
			{"catalog is the Codex subscription window", "gpt-5.5", "official", accountFor(baseURL, nil, nil, nil), 272000, 272000, 0},
			{"catalog default and real maximum stay distinct", "gpt-5.4", "official", accountFor(baseURL, nil, nil, nil), 272000, 1000000, 0},
			{"registry below catalog", "gpt-5.5-unlisted", "registry", accountFor(baseURL, nil, nil, &registry), 2000000, 2000000, 100000},
			{"unknown is not invented", "never-seen", "", accountFor(baseURL, nil, nil, nil), 0, 0, 0},
		}
		for _, test := range cases {
			t.Run(baseURL+"/"+test.name, func(t *testing.T) {
				got := ResolveAccountModelContextCapacity(test.account, test.model)
				if got.Source != test.source || got.ContextWindow != test.window || got.MaxContextWindow != test.maximum || got.MaxOutputTokens != test.output {
					t.Fatalf("unexpected resolution: %+v", got)
				}
			})
		}
	}
	inputOnly := resolveModelContextEvidence([]modelContextEvidence{
		{ModelContextSourceUpstream, ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536}},
		{ModelContextSourceOfficial, upstream},
	})
	if inputOnly.ContextWindow != 1048576 || inputOnly.CapacityBasis != "input_limit" || inputOnly.Source != "upstream" || inputOnly.MaxOutputTokens != 65536 {
		t.Fatalf("input budget was misrepresented or lost to lower evidence: %+v", inputOnly)
	}
	outputOnly := resolveModelContextEvidence([]modelContextEvidence{
		{ModelContextSourceUpstream, ModelContextCapacity{MaxOutputTokens: 8192}},
		{ModelContextSourceOfficial, ModelContextCapacity{ContextWindow: 400000}},
	})
	if outputOnly.Source != "official" || outputOnly.ContextWindow != 400000 || outputOnly.MaxOutputTokens != 8192 {
		t.Fatalf("an output limit alone is not a window but keeps its evidence: %+v", outputOnly)
	}
	smallCustom := resolveModelContextEvidence([]modelContextEvidence{
		{ModelContextSourceCustom, ModelContextCapacity{ContextWindow: 100000, MaxContextWindow: 100000}},
		{ModelContextSourceUpstream, ModelContextCapacity{MaxInputTokens: 500000, MaxOutputTokens: 8192}},
	})
	if smallCustom.MaxInputTokens != 0 || smallCustom.MaxOutputTokens != 8192 {
		t.Fatalf("incompatible independent limits were clamped or not safely omitted: %+v", smallCustom)
	}
}

func TestModelContextCapacityParserSafeNumbersAndFieldTolerance(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{"258000", 258000}, {"258000.0", 258000}, {"2.58e5", 258000}, {"2.58e+005", 258000},
		{"9007199254740991", MaxSafeModelContextTokens}, {"9007199254740992", 0},
		{"9007199254740990.99", 0}, {"0", 0}, {"-1", 0}, {"1.5", 0}, {"1e9999999", 0},
		{`"258000"`, 0}, {"true", 0}, {"null", 0}, {"{}", 0}, {"[]", 0},
	}
	for _, test := range cases {
		if got := parsePositiveSafeModelContextTokens(json.RawMessage(test.raw)); got != test.want {
			t.Errorf("%s: got %d, want %d", test.raw, got, test.want)
		}
	}
	value := ParseUpstreamModelContextCapacity(json.RawMessage(`{"context_window":"broken","max_context_window":500000,"max_input_tokens":true,"max_output_tokens":12000,"max_tokens":9876,"usage":{"input_tokens":999}}`), PlatformOpenAI)
	if value.ContextWindow != 0 || value.MaxContextWindow != 500000 || value.MaxInputTokens != 0 || value.MaxOutputTokens != 12000 {
		t.Fatalf("one bad field discarded unrelated explicit limits: %+v", value)
	}
	value = ParseUpstreamModelContextCapacity(json.RawMessage(`{"inputTokenLimit":1048576,"outputTokenLimit":65536}`), PlatformGemini)
	if value.ContextWindow != 0 || value.MaxInputTokens != 1048576 || value.CapacityBasis != "input_limit" {
		t.Fatalf("Gemini input limit semantics lost: %+v", value)
	}
	value = ParseUpstreamModelContextCapacity(json.RawMessage(`{"max_tokens":128000}`), PlatformOpenAI)
	if modelContextCapacityHasLimits(value) {
		t.Fatalf("generic max_tokens used as a model limit: %+v", value)
	}
	value = ParseUpstreamModelContextCapacity(json.RawMessage(`{"max_input_tokens":200000,"max_tokens":128000}`), PlatformAnthropic)
	if value.ContextWindow != 0 || value.MaxOutputTokens != 128000 || value.MaxInputTokens != 200000 {
		t.Fatalf("Anthropic output-model field semantics lost: %+v", value)
	}
	value = ParseUpstreamModelContextCapacity(json.RawMessage(`{"contextWindow":500000,"maxContextWindow":1000000,"limit":{"context":4,"input":300000,"output":64000}}`), PlatformOpenAI)
	if value.ContextWindow != 500000 || value.MaxContextWindow != 1000000 || value.MaxInputTokens != 300000 || value.MaxOutputTokens != 64000 {
		t.Fatalf("camel/nested limits failed: %+v", value)
	}
	value = ParseUpstreamModelContextCapacity(json.RawMessage(`{"id":"anthropic/claude-opus-4.6","context_length":null,"top_provider":{"context_length":1000000,"max_completion_tokens":128000}}`), PlatformOpenAI)
	if value.ContextWindow != 1000000 || value.MaxOutputTokens != 128000 {
		t.Fatalf("OpenRouter top_provider limits were not read: %+v", value)
	}
}

func TestModelContextCapacityCatalogParserKeepsIndependentModels(t *testing.T) {
	body := []byte(`{"data":[{"id":"a","context_window":512000,"reasoning":{}},{"id":"b","context_window":"bad","max_output_tokens":64000},{"id":"a","context_window":258000,"max_context_window":1000000},{"id":"no-capacity","max_tokens":9999}]}`)
	values, err := ParseUpstreamModelContextCapacities(body, PlatformOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values["a"].ContextWindow != 258000 || values["a"].MaxContextWindow != 1000000 || values["b"].MaxOutputTokens != 64000 {
		t.Fatalf("unexpected tolerant parse: %+v", values)
	}
	values, err = ParseUpstreamModelContextCapacities([]byte(`{"models":[{"id":"display-id","model":"grok-4.6","contextWindow":500000}]}`), PlatformGrok)
	if err != nil || values["grok-4.6"].ContextWindow != 500000 {
		t.Fatalf("Grok protocol ID selector diverged: %+v %v", values, err)
	}
	values, err = ParseUpstreamModelContextCapacities([]byte(`{"models":[{"name":"models/gemini-3.8-flash","inputTokenLimit":1048576}]}`), PlatformGemini)
	if err != nil || values["gemini-3.8-flash"].MaxInputTokens != 1048576 {
		t.Fatalf("Gemini model name normalization diverged: %+v %v", values, err)
	}
}

func TestModelContextCapacityOverridesPatchValidationAndIsolation(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	value := int64(1050000)
	if err := ValidateModelContextOverrides(account, map[string]*int64{"real-model": &value}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id    string
		value int64
	}{{"*", 1}, {" bad", 1}, {"", 1}, {"bad\n", 1}, {"good", 0}, {"good", -1}, {"good", MaxSafeModelContextTokens + 1}} {
		value := test.value
		if ValidateModelContextOverrides(account, map[string]*int64{test.id: &value}) == nil {
			t.Errorf("accepted invalid override %q=%d", test.id, value)
		}
	}
	existing := map[string]int64{"keep": 123000, "change": 400000, "delete": 200000}
	patch := map[string]*int64{"change": &value, "delete": nil}
	got, err := ApplyModelContextOverrides(existing, patch)
	if err != nil || got["keep"] != 123000 || got["change"] != value || len(got) != 2 {
		t.Fatalf("incremental patch failed: %+v %v", got, err)
	}
	if existing["delete"] != 200000 || existing["change"] != 400000 {
		t.Fatal("patch mutated the old snapshot")
	}
	account.Type = AccountTypeOAuth
	if ValidateModelContextOverrides(account, map[string]*int64{"gpt-6-sol": &value}) != nil {
		t.Fatal("an OAuth account must accept overrides like any other account")
	}
}

func TestModelContextCapacityRowsUseTrueTargetsAndPreserveSources(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-one": "real", "public-two": "real", "gpt-5.6-sol": "unknown-actual"}},
		Extra: map[string]any{
			ModelContextOverridesExtraKey: map[string]int64{"real": 600000, "custom-only": 750000},
			UpstreamModelMetadataExtraKey: UpstreamModelMetadataSnapshot{Source: "models.dev", Models: map[string]UpstreamModelMetadata{
				"real":          {ID: "real", ContextWindow: 100000, CapacitySource: ModelContextSourceUpstream, ObservedAt: "2026-09-06T01:00:00Z"},
				"registry-only": {ID: "registry-only", ContextWindow: 900000},
			}},
		},
	}
	rows := BuildAccountModelContextCapacityRows(account, []string{"registry-only"})
	byID := make(map[string]AccountModelContextCapacityRow)
	for _, row := range rows {
		byID[row.UpstreamModelID] = row
	}
	row := byID["real"]
	if row.Upstream == nil || row.Upstream.ContextWindow != 100000 || row.Upstream.ObservedAt != "2026-09-06T01:00:00Z" || row.EffectiveContextWindow != 600000 || row.AutomaticContextWindow != 100000 || !row.Editable {
		t.Fatalf("raw/custom/automatic provenance lost: %+v", row)
	}
	if !reflect.DeepEqual(row.Aliases, []string{"public-one", "public-two"}) {
		t.Fatalf("mapped aliases don't share real override: %+v", row.Aliases)
	}
	if registryRow := byID["registry-only"]; registryRow.Registry == nil || registryRow.Upstream != nil || registryRow.EffectiveSource != "registry" || registryRow.EffectiveContextWindow != 900000 {
		t.Fatalf("an enriched entry without provenance must stay a registry reference: %+v", registryRow)
	}
	if unknown := byID["unknown-actual"]; unknown.EffectiveSource != "" || unknown.EffectiveContextWindow != 0 || unknown.Upstream != nil || unknown.Official != nil {
		t.Errorf("capacity inferred from a public alias: %+v", unknown)
	}
	if byID["custom-only"].EffectiveContextWindow != 750000 {
		t.Fatal("custom-only model disappeared from management rows")
	}
}

func TestModelContextCapacityOfficialKimiCodingNeedsProductIdentity(t *testing.T) {
	for _, test := range []struct {
		name     string
		platform string
		baseURL  string
		mode     string
		want     bool
	}{
		{"Kimi coding relay", PlatformKimi, "https://custom-kimi.invalid", AccountModeCoding, true},
		{"generic official coding endpoint", PlatformOpenAI, "https://api.kimi.com/coding/v1", "", true},
		{"unknown generic coding vendor", PlatformOpenAI, "https://unknown-vendor.invalid", AccountModeCoding, false},
		{"Kimi payg is separate", PlatformKimi, "https://api.moonshot.cn/v1", AccountModePayG, false},
		{"lookalike official host", PlatformOpenAI, "https://api.kimi.com.attacker.invalid/coding", AccountModeCoding, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := &Account{Platform: test.platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": test.baseURL, "account_mode": test.mode}}
			got := LookupOfficialModelContextCapacity(account, "k3")
			if (got != nil) != test.want {
				t.Fatalf("Kimi product matching got %+v, want matching=%v", got, test.want)
			}
			if got != nil && got.ContextWindow != 262144 {
				t.Fatal("unknown Kimi Coding tier was given the open-platform 1M window")
			}
		})
	}
}
