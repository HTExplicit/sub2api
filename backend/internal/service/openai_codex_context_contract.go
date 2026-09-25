package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// This deterministic self-test reports policy, not production model limits.
	OfficialCodexContextContractSuccessLine = "CODEX_CONTEXT_CONTRACT|version=3|valid=true|priority=custom,upstream,official,registry|unknown=unchanged|aggregate=group_minimum|sentinel=preserved"
	OfficialCodexContextContractFailureLine = "CODEX_CONTEXT_CONTRACT|valid=false|reason=contract-mismatch"
	OfficialCodexContextInvalidArgsLine     = "CODEX_CONTEXT_CONTRACT|valid=false|reason=invalid-arguments"
	officialCodexContextContractSentinel    = "codex-context-contract-v3"
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
	Reason            string          `json:"context_capacity_reason"`
	ContractSentinel  string          `json:"contract_sentinel"`
	UnknownCapability json.RawMessage `json:"unknown_capability"`
}

// VerifyOfficialCodexContextContract exercises the production pure resolver,
// group aggregation and final projection without configuration, network,
// credentials, DB, or Redis.
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
	custom := modelContextEvidence{ModelContextSourceCustom, ModelContextCapacity{ContextWindow: 512000, MaxContextWindow: 512000}}
	upstream := modelContextEvidence{ModelContextSourceUpstream, ModelContextCapacity{ContextWindow: 64000, MaxContextWindow: 128000}}
	official := modelContextEvidence{ModelContextSourceOfficial, ModelContextCapacity{ContextWindow: 272000, MaxContextWindow: 872000}}
	registry := modelContextEvidence{ModelContextSourceRegistry, ModelContextCapacity{ContextWindow: 400000}}
	relay := modelContextEvidence{ModelContextSourceUpstream, ModelContextCapacity{ContextWindow: 1050000}}
	cases := []struct {
		slug     string
		capacity ResolvedModelContextCapacity
		compact  int64
	}{
		{"fixture-custom", resolveModelContextEvidence([]modelContextEvidence{custom, upstream, official, registry}), 9999999},
		{"fixture-upstream", resolveModelContextEvidence([]modelContextEvidence{upstream, official, registry}), 50000},
		{"fixture-official", resolveModelContextEvidence([]modelContextEvidence{official, registry}), 9999999},
		{"fixture-registry", resolveModelContextEvidence([]modelContextEvidence{registry}), 9999999},
		{"fixture-unknown", resolveModelContextEvidence(nil), 666666},
		{"fixture-group", minimumModelContextCapacity([]ResolvedModelContextCapacity{
			resolveModelContextEvidence([]modelContextEvidence{relay, official}),
			resolveModelContextEvidence([]modelContextEvidence{official}),
		}), 9999999},
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
		slug, source, reason, compact string
		contextWindow, maxWindow      int64
	}{
		{"fixture-custom", "custom", "", "null", 512000, 512000},
		{"fixture-upstream", "upstream", "", "50000", 64000, 128000},
		{"fixture-official", "official", "", "null", 272000, 872000},
		{"fixture-registry", "registry", "", "null", 400000, 400000},
		{"fixture-unknown", "", "", "666666", 777000, 888000},
		{"fixture-group", "official", "group_minimum", "null", 272000, 872000},
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
			model.Source != check.source || model.Reason != check.reason ||
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
