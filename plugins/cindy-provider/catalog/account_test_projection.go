package catalog

import (
	"slices"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func (r Registry) AccountTestPlanV1() extensionv1.CindyAccountTestPlanV1 {
	return accountTestPlanFromSnapshot(r.CatalogSnapshotV1(), r.CindyManagedModelMappings())
}

func accountTestPlanFromSnapshot(snapshot extensionv1.CindyCatalogSnapshotV1, aliases []CindyCatalogModel) extensionv1.CindyAccountTestPlanV1 {
	plan := extensionv1.CindyAccountTestPlanV1{
		SchemaVersion: 1, CatalogSnapshot: snapshot,
		Models: make([]CindyCatalogModel, 0, len(snapshot.CatalogModels)+len(aliases)),
	}
	// Management test choices retain their existing policy, independently of
	// publication flags. They do not enable a capability or authorize test IO.
	for _, model := range append(slices.Clone(snapshot.CatalogModels), aliases...) {
		if model.Managed && model.PublicModel && model.Verified &&
			(slices.Contains(model.Endpoints, CindyEndpointResponses) || slices.Contains(model.Endpoints, CindyEndpointImagesGenerate)) {
			plan.Models = append(plan.Models, model)
		}
	}
	for _, preferred := range []string{snapshot.DefaultTestModel.PublicID, "gpt-5.6-sol"} {
		for _, model := range plan.Models {
			if model.ID == preferred && slices.Contains(model.Endpoints, CindyEndpointResponses) {
				plan.DefaultModelID = model.ID
				return plan
			}
		}
	}
	for _, model := range plan.Models {
		if slices.Contains(model.Endpoints, CindyEndpointResponses) {
			plan.DefaultModelID = model.ID
			return plan
		}
	}
	for _, model := range plan.Models {
		if strings.Contains(model.ID, "sonnet") {
			plan.DefaultModelID = model.ID
			return plan
		}
	}
	if len(plan.Models) > 0 {
		plan.DefaultModelID = plan.Models[0].ID
	}
	return plan
}
