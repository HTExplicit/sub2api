package service

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestModelContextCapacityPriorityAndDistinctLimits(t *testing.T) {
	custom := int64(768000)
	official := &OfficialModelContextCapacity{ModelContextCapacity: ModelContextCapacity{ContextWindow: 1050000, MaxOutputTokens: 128000}}
	upstream := &ModelContextCapacity{ContextWindow: 128000, MaxContextWindow: 256000, MaxInputTokens: 120000, MaxOutputTokens: 8000}
	cases := []struct {
		name     string
		custom   *int64
		official *OfficialModelContextCapacity
		upstream *ModelContextCapacity
		window   int64
		source   string
		output   int64
	}{
		{"custom over official", &custom, official, upstream, 768000, "custom", 128000},
		{"official above smaller upstream", nil, official, upstream, 1050000, "official", 128000},
		{"upstream", nil, nil, upstream, 128000, "upstream", 8000},
		{"unknown", nil, nil, nil, 200000, "default", 0},
		{"output alone is not context", nil, nil, &ModelContextCapacity{MaxOutputTokens: 64000}, 200000, "default", 64000},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveModelContextCapacity(test.custom, test.official, test.upstream)
			if got.ContextWindow != test.window || got.Source != test.source || got.MaxOutputTokens != test.output {
				t.Fatalf("unexpected resolution: %+v", got)
			}
		})
	}
	got := ResolveModelContextCapacity(nil, nil, upstream)
	if got.MaxContextWindow != 256000 || got.MaxInputTokens != 120000 {
		t.Fatalf("lost distinct raw limits: %+v", got)
	}
	inputOnly := &OfficialModelContextCapacity{ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: ModelContextCapacityBasisInput}}
	got = ResolveModelContextCapacity(nil, inputOnly, upstream)
	if got.ContextWindow != 1048576 || got.CapacityBasis != "input_limit" || got.MaxContextWindow != 0 {
		t.Fatalf("input budget was misrepresented as a total window: %+v", got)
	}
	if inputOnly.ContextWindow != 0 || upstream.ContextWindow != 128000 {
		t.Fatal("resolver mutated its source records")
	}
	got = ResolveModelContextCapacity(nil, nil, &ModelContextCapacity{ContextWindow: 500000, MaxContextWindow: 250000})
	if got.MaxContextWindow != 0 {
		t.Fatal("projected a maximum below the selected planning context")
	}
	got = ResolveModelContextCapacity(&custom, nil, &ModelContextCapacity{MaxContextWindow: 128000, MaxOutputTokens: 8192})
	if got.MaxContextWindow != custom || got.MaxOutputTokens != 8192 {
		t.Fatalf("custom context discarded independent output or imported lower maximum: %+v", got)
	}
	smallCustom := int64(100000)
	got = ResolveModelContextCapacity(&smallCustom, official, &ModelContextCapacity{MaxInputTokens: 500000, MaxOutputTokens: 8192})
	if got.MaxInputTokens != 0 || got.MaxOutputTokens != 8192 {
		t.Fatalf("incompatible independent limits were clamped or not safely omitted: %+v", got)
	}
	got = ResolveModelContextCapacity(&smallCustom, official, nil)
	if got.MaxOutputTokens != 0 {
		t.Fatalf("larger official output was falsely clamped to the custom window: %+v", got)
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
	if ValidateModelContextOverrides(account, nil) != nil {
		t.Fatal("omitted patch rejected an ordinary protected account edit")
	}
	if ValidateModelContextOverrides(account, map[string]*int64{}) == nil {
		t.Fatal("explicit protected override patch was allowed")
	}
}

func TestModelContextCapacityManagedProviderBoundary(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformAntigravity} {
		account := &Account{Platform: platform, Type: AccountTypeAPIKey}
		if !CanManageModelContextCapacity(account) {
			t.Errorf("ordinary platform %s not editable", platform)
		}
		account.Type = AccountTypeOAuth
		if CanManageModelContextCapacity(account) {
			t.Errorf("OAuth platform %s editable", platform)
		}
	}
	for _, account := range []*Account{
		nil,
		{Platform: PlatformCindy, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, ProviderProfile: ProviderProfileCindyLaxaV1},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}},
		{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"base_url": "https://proxy.invalid"}},
		{Platform: PlatformAnthropic, Type: AccountTypeBedrock},
	} {
		if CanManageModelContextCapacity(account) {
			t.Errorf("protected/unsupported account editable: %+v", account)
		}
	}
}

