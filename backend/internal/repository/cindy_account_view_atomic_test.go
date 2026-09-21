package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountViewProbeAtomicTerminalTransaction(t *testing.T) {
	for _, failAt := range []string{"none", "health", "marker", "item"} {
		t.Run(failAt, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
			updated := now.Add(-time.Hour)
			credentials := map[string]any{"api_key": "synthetic-atomic-key", "base_url": "https://api.laxarouter.ai"}
			credentialsJSON, _ := json.Marshal(credentials)
			fingerprint, err := service.CindyAccountIdentityFingerprint(service.PlatformCindy, service.AccountTypeAPIKey, credentials)
			require.NoError(t, err)
			identity, err := service.AccountCredentialFingerprint(service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey, "https://api.laxarouter.ai", "synthetic-atomic-key")
			require.NoError(t, err)
			reservation := &service.CindyBalanceProbeReservation{JobID: 7, ItemID: 11, AccountID: 13, Stage: "terra", IdentityFingerprint: fingerprint, AccountUpdatedAt: updated}
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT j\.status.*FOR UPDATE OF j, i`).WithArgs(int64(7), int64(11), "lease").WillReturnRows(sqlmock.NewRows([]string{"status", "cancel", "state", "luna_at", "now"}).AddRow("running", nil, "terra_running", now.Add(-time.Minute), now))
			mock.ExpectExec(`UPDATE cindy_balance_probe_jobs.*consecutive_upstream_failures`).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`SELECT platform, type, status, schedulable, credentials, updated_at,.*FROM accounts`).WithArgs(int64(13)).WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "schedulable", "credentials", "updated", "marked", "deleted"}).AddRow(service.PlatformCindy, service.AccountTypeAPIKey, service.StatusActive, true, credentialsJSON, updated, nil, nil))
			mock.ExpectQuery(`SELECT i.generation,i.fingerprint.*FOR UPDATE OF a,i`).WillReturnRows(sqlmock.NewRows([]string{"generation", "fingerprint"}).AddRow(21, identity))
			mock.ExpectQuery(`SELECT i.id.*FOR UPDATE OF a, i`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(31))
			health := mock.ExpectExec(`INSERT INTO cindy_health_states`)
			if failAt == "health" {
				health.WillReturnError(errors.New("synthetic health failure"))
			} else {
				health.WillReturnResult(sqlmock.NewResult(0, 1))
				marker := mock.ExpectExec(`UPDATE accounts SET cindy_balance_insufficient_at`)
				if failAt == "marker" {
					marker.WillReturnError(errors.New("synthetic marker failure"))
				} else {
					marker.WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec(`INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec(`enqueue_group_api_key_auth_cache_invalidations`).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectQuery(`SELECT status,evidence FROM cindy_health_states`).WithArgs(int64(13)).WillReturnRows(sqlmock.NewRows([]string{"status", "evidence"}).AddRow(service.CindyHealthStatusBalanceInsufficient, service.CindyHealthEvidenceExactBudget))
					item := mock.ExpectExec(`UPDATE cindy_balance_probe_items`)
					if failAt == "item" {
						item.WillReturnError(context.Canceled)
					} else {
						item.WillReturnResult(sqlmock.NewResult(0, 1))
					}
				}
			}
			if failAt == "none" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			tx, err := db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			var committed service.CindyHealthEpisode
			state, err := (&cindyBalanceProbeRepository{db: db}).finalizeAccountMarkerTx(context.Background(), tx, reservation, nil, "lease", now, 5*time.Minute, true, true, &committed)
			if failAt == "none" {
				require.NoError(t, err)
				require.Equal(t, "exhausted", state)
				require.EqualValues(t, 13, committed.AccountID)
				require.Equal(t, identity, committed.Fingerprint)
			} else {
				require.Error(t, err)
				require.Empty(t, state)
				require.Zero(t, committed.AccountID, "failed/canceled transaction cannot publish a committed terminal fact")
				require.NoError(t, tx.Rollback())
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountViewProbeCanceledProgressDoesNotRequireBusinessLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cindy_balance_probe_jobs.*lease_token IS NULL.*cancel_requested.*origin.*FOR UPDATE`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("cancel_requested"))
	mock.ExpectExec(`UPDATE cindy_balance_probe_items.*luna_running.*terra_running`).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE cindy_balance_probe_items.*pending.*luna_exact`).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE cindy_balance_probe_jobs SET status = 'canceled'`).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	done, err := (&cindyBalanceProbeRepository{db: db}).FinishIfDone(context.Background(), 7, "")
	require.NoError(t, err)
	require.True(t, done)
	require.NoError(t, mock.ExpectationsWereMet(), "retained stop/progress cannot issue account/health SQL")
}

func TestAccountViewProbeResumeUsesExactOriginCAS(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		origin := &service.CindyBalanceProbeOrigin{Version: 1, PluginID: 3, PluginKey: service.CindyAccountViewPluginKey, PackageSHA256: strings.Repeat("a", 64), RuntimeGeneration: 4, OperationKey: "fixed", RequestDigest: strings.Repeat("b", 64), FrozenAccountIDs: []int64{2, 3}}
		origin.View.RuntimeGeneration, origin.View.PolicyRevision = 4, 7
		scope := service.CindyBalanceProbeScope{Mode: "all", Origin: origin}
		originalJSON := service.EncodeCindyBalanceProbeScope(scope)
		expectedJSON, _ := json.Marshal(origin)
		next := *origin
		next.RuntimeGeneration, next.View.RuntimeGeneration, next.View.PolicyRevision = 5, 5, 8
		updatedScope := scope
		updatedScope.Origin = &next
		mock.ExpectBegin()
		if conflict {
			other := scope
			otherOrigin := *origin
			otherOrigin.RuntimeGeneration = 99
			other.Origin = &otherOrigin
			mock.ExpectQuery(`SELECT scope FROM cindy_balance_probe_jobs.*FOR UPDATE`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"scope"}).AddRow(service.EncodeCindyBalanceProbeScope(other)))
			mock.ExpectRollback()
		} else {
			mock.ExpectQuery(`SELECT scope FROM cindy_balance_probe_jobs.*FOR UPDATE`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"scope"}).AddRow(originalJSON))
			mock.ExpectExec(`UPDATE cindy_balance_probe_jobs SET scope=\$2::jsonb.*scope=\$3::jsonb`).WithArgs(int64(7), service.EncodeCindyBalanceProbeScope(updatedScope), originalJSON).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
		}
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		err = resumeScopedProbeTx(context.Background(), tx, 7, expectedJSON, &next)
		if conflict {
			require.ErrorIs(t, err, service.ErrCindyBalanceProbeChanged)
			require.NoError(t, tx.Rollback())
		} else {
			require.NoError(t, err)
		}
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
