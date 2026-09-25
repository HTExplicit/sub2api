package service

import (
	"testing"
)

func TestOfficialModelCatalogPreservesProductScope(t *testing.T) {
	for _, tc := range []struct {
		name, model, host string
		want              bool
	}{
		{"known model", "gpt-6-astra", "api.openai.com", true},
		{"unknown date suffix", "gpt-6-astra-2099-01-01", "api.openai.com", false},
		{"known Bailian endpoint", "qwen3.8-max", "dashscope.aliyuncs.com", true},
		{"unrelated endpoint", "qwen3.8-max", "relay.example.test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := officialCatalogQuery{Candidates: []string{tc.model}, Platform: "openai", Scheme: "https", Host: tc.host}
			if match := lookupOfficialModelCatalog(query); (match != nil) != tc.want {
				t.Fatalf("unexpected reference: %+v", match)
			}
		})
	}
}

func TestOfficialModelCatalogSnapshotCannotMutateActiveReferences(t *testing.T) {
	before := OfficialModelCatalogSnapshot()
	after := OfficialModelCatalogSnapshot()
	after[0].Aliases[0] = "not-a-real-alias"
	if OfficialModelCatalogSnapshot()[0].Aliases[0] != before[0].Aliases[0] {
		t.Fatal("catalog exposed mutable alias storage")
	}
}

func TestOfficialModelCatalogGPT6UsesCodexSubscriptionValues(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		query := officialCatalogQuery{Candidates: []string{model}, Platform: "openai", Scheme: "https", Host: "api.openai.com"}
		entry := lookupOfficialModelCatalog(query)
		if entry == nil {
			t.Fatalf("%s: reference unavailable", model)
		}
		if entry.Product != "codex_subscription" || entry.ContextWindow != 272000 || entry.MaxContextWindow != 872000 || entry.MaxInputTokens != 0 || len(entry.Aliases) != 0 {
			t.Fatalf("%s: incorrect subscription capacity or invented alias: %+v", model, entry)
		}
		ref := entry.Reference
		if ref == nil || ref.ContextWindow != 272000 || ref.MaxContextWindow != 872000 || ref.SourceURL != GPT6ContextCapacityReferenceSource || ref.Release != GPT6ContextCapacityReferenceCommit {
			t.Fatalf("%s: incorrect subscription reference: %+v", model, ref)
		}
		query.Candidates = []string{model + "-2099-01-01"}
		if lookupOfficialModelCatalog(query) != nil {
			t.Fatal("unpublished dated snapshots must not inherit capacity")
		}
	}
}
