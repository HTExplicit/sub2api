//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClassifyStrictCindyGroupUsesAllNonDeletedMembersAndCindyGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		groupPlatform string
		members       []service.Account
		wantStrict    bool
	}{
		{
			name:          "disabled ordinary member makes Cindy group mixed",
			groupPlatform: service.PlatformCindy,
			members: []service.Account{
				cindyIdentityIntegrationAccount("active-cindy", service.StatusActive),
				ordinaryIdentityIntegrationAccount("disabled-ordinary", service.StatusDisabled),
			},
		},
		{
			name:          "disabled Cindy member remains part of pure Cindy identity",
			groupPlatform: service.PlatformCindy,
			members: []service.Account{
				cindyIdentityIntegrationAccount("active-cindy", service.StatusActive),
				cindyIdentityIntegrationAccount("disabled-cindy", service.StatusDisabled),
			},
			wantStrict: true,
		},
		{
			name:          "non Cindy group fails closed",
			groupPlatform: service.PlatformGemini,
			members: []service.Account{
				cindyIdentityIntegrationAccount("cindy-shaped-member", service.StatusActive),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := testEntTx(t)
			client := tx.Client()
			repo := newAccountRepositoryWithSQL(client, tx, nil)
			groupInput := &service.Group{
				Name:     "identity-" + test.name,
				Platform: test.groupPlatform,
				Status:   service.StatusActive,
			}
			if test.groupPlatform == service.PlatformCindy {
				groupInput.WirePlatform = service.WirePlatformOpenAI
				groupInput.ProviderProfile = service.ProviderProfileCindyLaxaV1
			}
			group := mustCreateGroup(t, client, groupInput)
			for i := range test.members {
				account := mustCreateAccount(t, client, &test.members[i])
				if test.name == "disabled ordinary member makes Cindy group mixed" && i == 1 {
					_, err := client.ExecContext(context.Background(), "SET LOCAL session_replication_role = replica")
					require.NoError(t, err)
					mustBindAccountToGroup(t, client, account.ID, group.ID, i+1)
					_, err = client.ExecContext(context.Background(), "SET LOCAL session_replication_role = origin")
					require.NoError(t, err)
					continue
				}
				mustBindAccountToGroup(t, client, account.ID, group.ID, i+1)
			}

			strict, err := repo.ClassifyStrictCindyGroup(context.Background(), group.ID)
			require.NoError(t, err)
			require.Equal(t, test.wantStrict, strict)
		})
	}
}

func cindyIdentityIntegrationAccount(name, status string) service.Account {
	return service.Account{
		Name:            name,
		Platform:        service.PlatformCindy,
		WirePlatform:    service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type:            service.AccountTypeAPIKey,
		Status:          status,
		Credentials: map[string]any{
			"api_key":  "test-secret",
			"base_url": "https://api.laxarouter.ai",
		},
	}
}

func ordinaryIdentityIntegrationAccount(name, status string) service.Account {
	return service.Account{
		Name:     name,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   status,
		Credentials: map[string]any{
			"api_key":  "test-secret",
			"base_url": "https://api.openai.com",
		},
	}
}
