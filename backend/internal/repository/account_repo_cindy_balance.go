package repository

import (
	"context"
	"fmt"
	"sort"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbaccountgroup "github.com/Wei-Shaw/sub2api/ent/accountgroup"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) lockCindyAccount(ctx context.Context, accountID int64) (*dbAccountCandidate, error) {
	account, err := r.client.Account.Query().
		Where(dbaccount.IDEQ(accountID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAccountNotFound
		}
		return nil, err
	}
	return &dbAccountCandidate{
		ID:              account.ID,
		Platform:        account.Platform,
		WirePlatform:    account.WirePlatform,
		ProviderProfile: account.ProviderProfile,
		Type:            account.Type,
		Status:          account.Status,
		Schedulable:     account.Schedulable,
		Credentials:     account.Credentials,
		MarkedAt:        account.CindyBalanceInsufficientAt,
	}, nil
}

type dbAccountCandidate struct {
	ID              int64
	Platform        string
	WirePlatform    string
	ProviderProfile string
	Type            string
	Status          string
	Schedulable     bool
	Credentials     map[string]any
	MarkedAt        *time.Time
	BannedAt        *time.Time
}

func isCindyCandidate(account *dbAccountCandidate) bool {
	return account != nil && account.Platform == service.PlatformCindy &&
		account.WirePlatform == service.WirePlatformOpenAI &&
		account.ProviderProfile == service.ProviderProfileCindyLaxaV1 &&
		service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
}

func (r *accountRepository) MarkCindyBalanceInsufficient(ctx context.Context, accountID int64, observedAt time.Time) (bool, error) {
	changed, _, err := r.markCindyBalanceInsufficient(ctx, accountID, observedAt, "", false)
	return changed, err
}

func (r *accountRepository) MarkCindyBalanceInsufficientIfCredentialsMatch(
	ctx context.Context,
	accountID int64,
	observedAt time.Time,
	credentialsFingerprint string,
) (bool, bool, error) {
	return r.markCindyBalanceInsufficient(ctx, accountID, observedAt, credentialsFingerprint, true)
}

func (r *accountRepository) markCindyBalanceInsufficient(
	ctx context.Context,
	accountID int64,
	observedAt time.Time,
	credentialsFingerprint string,
	requireCredentialsMatch bool,
) (bool, bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	txRepo := &accountRepository{client: tx.Client(), sql: tx.Client(), schedulerCache: r.schedulerCache}
	account, err := txRepo.lockCindyAccount(ctx, accountID)
	if err != nil {
		return false, false, err
	}
	if !isCindyCandidate(account) {
		if requireCredentialsMatch {
			return false, false, nil
		}
		return false, false, service.ErrCindyAccountRequired
	}
	if requireCredentialsMatch {
		expected := service.NormalizeCindyCredentialsFingerprint(credentialsFingerprint)
		actual, fingerprintErr := service.CindyAccountIdentityFingerprint(
			account.Platform,
			account.Type,
			account.Credentials,
		)
		if fingerprintErr != nil {
			return false, false, fingerprintErr
		}
		if expected == "" || actual != expected {
			return false, false, nil
		}
	}
	if account.MarkedAt != nil {
		return false, true, nil
	}
	if _, err := tx.Client().Account.UpdateOneID(accountID).
		SetCindyBalanceInsufficientAt(observedAt.UTC()).
		Save(ctx); err != nil {
		return false, true, err
	}
	if err := enqueueSchedulerOutbox(ctx, tx.Client(), service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return false, true, err
	}
	if err := tx.Commit(); err != nil {
		return false, true, err
	}
	r.syncSchedulerAccountSnapshot(ctx, accountID)
	return true, true, nil
}

func (r *accountRepository) ClearCindyBalanceInsufficient(ctx context.Context, accountID int64) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	txRepo := &accountRepository{client: tx.Client(), sql: tx.Client(), schedulerCache: r.schedulerCache}
	account, err := txRepo.lockCindyAccount(ctx, accountID)
	if err != nil {
		return false, err
	}
	if !isCindyCandidate(account) {
		return false, service.ErrCindyAccountRequired
	}
	if account.MarkedAt == nil {
		return false, nil
	}
	if _, err := tx.Client().Account.UpdateOneID(accountID).
		ClearCindyBalanceInsufficientAt().
		Save(ctx); err != nil {
		return false, err
	}
	if err := enqueueSchedulerOutbox(ctx, tx.Client(), service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	r.syncSchedulerAccountSnapshot(ctx, accountID)
	return true, nil
}

func cindyInsufficientCandidateIDs(accounts []*dbAccountCandidate) []int64 {
	return cindyTerminalCandidateIDs(accounts, false)
}

