package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminCreateAccountModelContextOverridesAreTypedAndAtomic(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{}
	svc := &adminServiceImpl{accountRepo: repo}
	value := int64(1050000)
	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "capacity", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test"}, SkipDefaultGroupBind: true,
		ModelContextOverrides: map[string]*int64{"real-model": &value},
		Extra: map[string]any{
			UpstreamModelContextCapacitiesExtraKey: map[string]any{"injected": true},
			UpstreamModelMetadataExtraKey:          map[string]any{"injected": true},
			ModelContextOverridesExtraKey:          map[string]int64{"injected-model": 9000000},
			"ordinary_setting":                     true,
		},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"real-model": value}, created.Extra[ModelContextOverridesExtraKey])
	require.NotContains(t, created.Extra, UpstreamModelContextCapacitiesExtraKey)
	require.NotContains(t, created.Extra, UpstreamModelMetadataExtraKey)
	require.Equal(t, true, created.Extra["ordinary_setting"])
	require.Equal(t, created.Extra, repo.accounts[created.ID].Extra)
	require.Nil(t, repo.updates, "creation must not split capacity overrides into a second save")
}

func TestAdminUpdateAccountModelContextPatchPreservesManagedState(t *testing.T) {
	oldRaw := UpstreamModelContextCapacitySnapshot{ObservedAt: "2026-09-01T00:00:00Z", Models: map[string]ModelContextCapacity{"real-model": {ContextWindow: 600000}}}
	oldMetadata := UpstreamModelMetadataSnapshot{Source: "models.dev", Models: map[string]UpstreamModelMetadata{"real-model": {ContextWindow: 700000}}}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {
		ID: 1, Name: "before", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"api_key": "test"},
		Extra: map[string]any{UpstreamModelContextCapacitiesExtraKey: oldRaw, UpstreamModelMetadataExtraKey: oldMetadata,
			ModelContextOverridesExtraKey: map[string]int64{"real-model": 800000, "other-model": 900000}},
	}}}
	svc := &adminServiceImpl{accountRepo: repo}
	value := int64(1050000)
	updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{
		Name: "after", ModelContextOverrides: map[string]*int64{"real-model": &value},
		Extra: map[string]any{UpstreamModelContextCapacitiesExtraKey: "stale", UpstreamModelMetadataExtraKey: "stale",
			ModelContextOverridesExtraKey: map[string]int64{"injected-model": 1}, "ordinary_setting": "new"},
	})
	require.NoError(t, err)
	require.Equal(t, oldRaw, updated.Extra[UpstreamModelContextCapacitiesExtraKey])
	require.Equal(t, oldMetadata, updated.Extra[UpstreamModelMetadataExtraKey])
	require.Equal(t, map[string]int64{"real-model": 1050000, "other-model": 900000}, updated.Extra[ModelContextOverridesExtraKey])
	require.Equal(t, "new", updated.Extra["ordinary_setting"])
	require.Equal(t, map[string]*int64{"real-model": &value}, repo.accounts[1].ModelContextOverridesPatch)

	updated, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{ModelContextOverrides: map[string]*int64{"real-model": nil}})
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"other-model": 900000}, updated.Extra[ModelContextOverridesExtraKey])
	updated, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Name: "unrelated"})
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"other-model": 900000}, updated.Extra[ModelContextOverridesExtraKey])
	require.Nil(t, repo.accounts[1].ModelContextOverridesPatch)
}

func TestAdminAccountModelContextValidationRejectsBeforePersistence(t *testing.T) {
	for _, tt := range []struct {
		name        string
		accountType string
		value       int64
	}{
		{"zero", AccountTypeAPIKey, 0},
		{"negative", AccountTypeAPIKey, -1},
		{"unsafe integer", AccountTypeAPIKey, 9007199254740992},
		{"protected OAuth", AccountTypeOAuth, 500000},
		{"protected setup token", AccountTypeSetupToken, 500000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &upstreamBillingProbeAccountRepo{}
			svc := &adminServiceImpl{accountRepo: repo}
			_, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
				Name: "invalid", Platform: PlatformOpenAI, Type: tt.accountType,
				Credentials: map[string]any{"api_key": "test"}, SkipDefaultGroupBind: true,
				ModelContextOverrides: map[string]*int64{"real-model": &tt.value},
			})
			require.Error(t, err)
			require.Empty(t, repo.accounts)
		})
	}
}

func TestAdminAccountExtraAndBulkCannotInjectModelContextRuntime(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}}
	svc := &adminServiceImpl{accountRepo: repo}
	malicious := func() map[string]any {
		return map[string]any{UpstreamModelContextCapacitiesExtraKey: "fake", UpstreamModelMetadataExtraKey: "fake", ModelContextOverridesExtraKey: "fake"}
	}
	require.NoError(t, svc.UpdateAccountExtra(context.Background(), 1, malicious()))
	require.Empty(t, repo.updates)
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: malicious()})
	require.NoError(t, err)
	for _, update := range repo.bulkUpdates {
		require.NotContains(t, update.Extra, UpstreamModelContextCapacitiesExtraKey)
		require.NotContains(t, update.Extra, UpstreamModelMetadataExtraKey)
		require.NotContains(t, update.Extra, ModelContextOverridesExtraKey)
	}
}

func TestAccountModelContextPatchIsNeverSerialized(t *testing.T) {
	value := int64(512000)
	body, err := json.Marshal(&Account{ModelContextOverridesPatch: map[string]*int64{"secret-draft-model": &value}})
	require.NoError(t, err)
	require.NotContains(t, string(body), "secret-draft-model")
	require.NotContains(t, string(body), "ModelContextOverridesPatch")
}
