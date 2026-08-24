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
	if terminalClearer, ok := s.runtimeBlocker.(CindyHealthTerminalPendingClearer); ok {
		if err := terminalClearer.ClearCindyHealthTerminalPending(ctx, id, CindyHealthStatusBalanceInsufficient); err != nil {
			slog.Warn("cindy_balance_terminal_pending_clear_failed", "account_id", id, "error", err)
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
	s.clearDeletedCindyRuntimeState(ctx, result.DeletedAccountIDs)
	return result, nil
}

func (s *adminServiceImpl) cindyBannedRepo() (CindyBannedAccountRepository, error) {
	repo, ok := s.accountRepo.(CindyBannedAccountRepository)
	if !ok {
		return nil, errors.New("cindy banned account repository is not configured")
	}
	return repo, nil
}

func (s *adminServiceImpl) PreviewCindyBannedDeletion(ctx context.Context) (*CindyInsufficientDeletePreview, error) {
	repo, err := s.cindyBannedRepo()
	if err != nil {
		return nil, err
	}
	return repo.PreviewCindyBannedDeletion(ctx)
}

func (s *adminServiceImpl) DeleteCindyBanned(ctx context.Context, expectedCount int, fingerprint string) (*CindyInsufficientDeleteResult, error) {
	repo, err := s.cindyBannedRepo()
	if err != nil {
		return nil, err
	}
	result, err := repo.DeleteCindyBanned(ctx, expectedCount, fingerprint)
	if err != nil {
		return nil, err
	}
	s.clearDeletedCindyRuntimeState(ctx, result.DeletedAccountIDs)
	return result, nil
}

func (s *adminServiceImpl) clearDeletedCindyRuntimeState(ctx context.Context, accountIDs []int64) {
	if s.runtimeBlocker == nil {
		return
	}
	for _, accountID := range accountIDs {
		if cleaner, ok := s.runtimeBlocker.(CindyHealthStateCleaner); ok {
			if clearErr := cleaner.ClearAllCindyHealthState(ctx, accountID); clearErr != nil {
				slog.Warn("cindy_cleanup_health_state_clear_failed", "account_id", accountID, "error", clearErr)
			}
		}
		s.runtimeBlocker.ClearAccountSchedulingBlock(accountID)
	}
}
