package repository

import (
	"context"
	"encoding/json"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountJobCindyMutationRunner struct {
	client   *dbent.Client
	identity service.AccountCredentialIdentityRepository
}

func NewAccountJobCindyMutationRunner(
	client *dbent.Client,
	identity service.AccountCredentialIdentityRepository,
) service.AccountJobCindyMutationRunner {
	return &accountJobCindyMutationRunner{client: client, identity: identity}
}

func (r *accountJobCindyMutationRunner) Run(
	ctx context.Context,
	accountID int64,
	mutate func(context.Context) (*service.Account, error),
) (*service.Account, error) {
	if r == nil || r.client == nil || mutate == nil {
		return nil, errors.New("cindy account mutation is unavailable")
	}
	identityRepo, ok := r.identity.(service.AccountCredentialIdentityTransactionalRepository)
	if !ok || identityRepo == nil {
		return nil, errors.New("cindy credential identity transaction is unavailable")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txClient := tx.Client()
	if accountID > 0 {
		if err = lockCindyAccountJobTarget(ctx, txClient, accountID); err != nil {
			return nil, err
		}
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	account, err := mutate(txCtx)
	if err != nil {
		return nil, err
	}
	if account == nil || account.ID <= 0 {
		return nil, errors.New("cindy account mutation returned no account")
	}
	current, err := loadCanonicalCindyAccountForUpdate(ctx, txClient, account.ID)
	if err != nil {
		return nil, err
	}
	if !service.IsCindyAPIKeyAccount(current.Platform, current.Type, current.Credentials) ||
		current.WirePlatform != service.PlatformOpenAI ||
		current.ProviderProfile != service.ProviderProfileCindyLaxaV1 {
		return nil, service.ErrCindyAccountRequired
	}
	normalizedURL, err := service.NormalizeCredentialIdentityBaseURL(
		service.ProviderProfileCindyLaxaV1, current.GetCredential("base_url"),
	)
	if err != nil {
		return nil, err
	}
	fingerprint, err := service.AccountCredentialFingerprint(
		service.ProviderProfileCindyLaxaV1,
		service.AccountTypeAPIKey,
		normalizedURL,
		current.GetCredential("api_key"),
	)
	if err != nil {
		return nil, err
	}
	if _, err = identityRepo.BindInTransaction(ctx, txClient, service.BindAccountCredentialIdentityParams{
		AccountID: current.ID, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		AuthType: service.AccountTypeAPIKey, NormalizedBaseURL: normalizedURL,
		Fingerprint: fingerprint,
	}); err != nil {
		return nil, err
	}
	if _, err = txClient.ExecContext(ctx, `DELETE FROM cindy_health_states WHERE account_id = $1`, current.ID); err != nil {
		return nil, err
	}
	if _, err = txClient.ExecContext(ctx, `
		UPDATE accounts SET cindy_balance_insufficient_at = NULL, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`, current.ID); err != nil {
		return nil, err
	}
	if err = enqueueSchedulerOutbox(ctx, txClient, service.SchedulerOutboxEventAccountChanged, &current.ID, nil, nil); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	current.CindyBalanceInsufficientAt = nil
	return current, nil
}

func lockCindyAccountJobTarget(ctx context.Context, client *dbent.Client, accountID int64) error {
	rows, err := client.QueryContext(ctx, `
		SELECT id FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`, accountID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return service.ErrAccountNotFound
	}
	var lockedID int64
	if err = rows.Scan(&lockedID); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	identityRows, err := client.QueryContext(ctx, `
		SELECT id FROM account_credential_identities
		WHERE account_id = $1 AND active
		FOR UPDATE`, accountID)
	if err != nil {
		return err
	}
	defer func() { _ = identityRows.Close() }()
	for identityRows.Next() {
		var identityID int64
		if err = identityRows.Scan(&identityID); err != nil {
			return err
		}
	}
	return identityRows.Err()
}

func loadCanonicalCindyAccountForUpdate(ctx context.Context, client *dbent.Client, accountID int64) (*service.Account, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT id, name, platform, wire_platform, provider_profile, type, credentials,
		       status, schedulable, updated_at
		FROM accounts WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrAccountNotFound
	}
	account := &service.Account{}
	var credentials []byte
	if err = rows.Scan(&account.ID, &account.Name, &account.Platform, &account.WirePlatform,
		&account.ProviderProfile, &account.Type, &credentials, &account.Status,
		&account.Schedulable, &account.UpdatedAt); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(credentials, &account.Credentials); err != nil {
		return nil, err
	}
	return account, rows.Err()
}

var _ service.AccountJobCindyMutationRunner = (*accountJobCindyMutationRunner)(nil)
