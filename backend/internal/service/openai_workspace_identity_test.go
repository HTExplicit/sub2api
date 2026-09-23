package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPreserveOpenAISelectedWorkspace(t *testing.T) {
	for _, plan := range []string{"team", "business", "self_serve_business_prolite", "pro"} {
		t.Run(plan, func(t *testing.T) {
			account := &Account{Credentials: map[string]any{"chatgpt_account_id": "selected", "organization_id": "selected-org", "plan_type": plan, "subscription_expires_at": "selected-expiry"}}
			info := &OpenAITokenInfo{ChatGPTAccountID: "other-default", OrganizationID: "default-org", PlanType: "free", SubscriptionExpiresAt: "other-expiry"}
			preserveOpenAISelectedWorkspace(info, account)
			require.Equal(t, "selected", info.ChatGPTAccountID)
			require.Equal(t, "selected-org", info.OrganizationID)
			require.Equal(t, plan, info.PlanType)
			require.Equal(t, "selected-expiry", info.SubscriptionExpiresAt)
			require.True(t, info.planTypeFromOtherWorkspace)
			require.Equal(t, plan, account.GetCredential("plan_type"), "parsing does not rewrite account data")
		})
	}
	info := &OpenAITokenInfo{ChatGPTAccountID: "personal", OrganizationID: "unrelated-default-org", PlanType: "pro"}
	preserveOpenAISelectedWorkspace(info, &Account{Credentials: map[string]any{"chatgpt_account_id": "personal", "plan_type": "plus"}})
	require.Equal(t, "pro", info.PlanType, "a fresh claim for the selected personal account remains authoritative")
	require.Empty(t, info.OrganizationID, "refresh must not invent an organization mapping")
	require.False(t, info.planTypeFromOtherWorkspace)
}

func TestEnrichTokenInfo_SelectedWorkspaceReplacesForeignPlanClaim(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	server := newChatGPTBackendTestServer(t, chatGPTBackendTestServerConfig{
		accountsCheck: map[string]any{"accounts": map[string]any{
			"default": map[string]any{"account": map[string]any{"account_id": "personal", "plan_type": "pro", "is_default": true}},
			"team":    map[string]any{"account": map[string]any{"account_id": "selected-team", "plan_type": "business"}, "entitlement": map[string]any{"expires_at": expiresAt}},
		}},
	})
	defer server.Close()
	info := &OpenAITokenInfo{AccessToken: "test-token", ChatGPTAccountID: "personal", PlanType: "pro"}
	account := &Account{Credentials: map[string]any{"chatgpt_account_id": "selected-team", "plan_type": "team"}}
	preserveOpenAISelectedWorkspace(info, account)
	svc := &OpenAIOAuthService{privacyClientFactory: newTestPrivacyClientFactory()}
	svc.enrichTokenInfo(context.Background(), info, "")
	require.Equal(t, "selected-team", info.ChatGPTAccountID)
	require.Equal(t, "business", info.PlanType)
	require.Equal(t, expiresAt, info.SubscriptionExpiresAt)
}

func TestEnrichTokenInfo_MissingSelectedWorkspaceDoesNotAdoptDefaultPlan(t *testing.T) {
	server := newChatGPTBackendTestServer(t, chatGPTBackendTestServerConfig{
		accountsCheck: map[string]any{"accounts": map[string]any{
			"default": map[string]any{"account": map[string]any{"account_id": "personal", "plan_type": "pro", "is_default": true}},
		}},
		onSubscription: func(accountID string) map[string]any {
			require.Equal(t, "selected-team", accountID)
			return map[string]any{}
		},
	})
	defer server.Close()
	info := &OpenAITokenInfo{AccessToken: "test-token", ChatGPTAccountID: "personal", PlanType: "pro"}
	preserveOpenAISelectedWorkspace(info, &Account{Credentials: map[string]any{"chatgpt_account_id": "selected-team"}})
	svc := &OpenAIOAuthService{privacyClientFactory: newTestPrivacyClientFactory()}
	svc.enrichTokenInfo(context.Background(), info, "")
	require.Equal(t, "selected-team", info.ChatGPTAccountID)
	require.Empty(t, info.PlanType)
	require.Empty(t, info.SubscriptionExpiresAt)
}

func TestChatGPTAccountInfoForSelectedWorkspace(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	accounts := map[string]any{
		"default":    map[string]any{"account": map[string]any{"account_id": "personal", "plan_type": "pro", "is_default": true}},
		"team-alias": map[string]any{"account": map[string]any{"account_id": "selected-team", "plan_type": "business"}},
		"expired":    map[string]any{"account": map[string]any{"plan_type": "team"}, "entitlement": map[string]any{"expires_at": now.Add(-time.Hour).Format(time.RFC3339)}},
		"disabled":   map[string]any{"account": map[string]any{"plan_type": "team", "is_deactivated": true}},
		"wrong-key":  map[string]any{"account": map[string]any{"account_id": "different-owner", "plan_type": "team"}},
	}
	team := chatGPTAccountInfoForSelectedWorkspace(accounts, "selected-team", now)
	require.NotNil(t, team)
	require.Equal(t, "selected-team", team.AccountID)
	require.Equal(t, "business", team.PlanType)
	personal := chatGPTAccountInfoForSelectedWorkspace(accounts, "personal", now)
	require.NotNil(t, personal)
	require.Equal(t, "pro", personal.PlanType)
	for _, selected := range []string{"missing-team", "expired", "disabled", "wrong-key"} {
		require.Nil(t, chatGPTAccountInfoForSelectedWorkspace(accounts, selected, now), "must not fall back to a different workspace: %s", selected)
	}
}
