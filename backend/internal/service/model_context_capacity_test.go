package service

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestModelContextCapacityPriorityAndDistinctLimits(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, baseURL := range []string{"https://api.openai.com/v1", "https://relay.example/v1"} {
			t.Run(accountType+"/"+baseURL, func(t *testing.T) {
				account := &Account{Platform: PlatformOpenAI, Type: accountType, Credentials: map[string]any{"base_url": baseURL}}
				account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
					Source: "upstream", SourceIdentity: UpstreamModelMetadataSourceIdentity(account), Models: map[string]UpstreamModelMetadata{
						"gpt-5.5":        {ContextWindow: 272000, MaxContextWindow: 872000, MaxInputTokens: 200000, MaxOutputTokens: 8000},
						"custom-model":   {ContextWindow: 272000, MaxContextWindow: 872000, MaxInputTokens: 200000, MaxOutputTokens: 8000},
						"registry-model": {ContextWindow: 2000000, MaxOutputTokens: 100000, CapacitySource: ModelContextSourceRegistry},
					},
				})
				for _, test := range []struct {
					model, source         string
					window, input, output int64
				}{
					{"gpt-5.5", ModelContextSourceOfficial, 1050000, 0, 128000},
					{"gpt-5.4", ModelContextSourceOfficial, 1050000, 0, 128000},
					{"gpt-5.4-mini", ModelContextSourceOfficial, 400000, 0, 128000},
					{"custom-model", ModelContextSourceUpstream, 872000, 200000, 8000},
					{"registry-model", ModelContextSourceRegistry, 2000000, 0, 100000},
					{"never-seen", "", 0, 0, 0},
				} {
					got := ResolveAccountModelContextCapacity(account, test.model)
					if got.Source != test.source || got.ContextWindow != test.window || got.MaxContextWindow != test.window || got.MaxInputTokens != test.input || got.MaxOutputTokens != test.output {
						t.Fatalf("%s: unexpected resolution: %+v", test.model, got)
					}
				}
				account.Extra[ModelContextOverridesExtraKey] = map[string]int64{"gpt-5.5": 768000}
				got := ResolveAccountModelContextCapacity(account, "gpt-5.5")
				if got.Source != ModelContextSourceCustom || got.ContextWindow != 768000 || got.MaxInputTokens != 0 || got.MaxOutputTokens != 128000 {
					t.Fatalf("custom override borrowed unrelated limits: %+v", got)
				}
				delete(account.Extra, ModelContextOverridesExtraKey)
				got = ResolveAccountModelContextCapacity(account, "gpt-5.5")
				if got.Source != ModelContextSourceOfficial || got.ContextWindow != 1050000 {
					t.Fatalf("clearing an override did not restore the API specification: %+v", got)
				}
			})
		}
	}
}

