package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClassifyCindyContinuation(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		proof      CindyContinuationProof
		wantMode   CindyContinuationMode
		wantAnchor bool
		wantOpaque bool
	}{
		{name: "full create", payload: `{"input":"hello"}`, wantMode: CindyContinuationFullReplay},
		{name: "null anchor", payload: `{"previous_response_id":null,"input":"hello"}`, wantMode: CindyContinuationFullReplay},
		{name: "empty anchor", payload: `{"previous_response_id":"  ","input":"hello"}`, wantMode: CindyContinuationFullReplay},
		{name: "anchor delta", payload: `{"previous_response_id":"resp_1","input":"next"}`, wantMode: CindyContinuationAnchorDelta, wantAnchor: true},
		{name: "anchor plus trusted full", payload: `{"previous_response_id":"resp_1","input":"next"}`, proof: CindyContinuationProof{VerifiedFullHistory: true}, wantMode: CindyContinuationAnchorPlusFull, wantAnchor: true},
		{name: "item reference wins", payload: `{"previous_response_id":"resp_1","input":[{"type":"item_reference","id":"fc_1"}]}`, proof: CindyContinuationProof{VerifiedFullHistory: true}, wantMode: CindyContinuationReferenceOnly, wantAnchor: true},
		{name: "output missing call id", payload: `{"input":[{"type":"function_call_output","output":"ok"}]}`, wantMode: CindyContinuationReferenceOnly},
		{name: "orphan output", payload: `{"input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`, wantMode: CindyContinuationReferenceOnly},
		{name: "reference does not cover concrete call", payload: `{"input":[{"type":"item_reference","id":"call_1"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`, wantMode: CindyContinuationReferenceOnly},
		{name: "concrete call output", payload: `{"input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`, wantMode: CindyContinuationFullReplay},
		{name: "encrypted concrete call", payload: `{"input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}","encrypted_content":"cipher-call"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`, wantMode: CindyContinuationOpaqueFull, wantOpaque: true},
		{name: "encrypted concrete output", payload: `{"input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok","encrypted_content":"cipher-output"}]}`, wantMode: CindyContinuationOpaqueFull, wantOpaque: true},
		{name: "encrypted single object call", payload: `{"input":{"type":"custom_tool_call","call_id":"call_1","name":"tool","input":"{}","encrypted_content":"cipher-single"}}`, wantMode: CindyContinuationOpaqueFull, wantOpaque: true},
		{name: "encrypted orphan output remains reference only", payload: `{"input":{"type":"function_call_output","call_id":"call_orphan","output":"ok","encrypted_content":"cipher-orphan"}}`, wantMode: CindyContinuationReferenceOnly, wantOpaque: true},
		{name: "opaque reasoning", payload: `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher","phase":"analysis"}]}`, wantMode: CindyContinuationOpaqueFull, wantOpaque: true},
		{name: "opaque compaction", payload: `{"input":[{"type":"compaction_summary","id":"cmp_1","encrypted_content":"cipher"}]}`, wantMode: CindyContinuationOpaqueFull, wantOpaque: true},
		{name: "equivalent opaque provider item", payload: `{"input":[{"type":"provider_state","id":"state_1","encrypted_content":"cipher"}]}`, wantMode: CindyContinuationOpaqueFull, wantOpaque: true},
		{name: "reasoning id only", payload: `{"input":[{"type":"reasoning","id":"rs_1"}]}`, wantMode: CindyContinuationReferenceOnly},
		{name: "reasoning id with empty carriers", payload: `{"input":[{"type":"reasoning","id":"rs_1","content":[ ],"summary":{ }}]}`, wantMode: CindyContinuationReferenceOnly},
		{name: "reasoning concrete content", payload: `{"input":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"kept"}],"phase":"analysis"}]}`, wantMode: CindyContinuationFullReplay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ClassifyCindyContinuation([]byte(tt.payload), tt.proof)
			require.NoError(t, err)
			require.Equal(t, tt.wantMode, got.Mode)
			require.Equal(t, tt.wantAnchor, got.HasAnchor)
			require.Equal(t, tt.wantOpaque, got.HasOpaqueState)
		})
	}
}

