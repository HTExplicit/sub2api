package service

import (
	"sort"
	"strings"
)

// CindyDuplicateIdentityGroup is a redacted duplicate cluster. Identity is a
// SHA-256 digest; raw credentials are never returned.
type CindyDuplicateIdentityGroup struct {
	IdentityHash    string  `json:"identity_hash"`
	ProposedOwnerID int64   `json:"proposed_owner_id"`
	OtherAccountIDs []int64 `json:"other_account_ids"`
}

// BuildCindyDuplicateIdentityInventory groups strict Laxa accounts by their
// normalized credential identity. It is informational only: no account is
// merged, deleted, or mutated.
func BuildCindyDuplicateIdentityInventory(accounts []Account) []CindyDuplicateIdentityGroup {
	type candidate struct {
		account Account
		hash    string
	}
	groups := make(map[string][]candidate)
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
		groups[hash] = append(groups[hash], candidate{account: account, hash: hash})
	}

	result := make([]CindyDuplicateIdentityGroup, 0, len(groups))
	for hash, members := range groups {
		if len(members) < 2 {
			continue
		}
		sort.Slice(members, func(i, j int) bool {
			left, right := members[i].account, members[j].account
			leftTerminal, rightTerminal := cindyDuplicateTerminal(left), cindyDuplicateTerminal(right)
			if leftTerminal != rightTerminal {
				return !leftTerminal
			}
			if !leftTerminal && !rightTerminal && !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.Before(right.UpdatedAt)
			}
			return left.ID < right.ID
		})
		owner := members[0].account.ID
		others := make([]int64, 0, len(members)-1)
		for _, member := range members[1:] {
			others = append(others, member.account.ID)
		}
		sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
		result = append(result, CindyDuplicateIdentityGroup{IdentityHash: hash, ProposedOwnerID: owner, OtherAccountIDs: others})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].IdentityHash < result[j].IdentityHash })
	return result
}

func cindyDuplicateTerminal(account Account) bool {
	if account.CindyBannedAt != nil || account.CindyBalanceInsufficientAt != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(account.Status)) {
	case StatusDisabled, CindyHealthStatusQuarantined:
		return true
	default:
		return false
	}
}