func TestModelContextCapacityEvidenceDoesNotMixSources(t *testing.T) {
	for _, test := range []struct {
		name                  string
		items                 []modelContextEvidence
		source, basis         string
		window, input, output int64
	}{
		{"official selected independent of evidence order", []modelContextEvidence{
			{ModelContextSourceUpstream, ModelContextCapacity{ContextWindow: 200000, MaxInputTokens: 100000, MaxOutputTokens: 8192}},
			{ModelContextSourceRegistry, ModelContextCapacity{ContextWindow: 600000, MaxOutputTokens: 32768}},
			{ModelContextSourceOfficial, ModelContextCapacity{ContextWindow: 400000}},
		}, ModelContextSourceOfficial, ModelContextCapacityBasisTotal, 400000, 0, 0},
		{"maximum before default and input", []modelContextEvidence{
			{ModelContextSourceUpstream, ModelContextCapacity{ContextWindow: 272000, MaxContextWindow: 872000, MaxInputTokens: 250000, MaxOutputTokens: 128000}},
		}, ModelContextSourceUpstream, ModelContextCapacityBasisMaximum, 872000, 250000, 128000},
		{"explicit smaller maximum honored", []modelContextEvidence{
			{ModelContextSourceUpstream, ModelContextCapacity{ContextWindow: 872000, MaxContextWindow: 272000, MaxOutputTokens: 128000}},
		}, ModelContextSourceUpstream, ModelContextCapacityBasisMaximum, 272000, 0, 128000},
		{"input only stays input only", []modelContextEvidence{
			{ModelContextSourceUpstream, ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536}},
			{ModelContextSourceRegistry, ModelContextCapacity{ContextWindow: 2000000}},
		}, ModelContextSourceUpstream, ModelContextCapacityBasisInput, 1048576, 1048576, 65536},
		{"output alone cannot fill lower priority record", []modelContextEvidence{
			{ModelContextSourceUpstream, ModelContextCapacity{MaxOutputTokens: 8192}},
			{ModelContextSourceRegistry, ModelContextCapacity{ContextWindow: 400000}},
		}, ModelContextSourceRegistry, ModelContextCapacityBasisTotal, 400000, 0, 0},
		{"bad context preserves independent valid input", []modelContextEvidence{
			{ModelContextSourceUpstream, ModelContextCapacity{ContextWindow: -1, MaxContextWindow: MaxSafeModelContextTokens + 1, MaxInputTokens: 100000, MaxOutputTokens: 8192}},
		}, ModelContextSourceUpstream, ModelContextCapacityBasisInput, 100000, 100000, 8192},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := resolveModelContextEvidence(test.items)
			if got.Source != test.source || got.CapacityBasis != test.basis || got.ContextWindow != test.window || got.MaxContextWindow != test.window || got.MaxInputTokens != test.input || got.MaxOutputTokens != test.output {
				t.Fatalf("unexpected complete evidence result: %+v", got)
			}
		})
	}
}

func TestModelContextCapacityCustomOverrideOverlaysOneAutomaticRecord(t *testing.T) {
	tests := []struct {
		name                           string
		items                          []modelContextEvidence
		window, maximum, input, output int64
		source                         string
	}{
		{
			name: "official output is preserved",
			items: []modelContextEvidence{
				{ModelContextSourceCustom, ModelContextCapacity{ContextWindow: 768000, MaxContextWindow: 768000}},
				{ModelContextSourceOfficial, ModelContextCapacity{ContextWindow: 1050000, MaxOutputTokens: 128000}},
			},
			window: 768000, maximum: 768000, output: 128000, source: ModelContextSourceCustom,
		},
		{
			name: "official missing output stays missing",
			items: []modelContextEvidence{
				{ModelContextSourceCustom, ModelContextCapacity{ContextWindow: 512000, MaxContextWindow: 512000}},
				{ModelContextSourceOfficial, ModelContextCapacity{ContextWindow: 400000}},
				{ModelContextSourceUpstream, ModelContextCapacity{ContextWindow: 400000, MaxOutputTokens: 32000}},
			},
			window: 512000, maximum: 512000, source: ModelContextSourceCustom,
		},
		{
			name: "unknown automatic evidence",
			items: []modelContextEvidence{
				{ModelContextSourceCustom, ModelContextCapacity{ContextWindow: 256000, MaxContextWindow: 256000}},
			},
			window: 256000, maximum: 256000, source: ModelContextSourceCustom,
		},
		{
			name: "independent limit above override is omitted",
			items: []modelContextEvidence{
				{ModelContextSourceCustom, ModelContextCapacity{ContextWindow: 32000, MaxContextWindow: 32000}},
				{ModelContextSourceOfficial, ModelContextCapacity{ContextWindow: 1050000, MaxInputTokens: 64000, MaxOutputTokens: 128000}},
			},
			window: 32000, maximum: 32000, source: ModelContextSourceCustom,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveModelContextEvidence(test.items)
			if got.Source != test.source || got.ContextWindow != test.window || got.MaxContextWindow != test.maximum || got.MaxInputTokens != test.input || got.MaxOutputTokens != test.output {
				t.Fatalf("custom overlay mismatch: %+v", got)
			}
		})
	}
}

