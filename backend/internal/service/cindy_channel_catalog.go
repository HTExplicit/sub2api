package service

import (
	"sort"
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

	capabilities := CindyCapabilities()
	mapping := make(map[string]string, len(capabilities)*2+len(cindyCompatibilityAliases))
	for i := range capabilities {
		capability := capabilities[i]
		if !capability.PublicModel || len(capability.VerifiedEndpoints) == 0 {
			continue
		}
		mapping[capability.PublicID] = capability.LiveUpstreamID
		mapping[capability.LiveUpstreamID] = capability.LiveUpstreamID
	}
	for alias, publicID := range cindyCompatibilityAliases {
		if capability := cindyCapabilityByPublicID[publicID]; capability != nil && capability.PublicModel {
			mapping[alias] = capability.LiveUpstreamID
		}
	}
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
	models := make([]string, 0, len(cindyCapabilityCatalog))
	for i := range cindyCapabilityCatalog {
		capability := cindyCapabilityCatalog[i]
		if capability.PublicModel && len(capability.VerifiedEndpoints) > 0 {
			models = append(models, capability.PublicID)
		}
	}
	sort.Strings(models)
	return models
}

func cindyManagedChannelModelAllowed(model string) bool {
	capability, ok := resolveKnownCindyCapability(strings.TrimSpace(model))
	return ok && capability.PublicModel && len(capability.VerifiedEndpoints) > 0
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
