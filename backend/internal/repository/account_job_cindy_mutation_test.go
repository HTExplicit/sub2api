package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type accountJobIdentityTransactionStub struct {
	bindErr error
	result  *service.BindAccountCredentialIdentityResult
	params  []service.BindAccountCredentialIdentityParams
}

func (s *accountJobIdentityTransactionStub) Bind(context.Context, service.BindAccountCredentialIdentityParams) (*service.BindAccountCredentialIdentityResult, error) {
	return nil, errors.New("unexpected non-transactional bind")
}

func (s *accountJobIdentityTransactionStub) FindByFingerprint(context.Context, string) (*service.AccountCredentialIdentity, error) {
	return nil, service.ErrCredentialIdentityNotFound
}

func (s *accountJobIdentityTransactionStub) GetActiveByAccountID(context.Context, int64) (*service.AccountCredentialIdentity, error) {
	return nil, service.ErrCredentialIdentityNotFound
}

func (s *accountJobIdentityTransactionStub) BindInTransaction(
	_ context.Context,
	tx service.AccountCredentialIdentityTransaction,
	params service.BindAccountCredentialIdentityParams,
) (*service.BindAccountCredentialIdentityResult, error) {
	if tx == nil {
		return nil, service.ErrCredentialIdentityInvalid
	}
	s.params = append(s.params, params)
	if s.bindErr != nil {
		return nil, s.bindErr
	}
	if s.result != nil {
		result := *s.result
		result.Identity.AccountID = params.AccountID
		result.Identity.ProviderProfile = params.ProviderProfile
		result.Identity.AuthType = params.AuthType
		result.Identity.NormalizedBaseURL = params.NormalizedBaseURL
		result.Identity.Fingerprint = params.Fingerprint
		return &result, nil
	}
	return &service.BindAccountCredentialIdentityResult{Identity: service.AccountCredentialIdentity{
		ID: 91, AccountID: params.AccountID, ProviderProfile: params.ProviderProfile,
		AuthType: params.AuthType, NormalizedBaseURL: params.NormalizedBaseURL,
		Fingerprint: params.Fingerprint, Generation: 3, Active: true,
	}}, nil
}

type accountJobRuntimeBlockerStub struct {
	cleared []int64
}

func (s *accountJobRuntimeBlockerStub) BlockAccountScheduling(*service.Account, time.Time, string) {}

func (s *accountJobRuntimeBlockerStub) ClearAccountSchedulingBlock(accountID int64) {
	s.cleared = append(s.cleared, accountID)
}

type accountJobSchedulerRefresherStub struct {
	mock         sqlmock.Sqlmock
	accountID    int64
	groupIDs     []int64
	beforeCommit bool
}

func (s *accountJobSchedulerRefresherStub) RefreshAccountAndGroups(_ context.Context, accountID int64, groupIDs []int64) error {
	if err := s.mock.ExpectationsWereMet(); err != nil {
		s.beforeCommit = true
	}
	s.accountID = accountID
	s.groupIDs = append([]int64(nil), groupIDs...)
	return nil
}

func newAccountJobCindyMutationTest(
	t *testing.T,
	identity *accountJobIdentityTransactionStub,
) (*accountJobCindyMutationRunner, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return &accountJobCindyMutationRunner{client: client, identity: identity}, mock
}

func expectAccountJobCindyMutationLocks(mock sqlmock.Sqlmock, accountID int64, groupIDs ...int64) {
	mock.ExpectQuery("(?s)SELECT id FROM accounts.*FOR UPDATE").
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(accountID))
	mock.ExpectQuery("(?s)SELECT id FROM account_credential_identities.*FOR UPDATE").
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(90))
	groupRows := sqlmock.NewRows([]string{"group_id"})
	for _, groupID := range groupIDs {
		groupRows.AddRow(groupID)
	}
	mock.ExpectQuery("(?s)SELECT group_id FROM account_groups.*FOR SHARE").
		WithArgs(accountID).
		WillReturnRows(groupRows)
}

func expectCanonicalCindyMutationAccount(mock sqlmock.Sqlmock, accountID int64) {
	expectCanonicalCindyMutationAccountWithCredential(mock, accountID, "test-key", cindyMutationTestDeviceID)
	expectAvailableCindyDeviceClaim(mock, accountID, cindyMutationTestDeviceID)
}

const cindyMutationTestDeviceID = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func expectCanonicalCindyMutationAccountWithCredential(mock sqlmock.Sqlmock, accountID int64, apiKey, deviceID string) {
	credentials := []byte(`{"base_url":"https://api.laxarouter.ai","api_key":"` + apiKey + `"}`)
	extra := []byte(`{"cindy_device_id":"` + deviceID + `","cindy_device_id_source":"input-preserved"}`)
	mock.ExpectQuery("(?s)SELECT id, name, platform, wire_platform, provider_profile, type, credentials, extra,.*FOR UPDATE").
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "platform", "wire_platform", "provider_profile", "type", "credentials", "extra",
			"status", "schedulable", "updated_at",
		}).AddRow(accountID, "Cindy", service.PlatformCindy, service.WirePlatformOpenAI,
			service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey, credentials, extra,
			service.StatusActive, true, time.Now().UTC()))
}