func TestModelContextCapacityRowsUseTrueTargetsAndPreserveSources(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-one": "real", "public-two": "real", "gpt-5.6-sol": "unknown-actual"}},
		Extra: map[string]any{
			ModelContextOverridesExtraKey: map[string]int64{"real": 600000, "custom-only": 750000},
			UpstreamModelMetadataExtraKey: UpstreamModelMetadataSnapshot{Source: "models.dev", Models: map[string]UpstreamModelMetadata{"legacy-only": {ID: "legacy-only", ContextWindow: 900000}}},
		},
	}
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{
		ObservedAt: "2026-09-07T01:00:00Z", Models: map[string]ModelContextCapacity{
			"real": {ContextWindow: 100000, ObservedAt: "2026-09-06T01:00:00Z"}, "dynamic-unknown": {},
		},
	})
	rows := BuildAccountModelContextCapacityRows(account, []string{"legacy-only"})
	byID := make(map[string]AccountModelContextCapacityRow)
	for _, row := range rows {
		byID[row.UpstreamModelID] = row
	}
	row := byID["real"]
	if row.Upstream == nil || row.Upstream.ContextWindow != 100000 || row.Upstream.ObservedAt != "2026-09-06T01:00:00Z" || row.EffectiveContextWindow != 600000 || row.AutomaticContextWindow != 100000 {
		t.Fatalf("raw/custom/automatic provenance lost: %+v", row)
	}
	if !reflect.DeepEqual(row.Aliases, []string{"public-one", "public-two"}) {
		t.Fatalf("mapped aliases don't share real override: %+v", row.Aliases)
	}
	for _, id := range []string{"legacy-only", "unknown-actual", "dynamic-unknown"} {
		if byID[id].EffectiveSource != "default" || byID[id].EffectiveContextWindow != 200000 || byID[id].Upstream != nil || byID[id].Official != nil {
			t.Errorf("%s inferred capacity from public alias or mixed legacy: %+v", id, byID[id])
		}
	}
	if byID["custom-only"].EffectiveContextWindow != 750000 {
		t.Fatal("custom-only model disappeared from management rows")
	}
}

func TestModelContextCapacityBatchLiveResolutionDoesNotMutateSnapshot(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{}}
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{Models: map[string]ModelContextCapacity{"actual": {ContextWindow: 128000}}})
	resolve := NewAccountModelContextCapacityResolver(account)
	live := &ModelContextCapacity{ContextWindow: 512000, MaxContextWindow: 1024000}
	if got := resolve("actual", live); got.ContextWindow != 512000 || got.MaxContextWindow != 1024000 || got.Source != "upstream" {
		t.Fatalf("did not prefer live raw capacity: %+v", got)
	}
	if resolve("actual", nil).ContextWindow != 128000 || account.GetUpstreamModelContextCapacitySnapshot().Models["actual"].ContextWindow != 128000 {
		t.Fatal("live override contaminated account or resolver snapshot")
	}
	value := int64(600000)
	account.Extra[ModelContextOverridesExtraKey] = map[string]int64{"actual": value}
	if NewAccountModelContextCapacityResolver(account)("actual", live).Source != "custom" {
		t.Fatal("live raw source bypassed custom priority")
	}
}

func TestModelContextCapacityProtectedRowsDoNotUseOverridesOrDefault(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		ModelContextOverridesExtraKey: map[string]int64{"known": 1000000},
		UpstreamModelMetadataExtraKey: UpstreamModelMetadataSnapshot{Source: "models.dev", Models: map[string]UpstreamModelMetadata{"known": {ID: "known", ContextWindow: 272000}}},
	}}
	rows := BuildAccountModelContextCapacityRows(account, []string{"known", "unknown"})
	if len(rows) != 2 || rows[0].Editable || rows[0].EffectiveSource != "protected" || rows[0].EffectiveContextWindow != 272000 || rows[0].CustomContextWindow != nil || rows[0].Upstream != nil {
		t.Fatalf("protected legacy value was rewritten: %+v", rows)
	}
	if rows[1].EffectiveContextWindow != 0 || rows[1].EffectiveSource != "protected" {
		t.Fatal("new default leaked into protected unknown model")
	}
}

