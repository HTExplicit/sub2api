package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIReasoningConfigurationNormalization(t *testing.T) {
	for _, model := range []string{"gpt-5.6-sol", "gpt-6-astra", "gpt-5.4", "provider/custom-model"} {
		for _, tc := range []struct {
			reasoning    string
			wantNonAstra string
		}{
			{`{"mode":"pro"}`, `{"effort":"max"}`},
			{`{"mode":"pro","effort":"xhigh","context":"all_turns","future":{"budget":9007199254740993}}`, `{"effort":"xhigh","context":"all_turns","future":{"budget":9007199254740993}}`},
			{`{"mode":"standard","effort":null,"summary":"","context":null}`, `{"effort":null,"summary":"","context":null}`},
			{`{"mode":"future-mode","future":{"precise":1.2345678901234567890123456789}}`, `{"future":{"precise":1.2345678901234567890123456789}}`},
		} {
			t.Run(model+tc.reasoning, func(t *testing.T) {
				original := []byte(`{"model":"` + model + `","input":[],"reasoning":` + tc.reasoning + `}`)
				originalSnapshot := append([]byte(nil), original...)
				wantOfficial := tc.wantNonAstra
				if model == "gpt-6-astra" {
					wantOfficial = tc.reasoning
				}
				for _, compact := range []bool{false, true} {
					body, _, err := normalizeOpenAIPassthroughOAuthBody(original, compact)
					require.NoError(t, err)
					assertOpenAIReasoningConfiguration(t, wantOfficial, body)
				}
				for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
					body, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(original, &Account{Platform: PlatformOpenAI, Type: accountType}, false)
					require.NoError(t, err)
					want := tc.reasoning
					if accountType != AccountTypeAPIKey {
						want = wantOfficial
					}
					assertOpenAIReasoningConfiguration(t, want, body)
				}
				require.Equal(t, originalSnapshot, original)
			})
		}
	}
}

func assertOpenAIReasoningConfiguration(t *testing.T, want string, body []byte) {
	t.Helper()
	var expected, got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(want), &expected))
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(body, "reasoning").Raw), &got))
	require.Equal(t, expected, got)
}

func TestOpenAIReasoningConfigurationAbsent(t *testing.T) {
	for _, original := range [][]byte{nil, {}, []byte(`{"model":"gpt-6-astra","input":[]}`), []byte(`{"model":"gpt-5.6-sol","reasoning":null,"input":[]}`)} {
		body, _, err := normalizeOpenAIPassthroughOAuthBody(original, false)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(body, "reasoning.mode").Exists())
		require.False(t, gjson.GetBytes(body, "reasoning.effort").Exists())
		if gjson.GetBytes(original, "reasoning").Exists() {
			require.Equal(t, "null", gjson.GetBytes(body, "reasoning").Raw)
		}
	}
}

func TestOpenAIReasoningConfigurationRequiresNative(t *testing.T) {
	for _, tc := range []struct {
		reasoning string
		native    bool
	}{
		{`null`, false},
		{`{}`, false},
		{`{"effort":"max","summary":"auto"}`, false},
		{`{"mode":"standard"}`, true},
		{`{"context":"all_turns"}`, true},
		{`{"generate_summary":"auto"}`, true},
		{`{"extension":9007199254740993}`, true},
	} {
		t.Run(tc.reasoning, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","input":[],"reasoning":` + tc.reasoning + `}`)
			require.Equal(t, tc.native, OpenAIResponsesRequireNativeUpstream(body))
		})
	}
}

func TestOpenAIReasoningConfigurationWSComparisonPreservesNumbers(t *testing.T) {
	for _, compare := range []struct {
		name string
		fn   func([]byte) ([]byte, error)
	}{
		{"item_compare", normalizeOpenAIWSJSONForCompare},
		{"payload_compare", normalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID},
	} {
		t.Run(compare.name, func(t *testing.T) {
			first := []byte(`{"model":"gpt-6-astra","reasoning":{"future":9007199254740992},"input":[]}`)
			second := []byte(`{"model":"gpt-6-astra","reasoning":{"future":9007199254740993},"input":[]}`)
			a, err := compare.fn(first)
			require.NoError(t, err)
			b, err := compare.fn(second)
			require.NoError(t, err)
			require.NotEqual(t, a, b, "different reasoning settings must not reuse the same state")
			require.Equal(t, "9007199254740992", gjson.GetBytes(a, "reasoning.future").Raw)
			require.Equal(t, "9007199254740993", gjson.GetBytes(b, "reasoning.future").Raw)
		})
	}
	const reasoning = `{"mode":"pro","context":"all_turns","future":9007199254740993}`
	body := []byte(`{"model":"gpt-6-astra","reasoning":` + reasoning + `,"input":[]}`)
	updated, err := setPreviousResponseIDToRawPayload(body, "resp_prior")
	require.NoError(t, err)
	assertOpenAIReasoningConfiguration(t, reasoning, updated)
	require.Equal(t, "resp_prior", gjson.GetBytes(updated, "previous_response_id").String())
}

func TestOpenAIReasoningConfigurationSurvivesAdjacentAdapters(t *testing.T) {
	const reasoning = `{"mode":"pro","context":"all_turns","future":{"integer":9007199254740993,"precise":1.2345678901234567890123456789}}`
	imageTool := []byte(`{"model":"gpt-5.6-sol","input":[],"reasoning":` + reasoning + `,"tools":[{"type":"image_generation","format":"png"}]}`)
	stripped, changed, err := stripOpenAIImageGenerationToolsFromRawPayload(imageTool)
	require.NoError(t, err)
	require.True(t, changed)
	assertOpenAIReasoningConfiguration(t, reasoning, stripped)
	normalized, changed, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(imageTool, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false)
	require.NoError(t, err)
	require.True(t, changed)
	assertOpenAIReasoningConfiguration(t, reasoning, normalized)
	require.Equal(t, "png", gjson.GetBytes(normalized, "tools.0.output_format").String())
	withEmptyImage := []byte(`{"model":"gpt-5.6-sol","reasoning":` + reasoning + `,"input":[{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,"}]}]}`)
	normalized, changed, err = sanitizeEmptyBase64InputImagesInOpenAIBody(withEmptyImage)
	require.NoError(t, err)
	require.True(t, changed)
	assertOpenAIReasoningConfiguration(t, reasoning, normalized)
}

func TestOpenAIReasoningConfigurationCindyCountNumberCompatibility(t *testing.T) {
	controls := &CindyImageRequestControls{MaxOutputCount: 1}
	for _, value := range []any{float64(1), json.Number("1"), json.Number("1.0"), json.Number("1e0")} {
		require.NoError(t, validateCindyResponsesImageToolControls("test", "test-model", controls, map[string]any{"n": value}))
	}
	for _, value := range []any{float64(0), json.Number("0"), json.Number("2"), json.Number("1.5"), json.Number("1e1000"), json.Number("invalid"), "1", nil} {
		require.Error(t, validateCindyResponsesImageToolControls("test", "test-model", controls, map[string]any{"n": value}))
	}
}
