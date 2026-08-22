package service

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyOpenAIPreviousResponseIDKind(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "empty", id: " ", want: OpenAIPreviousResponseIDKindEmpty},
		{name: "response_id", id: "resp_0906a621bc423a8d0169a108637ef88197b74b0e2f37ba358f", want: OpenAIPreviousResponseIDKindResponseID},
		{name: "message_id", id: "msg_123456", want: OpenAIPreviousResponseIDKindMessageID},
		{name: "item_id", id: "item_abcdef", want: OpenAIPreviousResponseIDKindMessageID},
		{name: "unknown", id: "foo_123456", want: OpenAIPreviousResponseIDKindUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyOpenAIPreviousResponseIDKind(tc.id); got != tc.want {
				t.Fatalf("ClassifyOpenAIPreviousResponseIDKind(%q)=%q want=%q", tc.id, got, tc.want)
			}
		})
	}
}

func TestIsOpenAIPreviousResponseIDLikelyMessageID(t *testing.T) {
	if !IsOpenAIPreviousResponseIDLikelyMessageID("msg_123") {
		t.Fatal("expected msg_123 to be identified as message id")
	}
	if IsOpenAIPreviousResponseIDLikelyMessageID("resp_123") {
		t.Fatal("expected resp_123 not to be identified as message id")
	}
}

func TestParseOpenAIContinuationAnchor(t *testing.T) {
	productionLengthID := "resp_" + strings.Repeat("a", 452)
	overLimitID := "resp_" + strings.Repeat("a", OpenAIContinuationAnchorMaxLength-len("resp_")+1)
	tests := []struct {
		name    string
		payload string
		want    string
		wantErr bool
	}{
		{name: "missing", payload: `{"input":"hello"}`},
		{name: "null", payload: `{"previous_response_id":null}`},
		{name: "empty", payload: `{"previous_response_id":""}`},
		{name: "whitespace", payload: `{"previous_response_id":"  "}`},
		{name: "valid", payload: `{"previous_response_id":"resp_123"}`, want: "resp_123"},
		{name: "valid trimmed", payload: `{"previous_response_id":"  resp_abc-123  "}`, want: "resp_abc-123"},
		{name: "production length response id", payload: `{"previous_response_id":"` + productionLengthID + `"}`, want: productionLengthID},
		{name: "over bounded length", payload: `{"previous_response_id":"` + overLimitID + `"}`, wantErr: true},
		{name: "number", payload: `{"previous_response_id":123}`, wantErr: true},
		{name: "boolean", payload: `{"previous_response_id":true}`, wantErr: true},
		{name: "object", payload: `{"previous_response_id":{"id":"resp_123"}}`, wantErr: true},
		{name: "array", payload: `{"previous_response_id":["resp_123"]}`, wantErr: true},
		{name: "unknown string", payload: `{"previous_response_id":"other_123"}`, wantErr: true},
		{name: "message string", payload: `{"previous_response_id":"msg_123"}`, wantErr: true},
		{name: "empty response suffix", payload: `{"previous_response_id":"resp_"}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOpenAIContinuationAnchor([]byte(tt.payload))
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidOpenAIContinuationAnchor) {
					t.Fatalf("ParseOpenAIContinuationAnchor() error=%v, want %v", err, ErrInvalidOpenAIContinuationAnchor)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOpenAIContinuationAnchor() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseOpenAIContinuationAnchor()=%q, want %q", got, tt.want)
			}
		})
	}
}