func TestModelContextCapacitySourceIdentityInvalidatesOnlyUpstreamIdentity(t *testing.T) {
	account := &Account{Platform: PlatformKimi, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.moonshot.cn/v1", "account_mode": AccountModePayG,
			"api_protocol": APIProtocolChatCompletions, "api_key": "first-key",
		}, Extra: map[string]any{ModelContextOverridesExtraKey: map[string]int64{"custom-model": 700000}},
	}
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{
		Models: map[string]ModelContextCapacity{"actual": {ContextWindow: 512000}},
	})
	identity := ModelContextCapacitySourceIdentity(account)
	account.Name = "renamed"
	account.Credentials["api_key"] = "rotated-key"
	account.Credentials["model_mapping"] = map[string]any{"new-public-alias": "actual"}
	account.Credentials["base_url"] = "https://API.MOONSHOT.CN:443/v1/"
	if ModelContextCapacitySourceIdentity(account) != identity || account.GetUpstreamModelContextCapacitySnapshot() == nil {
		t.Fatal("credential rotation/name/model mapping/equivalent URL invalidated capacity observations")
	}
	changes := []struct {
		name  string
		key   string
		value any
	}{
		{"endpoint", "base_url", "https://other-upstream.invalid/v1"},
		{"product", "account_mode", AccountModeCoding},
		{"protocol", "api_protocol", APIProtocolResponses},
		{"adaptive endpoint", "api_base_urls", map[string]any{APIProtocolResponses: "https://native-upstream.invalid/v1"}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			previous, exists := account.Credentials[change.key]
			account.Credentials[change.key] = change.value
			defer func() {
				if exists {
					account.Credentials[change.key] = previous
				} else {
					delete(account.Credentials, change.key)
				}
			}()
			if account.GetUpstreamModelContextCapacitySnapshot() != nil {
				t.Fatal("stale upstream capacity reused after upstream identity change")
			}
			got := ResolveAccountModelContextCapacity(account, "actual")
			if got.Source != "default" || got.ContextWindow != 200000 || got.Reason != "upstream_source_changed" {
				t.Fatalf("changed source did not fail safely: %+v", got)
			}
			if ResolveAccountModelContextCapacity(account, "custom-model").ContextWindow != 700000 {
				t.Fatal("upstream source change erased independent custom capacity")
			}
		})
	}
	if account.GetUpstreamModelContextCapacitySnapshot() == nil {
		t.Fatal("identity mismatch handling destructively removed the stored observation")
	}
	account.Extra[UpstreamModelContextCapacitiesExtraKey] = map[string]any{"models": map[string]any{"actual": map[string]any{"context_window": 999000}}}
	if account.GetUpstreamModelContextCapacitySnapshot() != nil {
		t.Fatal("unbound raw namespace was accepted as current upstream evidence")
	}
}

func TestModelContextCapacitySourceIdentityBindsCNAnthropicEndpoint(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"base_url": "https://messages-a.invalid", "api_protocol": APIProtocolAnthropic,
			}}
			account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{
				Models: map[string]ModelContextCapacity{"actual": {ContextWindow: 512000}},
			})
			identity, modelsURL := ModelContextCapacitySourceIdentity(account), upstreamModelRegistryBaseURL(account)
			account.Credentials["base_url"] = "https://messages-b.invalid"
			if upstreamModelRegistryBaseURL(account) != modelsURL {
				t.Fatal("fixture must exercise the unchanged official Models endpoint")
			}
			if ModelContextCapacitySourceIdentity(account) == identity || account.GetUpstreamModelContextCapacitySnapshot() != nil {
				t.Fatal("a changed real Messages endpoint reused observations bound only to the Models endpoint")
			}
		})
	}
}

func TestModelContextCapacitySourceIdentityBindsRouteQuery(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://gateway.invalid/v1?provider=A&region=one",
	}}
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{
		Models: map[string]ModelContextCapacity{"actual": {ContextWindow: 512000}},
	})
	identity := ModelContextCapacitySourceIdentity(account)
	account.Credentials["base_url"] = "https://gateway.invalid/v1?region=one&provider=A"
	if ModelContextCapacitySourceIdentity(account) != identity || account.GetUpstreamModelContextCapacitySnapshot() == nil {
		t.Fatal("cosmetic query parameter reorder invalidated the same source")
	}
	account.Credentials["base_url"] = "https://gateway.invalid/v1?region=one&provider=B"
	if ModelContextCapacitySourceIdentity(account) == identity || account.GetUpstreamModelContextCapacitySnapshot() != nil {
		t.Fatal("route-bearing provider query changed but reused upstream capacity")
	}
	account.Credentials["base_url"] = "https://gateway.invalid/v1?provider=A;invalid=1"
	identity = ModelContextCapacitySourceIdentity(account)
	account.Credentials["base_url"] = "https://gateway.invalid/v1?provider=B;invalid=1"
	if ModelContextCapacitySourceIdentity(account) == identity {
		t.Fatal("invalid query canonicalization discarded route-bearing raw data")
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