func cindyTerminalCandidateIDs(accounts []*dbAccountCandidate, banned bool) []int64 {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		terminal := account.MarkedAt != nil
		if banned {
			terminal = account.BannedAt != nil
		}
		if terminal && isCindyCandidate(account) && account.Status == service.StatusActive && account.Schedulable {
			ids = append(ids, account.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func loadCindyInsufficientCandidates(ctx context.Context, repo *accountRepository, lock bool) ([]*dbAccountCandidate, error) {
	return loadCindyTerminalCandidates(ctx, repo, lock, false)
}

func loadCindyTerminalCandidates(ctx context.Context, repo *accountRepository, lock, banned bool) ([]*dbAccountCandidate, error) {
	query := repo.client.Account.Query()
	if banned {
		query = query.Where(dbaccount.CindyBannedAtNotNil())
	} else {
		query = query.Where(dbaccount.CindyBalanceInsufficientAtNotNil())
	}
	query = query.
		Order(dbent.Asc(dbaccount.FieldID))
	if lock {
		query = query.ForUpdate()
	}
	accounts, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*dbAccountCandidate, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, &dbAccountCandidate{
			ID: account.ID, Platform: account.Platform, WirePlatform: account.WirePlatform,
			ProviderProfile: account.ProviderProfile, Type: account.Type,
			Status: account.Status, Schedulable: account.Schedulable,
			Credentials: account.Credentials, MarkedAt: account.CindyBalanceInsufficientAt,
			BannedAt: account.CindyBannedAt,
		})
	}
	return out, nil
}

func (r *accountRepository) PreviewCindyInsufficientDeletion(ctx context.Context) (*service.CindyInsufficientDeletePreview, error) {
	accounts, err := loadCindyInsufficientCandidates(ctx, r, false)
	if err != nil {
		return nil, err
	}
	ids := cindyInsufficientCandidateIDs(accounts)
	return &service.CindyInsufficientDeletePreview{
		Count: len(ids), Fingerprint: service.CindyInsufficientAccountFingerprint(ids),
	}, nil
}

func (r *accountRepository) PreviewCindyBannedDeletion(ctx context.Context) (*service.CindyInsufficientDeletePreview, error) {
	accounts, err := loadCindyTerminalCandidates(ctx, r, false, true)
	if err != nil {
		return nil, err
	}
	ids := cindyTerminalCandidateIDs(accounts, true)
	return &service.CindyInsufficientDeletePreview{
		Count: len(ids), Fingerprint: service.CindyInsufficientAccountFingerprint(ids),
	}, nil
}

func (r *accountRepository) DeleteCindyInsufficient(ctx context.Context, expectedCount int, fingerprint string) (*service.CindyInsufficientDeleteResult, error) {
	return r.deleteCindyTerminalAccounts(ctx, expectedCount, fingerprint, false)
}

func (r *accountRepository) DeleteCindyBanned(ctx context.Context, expectedCount int, fingerprint string) (*service.CindyInsufficientDeleteResult, error) {
	return r.deleteCindyTerminalAccounts(ctx, expectedCount, fingerprint, true)
}

func (r *accountRepository) deleteCindyTerminalAccounts(ctx context.Context, expectedCount int, fingerprint string, banned bool) (*service.CindyInsufficientDeleteResult, error) {
	if expectedCount < 0 || service.NormalizeCindyInsufficientFingerprint(fingerprint) == "" {
		return nil, service.ErrCindyInsufficientDeleteChanged
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txRepo := &accountRepository{client: tx.Client(), sql: tx.Client(), schedulerCache: r.schedulerCache}
	accounts, err := loadCindyTerminalCandidates(ctx, txRepo, true, banned)
	if err != nil {
		return nil, err
	}
	ids := cindyTerminalCandidateIDs(accounts, banned)
	actualFingerprint := service.CindyInsufficientAccountFingerprint(ids)
	if len(ids) != expectedCount || actualFingerprint != service.NormalizeCindyInsufficientFingerprint(fingerprint) {
		return nil, service.ErrCindyInsufficientDeleteChanged
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.CindyInsufficientDeleteResult{}, nil
	}

	children, err := tx.Client().Account.Query().
		Where(dbaccount.ParentAccountIDIn(ids...)).
		All(mixins.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}
	childIDs := make([]int64, 0, len(children))
	for _, child := range children {
		childIDs = append(childIDs, child.ID)
	}
	deleteIDs := append(append([]int64(nil), childIDs...), ids...)

	bindings, err := tx.Client().AccountGroup.Query().
		Where(dbaccountgroup.AccountIDIn(deleteIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	groupSet := make(map[int64]struct{})
	for _, binding := range bindings {
		groupSet[binding.GroupID] = struct{}{}
	}
	groupIDs := make([]int64, 0, len(groupSet))
	for groupID := range groupSet {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })

	if _, err := tx.Client().AccountGroup.Delete().Where(dbaccountgroup.AccountIDIn(deleteIDs...)).Exec(ctx); err != nil {
		return nil, err
	}
	if _, err := tx.Client().ExecContext(ctx, "DELETE FROM scheduled_test_plans WHERE account_id = ANY($1)", deleteIDs); err != nil {
		return nil, err
	}
	if _, err := tx.Client().ExecContext(ctx, "DELETE FROM usage_logs WHERE account_id = ANY($1)", deleteIDs); err != nil {
		return nil, err
	}
	if len(childIDs) > 0 {
		deletedChildren, childErr := tx.Client().Account.Delete().Where(dbaccount.IDIn(childIDs...)).Exec(mixins.SkipSoftDelete(ctx))
		if childErr != nil {
			return nil, childErr
		}
		if deletedChildren != len(childIDs) {
			return nil, fmt.Errorf("cindy dependent account delete count mismatch: expected %d, deleted %d", len(childIDs), deletedChildren)
		}
	}
	deleted, err := tx.Client().Account.Delete().Where(dbaccount.IDIn(ids...)).Exec(mixins.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}
	if deleted != len(ids) {
		return nil, fmt.Errorf("cindy account delete count mismatch: expected %d, deleted %d", len(ids), deleted)
	}
	payload := map[string]any{"account_ids": deleteIDs, "group_ids": groupIDs}
	if err := enqueueSchedulerOutbox(ctx, tx.Client(), service.SchedulerOutboxEventAccountBulkChanged, nil, nil, payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, accountID := range deleteIDs {
		r.deleteSchedulerAccountSnapshot(ctx, accountID)
	}
	return &service.CindyInsufficientDeleteResult{
		DeletedCount: len(ids), DependentDeletedCount: len(childIDs), DeletedAccountIDs: deleteIDs,
	}, nil
}
