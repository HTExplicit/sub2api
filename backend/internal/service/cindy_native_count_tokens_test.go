package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidAnthropicCountTokensResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "zero", body: `{"input_tokens":0}`, want: true},
		{name: "positive integer", body: `{"input_tokens":23}`, want: true},
		{name: "missing", body: `{}`, want: false},
		{name: "string", body: `{"input_tokens":"23"}`, want: false},
		{name: "negative", body: `{"input_tokens":-1}`, want: false},
		{name: "decimal", body: `{"input_tokens":1.5}`, want: false},
		{name: "exponent", body: `{"input_tokens":1e2}`, want: false},
		{name: "malformed", body: `{"input_tokens":`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, validAnthropicCountTokensResponse([]byte(tt.body)))
		})
	}
}
