package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/jackc/pgx/v5/pgconn"
)

type accountCredentialIdentityRepository struct{ db *sql.DB }

func NewAccountCredentialIdentityRepository(db *sql.DB) service.AccountCredentialIdentityRepository {
	return &accountCredentialIdentityRepository{db: db}
}

func (r *accountCredentialIdentityRepository) Bind(ctx context.Context, params service.BindAccountCredentialIdentityParams) (*service.BindAccountCredentialIdentityResult, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrCredentialIdentityInvalid
	}
	if err := service.ValidateAccountCredentialIdentityBinding(params); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.BindInTransaction(ctx, tx, params)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, translateCredentialIdentityError(err)
	}
	return result, nil
}

func (r *accountCredentialIdentityRepository) BindInTransaction(
	ctx context.Context,
	tx service.AccountCredentialIdentityTransaction,
	params service.BindAccountCredentialIdentityParams,
) (*service.BindAccountCredentialIdentityResult, error) {
	if tx == nil {
		return nil, service.ErrCredentialIdentityInvalid
	}
	if err := service.ValidateAccountCredentialIdentityBinding(params); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, params.Fingerprint); err != nil {
		return nil, err
	}

	active, err := queryCredentialIdentity(ctx, tx, credentialIdentitySelect+` WHERE account_id = $1 AND active FOR UPDATE`, params.AccountID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if params.ExpectedGeneration > 0 && (active == nil ||
		active.Generation != params.ExpectedGeneration ||
		active.Fingerprint != params.ExpectedFingerprint) {
		return nil, service.ErrCredentialIdentityGenerationConflict
	}
	if active != nil && active.Fingerprint == params.Fingerprint {
		if active.ProviderProfile != params.ProviderProfile || active.AuthType != params.AuthType ||
			active.NormalizedBaseURL != params.NormalizedBaseURL {
			return nil, service.ErrCredentialIdentityInvalid
		}
		return &service.BindAccountCredentialIdentityResult{Identity: *active}, nil
	}

	activeOwner, err := queryCredentialIdentity(ctx, tx, credentialIdentitySelect+`
		WHERE fingerprint = $1 AND active AND account_id <> $2
		ORDER BY account_id LIMIT 1 FOR UPDATE`, params.Fingerprint, params.AccountID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if activeOwner != nil {
		return nil, service.ErrCredentialIdentityConflict
	}

	generation := int64(1)
	if active != nil {
		generation = active.Generation + 1
		if _, err = tx.ExecContext(ctx, `
			UPDATE account_credential_identities
			SET active = FALSE, retired_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND active`, active.ID); err != nil {
			return nil, err
		}
	} else {
		rows, queryErr := tx.QueryContext(ctx, `
			SELECT COALESCE(MAX(generation), 0) + 1
			FROM account_credential_identities WHERE account_id = $1`, params.AccountID)
		if queryErr != nil {
			return nil, queryErr
		}
		if !rows.Next() {
			_ = rows.Close()
			if rows.Err() != nil {
				return nil, rows.Err()
			}
			return nil, sql.ErrNoRows
		}
		err = rows.Scan(&generation)
		closeErr := rows.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}

	existing, err := queryCredentialIdentity(ctx, tx, `
		INSERT INTO account_credential_identities
			(account_id, provider_profile, auth_type, normalized_base_url, fingerprint, generation, active)
		VALUES ($1, $2, $3, $4, $5, $6, TRUE)
		RETURNING id, account_id, provider_profile, auth_type, normalized_base_url,
		          fingerprint, generation, active
	`, params.AccountID, params.ProviderProfile, params.AuthType, params.NormalizedBaseURL, params.Fingerprint, generation)
	if err != nil {
		return nil, translateCredentialIdentityError(err)
	}
	return &service.BindAccountCredentialIdentityResult{
		Identity: *existing,
		Rotated:  active != nil,
		Created:  true,
	}, nil
}

func (r *accountCredentialIdentityRepository) GetActiveByAccountID(ctx context.Context, accountID int64) (*service.AccountCredentialIdentity, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrCredentialIdentityInvalid
	}
	identity, err := scanCredentialIdentity(r.db.QueryRowContext(ctx, credentialIdentitySelect+` WHERE account_id = $1 AND active`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCredentialIdentityNotFound
	}
	return identity, err
}

func (r *accountCredentialIdentityRepository) FindByFingerprint(ctx context.Context, fingerprint string) (*service.AccountCredentialIdentity, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrCredentialIdentityInvalid
	}
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(fingerprint) != fingerprint {
		return nil, service.ErrCredentialIdentityInvalid
	}
	identity, err := scanCredentialIdentity(r.db.QueryRowContext(ctx, credentialIdentitySelect+` WHERE fingerprint = $1 ORDER BY account_id LIMIT 1`, fingerprint))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCredentialIdentityNotFound
	}
	return identity, err
}

const credentialIdentitySelect = `
	SELECT id, account_id, provider_profile, auth_type, normalized_base_url,
	       fingerprint, generation, active
	FROM account_credential_identities`

type credentialIdentityScanner interface{ Scan(...any) error }

func queryCredentialIdentity(
	ctx context.Context,
	tx service.AccountCredentialIdentityTransaction,
	query string,
	args ...any,
) (*service.AccountCredentialIdentity, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	return scanCredentialIdentity(rows)
}

func scanCredentialIdentity(row credentialIdentityScanner) (*service.AccountCredentialIdentity, error) {
	identity := &service.AccountCredentialIdentity{}
	if err := row.Scan(&identity.ID, &identity.AccountID, &identity.ProviderProfile, &identity.AuthType,
		&identity.NormalizedBaseURL, &identity.Fingerprint, &identity.Generation, &identity.Active); err != nil {
		return nil, err
	}
	return identity, nil
}

func translateCredentialIdentityError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr != nil && pgErr.Code == "23505" {
		return service.ErrCredentialIdentityConflict
	}
	return err
}

var _ service.AccountCredentialIdentityRepository = (*accountCredentialIdentityRepository)(nil)
var _ service.AccountCredentialIdentityTransactionalRepository = (*accountCredentialIdentityRepository)(nil)
