package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptRepositoryLoadsActiveSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.enabled")).
		WillReturnRows(sqlmock.NewRows([]string{
			"enabled", "expose_server_prompt", "compact_enabled", "active_template_id", "active_version_id", "revision", "template_version", "body", "sha256", "byte_length", "updated_at",
		}).AddRow(true, false, false, int64(3), int64(8), int64(4), int64(2), "body", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", 4, time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC)))

	store := NewBusinessSystemPromptRepository(db)
	snapshot, err := store.LoadBusinessSystemPrompt(context.Background())
	require.NoError(t, err)
	require.True(t, snapshot.Enabled)
	require.Equal(t, int64(4), snapshot.Revision)
	require.Equal(t, int64(2), snapshot.TemplateVersion)
	require.Equal(t, "body", snapshot.Body)
	require.Equal(t, time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC), snapshot.UpdatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPublishBusinessSystemPromptVersionRejectsCorruptStoredBodyBeforeCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectQuery("SELECT v\\.body, v\\.sha256, v\\.byte_length").
		WithArgs(int64(3), int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"body", "sha256", "byte_length"}).AddRow("body", strings.Repeat("0", 64), 4))
	mock.ExpectRollback()

	store := NewBusinessSystemPromptRepository(db)
	_, err = store.PublishBusinessSystemPromptVersion(context.Background(), 2, 3, 7, 9)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateStoredBusinessSystemPromptAcceptsMatchingDigest(t *testing.T) {
	body := "stored body"
	digest := sha256.Sum256([]byte(body))
	require.NoError(t, validateStoredBusinessSystemPrompt(body, hex.EncodeToString(digest[:]), len(body)))
	require.ErrorIs(t, validateStoredBusinessSystemPrompt(body, strings.Repeat("0", 64), len(body)), service.ErrBusinessSystemPromptUnavailable)
}

func TestEnsureBusinessSystemPromptSeedDoesNotTouchExistingSlug(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("moxinggang_reverse_skill", "seed", "description").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "moxinggang_reverse_skill", Name: "seed", Description: "description", Body: "captured body",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedRejectsMismatchedByteLength(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "moxinggang_reverse_skill", Name: "seed", Body: "captured body", ByteLength: 99,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "seed byte length mismatch")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockBusinessSystemPromptRuntimeRevisionRejectsStaleExpectedRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectRollback()

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = lockBusinessSystemPromptRuntimeRevision(context.Background(), tx, 6)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptRevisionConflict)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
