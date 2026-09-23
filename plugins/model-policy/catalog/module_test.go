package catalog

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestCatalogProcessContractPreservesProductScope(t *testing.T) {
	module := New()
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
			raw, _ := json.Marshal(query)
			out, err := module.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityCatalog, Operation: "resolve", Payload: raw})
			if err != nil {
				t.Fatal(err)
			}
			var match extensionv1.CatalogMatch
			if json.Unmarshal(out.Payload, &match) != nil || (match.Entry != nil) != tc.want {
				t.Fatalf("unexpected reference: %s", out.Payload)
			}
		})
	}
}

func TestCatalogSnapshotCannotMutateActiveReferences(t *testing.T) {
	before := Snapshot()
	after := Snapshot()
	after[0].Aliases[0] = "not-a-real-alias"
	if Snapshot()[0].Aliases[0] != before[0].Aliases[0] {
		t.Fatal("catalog exposed mutable alias storage")
	}
	if _, err := New().ValidateConfig(context.Background(), json.RawMessage(`{"arbitrary_host_command":"ignored"}`)); err == nil {
		t.Fatal("unknown configuration accepted")
	}
}

func TestGPT6NamedModelReferencesSeparateAPIAndCodex(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		query := extensionv1.CatalogQuery{Candidates: []string{model}, Platform: "openai", AccountType: "apikey", Scheme: "https", Host: "api.openai.com"}
		match, err := New().ResolveCatalog(context.Background(), query)
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
		unknown, err := New().ResolveCatalog(context.Background(), query)
		if err != nil || unknown.Entry != nil {
			t.Fatal("unpublished dated snapshots must not inherit capacity")
		}
	}
}
