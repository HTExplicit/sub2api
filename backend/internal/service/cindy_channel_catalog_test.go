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
	require.Empty(t, channel.ModelPricing)

	for _, capability := range CindyCapabilities() {
		if capability.PublicModel {
			require.Equal(t, capability.LiveUpstreamID, channel.ModelMapping[PlatformCindy][capability.PublicID])
			require.Equal(t, capability.LiveUpstreamID, channel.ModelMapping[PlatformCindy][capability.LiveUpstreamID])
		} else {
			require.NotContains(t, channel.ModelMapping[PlatformCindy], capability.PublicID)
			require.NotContains(t, channel.ModelMapping[PlatformCindy], capability.LiveUpstreamID)
		}
	}
	require.NotContains(t, channel.ModelMapping[PlatformCindy], "gpt-5.4")
	require.Equal(t, "openai/gpt-5.6-luna", channel.ModelMapping[PlatformCindy]["gpt-5.4-mini"])
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
