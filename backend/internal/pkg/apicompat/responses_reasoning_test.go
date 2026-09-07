package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesReasoningConfigurationRoundTrip(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"mode":"pro"}`,
		`{"mode":"standard","effort":"xhigh","context":"all_turns","summary":"auto","generate_summary":"detailed"}`,
		`{"mode":null,"effort":null,"context":null,"summary":"","generate_summary":null}`,
		`{"mode":"pro","effort":"","context":"current_turn","future":{"integer":9007199254740993,"precise":1.2345678901234567890123456789,"huge":1e1000,"items":[null,false,{"n":9007199254740993}]}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var config ResponsesReasoning
			require.NoError(t, json.Unmarshal([]byte(raw), &config))
			encoded, err := json.Marshal(config)
			require.NoError(t, err)
			assertReasoningRawMembers(t, raw, encoded)
		})
	}
}

// Raw members, rather than JSONEq's float64 interpretation, verify that large
// integers and decimal/exponent spellings did not change during serialization.
func assertReasoningRawMembers(t *testing.T, want string, encoded []byte) {
	t.Helper()
	var expected, got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(want), &expected))
	require.NoError(t, json.Unmarshal(encoded, &got))
	require.Equal(t, expected, got)
}

func TestResponsesReasoningConfigurationTypedEnvelopes(t *testing.T) {
	const reasoning = `{"mode":"pro","context":"all_turns","future":{"budget":9007199254740993}}`
	for _, envelope := range []struct {
		name, raw, path string
		target          any
	}{
		{"request", `{"model":"gpt-5.6-sol","input":"hello","reasoning":` + reasoning + `}`, "reasoning", &ResponsesRequest{}},
		{"response", `{"id":"resp_1","model":"gpt-5.6-sol","status":"completed","output":[],"reasoning":` + reasoning + `}`, "reasoning", &ResponsesResponse{}},
		{"terminal", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","reasoning":` + reasoning + `}}`, "response.reasoning", &ResponsesStreamEvent{}},
		{"chat_ingress", `{"model":"gpt-5.6-sol","messages":[],"reasoning":` + reasoning + `}`, "reasoning", &ChatCompletionsRequest{}},
	} {
		t.Run(envelope.name, func(t *testing.T) {
			require.NoError(t, json.Unmarshal([]byte(envelope.raw), envelope.target))
			encoded, err := json.Marshal(envelope.target)
			require.NoError(t, err)
			assertReasoningRawMembers(t, reasoning, []byte(gjson.GetBytes(encoded, envelope.path).Raw))
			require.False(t, gjson.GetBytes(encoded, envelope.path+".effort").Exists())
		})
	}
}

func TestResponsesReasoningConfigurationCloneAndExplicitOptions(t *testing.T) {
	var absent *ResponsesReasoning
	require.Nil(t, absent.Clone())
	require.False(t, absent.HasField("effort"))
	encoded, err := json.Marshal(absent)
	require.NoError(t, err)
	require.Equal(t, "null", string(encoded))
	encoded, err = json.Marshal(ResponsesReasoning{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(encoded))

	const original = `{"effort":null,"summary":"","mode":"pro","context":"all_turns","future":9007199254740993}`
	var config ResponsesReasoning
	require.NoError(t, json.Unmarshal([]byte(original), &config))
	require.True(t, config.HasField("effort"))
	require.True(t, config.HasField("summary"))
	require.True(t, config.HasField("future"))
	require.False(t, config.HasField("generate_summary"))
	cloned := config.Clone()
	cloned.Effort = "xhigh"
	cloned.Mode = "standard"
	cloned.rawFields["future"][15] = '2'
	encoded, err = json.Marshal(cloned)
	require.NoError(t, err)
	require.Equal(t, "xhigh", gjson.GetBytes(encoded, "effort").String())
	require.Equal(t, "standard", gjson.GetBytes(encoded, "mode").String())
	require.Equal(t, "9007199254740992", gjson.GetBytes(encoded, "future").Raw)
	require.Equal(t, `""`, gjson.GetBytes(encoded, "summary").Raw)
	unchanged, err := json.Marshal(config)
	require.NoError(t, err)
	assertReasoningRawMembers(t, original, unchanged)

	// A typed mutation to empty remains an explicit empty value, not omission.
	cloned.Mode = ""
	encoded, err = json.Marshal(cloned)
	require.NoError(t, err)
	require.Equal(t, `""`, gjson.GetBytes(encoded, "mode").Raw)
}

func TestResponsesReasoningConfigurationDoesNotInventDefaults(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.6-sol","input":"hello"}`,
		`{"model":"gpt-6-astra","input":"hello","reasoning":null}`,
	} {
		var request ResponsesRequest
		require.NoError(t, json.Unmarshal([]byte(body), &request))
		require.Nil(t, request.Reasoning)
		encoded, err := json.Marshal(request)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(encoded, "reasoning").Exists())
	}
}

