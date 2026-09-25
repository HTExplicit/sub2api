package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPromptSourceTargetsRejectStaleOrPartialPublication(t *testing.T) {
	for _, scenario := range []string{"stale", "missing_subscriber", "extra_target"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT revision FROM system_prompt_runtime").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(7))
			targets := service.PromptSourceTargets{ExpectedRevision: 7, RuleIDs: []string{"one"}}
			wantErr := service.ErrBusinessSystemPromptInvalid
			if scenario == "stale" {
				targets.ExpectedRevision = 6
				wantErr = service.ErrBusinessSystemPromptRevisionConflict
			} else {
				rows := sqlmock.NewRows([]string{"id"}).AddRow("one")
				if scenario == "missing_subscriber" {
					rows.AddRow("two")
				} else {
					targets.RuleIDs = append(targets.RuleIDs, "extra")
				}
				mock.ExpectQuery("SELECT rule->>'id'").WillReturnRows(rows)
			}
			mock.ExpectRollback()
			store := &remoteSkillRegistryRepository{db: db}
			_, err = store.PublishRemoteSkillVersionWithPromptTargets(context.Background(), 3, 9, 2, targets)
			require.ErrorIs(t, err, wantErr)
			require.NoError(t, mock.ExpectationsWereMet(), "no source or prompt write is allowed after a target conflict")
		})
	}
}
