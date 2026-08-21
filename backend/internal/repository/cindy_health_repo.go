package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type cindyHealthRepository struct {
	db *sql.DB
}

func NewCindyHealthRepository(db *sql.DB) service.CindyHealthRepository {
	return &cindyHealthRepository{db: db}
}

const activeCindyCredentialIdentityForUpdate = `
	SELECT i.id
	FROM account_credential_identities i
	JOIN accounts a ON a.id = i.account_id
	WHERE i.account_id = $1 AND i.generation = $2 AND i.active
	  AND i.provider_profile = $3 AND i.auth_type = $4 AND i.normalized_base_url = $5
	  AND a.deleted_at IS NULL AND a.platform = 'cindy' AND a.wire_platform = 'openai'
	  AND a.provider_profile = 'cindy_laxa_v1' AND a.type = 'apikey'
	FOR UPDATE OF a, i`

func (r *cindyHealthRepository) BeginCindyHealthEpisode(
	ctx context.Context,
	episode service.CindyHealthEpisode,
	evidence string,
	observedAt, quarantineUntil time.Time,
) (bool, error) {
	if r == nil || r.db == nil || episode.AccountID <= 0 || episode.Generation <= 0 ||
		strings.TrimSpace(episode.EpisodeID) == "" || strings.TrimSpace(evidence) == "" ||
		observedAt.IsZero() || !quarantineUntil.After(observedAt) {
		return false, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var identityID int64
	err = tx.QueryRowContext(
		ctx, activeCindyCredentialIdentityForUpdate,
		episode.AccountID, episode.Generation, service.ProviderProfileCindyLaxaV1,
		service.AccountTypeAPIKey, "https://api.laxarouter.ai",
	).Scan(&identityID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var accountID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO cindy_health_states (
			account_id, credential_identity_id, credential_generation, episode_id,
			status, evidence, observed_at, quarantine_until, confirmed_at, updated_at
		) VALUES ($1, $2, $3, $4, 'quarantined', $5, $6, $7, NULL, NOW())
		ON CONFLICT (account_id) DO UPDATE SET
			credential_identity_id = EXCLUDED.credential_identity_id,
			credential_generation = EXCLUDED.credential_generation,
			episode_id = EXCLUDED.episode_id,
			status = EXCLUDED.status,
			evidence = EXCLUDED.evidence,
			observed_at = EXCLUDED.observed_at,
			quarantine_until = EXCLUDED.quarantine_until,
			confirmed_at = NULL,
			updated_at = NOW()
		WHERE cindy_health_states.credential_generation <> EXCLUDED.credential_generation
		   OR (cindy_health_states.status <> 'confirmed_exhausted'
		       AND (EXCLUDED.evidence = 'exact_budget' OR cindy_health_states.evidence <> 'exact_budget'))
		RETURNING account_id`,
		episode.AccountID, identityID, episode.Generation, episode.EpisodeID,
		evidence, observedAt.UTC(), quarantineUntil.UTC(),
	).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return accountID == episode.AccountID, nil
}

func (r *cindyHealthRepository) FinalizeCindyHealthEpisode(
	ctx context.Context,
	episode service.CindyHealthEpisode,
	finalization service.CindyHealthFinalization,
) (bool, error) {
	if r == nil || r.db == nil || episode.AccountID <= 0 || episode.Generation <= 0 ||
		strings.TrimSpace(episode.EpisodeID) == "" || finalization.ObservedAt.IsZero() {
		return false, nil
	}
	switch finalization.Status {
	case service.CindyHealthStatusHealthy, service.CindyHealthStatusConfirmedExhausted:
	case service.CindyHealthStatusQuarantined:
		if finalization.QuarantineUntil == nil || !finalization.QuarantineUntil.After(finalization.ObservedAt) {
			return false, nil
		}
	default:
		return false, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var accountID int64
	err = tx.QueryRowContext(ctx, `
		SELECT h.account_id
		FROM cindy_health_states h
		JOIN account_credential_identities i
		  ON i.id = h.credential_identity_id AND i.account_id = h.account_id
		JOIN accounts a ON a.id = h.account_id
		WHERE h.account_id = $1 AND h.credential_generation = $2 AND h.episode_id = $3
		  AND i.active AND i.generation = $2
		  AND a.deleted_at IS NULL AND a.platform = 'cindy' AND a.wire_platform = 'openai'
		  AND a.provider_profile = 'cindy_laxa_v1' AND a.type = 'apikey'
		FOR UPDATE OF h, i, a`, episode.AccountID, episode.Generation, episode.EpisodeID).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	switch finalization.Status {
	case service.CindyHealthStatusHealthy:
		result, execErr := tx.ExecContext(ctx, `
			DELETE FROM cindy_health_states
			WHERE account_id = $1 AND credential_generation = $2 AND episode_id = $3
			  AND status = 'quarantined'`, episode.AccountID, episode.Generation, episode.EpisodeID)
		if execErr != nil {
			return false, execErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return false, rowsErr
		}
	case service.CindyHealthStatusQuarantined:
		result, execErr := tx.ExecContext(ctx, `
			UPDATE cindy_health_states
			SET status = $1, evidence = $2, observed_at = $3,
			    quarantine_until = $4, confirmed_at = NULL, updated_at = NOW()
			WHERE account_id = $5 AND credential_generation = $6 AND episode_id = $7
			  AND status = 'quarantined'`,
			finalization.Status, finalization.Evidence, finalization.ObservedAt.UTC(),
			finalization.QuarantineUntil.UTC(), episode.AccountID, episode.Generation, episode.EpisodeID)
		if execErr != nil {
			return false, execErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return false, rowsErr
		}
	case service.CindyHealthStatusConfirmedExhausted:
		result, execErr := tx.ExecContext(ctx, `
			UPDATE cindy_health_states
			SET status = $1, evidence = $2, observed_at = $3,
			    quarantine_until = NULL, confirmed_at = $3, updated_at = NOW()
			WHERE account_id = $4 AND credential_generation = $5 AND episode_id = $6
			  AND status = 'quarantined'`,
			finalization.Status, finalization.Evidence, finalization.ObservedAt.UTC(),
			episode.AccountID, episode.Generation, episode.EpisodeID)
		if execErr != nil {
			return false, execErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return false, rowsErr
		}
		result, execErr = tx.ExecContext(ctx, `
			UPDATE accounts
			SET cindy_balance_insufficient_at = COALESCE(cindy_balance_insufficient_at, $1), updated_at = NOW()
			WHERE id = $2 AND deleted_at IS NULL`, finalization.ObservedAt.UTC(), episode.AccountID)
		if execErr != nil {
			return false, execErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return false, rowsErr
		}
		if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &episode.AccountID, nil, nil); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return accountID == episode.AccountID, nil
}

func (r *cindyHealthRepository) RecoverTransientCindyHealth(
	ctx context.Context,
	accountID, generation int64,
	_ time.Time,
) (*service.CindyHealthEpisode, bool, error) {
	if r == nil || r.db == nil || accountID <= 0 || generation <= 0 {
		return nil, false, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var episodeID string
	err = tx.QueryRowContext(ctx, `
		SELECT h.episode_id
		FROM cindy_health_states h
		JOIN account_credential_identities i
		  ON i.id = h.credential_identity_id AND i.account_id = h.account_id
		JOIN accounts a ON a.id = h.account_id
		WHERE h.account_id = $1 AND h.credential_generation = $2
		  AND h.status = 'quarantined' AND i.active AND i.generation = $2
		  AND a.deleted_at IS NULL AND a.platform = 'cindy' AND a.wire_platform = 'openai'
		  AND a.provider_profile = 'cindy_laxa_v1' AND a.type = 'apikey'
		FOR UPDATE OF h, i, a`, accountID, generation).Scan(&episodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM cindy_health_states
		WHERE account_id = $1 AND credential_generation = $2 AND episode_id = $3
		  AND status = 'quarantined'`, accountID, generation, episodeID)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return &service.CindyHealthEpisode{AccountID: accountID, Generation: generation, EpisodeID: episodeID}, true, nil
}

var _ service.CindyHealthRepository = (*cindyHealthRepository)(nil)