func TestResponsesReasoningConfigurationNativeCapability(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		wantNative bool
	}{
		{`null`, false},
		{`{}`, false},
		{`{"effort":"xhigh","summary":"auto"}`, false},
		{`{"effort":null,"summary":null}`, false},
		{`{"mode":"standard"}`, true},
		{`{"mode":null}`, true},
		{`{"context":"auto"}`, true},
		{`{"generate_summary":"auto"}`, true},
		{`{"future":9007199254740993}`, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var config *ResponsesReasoning
			require.NoError(t, json.Unmarshal([]byte(tc.raw), &config))
			require.Equal(t, tc.wantNative, ResponsesReasoningRequiresNative(config))
			request := &ResponsesRequest{Model: "gpt-5.6-sol", Input: json.RawMessage(`"hello"`), Reasoning: config}
			converted, err := ResponsesToChatCompletionsRequest(request)
			if tc.wantNative {
				var conversionError *ResponsesConversionError
				require.ErrorAs(t, err, &conversionError)
				require.Equal(t, "reasoning", conversionError.Param)
				require.Nil(t, converted)
			} else {
				require.NoError(t, err)
				require.NotNil(t, converted)
			}
		})
	}
	require.True(t, ResponsesReasoningRequiresNative(&ResponsesReasoning{Mode: "pro"}))
	require.True(t, ResponsesReasoningRequiresNative(&ResponsesReasoning{Context: "all_turns"}))
	require.True(t, ResponsesReasoningRequiresNative(&ResponsesReasoning{GenerateSummary: "auto"}))
}

func TestResponsesReasoningConfigurationChatMerge(t *testing.T) {
	for _, tc := range []struct {
		name, fields, want string
	}{
		{
			"nested only has no defaults",
			`"reasoning":{"mode":"pro","context":"all_turns","future":{"integer":9007199254740993}}`,
			`{"mode":"pro","context":"all_turns","future":{"integer":9007199254740993}}`,
		},
		{
			"explicit flat effort overlays only effort",
			`"reasoning":{"mode":"standard","effort":"low","context":"all_turns","future":{"integer":9007199254740993}},"reasoning_effort":"xhigh"`,
			`{"mode":"standard","effort":"xhigh","summary":"auto","context":"all_turns","future":{"integer":9007199254740993}}`,
		},
		{
			"explicit null summary remains null",
			`"reasoning":{"mode":"pro","effort":null,"summary":null},"reasoning_effort":"minimal"`,
			`{"mode":"pro","effort":"minimal","summary":null}`,
		},
		{
			"explicit empty summary remains empty",
			`"reasoning":{"effort":"max","summary":""},"reasoning_effort":"high"`,
			`{"effort":"high","summary":""}`,
		},
		{
			"legacy flat effort keeps compatible summary default",
			`"reasoning_effort":"high"`,
			`{"effort":"high","summary":"auto"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request ChatCompletionsRequest
			require.NoError(t, json.Unmarshal([]byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}],`+tc.fields+`}`), &request))
			original, err := json.Marshal(request)
			require.NoError(t, err)
			converted, err := ChatCompletionsToResponses(&request)
			require.NoError(t, err)
			require.NotNil(t, converted.Reasoning)
			encoded, err := json.Marshal(converted.Reasoning)
			require.NoError(t, err)
			assertReasoningRawMembers(t, tc.want, encoded)
			converted.Reasoning.Mode = "changed-for-test"
			unchanged, err := json.Marshal(request)
			require.NoError(t, err)
			require.Equal(t, original, unchanged)
		})
	}
}
