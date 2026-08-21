package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func credentialIdentityBinding(t *testing.T, accountID int64, key string) service.BindAccountCredentialIdentityParams {
	t.Helper()
	fingerprint, err := service.AccountCredentialFingerprint(
		service.ProviderProfileCindyLaxaV1,
		service.AccountTypeAPIKey,
		"https://api.laxarouter.ai",
		key,
	)
	require.NoError(t, err)
	return service.BindAccountCredentialIdentityParams{
		AccountID: accountID, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		AuthType: service.AccountTypeAPIKey, NormalizedBaseURL: "https://api.laxarouter.ai",
		Fingerprint: fingerprint,
	}
}

func credentialIdentityRows(params service.BindAccountCredentialIdentityParams, id, generation int64, active bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "account_id", "provider_profile", "auth_type", "normalized_base_url",
		"fingerprint", "generation", "active",
	}).AddRow(id, params.AccountID, params.ProviderProfile, params.AuthType, params.NormalizedBaseURL, params.Fingerprint, generation, active)
}

func newCredentialIdentityRepoTest(t *testing.T) (*accountCredentialIdentityRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &accountCredentialIdentityRepository{db: db}, mock
}

func expectCredentialIdentityLookup(mock sqlmock.Sqlmock, params service.BindAccountCredentialIdentityParams, fingerprintRows *sqlmock.Rows, fingerprintErr error, activeRows *sqlmock.Rows, activeErr error) {
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(params.Fingerprint).WillReturnResult(sqlmock.NewResult(0, 1))
	fingerprintQuery := regexp.QuoteMeta(credentialIdentitySelect + ` WHERE fingerprint = $1 ORDER BY account_id LIMIT 1 FOR UPDATE`)
	fingerprintExpectation := mock.ExpectQuery(fingerprintQuery).WithArgs(params.Fingerprint)
	if fingerprintErr != nil {
		fingerprintExpectation.WillReturnError(fingerprintErr)
	} else {
		fingerprintExpectation.WillReturnRows(fingerprintRows)
	}
	activeQuery := regexp.QuoteMeta(credentialIdentitySelect + ` WHERE account_id = $1 AND active FOR UPDATE`)
	activeExpectation := mock.ExpectQuery(activeQuery).WithArgs(params.AccountID)
	if activeErr != nil {
		activeExpectation.WillReturnError(activeErr)
	} else {
		activeExpectation.WillReturnRows(activeRows)
	}
}

func TestAccountCredentialIdentityBindCreatesGenerationOne(t *testing.T) {
	repo, mock := newCredentialIdentityRepoTest(t)
	params := credentialIdentityBinding(t, 41, "first-key")

	mock.ExpectBegin()
	expectCredentialIdentityLookup(mock, params, nil, sql.ErrNoRows, nil, sql.ErrNoRows)
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(generation\\), 0\\) \\+ 1").
		WithArgs(params.AccountID).WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(1))
	mock.ExpectQuery("INSERT INTO account_credential_identities").
		WithArgs(params.AccountID, params.ProviderProfile, params.AuthType, params.NormalizedBaseURL, params.Fingerprint, int64(1)).
		WillReturnRows(credentialIdentityRows(params, 11, 1, true))
	mock.ExpectCommit()

	result, err := repo.Bind(context.Background(), params)
	require.NoError(t, err)
	require.True(t, result.Created)
	require.False(t, result.Rotated)
	require.Equal(t, int64(1), result.Identity.Generation)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCredentialIdentityBindRerunIsIdempotent(t *testing.T) {
	repo, mock := newCredentialIdentityRepoTest(t)
	params := credentialIdentityBinding(t, 42, "same-key")

	mock.ExpectBegin()
	expectCredentialIdentityLookup(mock, params, credentialIdentityRows(params, 12, 3, true), nil, credentialIdentityRows(params, 12, 3, true), nil)
	mock.ExpectCommit()

	result, err := repo.Bind(context.Background(), params)
	require.NoError(t, err)
	require.False(t, result.Created)
	require.False(t, result.Rotated)
	require.Equal(t, int64(3), result.Identity.Generation)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCredentialIdentityBindRotationIncrementsGeneration(t *testing.T) {
	repo, mock := newCredentialIdentityRepoTest(t)
	oldParams := credentialIdentityBinding(t, 43, "old-key")
	newParams := credentialIdentityBinding(t, 43, "new-key")

	mock.ExpectBegin()
	expectCredentialIdentityLookup(mock, newParams, nil, sql.ErrNoRows, credentialIdentityRows(oldParams, 13, 4, true), nil)
	mock.ExpectExec("UPDATE account_credential_identities").WithArgs(int64(13)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO account_credential_identities").
		WithArgs(newParams.AccountID, newParams.ProviderProfile, newParams.AuthType, newParams.NormalizedBaseURL, newParams.Fingerprint, int64(5)).
		WillReturnRows(credentialIdentityRows(newParams, 14, 5, true))
	mock.ExpectCommit()

	result, err := repo.Bind(context.Background(), newParams)
	require.NoError(t, err)
	require.True(t, result.Created)
	require.True(t, result.Rotated)
	require.Equal(t, int64(5), result.Identity.Generation)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCredentialIdentityBindRejectsAnotherAccountFingerprint(t *testing.T) {
	repo, mock := newCredentialIdentityRepoTest(t)
	requested := credentialIdentityBinding(t, 44, "duplicate-key")
	owner := requested
	owner.AccountID = 45

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(requested.Fingerprint).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(credentialIdentitySelect + ` WHERE fingerprint = $1 ORDER BY account_id LIMIT 1 FOR UPDATE`)).
		WithArgs(requested.Fingerprint).WillReturnRows(credentialIdentityRows(owner, 15, 1, true))
	mock.ExpectRollback()

	_, err := repo.Bind(context.Background(), requested)
	require.ErrorIs(t, err, service.ErrCredentialIdentityConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}
