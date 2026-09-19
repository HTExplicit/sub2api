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