func TestCindyOpaqueBindingIDDependsOnlyOnProviderCarrier(t *testing.T) {
	first, err := ClassifyCindyContinuation([]byte(`{"input":[{"type":"message","role":"user","content":"first user text"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}","encrypted_content":"stable-cipher"},{"type":"function_call_output","call_id":"call_1","output":"first output"}]}`), CindyContinuationProof{})
	require.NoError(t, err)
	require.Equal(t, CindyContinuationOpaqueFull, first.Mode)
	require.False(t, first.CanSwitchAccount())
	require.Len(t, first.OpaqueBindingIDs, 1)

	second, err := ClassifyCindyContinuation([]byte(`{"input":[{"type":"message","role":"user","content":"different current text"},{"type":"function_call","call_id":"call_2","name":"other_tool","arguments":"{\"changed\":true}","encrypted_content":"stable-cipher"},{"type":"function_call_output","call_id":"call_2","output":"different output"}]}`), CindyContinuationProof{})
	require.NoError(t, err)
	require.Equal(t, CindyContinuationOpaqueFull, second.Mode)
	require.False(t, second.CanSwitchAccount())
	require.Equal(t, first.OpaqueBindingIDs, second.OpaqueBindingIDs)

	responseIDs := cindyOpaqueBindingIDsFromResponsePayload([]byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"call_response","encrypted_content":"stable-cipher"}]}}`))
	require.Equal(t, first.OpaqueBindingIDs, responseIDs)
}

func TestLegacyCindyOpaqueRecoveryRequiresSelfContainedHistory(t *testing.T) {
	legacy := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test", "base_url": "https://api.laxarouter.ai",
		},
	}
	canonical := *legacy
	canonical.Platform = PlatformCindy

	require.True(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(
		`{"input":[{"type":"message","role":"user","content":"continue"},{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"}]}`,
	)))
	require.False(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(
		`{"previous_response_id":"resp_1","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"}]}`,
	)), "an unverified anchor cannot be replayed")
	require.True(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(
		`{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
	)), "paired concrete tool history remains replayable when only reasoning state is removed")
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{
			name:    "mixed encrypted function call",
			payload: `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"reasoning-cipher"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}","encrypted_content":"tool-cipher"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
		},
		{
			name:    "mixed encrypted custom tool call",
			payload: `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"reasoning-cipher"},{"type":"custom_tool_call","call_id":"call_1","name":"tool","input":"{}","encrypted_content":"tool-cipher"},{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"}]}`,
		},
		{
			name:    "mixed encrypted function output",
			payload: `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"reasoning-cipher"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok","encrypted_content":"output-cipher"}]}`,
		},
		{
			name:    "mixed encrypted provider state",
			payload: `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"reasoning-cipher"},{"type":"provider_state","id":"state_1","encrypted_content":"provider-cipher"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.False(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(tc.payload)),
				"any non-removable opaque carrier must reject the entire recovery")
		})
	}
	require.True(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(
		`{"input":[{"type":"compaction","id":"cmp_1","encrypted_content":"cipher"}]}`,
	)), "encrypted compaction is a portable state item")
	require.True(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(
		`{"input":[{"type":"compaction_summary","id":"cmp_1","encrypted_content":"cipher"}]}`,
	)), "encrypted compaction summaries are portable state items")
	require.False(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(
		`{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"},{"type":"function_call_output","call_id":"call_orphan","output":"ok"}]}`,
	)), "orphan tool output remains reference-only")
	require.False(t, canRecoverLegacyCindyOpaqueContinuation(legacy, []byte(
		`{"input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}","encrypted_content":"cipher"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
	)), "opaque tool state is not removable by the reasoning recovery")
	require.False(t, canRecoverLegacyCindyOpaqueContinuation(&canonical, []byte(
		`{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"}]}`,
	)), "canonical Cindy remains binding-strict")
	ordinary := *legacy
	ordinary.Credentials = map[string]any{"api_key": "test", "base_url": "https://api.openai.com"}
	require.False(t, canRecoverLegacyCindyOpaqueContinuation(&ordinary, []byte(
		`{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"}]}`,
	)), "ordinary OpenAI remains binding-strict")
}

func TestClassifyCindyContinuationRejectsInvalidJSON(t *testing.T) {
	_, err := ClassifyCindyContinuation([]byte(`{"input":`), CindyContinuationProof{})
	require.ErrorIs(t, err, ErrInvalidCindyContinuationPayload)
}

func TestVerifiedCindyContinuationHistoryRequiresExactBaselineAnchor(t *testing.T) {
	require.True(t, verifiedCindyContinuationHistory("", "", false))
	require.True(t, verifiedCindyContinuationHistory("  ", "resp_1", false))
	require.True(t, verifiedCindyContinuationHistory("resp_1", "resp_1", true))
	require.True(t, verifiedCindyContinuationHistory(" resp_1 ", "resp_1", true))
	require.False(t, verifiedCindyContinuationHistory("resp_1", "resp_1", false))
	require.False(t, verifiedCindyContinuationHistory("resp_external", "resp_1", true))
}

func TestCindyContinuationReplayPolicy(t *testing.T) {
	plain, err := ClassifyCindyContinuation([]byte(`{"input":"hello"}`), CindyContinuationProof{})
	require.NoError(t, err)
	require.True(t, plain.CanReplayWithoutAnchor())
	require.True(t, plain.CanSwitchAccount())

	opaque, err := ClassifyCindyContinuation([]byte(`{"input":[{"type":"reasoning","encrypted_content":"cipher"}]}`), CindyContinuationProof{})
	require.NoError(t, err)
	require.True(t, opaque.CanReplayWithoutAnchor())
	require.False(t, opaque.CanSwitchAccount())

	anchored, err := ClassifyCindyContinuation([]byte(`{"previous_response_id":"resp_1","input":"next"}`), CindyContinuationProof{VerifiedFullHistory: true})
	require.NoError(t, err)
	require.True(t, anchored.CanReplayWithoutAnchor())
	require.True(t, anchored.CanSwitchAccount())
}

