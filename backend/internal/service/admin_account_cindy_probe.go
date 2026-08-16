package service

import (
	"context"
	"fmt"
)

func (s *adminServiceImpl) hydrateCindyBalanceProbeLatestValues(ctx context.Context, accounts []Account) error {
	accountPointers := make([]*Account, 0, len(accounts))
	for index := range accounts {
		accountPointers = append(accountPointers, &accounts[index])
	}
	return s.hydrateCindyBalanceProbeLatest(ctx, accountPointers)
}

func (s *adminServiceImpl) hydrateCindyBalanceProbeLatest(ctx context.Context, accounts []*Account) error {
	if len(accounts) == 0 {
		return nil
	}

	byID := make(map[int64][]*Account, len(accounts))
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		account.CindyBalanceProbeJobID = nil
		account.CindyBalanceProbeOutcome = nil
		account.CindyBalanceProbeCheckedAt = nil
		if s == nil || s.cindyBalanceProbeRepo == nil || account.ID <= 0 ||
			!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			continue
		}
		if _, exists := byID[account.ID]; !exists {
			accountIDs = append(accountIDs, account.ID)
		}
		byID[account.ID] = append(byID[account.ID], account)
	}
	if len(accountIDs) == 0 {
		return nil
	}

	latestByAccountID, err := s.cindyBalanceProbeRepo.LatestByAccountIDs(ctx, accountIDs)
	if err != nil {
		return fmt.Errorf("load latest Cindy balance probes: %w", err)
	}
	for accountID, accountsForID := range byID {
		latest, exists := latestByAccountID[accountID]
		if !exists {
			continue
		}
		for _, account := range accountsForID {
			jobID := latest.JobID
			outcome := latest.Outcome
			checkedAt := latest.CheckedAt
			account.CindyBalanceProbeJobID = &jobID
			account.CindyBalanceProbeOutcome = &outcome
			account.CindyBalanceProbeCheckedAt = &checkedAt
		}
	}
	return nil
}
