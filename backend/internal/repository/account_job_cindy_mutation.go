package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountSchedulerRefresher interface {
	RefreshAccountAndGroups(context.Context, int64, []int64) error
}

type accountJobCindyMutationRunner struct {
	client         *dbent.Client
	identity       service.AccountCredentialIdentityRepository
	scheduler      accountSchedulerRefresher
	runtimeBlocker service.AccountRuntimeBlocker
}

func NewAccountJobCindyMutationRunner(
	client *dbent.Client,
	identity service.AccountCredentialIdentityRepository,
	scheduler *service.SchedulerSnapshotService,
	runtimeBlocker service.AccountRuntimeBlocker,
) service.AccountJobCindyMutationRunner {
	return &accountJobCindyMutationRunner{
		client: client, identity: identity, scheduler: scheduler, runtimeBlocker: runtimeBlocker,
	}
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
	var previousGroupIDs []int64
	if accountID > 0 {
		if err = lockCindyAccountJobTarget(ctx, txClient, accountID); err != nil {
			return nil, err
		}
		previousGroupIDs, err = loadCindyAccountJobGroupIDs(ctx, txClient, accountID)
		if err != nil {
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
	if err = claimCindyDeviceIdentity(ctx, txClient, current); err != nil {
		return nil, err
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
	bindResult, err := identityRepo.BindInTransaction(ctx, txClient, service.BindAccountCredentialIdentityParams{
		AccountID: current.ID, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		AuthType: service.AccountTypeAPIKey, NormalizedBaseURL: normalizedURL,
		Fingerprint: fingerprint,
	})
	if err != nil {
		return nil, err
	}
	credentialChanged := bindResult != nil && (bindResult.Created || bindResult.Rotated)
	if credentialChanged {
		if _, err = txClient.ExecContext(ctx, `DELETE FROM cindy_health_states WHERE account_id = $1`, current.ID); err != nil {
			return nil, err
		}
		if _, err = txClient.ExecContext(ctx, `
			UPDATE accounts SET cindy_balance_insufficient_at = NULL, updated_at = NOW()
			WHERE id = $1 AND deleted_at IS NULL`, current.ID); err != nil {
			return nil, err
		}
	}
	if err = enqueueSchedulerOutbox(ctx, txClient, service.SchedulerOutboxEventAccountChanged, &current.ID, nil, nil); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if credentialChanged {
		account.CindyBalanceInsufficientAt = nil
	}
	if r.scheduler != nil {
		cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		groupIDs := mergeCindyAccountMutationGroupIDs(previousGroupIDs, account.GroupIDs)
		if cacheErr := r.scheduler.RefreshAccountAndGroups(cacheCtx, account.ID, groupIDs); cacheErr != nil {
			logger.LegacyPrintf("repository.account", "[Scheduler] sync Cindy account snapshot failed: id=%d err=%v", account.ID, cacheErr)
		}
		cancel()
	}
	if credentialChanged {
		if r.runtimeBlocker != nil {
			r.runtimeBlocker.ClearAccountSchedulingBlock(account.ID)
		}
	}
	return account, nil
}

func claimCindyDeviceIdentity(ctx context.Context, client *dbent.Client, account *service.Account) error {
	if client == nil || account == nil || account.ID <= 0 {
		return errors.New("cindy device identity claim is unavailable")
	}
	deviceID := strings.TrimSpace(account.GetExtraString(service.CindyDeviceIDExtraKey))
	if !service.ValidCindyDeviceID(deviceID) {
		return errors.New("cindy device identity is invalid")
	}

	lockRows, err := client.QueryContext(ctx, "SELECT pg_advisory_xact_lock($1)", advisoryLockHash("cindy-device:"+deviceID))
	if err != nil {
		return err
	}
	for lockRows.Next() {
	}
	if err = lockRows.Err(); err != nil {
		_ = lockRows.Close()
		return err
	}
	if err = lockRows.Close(); err != nil {
		return err
	}

	ownerRows, err := client.QueryContext(ctx, `
		SELECT id FROM accounts
		WHERE deleted_at IS NULL
		  AND id <> $1
		  AND platform = $2
		  AND wire_platform = $3
		  AND provider_profile = $4
		  AND type = $5
		  AND lower(btrim(credentials->>'base_url')) IN ($6, $7)
		  AND jsonb_typeof(credentials->'api_key') = 'string'
		  AND btrim(credentials->>'api_key') <> ''
		  AND jsonb_typeof(extra->'cindy_device_id') = 'string'
		  AND btrim(extra->>'cindy_device_id') = $8
		LIMIT 1
		FOR UPDATE`,
		account.ID, service.PlatformCindy, service.WirePlatformOpenAI,
		service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey,
		"https://api.laxarouter.ai", "https://api.laxarouter.ai/", deviceID,
	)
	if err != nil {
		return err
	}
	defer func() { _ = ownerRows.Close() }()
	if !ownerRows.Next() {
		return ownerRows.Err()
	}
	var ownerID int64
	if err = ownerRows.Scan(&ownerID); err != nil {
		return err
	}
	return service.ErrCindyDeviceIdentityConflict
}

func loadCindyAccountJobGroupIDs(ctx context.Context, client *dbent.Client, accountID int64) ([]int64, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT group_id FROM account_groups
		WHERE account_id = $1
		ORDER BY group_id
		FOR SHARE`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	groupIDs := make([]int64, 0)
	for rows.Next() {
		var groupID int64
		if err = rows.Scan(&groupID); err != nil {
			return nil, err
		}
		if groupID > 0 {
			groupIDs = append(groupIDs, groupID)
		}
	}
	return groupIDs, rows.Err()
}

func mergeCindyAccountMutationGroupIDs(previous, current []int64) []int64 {
	seen := make(map[int64]struct{}, len(previous)+len(current))
	merged := make([]int64, 0, len(previous)+len(current))
	for _, groupIDs := range [][]int64{previous, current} {
		for _, groupID := range groupIDs {
			if groupID <= 0 {
				continue
			}
			if _, exists := seen[groupID]; exists {
				continue
			}
			seen[groupID] = struct{}{}
			merged = append(merged, groupID)
		}
	}
	return merged
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
		SELECT id, name, platform, wire_platform, provider_profile, type, credentials, extra,
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
	var credentials, extra []byte
	if err = rows.Scan(&account.ID, &account.Name, &account.Platform, &account.WirePlatform,
		&account.ProviderProfile, &account.Type, &credentials, &extra, &account.Status,
		&account.Schedulable, &account.UpdatedAt); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(credentials, &account.Credentials); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(extra, &account.Extra); err != nil {
		return nil, err
	}
	return account, rows.Err()
}

var _ service.AccountJobCindyMutationRunner = (*accountJobCindyMutationRunner)(nil)
