package service

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
	mapping := make(map[string]string, len(capabilities))
	pricing := make([]ChannelModelPricing, 0, len(capabilities))
	for i := range capabilities {
		capability := capabilities[i]
		if !capability.PublicModel || len(capability.VerifiedEndpoints) == 0 {
			continue
		}
		mapping[capability.PublicID] = capability.LiveUpstreamID
		pricing = append(pricing, cindyCatalogChannelPricing(capability))
	}
	channel.ModelMapping = map[string]map[string]string{PlatformCindy: mapping}
	channel.ModelPricing = pricing
	return true
}

func isManagedCindyCatalogChannel(channel *Channel) bool {
	if channel == nil || channel.FeaturesConfig == nil {
		return false
	}
	marker, ok := channel.FeaturesConfig[CindyCatalogChannelMarkerKey].(string)
	return ok && marker == CindyCatalogChannelMarkerValue
}

func cindyCatalogChannelPricing(capability CindyCapability) ChannelModelPricing {
	entry := ChannelModelPricing{
		Platform:    PlatformCindy,
		Models:      []string{capability.PublicID},
		BillingMode: BillingModeToken,
	}
	if capability.TextPricing != nil {
		pricing := *capability.TextPricing
		if discounted, err := cindyCatalogCostDiscount(capability.PublicID); err == nil {
			pricing = applyCindyCatalogCostDiscount(pricing, discounted)
		}
		entry.InputPrice = optionalCindyChannelPrice(pricing.InputCostPerToken)
		entry.OutputPrice = optionalCindyChannelPrice(pricing.OutputCostPerToken)
		entry.CacheReadPrice = optionalCindyChannelPrice(pricing.CacheReadInputTokenCost)
		if pricing.CacheCreationInputTokenCostPresent {
			entry.CacheWritePrice = optionalCindyChannelPrice(pricing.CacheCreationInputTokenCost)
		}
	}
	if capability.ImagePricing != nil {
		pricing := capability.ImagePricing
		entry.InputPrice = optionalCindyChannelPrice(pricing.InputCostPerToken)
		entry.OutputPrice = optionalCindyChannelPrice(pricing.OutputCostPerToken)
		entry.CacheReadPrice = optionalCindyChannelPrice(pricing.CacheReadInputTokenCost)
		entry.ImageInputPrice = optionalCindyChannelPrice(pricing.InputCostPerImageToken)
		entry.ImageOutputPrice = optionalCindyChannelPrice(pricing.OutputCostPerImageToken)
	}
	return entry
}

func optionalCindyChannelPrice(value float64) *float64 {
	if value == 0 {
		return nil
	}
	return &value
}
