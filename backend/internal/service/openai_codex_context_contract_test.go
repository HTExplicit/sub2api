package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT56OfficialCodexContextContractUsesRuntimeTransformer(t *testing.T) {
	line, err := VerifyOfficialCodexContextContract()
	require.NoError(t, err)
	require.Equal(t, OfficialCodexContextContractSuccessLine, line)
}

func TestGPT56NormalizedOfficialCodexContextContractFailsClosed(t *testing.T) {
	valid := `{"models":[{"slug":"gpt-5.6-sol","context_window":1000000,"max_context_window":1000000,"auto_compact_token_limit":900000},{"slug":"gpt-5.6-terra","context_window":272000,"max_context_window":921000,"auto_compact_token_limit":null},{"slug":"gpt-5.6-luna","context_window":272000,"max_context_window":921000,"auto_compact_token_limit":null},{"slug":"gpt-5.5","context_window":777000,"contract_sentinel":"codex-context-contract-v1"}],"contract_sentinel":"codex-context-contract-v1"}`
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid envelope", body: `{"models":`},
		{name: "missing target", body: strings.Replace(valid, `{"slug":"gpt-5.6-luna","context_window":272000,"max_context_window":921000,"auto_compact_token_limit":null},`, "", 1)},
		{name: "duplicate target", body: strings.Replace(valid, `{"slug":"gpt-5.6-luna"`, `{"slug":"gpt-5.6-sol","context_window":1000000,"max_context_window":1000000,"auto_compact_token_limit":900000},{"slug":"gpt-5.6-luna"`, 1)},
		{name: "wrong sol context", body: strings.Replace(valid, `"context_window":1000000`, `"context_window":1050000`, 1)},
		{name: "numeric terra compact", body: strings.Replace(valid, `"auto_compact_token_limit":null`, `"auto_compact_token_limit":900000`, 1)},
		{name: "missing luna compact", body: strings.Replace(valid, `,"auto_compact_token_limit":null},{"slug":"gpt-5.5"`, `},{"slug":"gpt-5.5"`, 1)},
		{name: "top sentinel changed", body: strings.Replace(valid, `}],"contract_sentinel":"codex-context-contract-v1"}`, `}],"contract_sentinel":"changed"}`, 1)},
		{name: "model sentinel changed", body: strings.Replace(valid, `"context_window":777000,"contract_sentinel":"codex-context-contract-v1"`, `"context_window":777000,"contract_sentinel":"changed"`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, verifyNormalizedOfficialCodexContextContract([]byte(test.body)))
		})
	}
}

func TestGPT56NormalizedOfficialCodexContextContractAcceptsExactContract(t *testing.T) {
	body := []byte(`{"models":[{"slug":"gpt-5.6-sol","context_window":1000000,"max_context_window":1000000,"auto_compact_token_limit":900000},{"slug":"gpt-5.6-terra","context_window":272000,"max_context_window":921000,"auto_compact_token_limit":null},{"slug":"gpt-5.6-luna","context_window":272000,"max_context_window":921000,"auto_compact_token_limit":null},{"slug":"gpt-5.5","context_window":777000,"contract_sentinel":"codex-context-contract-v1"}],"contract_sentinel":"codex-context-contract-v1"}`)
	require.NoError(t, verifyNormalizedOfficialCodexContextContract(body))
}
