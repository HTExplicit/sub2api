package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHydrateManagedCindyCatalogChannelUsesCanonicalCatalog(t *testing.T) {
	t.Parallel()

	channel := &Channel{
		Name:           CindyCatalogChannelName,
		RestrictModels: true,
		FeaturesConfig: map[string]any{CindyCatalogChannelMarkerKey: CindyCatalogChannelMarkerValue},
	}

	require.True(t, hydrateManagedCindyCatalogChannel(channel))
	require.Len(t, channel.ModelPricing, 22)
	require.Len(t, channel.ModelMapping[PlatformCindy], 22)

	for _, capability := range CindyCapabilities() {
		require.Equal(t, capability.LiveUpstreamID, channel.ModelMapping[PlatformCindy][capability.PublicID])
		pricing := channel.GetModelPricingByPlatform(PlatformCindy, capability.PublicID)
		require.NotNil(t, pricing, capability.PublicID)
	}
	require.Nil(t, channel.ModelMapping[PlatformOpenAI])
}

func TestHydrateManagedCindyCatalogChannelLeavesOrdinaryChannelUntouched(t *testing.T) {
	t.Parallel()

	channel := &Channel{
		Name:         "ordinary",
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"gpt": "upstream"}},
	}

	require.False(t, hydrateManagedCindyCatalogChannel(channel))
	require.Equal(t, map[string]map[string]string{PlatformOpenAI: {"gpt": "upstream"}}, channel.ModelMapping)
	require.Empty(t, channel.ModelPricing)
}
