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
	return BuildCindyDuplicateIdentityInventory(accounts), nil
}
