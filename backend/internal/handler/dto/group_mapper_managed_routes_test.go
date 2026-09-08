package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupMapperManagedModelRoutesAreAdminOnly(t *testing.T) {
	group := &service.Group{
		ID: 23, Name: "managed-group", Platform: service.PlatformOpenAI, Status: service.StatusActive,
		ManagedModelRoutes: service.ManagedModelRoutesConfig{
			Version: 1, Enabled: true,
			Routes: []service.ManagedModelRoute{{
				PublicModel: "gpt-public", Aliases: []string{"gpt-public-alias"},
				Selector: "s2pub-g23-m0123456789abcdef", TargetPlatform: service.PlatformOpenAI,
				Endpoints: []string{"responses"},
				Accounts: []service.ManagedModelRouteAccount{{
					AccountID: 42, UpstreamModel: "namespace/upstream-vip",
					AccountFingerprint: "internal-account-fingerprint", Endpoints: []string{"responses"},
				}},
			}},
		},
	}

	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	var adminEnvelope struct {
		ManagedModelRoutes *service.ManagedModelRoutesConfig `json:"managed_model_routes"`
	}
	require.NoError(t, json.Unmarshal(adminJSON, &adminEnvelope))
	require.NotNil(t, adminEnvelope.ManagedModelRoutes)
	require.Equal(t, group.ManagedModelRoutes, *adminEnvelope.ManagedModelRoutes)

	for name, userGroup := range map[string]*Group{
		"full":    GroupFromService(group),
		"shallow": GroupFromServiceShallow(group),
	} {
		t.Run(name, func(t *testing.T) {
			userJSON, err := json.Marshal(userGroup)
			require.NoError(t, err)
			for _, private := range []string{
				"managed_model_routes", "selector", "account_fingerprint",
				"s2pub-g23-m0123456789abcdef", "namespace/upstream-vip", "internal-account-fingerprint",
			} {
				require.NotContains(t, string(userJSON), private)
			}
		})
	}
}
