package service

import (
	"strings"
)

const (
	CindyCatalogChannelName        = "Cindy Catalog"
	CindyCatalogChannelMarkerKey   = "cindy_catalog_managed"
	CindyCatalogChannelMarkerValue = "cindy_laxa_v1"
)

// hydrateManagedCindyCatalogChannel projects the release-owned catalog onto
// the managed channel. The database stores only a stable marker and binding,
// so model IDs and prices continue to have one source of truth.
func hydrateManagedCindyCatalogChannel(channel *Channel) bool {
	if !isManagedCindyCatalogChannel(channel) {
		return false
	}

	mapping, _ := cindyManagedChannelProjection()
	channel.ModelMapping = map[string]map[string]string{PlatformCindy: mapping}
	channel.ModelPricing = nil
	return true
}

func isManagedCindyCatalogChannel(channel *Channel) bool {
	if channel == nil || channel.FeaturesConfig == nil {
		return false
	}
	marker, ok := channel.FeaturesConfig[CindyCatalogChannelMarkerKey].(string)
	return ok && marker == CindyCatalogChannelMarkerValue
}

func cindyInternalPublicModelIDs() []string {
	_, models := cindyManagedChannelProjection()
	return models
}

func cindyManagedChannelProjection() (map[string]string, []string) {
	mapping := map[string]string{}
	var models []string
	queryCindyCatalog("ManagedChannelProjection", nil, []any{&mapping, &models})
	return mapping, models
}

func cindyManagedChannelModelAllowed(model string) bool {
	var allowed bool
	queryCindyCatalog("ManagedChannelModelAllowed", []any{model}, []any{&allowed})
	return allowed
}

func cindyManagedChannelNameReserved(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), CindyCatalogChannelName)
}

func hasCindyManagedChannelMarker(featuresConfig map[string]any) bool {
	if featuresConfig == nil {
		return false
	}
	_, ok := featuresConfig[CindyCatalogChannelMarkerKey]
	return ok
}