func TestModelContextCapacityOfficialPolicyAppliesToOtherVendors(t *testing.T) {
	for _, test := range []struct {
		platform, model string
		window          int64
	}{
		{PlatformAnthropic, "claude-opus-4-6", 1000000},
		{PlatformGemini, "gemini-3.8-flash", 1048576},
		{PlatformOpenAI, "deepseek-v4-pro", 1000000},
		{PlatformOpenAI, "MiniMax-M3", 1000000},
	} {
		t.Run(test.model, func(t *testing.T) {
			account := &Account{Platform: test.platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example/v1"}}
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", SourceIdentity: UpstreamModelMetadataSourceIdentity(account), Models: map[string]UpstreamModelMetadata{test.model: {ContextWindow: 16000, MaxInputTokens: 15000, MaxOutputTokens: 8000}}})
			got := ResolveAccountModelContextCapacity(account, test.model)
			if got.Source != ModelContextSourceOfficial || got.ContextWindow != test.window {
				t.Fatalf("official API policy did not apply across vendors: %+v", got)
			}
			if test.model == "MiniMax-M3" && (got.MaxInputTokens != 0 || got.MaxOutputTokens != 0) {
				t.Fatalf("unknown official limits filled from relay: %+v", got)
			}
		})
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
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	snapshot.SourceIdentity = UpstreamModelMetadataSourceIdentity(account)
	account.SetUpstreamModelMetadataSnapshot(*snapshot)
	rows := BuildAccountModelContextCapacityRows(account, []string{"registry-only"})
	byID := make(map[string]AccountModelContextCapacityRow)
	for _, row := range rows {
		byID[row.UpstreamModelID] = row
	}
	row := byID["real"]
	if row.Upstream == nil || row.Upstream.ContextWindow != 100000 || row.Upstream.ObservedAt != "2026-09-06T01:00:00Z" || row.UpstreamEvidenceStatus != "current" || row.EffectiveContextWindow != 600000 || row.AutomaticContextWindow != 100000 || !row.Editable {
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

func TestModelContextCapacityRowsDescribeIgnoredUpstreamEvidence(t *testing.T) {
	for _, status := range []string{"current", "unbound", "source_mismatch"} {
		t.Run(status, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example/v1"}}
			snapshot := UpstreamModelMetadataSnapshot{Source: ModelContextSourceUpstream, Models: map[string]UpstreamModelMetadata{
				"unknown-upstream-model": {ContextWindow: 272000, MaxContextWindow: 872000},
			}}
			switch status {
			case "current":
				snapshot.SourceIdentity = UpstreamModelMetadataSourceIdentity(account)
			case "source_mismatch":
				prior := *account
				prior.Credentials = map[string]any{"base_url": "https://prior.example/v1"}
				snapshot.SourceIdentity = UpstreamModelMetadataSourceIdentity(&prior)
			}
			account.SetUpstreamModelMetadataSnapshot(snapshot)
			rows := BuildAccountModelContextCapacityRows(account, nil)
			if len(rows) != 1 || rows[0].Upstream == nil || rows[0].Upstream.ContextWindow != 272000 || rows[0].Upstream.MaxContextWindow != 872000 || rows[0].UpstreamEvidenceStatus != status {
				t.Fatalf("raw evidence lost or incorrectly labelled: %+v", rows)
			}
			row := rows[0]
			if status == "current" {
				if row.AutomaticContextWindow != 872000 || row.AutomaticSource != ModelContextSourceUpstream {
					t.Fatalf("current evidence not selected: %+v", row)
				}
			} else if row.AutomaticContextWindow != 0 || row.AutomaticSource != "" {
				t.Fatalf("ignored upstream evidence was applied: %+v", row)
			}
		})
	}
}

func TestModelContextCapacityRowsRetainIgnoredRegistryEvidence(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example/v1"}}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: ModelContextSourceRegistry, Models: map[string]UpstreamModelMetadata{
		"legacy-registry-model": {ContextWindow: 700000, CapacitySource: ModelContextSourceRegistry},
	}})
	rows := BuildAccountModelContextCapacityRows(account, nil)
	if len(rows) != 1 || rows[0].Registry == nil || rows[0].Registry.ContextWindow != 700000 || rows[0].RegistryEvidenceStatus != "unbound" {
		t.Fatalf("legacy registry evidence was not retained as diagnostic data: %+v", rows)
	}
	if rows[0].AutomaticContextWindow != 0 || rows[0].EffectiveContextWindow != 0 {
		t.Fatalf("unbound registry evidence was applied: %+v", rows[0])
	}
}
