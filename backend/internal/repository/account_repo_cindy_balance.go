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
	"github.com/lib/pq"
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
		ID:          account.ID,
		Platform:    account.Platform,
		Type:        account.Type,
		Status:      account.Status,
		Schedulable: account.Schedulable,
		Credentials: account.Credentials,
		MarkedAt:    account.CindyBalanceInsufficientAt,
	}, nil
}

type dbAccountCandidate struct {
	ID          int64
	Platform    string
	Type        string
	Status      string
	Schedulable bool
	Credentials map[string]any
	MarkedAt    *time.Time
}

func isCindyCandidate(account *dbAccountCandidate) bool {
	return account != nil && service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
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
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account.MarkedAt != nil && isCindyCandidate(account) && account.Status == service.StatusActive && account.Schedulable {
			ids = append(ids, account.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func loadCindyInsufficientCandidates(ctx context.Context, repo *accountRepository, lock bool) ([]*dbAccountCandidate, error) {
	query := repo.client.Account.Query().
		Where(dbaccount.CindyBalanceInsufficientAtNotNil()).
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
			ID: account.ID, Platform: account.Platform, Type: account.Type,
			Status: account.Status, Schedulable: account.Schedulable,
			Credentials: account.Credentials, MarkedAt: account.CindyBalanceInsufficientAt,
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

func (r *accountRepository) DeleteCindyInsufficient(ctx context.Context, expectedCount int, fingerprint string) (*service.CindyInsufficientDeleteResult, error) {
	if expectedCount < 0 || service.NormalizeCindyInsufficientFingerprint(fingerprint) == "" {
		return nil, service.ErrCindyInsufficientDeleteChanged
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txRepo := &accountRepository{client: tx.Client(), sql: tx.Client(), schedulerCache: r.schedulerCache}
	accounts, err := loadCindyInsufficientCandidates(ctx, txRepo, true)
	if err != nil {
		return nil, err
	}
	ids := cindyInsufficientCandidateIDs(accounts)
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

	bindings, err := tx.Client().AccountGroup.Query().
		Where(dbaccountgroup.AccountIDIn(ids...)).
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

	if _, err := tx.Client().AccountGroup.Delete().Where(dbaccountgroup.AccountIDIn(ids...)).Exec(ctx); err != nil {
		return nil, err
	}
	if _, err := tx.Client().ExecContext(ctx, "DELETE FROM scheduled_test_plans WHERE account_id = ANY($1)", pq.Array(ids)); err != nil {
		return nil, err
	}
	deleted, err := tx.Client().Account.Delete().Where(dbaccount.IDIn(ids...)).Exec(mixins.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}
	if deleted != len(ids) {
		return nil, fmt.Errorf("cindy account delete count mismatch: expected %d, deleted %d", len(ids), deleted)
	}
	payload := map[string]any{"account_ids": ids, "group_ids": groupIDs}
	if err := enqueueSchedulerOutbox(ctx, tx.Client(), service.SchedulerOutboxEventAccountBulkChanged, nil, nil, payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, accountID := range ids {
		r.deleteSchedulerAccountSnapshot(ctx, accountID)
	}
	return &service.CindyInsufficientDeleteResult{DeletedCount: len(ids), DeletedAccountIDs: ids}, nil
}
