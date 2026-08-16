package service

import (
	"context"
	"errors"
	"log/slog"
)

func (s *adminServiceImpl) cindyBalanceRepo() (CindyBalanceAccountRepository, error) {
	repo, ok := s.accountRepo.(CindyBalanceAccountRepository)
	if !ok {
		return nil, errors.New("cindy balance account repository is not configured")
	}
	return repo, nil
}

func (s *adminServiceImpl) ClearCindyBalanceInsufficient(ctx context.Context, id int64) (*Account, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return nil, ErrCindyAccountRequired
	}
	repo, err := s.cindyBalanceRepo()
	if err != nil {
		return nil, err
	}
	if _, err := repo.ClearCindyBalanceInsufficient(ctx, id); err != nil {
		return nil, err
	}
	if pendingClearer, ok := s.runtimeBlocker.(interface {
		ClearCindyBalancePending(context.Context, int64) error
	}); ok {
		if err := pendingClearer.ClearCindyBalancePending(ctx, id); err != nil {
			// Redis is a legacy cleanup hint. Once the authoritative DB marker is
			// clear, cache cleanup failure must not keep or recreate the block.
			slog.Warn("cindy_balance_legacy_pending_clear_failed", "account_id", id, "error", err)
		}
	}
	if s.runtimeBlocker != nil {
		s.runtimeBlocker.ClearAccountSchedulingBlock(id)
	}
	return s.accountRepo.GetByID(ctx, id)
}

func (s *adminServiceImpl) PreviewCindyInsufficientDeletion(ctx context.Context) (*CindyInsufficientDeletePreview, error) {
	repo, err := s.cindyBalanceRepo()
	if err != nil {
		return nil, err
	}
	return repo.PreviewCindyInsufficientDeletion(ctx)
}

func (s *adminServiceImpl) DeleteCindyInsufficient(ctx context.Context, expectedCount int, fingerprint string) (*CindyInsufficientDeleteResult, error) {
	repo, err := s.cindyBalanceRepo()
	if err != nil {
		return nil, err
	}
	result, err := repo.DeleteCindyInsufficient(ctx, expectedCount, fingerprint)
	if err != nil {
		return nil, err
	}
	if s.runtimeBlocker != nil {
		for _, accountID := range result.DeletedAccountIDs {
			s.runtimeBlocker.ClearAccountSchedulingBlock(accountID)
		}
	}
	return result, nil
}
