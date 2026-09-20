package catalog

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestManagedChannelProjectionPreservesAliasesAndRejectsUnverifiedModels(t *testing.T) {
	registry := Registry{Config: extensionv1.CindyProviderConfig{CatalogEnabled: false}}
	mapping, models := registry.managedChannelProjection()
	if len(models) == 0 || mapping["gpt-5.4-mini"] != "openai/gpt-5.6-luna" {
		t.Fatal("managed projection must be independent of public catalog visibility")
	}
	for _, model := range []string{"cindy/auto-review", "gpt-5.4", "unrelated-model"} {
		if registry.managedChannelModelAllowed(model) {
			t.Fatalf("unexpected model: %s", model)
		}
	}
	if !registry.managedChannelModelAllowed(" gpt-5.4-mini ") {
		t.Fatal("managed alias was lost")
	}
}
