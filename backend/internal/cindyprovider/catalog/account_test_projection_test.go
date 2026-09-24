package catalog

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func TestCindyAccountTestPlanOwnsProjectionAndKeepsV1(t *testing.T) {
	registry := Registry{Config: extensionv1.CindyProviderConfig{CatalogEnabled: true}}
	before, err := registry.Query(extensionv1.CindyCatalogQuery{Method: extensionv1.CindyCatalogSnapshotMethodV1})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := registry.Query(extensionv1.CindyCatalogQuery{Method: extensionv1.CindyAccountTestPlanMethodV1})
	if err != nil {
		t.Fatal(err)
	}
	var values []extensionv1.CindyAccountTestPlanV1
	if json.Unmarshal(raw, &values) != nil || len(values) != 1 || len(values[0].Models) == 0 {
		t.Fatal("the named method must return one complete nonempty provider plan")
	}
	if values[0].DefaultModelID != values[0].CatalogSnapshot.DefaultTestModel.PublicID {
		t.Fatal("the current default must come from the companion snapshot")
	}
	after, err := registry.Query(extensionv1.CindyCatalogQuery{Method: extensionv1.CindyCatalogSnapshotMethodV1})
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("requesting a plan changed the old strict V1 reply")
	}
	var old []map[string]json.RawMessage
	if json.Unmarshal(after, &old) != nil || len(old) != 1 {
		t.Fatal("invalid old V1 envelope")
	}
	for _, field := range []string{"models", "catalog_snapshot", "test_plan", "default_model_id"} {
		if _, exists := old[0][field]; exists {
			t.Fatalf("new presentation field leaked into old V1: %s", field)
		}
	}
	if _, err := registry.Query(extensionv1.CindyCatalogQuery{Method: extensionv1.CindyAccountTestPlanMethodV1, Args: []json.RawMessage{json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("unexpected query arguments accepted")
	}
	model := func(id string, public, verified bool, endpoint CindyEndpoint) CindyCatalogModel {
		return CindyCatalogModel{ID: id, DisplayName: id, Managed: true, PublicModel: public, Verified: verified, Endpoints: []CindyEndpoint{endpoint}}
	}
	snapshot := extensionv1.CindyCatalogSnapshotV1{
		DefaultTestModel: extensionv1.CindyModelReference{PublicID: "provider-default-b"},
		CatalogModels: []CindyCatalogModel{
			model("special-review", true, true, CindyEndpointReview),
			model("unverified", true, false, CindyEndpointResponses),
			model("private", false, true, CindyEndpointResponses),
			model("messages-only", true, true, CindyEndpointMessages),
			model("image-choice", true, true, CindyEndpointImagesGenerate),
			model("gpt-5.6-sol", true, true, CindyEndpointResponses),
			model("provider-default-b", true, true, CindyEndpointResponses),
		},
	}
	projection := accountTestPlanFromSnapshot(snapshot, nil)
	ids := make([]string, 0, len(projection.Models))
	for _, choice := range projection.Models {
		ids = append(ids, choice.ID)
	}
	if !reflect.DeepEqual(ids, []string{"image-choice", "gpt-5.6-sol", "provider-default-b"}) || projection.DefaultModelID != "provider-default-b" {
		t.Fatalf("provider selection was not authoritative: %v default=%s", ids, projection.DefaultModelID)
	}
	// Publication off does not change the management test projection or enable
	// any IO: only the descriptive snapshot flag differs.
	registry.Config.CatalogEnabled = false
	off := registry.AccountTestPlanV1()
	if !reflect.DeepEqual(values[0].Models, off.Models) || off.DefaultModelID != values[0].DefaultModelID {
		t.Fatal("management choices were incorrectly tied to catalog publication")
	}
}
