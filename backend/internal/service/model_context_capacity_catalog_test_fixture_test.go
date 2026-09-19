package service

// Contract tests use the actual independent catalog module as their in-process
// fixture. Production reaches it only through the enabled plugin process.
import catalog "github.com/HTExplicit/sub2api-plugins/modelpolicy/catalog"

var officialModelContextCapacityCatalog = catalog.Snapshot()

const GPTContextCapacityReferenceRelease = catalog.ReferenceRelease
const gptContextCapacityReferenceSource = catalog.ReferenceSource
const gptContextCapacityReferenceVerifiedAt = catalog.ReferenceVerifiedAt

func init() { processExtensionCatalog.Store(&extensionCatalogProvider{resolver: catalog.New()}) }
