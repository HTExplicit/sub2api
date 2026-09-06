//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentRequestCapabilitiesIgnoreNullDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		native     bool
	}{
		{"absent", `{}`, false},
		{"nullable_defaults", `{"conversation":null,"previous_response_id":null,"background":false}`, false},
		{"conversation_id", `{"conversation":"conv_fixture"}`, true},
		{"conversation_object", `{"conversation":{"id":"conv_fixture"}}`, true},
		{"previous_response", `{"previous_response_id":"resp_fixture"}`, true},
		{"background", `{"background":true}`, true},
		{"portable_tool", `{"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}`, false},
		{"hosted_tool", `{"tools":[{"type":"file_search","vector_store_ids":["vs_fixture"]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.native, OpenAIResponsesRequireNativeUpstream([]byte(tc.body)))
		})
	}
}
