package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPromptRulesRepositoryCASAndReferencedAccounts(t *testing.T) {
	for _, scenario := range []string{"revision_conflict", "referenced", "save"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			policy := extensionv1.PromptRulePolicy{Version: 1, Rules: []extensionv1.PromptRule{{ID: "default", Name: "Default", FollowActive: true, Delivery: "native_control", Position: "control_append"}}, DefaultRuleIDs: []string{"default"}}
			mock.ExpectBegin()
			revision := int64(7)
			if scenario == "revision_conflict" {
				revision++
			}
			mock.ExpectQuery("SELECT revision FROM system_prompt_runtime").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(revision))
			if scenario == "revision_conflict" {
				mock.ExpectRollback()
			} else {
				mock.ExpectQuery("SELECT EXISTS").WithArgs(`["default"]`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(scenario == "referenced"))
				if scenario == "referenced" {
					mock.ExpectRollback()
				} else {
					raw, _ := json.Marshal(policy)
					mock.ExpectExec("INSERT INTO system_prompt_rule_policies").WithArgs(string(raw)).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec("UPDATE system_prompt_runtime SET revision").WithArgs(int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				}
			}
			repo := &businessSystemPromptRepository{db: db}
			err = repo.UpdateBusinessSystemPromptRules(context.Background(), policy, 7, 9)
			switch scenario {
			case "revision_conflict":
				require.ErrorIs(t, err, service.ErrBusinessSystemPromptRevisionConflict)
			case "referenced":
				require.ErrorIs(t, err, service.ErrPromptRuleReferenced)
			default:
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
