package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// OfficialCodexContextContractSuccessLine is the stable machine-readable
	// success record consumed by production acceptance.
	OfficialCodexContextContractSuccessLine = "CODEX_CONTEXT_CONTRACT|valid=true|sol_context=1000000|sol_max=1000000|sol_compact=900000|terra_context=272000|terra_max=921000|terra_compact=null|luna_context=272000|luna_max=921000|luna_compact=null|sentinel=preserved"
	// OfficialCodexContextContractFailureLine is the fixed redacted record for
	// deterministic contract failures.
	OfficialCodexContextContractFailureLine = "CODEX_CONTEXT_CONTRACT|valid=false|reason=contract-mismatch"
	// OfficialCodexContextInvalidArgsLine is emitted when the verification flag
	// is combined with another execution mode or positional arguments.
	OfficialCodexContextInvalidArgsLine = "CODEX_CONTEXT_CONTRACT|valid=false|reason=invalid-arguments"

	officialCodexContextContractSentinel = "codex-context-contract-v1"
	officialCodexContextContractFixture  = `{"models":[{"slug":"gpt-5.6-sol","context_window":1,"max_context_window":2,"auto_compact_token_limit":null,"contract_sentinel":"codex-context-contract-v1"},{"slug":"gpt-5.6-terra","context_window":3,"max_context_window":4,"auto_compact_token_limit":5},{"slug":"gpt-5.6-luna"},{"slug":"gpt-5.5","context_window":777000,"contract_sentinel":"codex-context-contract-v1"}],"contract_sentinel":"codex-context-contract-v1"}`
)

type officialCodexContextContractEnvelope struct {
	Models           []officialCodexContextContractModel `json:"models"`
	ContractSentinel string                              `json:"contract_sentinel"`
}

type officialCodexContextContractModel struct {
	Slug             string          `json:"slug"`
	ContextWindow    *int            `json:"context_window"`
	MaxContextWindow *int            `json:"max_context_window"`
	AutoCompactLimit json.RawMessage `json:"auto_compact_token_limit"`
	ContractSentinel string          `json:"contract_sentinel"`
}

// VerifyOfficialCodexContextContract exercises the exact runtime transformer
// used for ordinary non-Cindy API-key manifests. It performs no network,
// configuration, database, Redis, or credential access.
func VerifyOfficialCodexContextContract() (string, error) {
	normalized, err := normalizeOfficialCodexModelContexts([]byte(officialCodexContextContractFixture))
	if err != nil {
		return "", fmt.Errorf("normalize canonical manifest: %w", err)
	}
	if err := verifyNormalizedOfficialCodexContextContract(normalized); err != nil {
		return "", err
	}
	return OfficialCodexContextContractSuccessLine, nil
}

func verifyNormalizedOfficialCodexContextContract(body []byte) error {
	var envelope officialCodexContextContractEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode normalized manifest: %w", err)
	}
	if envelope.ContractSentinel != officialCodexContextContractSentinel {
		return errors.New("top-level sentinel changed")
	}

	bySlug := make(map[string][]officialCodexContextContractModel, len(envelope.Models))
	for _, model := range envelope.Models {
		bySlug[model.Slug] = append(bySlug[model.Slug], model)
	}
	checks := []struct {
		slug                            string
		contextWindow, maxContextWindow int
		autoCompact                     string
	}{
		{slug: "gpt-5.6-sol", contextWindow: 1000000, maxContextWindow: 1000000, autoCompact: "900000"},
		{slug: "gpt-5.6-terra", contextWindow: 272000, maxContextWindow: 921000, autoCompact: "null"},
		{slug: "gpt-5.6-luna", contextWindow: 272000, maxContextWindow: 921000, autoCompact: "null"},
	}
	for _, check := range checks {
		models := bySlug[check.slug]
		if len(models) != 1 {
			return fmt.Errorf("model %q count is %d", check.slug, len(models))
		}
		model := models[0]
		if model.ContextWindow == nil || *model.ContextWindow != check.contextWindow ||
			model.MaxContextWindow == nil || *model.MaxContextWindow != check.maxContextWindow ||
			!bytes.Equal(bytes.TrimSpace(model.AutoCompactLimit), []byte(check.autoCompact)) {
			return fmt.Errorf("model %q context contract mismatch", check.slug)
		}
	}

	sentinelModels := bySlug["gpt-5.5"]
	if len(sentinelModels) != 1 || sentinelModels[0].ContextWindow == nil ||
		*sentinelModels[0].ContextWindow != 777000 ||
		sentinelModels[0].ContractSentinel != officialCodexContextContractSentinel {
		return errors.New("unrelated model sentinel changed")
	}
	return nil
}
