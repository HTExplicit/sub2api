package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProviderIdentityMappersExposeCanonicalAxes(t *testing.T) {
	account := AccountFromServiceShallow(&service.Account{
		Platform:        service.PlatformCindy,
		WirePlatform:    service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1,
	})
	require.Equal(t, service.PlatformCindy, account.Platform)
	require.Equal(t, service.WirePlatformOpenAI, account.WirePlatform)
	require.Equal(t, service.ProviderProfileCindyLaxaV1, account.ProviderProfile)

	group := GroupFromService(&service.Group{
		Platform:        service.PlatformCindy,
		WirePlatform:    service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1,
	})
	require.Equal(t, service.PlatformCindy, group.Platform)
	require.Equal(t, service.WirePlatformOpenAI, group.WirePlatform)
	require.Equal(t, service.ProviderProfileCindyLaxaV1, group.ProviderProfile)
}
