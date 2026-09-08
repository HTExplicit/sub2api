package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// This deterministic self-test reports policy, not production model limits.
	OfficialCodexContextContractSuccessLine = "CODEX_CONTEXT_CONTRACT|version=2|valid=true|priority=custom,official,upstream,default|default_context=200000|protected=preserved|sentinel=preserved"
	OfficialCodexContextContractFailureLine = "CODEX_CONTEXT_CONTRACT|valid=false|reason=contract-mismatch"
	OfficialCodexContextInvalidArgsLine     = "CODEX_CONTEXT_CONTRACT|valid=false|reason=invalid-arguments"
	officialCodexContextContractSentinel    = "codex-context-contract-v2"
)

type officialCodexContextContractEnvelope struct {
	Models           []officialCodexContextContractModel `json:"models"`
	ContractSentinel string                              `json:"contract_sentinel"`
}

type officialCodexContextContractModel struct {
	Slug              string          `json:"slug"`
	ContextWindow     *int64          `json:"context_window"`
	MaxContextWindow  *int64          `json:"max_context_window"`
	AutoCompactLimit  json.RawMessage `json:"auto_compact_token_limit"`
	Source            string          `json:"context_capacity_source"`
	Basis             string          `json:"context_capacity_basis"`
	ContractSentinel  string          `json:"contract_sentinel"`
	UnknownCapability json.RawMessage `json:"unknown_capability"`
}

// VerifyOfficialCodexContextContract exercises the production pure resolver and
// final projection without configuration, network, credentials, DB, or Redis.
func VerifyOfficialCodexContextContract() (string, error) {
	body, err := buildOfficialCodexContextContractFixture()
	if err != nil {
		return "", err
	}
	if err := verifyNormalizedOfficialCodexContextContract(body); err != nil {
		return "", err
	}
	return OfficialCodexContextContractSuccessLine, nil
}

func buildOfficialCodexContextContractFixture() ([]byte, error) {
	custom := int64(512000)
	official := &OfficialModelContextCapacity{
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000},
		ModelID:              "fixture-official",
	}
	upstream := &ModelContextCapacity{ContextWindow: 64000, MaxContextWindow: 128000}
	cases := []struct {
		slug     string
		capacity ResolvedModelContextCapacity
		compact  int64
	}{
		{"fixture-custom", ResolveModelContextCapacity(&custom, official, upstream), 9999999},
		{"fixture-official", ResolveModelContextCapacity(nil, official, upstream), 9999999},
		{"fixture-upstream", ResolveModelContextCapacity(nil, nil, upstream), 50000},
		{"fixture-default", ResolveModelContextCapacity(nil, nil, nil), 9999999},
		{"fixture-protected", ResolvedModelContextCapacity{Source: "protected"}, 666666},
	}
	models := make([]map[string]json.RawMessage, 0, len(cases))
	for _, test := range cases {
		fields := map[string]json.RawMessage{
			"slug":                     json.RawMessage(fmt.Sprintf("%q", test.slug)),
			"context_window":           json.RawMessage("777000"),
			"max_context_window":       json.RawMessage("888000"),
			"auto_compact_token_limit": json.RawMessage(fmt.Sprintf("%d", test.compact)),
			"contract_sentinel":        json.RawMessage(fmt.Sprintf("%q", officialCodexContextContractSentinel)),
			"unknown_capability":       json.RawMessage(`{"keep":true}`),
		}
		ApplyModelContextCapacityToFields(fields, test.capacity, true)
		models = append(models, fields)
	}
	return json.Marshal(map[string]any{"models": models, "contract_sentinel": officialCodexContextContractSentinel})
}

func verifyNormalizedOfficialCodexContextContract(body []byte) error {
	var envelope officialCodexContextContractEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode normalized manifest: %w", err)
	}
	if envelope.ContractSentinel != officialCodexContextContractSentinel {
		return errors.New("top-level sentinel changed")
	}
	checks := []struct {
		slug, source, basis, compact string
		contextWindow, maxWindow     int64
	}{
		{"fixture-custom", "custom", "total_context", "null", 512000, 512000},
		{"fixture-official", "official", "total_context", "null", 1000000, 1000000},
		{"fixture-upstream", "upstream", "total_context", "50000", 64000, 128000},
		{"fixture-default", "default", "total_context", "null", 200000, 200000},
		{"fixture-protected", "", "", "666666", 777000, 888000},
	}
	if len(envelope.Models) != len(checks) {
		return errors.New("model fixture count changed")
	}
	bySlug := make(map[string]officialCodexContextContractModel, len(envelope.Models))
	for _, model := range envelope.Models {
		if _, exists := bySlug[model.Slug]; exists {
			return errors.New("duplicate model fixture")
		}
		bySlug[model.Slug] = model
	}
	for _, check := range checks {
		model, exists := bySlug[check.slug]
		if !exists || model.ContextWindow == nil || *model.ContextWindow != check.contextWindow ||
			model.MaxContextWindow == nil || *model.MaxContextWindow != check.maxWindow ||
			model.Source != check.source || model.Basis != check.basis ||
			!bytes.Equal(bytes.TrimSpace(model.AutoCompactLimit), []byte(check.compact)) {
			return fmt.Errorf("model %q policy mismatch", check.slug)
		}
		if model.ContractSentinel != officialCodexContextContractSentinel ||
			!bytes.Equal(bytes.TrimSpace(model.UnknownCapability), []byte(`{"keep":true}`)) {
			return errors.New("unrelated model capability changed")
		}
	}
	return nil
}
