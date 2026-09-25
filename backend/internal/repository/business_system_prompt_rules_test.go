package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPromptRulesRepositoryRejectsLegacyPolicyWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &businessSystemPromptRepository{db: db}
	err = repo.UpdateBusinessSystemPromptRules(context.Background(), nativeapi.PromptRulePolicy{Version: 1}, 7, 9)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptInvalid)
	require.NoError(t, mock.ExpectationsWereMet(), "legacy requests must not write a second effective policy")
}
