package service

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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
			query := extensionv1.CatalogQuery{Candidates: []string{tc.model}, Platform: "openai", AccountType: "apikey", Scheme: "https", Host: tc.host}
			match, err := resolveOfficialModelCatalog(query)
			if err != nil || (match.Entry != nil) != tc.want {
				t.Fatalf("unexpected reference: %+v, %v", match, err)
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

func TestOfficialModelCatalogGPT6SeparatesAPIAndCodex(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		query := extensionv1.CatalogQuery{Candidates: []string{model}, Platform: "openai", AccountType: "apikey", Scheme: "https", Host: "api.openai.com"}
		match, err := resolveOfficialModelCatalog(query)
		if err != nil || match.Entry == nil {
			t.Fatalf("%s: reference unavailable: %v", model, err)
		}
		entry := match.Entry
		if entry.Product != "api" || entry.ContextWindow != 1050000 || entry.MaxOutputTokens != 128000 || entry.MaxInputTokens != 0 || len(entry.Aliases) != 0 {
			t.Fatalf("%s: incorrect API capacity or invented alias: %+v", model, entry)
		}
		ref := entry.Reference
		if ref == nil || ref.ContextWindow != 272000 || ref.MaxContextWindow != 872000 || ref.SourceURL != GPT6ContextCapacityReferenceSource || ref.Release != GPT6ContextCapacityReferenceCommit {
			t.Fatalf("%s: incorrect subscription reference: %+v", model, ref)
		}
		query.Candidates = []string{model + "-2099-01-01"}
		unknown, err := resolveOfficialModelCatalog(query)
		if err != nil || unknown.Entry != nil {
			t.Fatal("unpublished dated snapshots must not inherit capacity")
		}
	}
}