func TestEnsureCindyResponsesStoreFalsePreservesContinuationFields(t *testing.T) {
	body := []byte(`{"store":true,"input":[{"type":"reasoning","id":"rs_foreign","encrypted_content":"cipher","phase":"analysis"},{"type":"function_call","id":"fc_foreign","call_id":"call_foreign","name":"tool","arguments":"{}"}]}`)
	got, err := EnsureCindyResponsesStoreFalse(body)
	require.NoError(t, err)
	require.JSONEq(t, `{"store":false,"input":[{"type":"reasoning","id":"rs_foreign","encrypted_content":"cipher","phase":"analysis"},{"type":"function_call","id":"fc_foreign","call_id":"call_foreign","name":"tool","arguments":"{}"}]}`, string(got))
}

func TestCindyContinuationAccumulatorProof(t *testing.T) {
	baseline := []json.RawMessage{
		json.RawMessage(`{"type":"message","id":"msg_foreign","role":"user","content":[{"type":"input_text","text":"first"}]}`),
		json.RawMessage(`{"type":"function_call","id":"fc_foreign","call_id":"call_foreign","name":"tool","arguments":"{}","phase":"analysis"}`),
	}
	delta := []byte(`{"previous_response_id":"resp_1","input":[{"type":"function_call_output","call_id":"call_foreign","output":"ok"}]}`)

	withoutProof, err := ClassifyCindyContinuation(delta, CindyContinuationProof{})
	require.NoError(t, err)
	require.Equal(t, CindyContinuationReferenceOnly, withoutProof.Mode)

	fullInput, exists, err := buildOpenAIWSReplayInputSequence(baseline, true, delta, true)
	require.NoError(t, err)
	require.True(t, exists)
	accumulated, err := setOpenAIWSPayloadInputSequence(delta, fullInput, true)
	require.NoError(t, err)

	withProof, err := ClassifyCindyContinuation(accumulated, CindyContinuationProof{VerifiedFullHistory: true})
	require.NoError(t, err)
	require.Equal(t, CindyContinuationAnchorPlusFull, withProof.Mode)
	require.Equal(t, "msg_foreign", gjson.GetBytes(accumulated, "input.0.id").String())
	require.Equal(t, "fc_foreign", gjson.GetBytes(accumulated, "input.1.id").String())
	require.Equal(t, "analysis", gjson.GetBytes(accumulated, "input.1.phase").String())
	require.Equal(t, "call_foreign", gjson.GetBytes(accumulated, "input.2.call_id").String())
}

func TestCindyContinuationFullLookingAnchorWithoutAccumulatorStaysDelta(t *testing.T) {
	body := []byte(`{"previous_response_id":"resp_1","input":[{"type":"message","role":"user","content":"first"},{"type":"message","role":"assistant","content":"second"},{"type":"message","role":"user","content":"third"}]}`)
	classification, err := ClassifyCindyContinuation(body, CindyContinuationProof{})
	require.NoError(t, err)
	require.Equal(t, CindyContinuationAnchorDelta, classification.Mode)
}

func TestPrepareCindyContinuationReplayPayload(t *testing.T) {
	baseline := []json.RawMessage{
		json.RawMessage(`{"type":"message","id":"msg_foreign","role":"user","content":"first","phase":"analysis"}`),
		json.RawMessage(`{"type":"function_call","id":"fc_foreign","call_id":"call_1","name":"tool","arguments":"{}"}`),
	}
	delta := []byte(`{"previous_response_id":"resp_1","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	fullInput, exists, err := buildOpenAIWSReplayInputSequence(baseline, true, delta, true)
	require.NoError(t, err)

	replayed, classification, ok := prepareCindyContinuationReplayPayload(delta, fullInput, exists, true)
	require.True(t, ok)
	require.Equal(t, CindyContinuationAnchorPlusFull, classification.Mode)
	require.False(t, gjson.GetBytes(replayed, "previous_response_id").Exists())
	require.Equal(t, "msg_foreign", gjson.GetBytes(replayed, "input.0.id").String())
	require.Equal(t, "analysis", gjson.GetBytes(replayed, "input.0.phase").String())
	require.Equal(t, "fc_foreign", gjson.GetBytes(replayed, "input.1.id").String())
	require.Equal(t, "call_1", gjson.GetBytes(replayed, "input.2.call_id").String())

	unchanged, noProof, ok := prepareCindyContinuationReplayPayload(delta, fullInput, exists, false)
	require.False(t, ok)
	require.Equal(t, CindyContinuationAnchorDelta, noProof.Mode)
	require.JSONEq(t, string(delta), string(unchanged))

	reference := []byte(`{"previous_response_id":"resp_1","input":[{"type":"item_reference","id":"call_1"}]}`)
	unchanged, referenceClass, ok := prepareCindyContinuationReplayPayload(reference, nil, false, true)
	require.False(t, ok)
	require.Equal(t, CindyContinuationReferenceOnly, referenceClass.Mode)
	require.JSONEq(t, string(reference), string(unchanged))
}
