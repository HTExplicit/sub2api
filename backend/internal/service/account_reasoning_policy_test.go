//go:build unit

package service

import (
	"context"
	"maps"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestOpenAIReasoningPolicyAccountDefaultsAndIsolation(t *testing.T) {
	policies := []struct {
		key string
		get func(*Account) bool
	}{
		{OpenAIChatReasoningReplayEnabledExtraKey, (*Account).IsOpenAIChatReasoningReplayEnabled},
		{OpenAIReasoningSignatureRecoveryEnabledExtraKey, (*Account).IsOpenAIReasoningSignatureRecoveryEnabled},
	}
	for _, policy := range policies {
		t.Run(policy.key, func(t *testing.T) {
			require.False(t, policy.get(nil))
			for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
				t.Run(accountType, func(t *testing.T) {
					account := &Account{Platform: PlatformOpenAI, Type: accountType}
					require.True(t, policy.get(account), "missing extra uses the enabled policy default")
					account.Extra = map[string]any{}
					require.True(t, policy.get(account))
					for _, value := range []bool{true, false} {
						account.Extra[policy.key] = value
						require.Equal(t, value, policy.get(account))
					}
					for _, malformed := range []any{nil, "true", "false", 1, []bool{true}, map[string]any{}} {
						account.Extra[policy.key] = malformed
						require.False(t, policy.get(account), "stored malformed policy must fail closed")
					}
				})
			}
			for _, platform := range []string{PlatformAnthropic, PlatformGrok, PlatformGemini, PlatformAntigravity} {
				require.False(t, policy.get(&Account{Platform: platform, Type: AccountTypeAPIKey, Extra: map[string]any{policy.key: true}}))
			}
			for _, accountType := range []string{"", AccountTypeUpstream, AccountTypeBedrock} {
				require.False(t, policy.get(&Account{Platform: PlatformOpenAI, Type: accountType, Extra: map[string]any{policy.key: true}}))
			}
			require.True(t, policy.get(&Account{Platform: PlatformCindy, Type: AccountTypeAPIKey}))
			require.False(t, policy.get(&Account{Platform: PlatformOpenAI, WirePlatform: PlatformGrok, Type: AccountTypeAPIKey}))
			require.True(t, policy.get(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_passthrough": true}}))
		})
	}
}

func TestOpenAIReasoningPolicyCreateDefaultsAndExplicitValues(t *testing.T) {
	for _, extra := range []map[string]any{
		nil,
		{},
		{OpenAIChatReasoningReplayEnabledExtraKey: false, OpenAIReasoningSignatureRecoveryEnabledExtraKey: true},
		{OpenAIChatReasoningReplayEnabledExtraKey: true, OpenAIReasoningSignatureRecoveryEnabledExtraKey: false},
	} {
		repo := &longContextBillingRepoStub{}
		svc := &adminServiceImpl{accountRepo: repo}
		original := maps.Clone(extra)
		account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
			Name: "reasoning-policy", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{}, Extra: extra, SkipDefaultGroupBind: true,
		})
		require.NoError(t, err)
		require.Equal(t, original, extra, "creation must not populate defaults into the caller's map")
		for _, key := range []string{OpenAIChatReasoningReplayEnabledExtraKey, OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
			value, provided := original[key]
			if provided {
				require.Equal(t, value, account.Extra[key])
			} else {
				require.NotContains(t, account.Extra, key, "read-time defaults need no persistence migration")
			}
		}
	}
}

func TestOpenAIReasoningPolicyUpdatePreservesOmittedPolicies(t *testing.T) {
	for _, stored := range []any{true, false, "legacy-invalid", nil} {
		for _, incoming := range []map[string]any{nil, {}, {"base_rpm": 20}} {
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{OpenAIChatReasoningReplayEnabledExtraKey: stored, OpenAIReasoningSignatureRecoveryEnabledExtraKey: false},
			}
			repo := &longContextBillingRepoStub{account: account}
			svc := &adminServiceImpl{accountRepo: repo}
			original := maps.Clone(incoming)
			updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Name: "renamed", Extra: incoming})
			require.NoError(t, err)
			require.Contains(t, updated.Extra, OpenAIChatReasoningReplayEnabledExtraKey)
			require.Equal(t, stored, updated.Extra[OpenAIChatReasoningReplayEnabledExtraKey])
			require.Equal(t, false, updated.Extra[OpenAIReasoningSignatureRecoveryEnabledExtraKey])
			require.Equal(t, original, incoming)
		}
	}
}

func TestOpenAIReasoningPolicyUpdateCanOverrideLegacyMalformedValuesIndependently(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		repo := &longContextBillingRepoStub{account: &Account{
			ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{OpenAIChatReasoningReplayEnabledExtraKey: "legacy-invalid", OpenAIReasoningSignatureRecoveryEnabledExtraKey: false},
		}}
		svc := &adminServiceImpl{accountRepo: repo}
		updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{OpenAIChatReasoningReplayEnabledExtraKey: enabled}})
		require.NoError(t, err)
		require.Equal(t, enabled, updated.IsOpenAIChatReasoningReplayEnabled())
		require.False(t, updated.IsOpenAIReasoningSignatureRecoveryEnabled())
	}
}

