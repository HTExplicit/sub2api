package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelContextCapacityContractUsesRuntimePolicyAndProjection(t *testing.T) {
	line, err := VerifyOfficialCodexContextContract()
	require.NoError(t, err)
	require.Equal(t, OfficialCodexContextContractSuccessLine, line)
	require.Contains(t, line, "version=2")
	require.NotContains(t, line, "sol_context=")
}

func TestModelContextCapacityContractFailsClosed(t *testing.T) {
	body, err := buildOfficialCodexContextContractFixture()
	require.NoError(t, err)
	valid := string(body)
	tests := []struct{ name, body string }{
		{"invalid envelope", `{"models":`},
		{"wrong custom context", strings.Replace(valid, `"context_window":512000`, `"context_window":512001`, 1)},
		{"wrong priority source", strings.Replace(valid, `"context_capacity_source":"official"`, `"context_capacity_source":"upstream"`, 1)},
		{"wrong default", strings.Replace(valid, `"context_window":200000`, `"context_window":272000`, 1)},
		{"unsafe compact threshold", strings.Replace(valid, `"auto_compact_token_limit":null`, `"auto_compact_token_limit":9999999`, 1)},
		{"protected model changed", strings.Replace(valid, `"context_window":777000`, `"context_window":258000`, 1)},
		{"sentinel changed", strings.Replace(valid, officialCodexContextContractSentinel, "changed", 1)},
		{"unknown capability changed", strings.Replace(valid, `"keep":true`, `"keep":false`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NotEqual(t, valid, test.body, "mutation must alter the fixture")
			require.Error(t, verifyNormalizedOfficialCodexContextContract([]byte(test.body)))
		})
	}
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &envelope))
	var models []json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["models"], &models))
	t.Run("missing model", func(t *testing.T) {
		envelope["models"], err = json.Marshal(models[1:])
		require.NoError(t, err)
		missing, marshalErr := json.Marshal(envelope)
		require.NoError(t, marshalErr)
		require.Error(t, verifyNormalizedOfficialCodexContextContract(missing))
	})
	t.Run("duplicate model", func(t *testing.T) {
		duplicated := append([]json.RawMessage(nil), models...)
		duplicated[1] = duplicated[0]
		envelope["models"], err = json.Marshal(duplicated)
		require.NoError(t, err)
		duplicate, marshalErr := json.Marshal(envelope)
		require.NoError(t, marshalErr)
		require.Error(t, verifyNormalizedOfficialCodexContextContract(duplicate))
	})
}

func TestModelContextCapacityContractAcceptsDeterministicFixture(t *testing.T) {
	body, err := buildOfficialCodexContextContractFixture()
	require.NoError(t, err)
	require.NoError(t, verifyNormalizedOfficialCodexContextContract(body))
}
