package service

import extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"

// lookupExtensionCatalog resolves the release-pinned official catalog. No
// remaining plugin declares a model catalog, so plugin health cannot hide it.
func lookupExtensionCatalog(query extensionv1.CatalogQuery) *OfficialModelContextCapacity {
	official, err := resolveOfficialModelCatalog(query)
	if err != nil {
		return nil
	}
	return official.Entry
}