func TestOpenAIReasoningPolicyRejectsMalformedNewAdminInputBeforeWrite(t *testing.T) {
	for _, key := range []string{OpenAIChatReasoningReplayEnabledExtraKey, OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
		for _, invalid := range []any{nil, "rejected-sensitive-marker", 1, []bool{false}, map[string]any{}} {
			for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
				for _, operation := range []string{"create", "update", "merge", "bulk"} {
					t.Run(key+"/"+platform+"/"+operation, func(t *testing.T) {
						repo := &longContextBillingRepoStub{account: &Account{ID: 1, Name: "unchanged", Platform: platform, Type: AccountTypeAPIKey}}
						svc := &adminServiceImpl{accountRepo: repo}
						extra := map[string]any{key: invalid}
						var err error
						switch operation {
						case "create":
							_, err = svc.CreateAccount(context.Background(), &CreateAccountInput{Platform: platform, Type: AccountTypeAPIKey, Extra: extra, SkipDefaultGroupBind: true})
						case "update":
							_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Name: "changed", Extra: extra})
						case "merge":
							err = svc.UpdateAccountExtra(context.Background(), 1, extra)
						case "bulk":
							_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
						}
						require.Error(t, err)
						require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
						requireApplicationErrorReason(t, err, "OPENAI_REASONING_POLICY_INVALID")
						require.NotContains(t, err.Error(), "rejected-sensitive-marker")
						require.Nil(t, repo.createdAccount)
						require.Zero(t, repo.updateExtraCalls)
						require.Zero(t, repo.bulkUpdateCalls)
						require.Equal(t, "unchanged", repo.account.Name)
					})
				}
			}
		}
	}
}

func TestOpenAIReasoningPolicyBulkOnlySubmitsExplicitKeys(t *testing.T) {
	for _, extra := range []map[string]any{
		{"base_rpm": 20},
		{OpenAIChatReasoningReplayEnabledExtraKey: false},
		{OpenAIReasoningSignatureRecoveryEnabledExtraKey: true},
	} {
		repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{
			{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		}}
		svc := &adminServiceImpl{accountRepo: repo}
		original := maps.Clone(extra)
		result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1, 2}, Extra: extra})
		require.NoError(t, err)
		require.Equal(t, 2, result.Success)
		require.Equal(t, original, repo.lastBulkUpdate.Extra)
		// The repository applies this exact patch with JSONB key merge. No default
		// for an omitted policy may enter the patch and override per-row choices.
		for _, key := range []string{OpenAIChatReasoningReplayEnabledExtraKey, OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
			if _, supplied := original[key]; !supplied {
				require.NotContains(t, repo.lastBulkUpdate.Extra, key)
			}
		}
	}
}

func TestOpenAIReasoningPolicyBulkRejectsInvalidCompleteTargetSet(t *testing.T) {
	for _, invalid := range []*Account{
		nil,
		{ID: 101, Platform: PlatformGrok, Type: AccountTypeOAuth},
		{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeUpstream},
		{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeBedrock},
		{ID: 101, Platform: PlatformOpenAI},
		{ID: 101, Platform: PlatformOpenAI, WirePlatform: PlatformGrok, Type: AccountTypeAPIKey},
	} {
		ids := make([]int64, 101)
		accounts := make([]*Account, 101)
		for index := range accounts {
			ids[index] = int64(index + 1)
			accounts[index] = &Account{ID: ids[index], Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		}
		accounts[100] = invalid
		for _, key := range []string{OpenAIChatReasoningReplayEnabledExtraKey, OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
			repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: accounts}
			svc := &adminServiceImpl{accountRepo: repo}
			result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: ids, Extra: map[string]any{key: false}})
			require.Nil(t, result)
			requireApplicationErrorReason(t, err, "OPENAI_BULK_TARGET_INVALID")
			require.True(t, repo.getByIDsCalled)
			require.Equal(t, ids, repo.getByIDsIDs, "the full set must be loaded, not the first UI page")
			require.Zero(t, repo.bulkUpdateCalls, "no target may be written when any target is invalid")
		}
	}
}

func TestOpenAIReasoningPolicyBulkAcceptsEligibleTargetsIndependentlyOfCurrentPolicy(t *testing.T) {
	parentID := int64(1)
	for _, account := range []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeSetupToken},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark},
		{ID: 1, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, Type: AccountTypeAPIKey},
	} {
		account.Extra = map[string]any{OpenAIChatReasoningReplayEnabledExtraKey: false, OpenAIReasoningSignatureRecoveryEnabledExtraKey: "legacy-invalid"}
		repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{account}}
		svc := &adminServiceImpl{accountRepo: repo}
		result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs: []int64{account.ID},
			Extra:      map[string]any{OpenAIChatReasoningReplayEnabledExtraKey: true, OpenAIReasoningSignatureRecoveryEnabledExtraKey: true},
		})
		require.NoError(t, err)
		require.Equal(t, 1, result.Success)
		require.Equal(t, 1, repo.bulkUpdateCalls)
	}
}
