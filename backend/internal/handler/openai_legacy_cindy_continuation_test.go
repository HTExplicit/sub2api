package handler

import "testing"

func TestLegacyLaxaContinuationPayloadCandidateRecognizesOpaqueAndReferenceItems(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "anchor", payload: `{"previous_response_id":"resp_1","input":"next"}`, want: true},
		{name: "item reference", payload: `{"input":[{"type":"item_reference","id":"item_1"}]}`, want: true},
		{name: "nested item reference", payload: `{"input":[{"content":[{"type":"item_reference","id":"item_1"}]}]}`, want: true},
		{name: "reasoning id only", payload: `{"input":[{"type":"reasoning","id":"rs_1"}]}`, want: true},
		{name: "compaction id only", payload: `{"input":[{"type":"compaction","id":"cmp_1"}]}`, want: true},
		{name: "compaction summary id only", payload: `{"input":[{"type":"compaction_summary","id":"sum_1"}]}`, want: true},
		{name: "encrypted reasoning", payload: `{"input":[{"type":"reasoning","encrypted_content":"cipher"}]}`, want: true},
		{name: "ordinary id is not continuation", payload: `{"input":[{"type":"message","id":"msg_1","content":"hello"}]}`, want: false},
		{name: "metadata marker is not continuation", payload: `{"metadata":{"item_reference":"schema-token","encrypted_content":"schema"},"input":"hello"}`, want: false},
		{name: "invalid json", payload: `{"input":`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := legacyLaxaContinuationPayloadCandidate([]byte(tt.payload)); got != tt.want {
				t.Fatalf("legacyLaxaContinuationPayloadCandidate() = %v, want %v", got, tt.want)
			}
		})
	}
}
