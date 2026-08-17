//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClassifyStrictCindyGroupUsesAllNonDeletedMembersAndOpenAIGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		groupPlatform string
		members       []service.Account
		wantStrict    bool
	}{
		{
			name:          "disabled ordinary member makes OpenAI group mixed",
			groupPlatform: service.PlatformOpenAI,
			members: []service.Account{
				cindyIdentityIntegrationAccount("active-cindy", service.StatusActive),
				ordinaryIdentityIntegrationAccount("disabled-ordinary", service.StatusDisabled),
			},
		},
		{
			name:          "disabled Cindy member remains part of pure OpenAI identity",
			groupPlatform: service.PlatformOpenAI,
			members: []service.Account{
				cindyIdentityIntegrationAccount("active-cindy", service.StatusActive),
				cindyIdentityIntegrationAccount("disabled-cindy", service.StatusDisabled),
			},
			wantStrict: true,
		},
		{
			name:          "non OpenAI group fails closed",
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
			group := mustCreateGroup(t, client, &service.Group{
				Name:     "identity-" + test.name,
				Platform: test.groupPlatform,
				Status:   service.StatusActive,
			})
			for i := range test.members {
				account := mustCreateAccount(t, client, &test.members[i])
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
		Name:     name,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   status,
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
