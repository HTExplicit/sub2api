package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newCindyHealthRepoTest(t *testing.T) (*cindyHealthRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &cindyHealthRepository{db: db}, mock
}

func TestCindyHealthRepositoryBeginsEpisodeOnlyForActiveGeneration(t *testing.T) {
	repo, mock := newCindyHealthRepoTest(t)
	episode := service.CindyHealthEpisode{AccountID: 9201, Generation: 11, EpisodeID: "episode-11"}
	observedAt := time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC)
	quarantineUntil := observedAt.Add(5 * time.Minute)

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT i.id.*JOIN accounts a.*a.platform = 'cindy'.*FOR UPDATE OF a, i").
		WithArgs(episode.AccountID, episode.Generation, service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey, "https://api.laxarouter.ai").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(301))
	mock.ExpectQuery("INSERT INTO cindy_health_states").
		WithArgs(episode.AccountID, int64(301), episode.Generation, episode.EpisodeID, service.CindyHealthEvidenceExactBudget, observedAt, quarantineUntil).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(episode.AccountID))
	mock.ExpectCommit()

	applied, err := repo.BeginCindyHealthEpisode(
		context.Background(), episode, service.CindyHealthEvidenceExactBudget, observedAt, quarantineUntil,
	)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyHealthRepositoryRejectsStaleGenerationBeforeStateWrite(t *testing.T) {
	repo, mock := newCindyHealthRepoTest(t)
	episode := service.CindyHealthEpisode{AccountID: 9202, Generation: 4, EpisodeID: "stale"}
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT i.id.*JOIN accounts a.*a.platform = 'cindy'.*FOR UPDATE OF a, i").
		WithArgs(episode.AccountID, episode.Generation, service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey, "https://api.laxarouter.ai").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	applied, err := repo.BeginCindyHealthEpisode(context.Background(), episode, service.CindyHealthEvidenceExactBudget, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.False(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyHealthRepositoryConfirmsOnlyMatchingEpisodeAndPublishesSchedulerChange(t *testing.T) {
	repo, mock := newCindyHealthRepoTest(t)
	episode := service.CindyHealthEpisode{AccountID: 9203, Generation: 8, EpisodeID: "episode-8"}
	confirmedAt := time.Date(2026, 8, 21, 2, 3, 4, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT h.account_id.*JOIN account_credential_identities.*i.active").
		WithArgs(episode.AccountID, episode.Generation, episode.EpisodeID).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(episode.AccountID))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE cindy_health_states")).
		WithArgs(service.CindyHealthStatusConfirmedExhausted, service.CindyHealthEvidenceDualExact, confirmedAt, episode.AccountID, episode.Generation, episode.EpisodeID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts")).
		WithArgs(confirmedAt, episode.AccountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, &episode.AccountID, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	applied, err := repo.FinalizeCindyHealthEpisode(context.Background(), episode, service.CindyHealthFinalization{
		Status: service.CindyHealthStatusConfirmedExhausted, Evidence: service.CindyHealthEvidenceDualExact, ObservedAt: confirmedAt,
	})
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyHealthRepositorySuccessDeletesTransientWithoutClearingConfirmedMarker(t *testing.T) {
	repo, mock := newCindyHealthRepoTest(t)
	const accountID int64 = 9204
	const generation int64 = 9

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT h.episode_id").
		WithArgs(accountID, generation).
		WillReturnRows(sqlmock.NewRows([]string{"episode_id"}).AddRow("episode-9"))
	mock.ExpectExec("DELETE FROM cindy_health_states").
		WithArgs(accountID, generation, "episode-9").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	episode, recovered, err := repo.RecoverTransientCindyHealth(context.Background(), accountID, generation, time.Now())
	require.NoError(t, err)
	require.True(t, recovered)
	require.Equal(t, "episode-9", episode.EpisodeID)
	require.NoError(t, mock.ExpectationsWereMet())
}
