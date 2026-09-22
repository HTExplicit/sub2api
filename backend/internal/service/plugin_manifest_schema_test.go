//go:build unit

package service

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// The manifest closes each contribution with additionalProperties:false.
// Mentioning a member only in an if/then condition does not declare it there.
func TestPluginManifestSchemaDeclaresContributionFields(t *testing.T) {
	raw, err := os.ReadFile("../../pkg/pluginapi/v1/manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Properties struct {
			Contributions struct {
				Items struct {
					AdditionalProperties bool `json:"additionalProperties"`
					Properties map[string]struct { Type string `json:"type"` } `json:"properties"`
				} `json:"items"`
			} `json:"contributions"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	items := document.Properties.Contributions.Items
	if items.AdditionalProperties || len(items.Properties) == 0 {
		t.Fatal("contributions must declare a closed property set")
	}
	fields := reflect.TypeOf(extensionv1.Contribution{})
	for index := 0; index < fields.NumField(); index++ {
		name := strings.Split(fields.Field(index).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, declared := items.Properties[name]; !declared {
			t.Errorf("public contribution field %q is missing from the closed manifest schema", name)
		}
	}
	if items.Properties["all_accounts"].Type != "boolean" {
		t.Error("all_accounts must use its existing boolean SDK contract")
	}
}

func TestPluginManifestAcceptsRegisteredHyphenatedResources(t *testing.T) {
	raw, err := os.ReadFile("../../../plugins/admin-observability/manifest.source.json")
	if err != nil {
		t.Fatal(err)
	}
	var source PluginManifest
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Resources) == 0 {
		t.Fatal("the fixture must use the actual registered Prompt Audit resources")
	}
	manifest := testPluginManifest(nil)
	manifest.Requires.ExtensionAPI = extensionv1.Version
	manifest.Capabilities = []PluginCapability{{ID: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*"}}
	manifest.Resources = source.Resources
	if err := manifest.Validate(); err != nil {
		t.Fatalf("existing registered resource names must be representable: %v", err)
	}
}
