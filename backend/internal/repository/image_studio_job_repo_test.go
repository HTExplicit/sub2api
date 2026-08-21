//go:build unit

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

func TestImageStudioRepositoryCreatePersistsOneItemPerRequestedImage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	requestExpires := now.Add(service.ImageStudioFileRetention)
	retainUntil := now.Add(service.ImageStudioMetadataRetention)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO image_studio_jobs")).
		WithArgs(int64(7), int64(11), service.ImageStudioModeEdit, service.ImageStudioModelGeminiProImage,
			"replace the sky", "1024x1024", "low", 4, requestExpires, retainUntil).
		WillReturnRows(imageStudioJobRows(now).AddRow(
			int64(41), int64(7), int64(11), service.ImageStudioModeEdit, service.ImageStudioModelGeminiProImage,
			"replace the sky", "1024x1024", "low", 4, service.ImageStudioJobPending,
			0, 0, 0, 0, nil, nil, nil, requestExpires, retainUntil, nil, nil, now, now,
		))
	for ordinal := 1; ordinal <= 4; ordinal++ {
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO image_studio_items")).
			WithArgs(int64(41), ordinal).
			WillReturnResult(sqlmock.NewResult(int64(ordinal), 1))
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO image_studio_artifacts")).
		WithArgs(int64(41), service.ImageStudioArtifactReference, "ref-key", "image/png", int64(12), requestExpires).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO image_studio_artifacts")).
		WithArgs(int64(41), service.ImageStudioArtifactMask, "mask-key", "image/png", int64(8), requestExpires).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	repo := NewImageStudioRepository(db)
	job, err := repo.Create(context.Background(), service.ImageStudioCreateParams{
		UserID: 7,
		Input: service.ImageStudioCreateInput{
			APIKeyID: 11, Mode: service.ImageStudioModeEdit, Model: service.ImageStudioModelGeminiProImage,
			Prompt: "replace the sky", Size: "1024x1024", Quality: "low", Count: 4,
		},
		RequestExpiresAt: requestExpires,
		RetainUntil:      retainUntil,
		InputArtifacts: []service.ImageStudioInputArtifact{
			{Kind: service.ImageStudioArtifactReference, StorageKey: "ref-key", ContentType: "image/png", ByteSize: 12},
			{Kind: service.ImageStudioArtifactMask, StorageKey: "mask-key", ContentType: "image/png", ByteSize: 8},
		},
	})

	require.NoError(t, err)
	require.Equal(t, int64(41), job.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageStudioRepositoryGetScopesJobToSessionOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)FROM image_studio_jobs\s+WHERE id=\$1 AND user_id=\$2`).
		WithArgs(int64(99), int64(7)).
		WillReturnError(sql.ErrNoRows)

	_, err = NewImageStudioRepository(db).Get(context.Background(), 7, 99)
	require.ErrorIs(t, err, service.ErrImageStudioNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageStudioRepositoryExpireRequestsPreservesSucceededItemCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE image_studio_items item`).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`succeeded_count=counts\.succeeded`).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE image_studio_jobs SET prompt=''`).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, NewImageStudioRepository(db).ExpireRequests(context.Background(), now))
	require.NoError(t, mock.ExpectationsWereMet())
}

func imageStudioJobRows(_ time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "user_id", "api_key_id", "mode", "model", "prompt", "size", "quality", "count", "status",
		"processed_count", "succeeded_count", "failed_count", "canceled_count", "cancel_requested_at",
		"error_code", "error_message", "request_expires_at", "retain_until", "started_at", "finished_at",
		"created_at", "updated_at",
	})
}