func expectAvailableCindyDeviceClaim(mock sqlmock.Sqlmock, accountID int64, deviceID string) {
	mock.ExpectQuery("SELECT pg_advisory_xact_lock").WithArgs(advisoryLockHash("cindy-device:" + deviceID)).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_xact_lock"}).AddRow(nil))
	mock.ExpectQuery("(?s)SELECT id FROM accounts.*cindy_device_id.*FOR UPDATE").
		WithArgs(accountID, service.PlatformCindy, service.WirePlatformOpenAI,
			service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey,
			"https://api.laxarouter.ai", "https://api.laxarouter.ai/", deviceID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func TestAccountJobCindyMutationCommitsIdentityHealthAndSchedulerTogether(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		created   bool
		rotated   bool
	}{
		{name: "created", accountID: 71, created: true},
		{name: "rotated", accountID: 72, rotated: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := &accountJobIdentityTransactionStub{result: &service.BindAccountCredentialIdentityResult{
				Created: test.created, Rotated: test.rotated,
				Identity: service.AccountCredentialIdentity{ID: 91, Generation: 2, Active: true},
			}}
			runner, mock := newAccountJobCindyMutationTest(t, identity)
			runtime := &accountJobRuntimeBlockerStub{}
			runner.runtimeBlocker = runtime

			mock.ExpectBegin()
			expectAccountJobCindyMutationLocks(mock, test.accountID)
			expectCanonicalCindyMutationAccount(mock, test.accountID)
			mock.ExpectExec(regexp.QuoteMeta("DELETE FROM cindy_health_states WHERE account_id = $1")).
				WithArgs(test.accountID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE accounts SET cindy_balance_insufficient_at = NULL").
				WithArgs(test.accountID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO scheduler_outbox").
				WithArgs(service.SchedulerOutboxEventAccountChanged, &test.accountID, nil, nil, sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			account, err := runner.Run(context.Background(), test.accountID, func(ctx context.Context) (*service.Account, error) {
				require.NotNil(t, dbent.TxFromContext(ctx))
				return canonicalCindyJobMutationAccount(test.accountID), nil
			})

			require.NoError(t, err)
			require.Equal(t, test.accountID, account.ID)
			require.Len(t, identity.params, 1)
			require.Equal(t, test.accountID, identity.params[0].AccountID)
			require.Equal(t, []int64{test.accountID}, runtime.cleared)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountJobCindyMutationKeepsHealthWhenCredentialIdentityIsUnchanged(t *testing.T) {
	identity := &accountJobIdentityTransactionStub{result: &service.BindAccountCredentialIdentityResult{
		Identity: service.AccountCredentialIdentity{ID: 91, Generation: 3, Active: true},
	}}
	runner, mock := newAccountJobCindyMutationTest(t, identity)
	runtime := &accountJobRuntimeBlockerStub{}
	runner.runtimeBlocker = runtime
	accountID := int64(73)

	mock.ExpectBegin()
	expectAccountJobCindyMutationLocks(mock, accountID)
	expectCanonicalCindyMutationAccount(mock, accountID)
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	account, err := runner.Run(context.Background(), accountID, func(ctx context.Context) (*service.Account, error) {
		require.NotNil(t, dbent.TxFromContext(ctx))
		return canonicalCindyJobMutationAccount(accountID), nil
	})

	require.NoError(t, err)
	require.Equal(t, accountID, account.ID)
	require.Empty(t, runtime.cleared)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountJobCindyMutationReturnsAndCachesCompleteCallbackAccountAfterCommit(t *testing.T) {
	identity := &accountJobIdentityTransactionStub{result: &service.BindAccountCredentialIdentityResult{
		Rotated:  true,
		Identity: service.AccountCredentialIdentity{ID: 91, Generation: 4, Active: true},
	}}
	runner, mock := newAccountJobCindyMutationTest(t, identity)
	refresher := &accountJobSchedulerRefresherStub{mock: mock}
	runner.scheduler = refresher
	accountID := int64(74)
	markedAt := time.Now().UTC()
	callbackAccount := canonicalCindyJobMutationAccount(accountID)
	callbackAccount.GroupIDs = []int64{8, 9}
	callbackAccount.Extra = map[string]any{"complete": true}
	callbackAccount.CindyBalanceInsufficientAt = &markedAt

	mock.ExpectBegin()
	expectAccountJobCindyMutationLocks(mock, accountID, 7, 8)
	expectCanonicalCindyMutationAccount(mock, accountID)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM cindy_health_states WHERE account_id = $1")).
		WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE accounts SET cindy_balance_insufficient_at = NULL").
		WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	returned, err := runner.Run(context.Background(), accountID, func(context.Context) (*service.Account, error) {
		return callbackAccount, nil
	})

	require.NoError(t, err)
	require.Same(t, callbackAccount, returned)
	require.Nil(t, returned.CindyBalanceInsufficientAt)
	require.Equal(t, accountID, refresher.accountID)
	require.Equal(t, []int64{7, 8, 9}, refresher.groupIDs)
	require.Equal(t, true, returned.Extra["complete"])
	require.False(t, refresher.beforeCommit)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountJobCindyMutationRollsBackAccountAndGroupsWhenIdentityBindFails(t *testing.T) {
	bindErr := errors.New("synthetic identity bind failure")
	identity := &accountJobIdentityTransactionStub{bindErr: bindErr}
	runner, mock := newAccountJobCindyMutationTest(t, identity)
	accountID := int64(72)

	mock.ExpectBegin()
	expectAccountJobCindyMutationLocks(mock, accountID)
	mock.ExpectExec("UPDATE accounts SET name").WithArgs("Changed", accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO account_groups").WithArgs(accountID, int64(9)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectCanonicalCindyMutationAccount(mock, accountID)
	mock.ExpectRollback()

	_, err := runner.Run(context.Background(), accountID, func(ctx context.Context) (*service.Account, error) {
		tx := dbent.TxFromContext(ctx)
		require.NotNil(t, tx)
		if _, updateErr := tx.Client().ExecContext(ctx, "UPDATE accounts SET name = $1 WHERE id = $2", "Changed", accountID); updateErr != nil {
			return nil, updateErr
		}
		if _, groupErr := tx.Client().ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)", accountID, int64(9)); groupErr != nil {
			return nil, groupErr
		}
		return canonicalCindyJobMutationAccount(accountID), nil
	})

	require.ErrorIs(t, err, bindErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountJobCindyMutationSerializesSameDeviceAcrossCredentialKeys(t *testing.T) {
	identity := &accountJobIdentityTransactionStub{}
	runner, mock := newAccountJobCindyMutationTest(t, identity)
	firstAccountID := int64(75)
	secondAccountID := int64(76)
	deviceID := cindyMutationTestDeviceID

	mock.ExpectBegin()
	expectAccountJobCindyMutationLocks(mock, firstAccountID)
	expectCanonicalCindyMutationAccountWithCredential(mock, firstAccountID, "test-key-one", deviceID)
	expectAvailableCindyDeviceClaim(mock, firstAccountID, deviceID)
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, &firstAccountID, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	first, err := runner.Run(context.Background(), firstAccountID, func(context.Context) (*service.Account, error) {
		return canonicalCindyJobMutationAccountWithCredential(firstAccountID, "test-key-one", deviceID), nil
	})
	require.NoError(t, err)
	require.Equal(t, firstAccountID, first.ID)

	mock.ExpectBegin()
	expectAccountJobCindyMutationLocks(mock, secondAccountID)
	expectCanonicalCindyMutationAccountWithCredential(mock, secondAccountID, "test-key-two", deviceID)
	mock.ExpectQuery("SELECT pg_advisory_xact_lock").WithArgs(advisoryLockHash("cindy-device:" + deviceID)).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_xact_lock"}).AddRow(nil))
	mock.ExpectQuery("(?s)SELECT id FROM accounts.*cindy_device_id.*FOR UPDATE").
		WithArgs(secondAccountID, service.PlatformCindy, service.WirePlatformOpenAI,
			service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey,
			"https://api.laxarouter.ai", "https://api.laxarouter.ai/", deviceID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(firstAccountID))
	mock.ExpectRollback()

	_, err = runner.Run(context.Background(), secondAccountID, func(context.Context) (*service.Account, error) {
		return canonicalCindyJobMutationAccountWithCredential(secondAccountID, "test-key-two", deviceID), nil
	})

	require.ErrorIs(t, err, service.ErrCindyDeviceIdentityConflict)
	require.Len(t, identity.params, 1, "only the first serialized device claim may bind and commit")
	require.Equal(t, firstAccountID, identity.params[0].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func canonicalCindyJobMutationAccountWithCredential(accountID int64, apiKey, deviceID string) *service.Account {
	account := canonicalCindyJobMutationAccount(accountID)
	account.Credentials["api_key"] = apiKey
	account.Extra = map[string]any{
		service.CindyDeviceIDExtraKey:       deviceID,
		service.CindyDeviceIDSourceExtraKey: "input-preserved",
	}
	return account
}

func canonicalCindyJobMutationAccount(accountID int64) *service.Account {
	return &service.Account{
		ID: accountID, Name: "Cindy", Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.laxarouter.ai",
			"api_key":  "test-key",
		},
		Status: service.StatusActive, Schedulable: true,
	}
}

var _ service.AccountCredentialIdentityTransactionalRepository = (*accountJobIdentityTransactionStub)(nil)
