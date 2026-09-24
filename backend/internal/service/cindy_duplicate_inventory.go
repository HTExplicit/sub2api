package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type CindyDuplicateIdentityGroup = extensionv1.CindyDuplicateIdentityGroup

// The compatibility helper delegates to the same policy as the management API.
func BuildCindyDuplicateIdentityInventory(accounts []Account) []CindyDuplicateIdentityGroup {
	groups, _ := buildCindyDuplicateIdentityInventory(context.Background(), accounts)
	return groups
}

func buildCindyDuplicateIdentityInventory(ctx context.Context, accounts []Account) ([]CindyDuplicateIdentityGroup, error) {
	facts := make([]extensionv1.CindyDuplicateCandidate, 0, len(accounts))
	identities := map[string]map[int64]bool{}
	for _, account := range accounts {
		if !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			continue
		}
		baseURL, err := NormalizeCredentialIdentityBaseURL(ProviderProfileCindyLaxaV1, account.GetCredential("base_url"))
		if err != nil {
			continue
		}
		hash, err := AccountCredentialFingerprint(ProviderProfileCindyLaxaV1, AccountTypeAPIKey, baseURL, account.GetCredential("api_key"))
		if err != nil {
			continue
		}
		if identities[hash] == nil {
			identities[hash] = map[int64]bool{}
		}
		if identities[hash][account.ID] {
			continue
		}
		identities[hash][account.ID] = true
		facts = append(facts, extensionv1.CindyDuplicateCandidate{ID: account.ID, IdentityHash: hash, Status: account.Status, Banned: account.CindyBannedAt != nil, Exhausted: account.CindyBalanceInsufficientAt != nil, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt})
	}
	var groups []CindyDuplicateIdentityGroup
	if err := invokeCindyManagement(ctx, "cindy.duplicates.plan", facts, &groups); err != nil {
		return nil, err
	}
	for _, group := range groups {
		available, known := identities[group.IdentityHash]
		ids := append([]int64{}, group.OtherAccountIDs...)
		if group.ProposedOwnerID > 0 {
			ids = append(ids, group.ProposedOwnerID)
		}
		if !known || len(ids) < 2 || len(ids) != len(available) {
			return nil, ErrCindyGroupAdminUnavailable
		}
		seen := map[int64]bool{}
		for _, id := range ids {
			if !available[id] || seen[id] {
				return nil, ErrCindyGroupAdminUnavailable
			}
			seen[id] = true
		}
		delete(identities, group.IdentityHash)
	}
	for _, members := range identities {
		if len(members) > 1 {
			return nil, ErrCindyGroupAdminUnavailable
		}
	}
	if groups == nil {
		groups = []CindyDuplicateIdentityGroup{}
	}
	return groups, nil
}
