package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestValidateCreateAccountProviderIdentityUsesResolvedFinalAxes(t *testing.T) {
	req := CreateAccountRequest{
		Platform:        service.PlatformCindy,
		WirePlatform:    service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type:            service.AccountTypeAPIKey,
		Credentials:     map[string]any{"api_key": "secret", "base_url": "https://api.laxarouter.ai"},
	}
	require.NoError(t, validateCreateAccountProviderIdentity(req))

	req.WirePlatform = service.PlatformGemini
	require.ErrorContains(t, validateCreateAccountProviderIdentity(req), "wire_platform mismatch")

	req.WirePlatform = service.WirePlatformOpenAI
	req.ProviderProfile = "cindy_future_v2"
	require.ErrorContains(t, validateCreateAccountProviderIdentity(req), "provider_profile mismatch")
}
