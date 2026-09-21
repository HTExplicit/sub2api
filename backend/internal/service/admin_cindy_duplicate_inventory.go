package service

import "context"

// BuildCindyDuplicateIdentityInventory loads the current strict Cindy API-key
// accounts and returns a read-only, redacted duplicate inventory.
func (s *adminServiceImpl) BuildCindyDuplicateIdentityInventory(ctx context.Context) ([]CindyDuplicateIdentityGroup, error) {
	if s == nil || s.accountRepo == nil {
		return []CindyDuplicateIdentityGroup{}, nil
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformCindy)
	if err != nil {
		return nil, err
	}
	if _, bound := AccountViewFromContext(ctx); bound {
		candidates := make([]*Account, 0, len(accounts))
		for index := range accounts {
			candidates = append(candidates, &accounts[index])
		}
		candidates, err = filterAccountViewAccounts(ctx, candidates)
		if err != nil {
			return nil, err
		}
		// Aggregate only admitted facts, including the active preset and bucket.
		// The native no-view inventory retains its existing candidate semantics.
		accounts = make([]Account, 0, len(candidates))
		for _, account := range candidates {
			accounts = append(accounts, *account)
		}
	}
	return buildCindyDuplicateIdentityInventory(ctx, accounts)
}
