package repository

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// The system prompt binding is written only by its own endpoint: an account
// update keeps what is stored under the row lock, its absence included,
// whatever the editor submitted.
func TestLockAndMergeAccountProbeExtraKeepsLockedSystemPromptBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		locked []byte
		want   any
	}{
		{name: "stored binding", locked: []byte(`{"mode":"off"}`), want: map[string]any{"mode": "off"}},
		{name: "no stored binding"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectQuery(`(?s)SELECT.*extra -> 'system_prompt'.*FOR NO KEY UPDATE`).
				WithArgs(int64(41), service.PlatformOpenAI, service.AccountTypeOAuth, `{"access_token":"test"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{
					"identity_unchanged", "ollama_group_unchanged", "ollama_proxy_unchanged",
					"enabled", "rate_sync_enabled", "snapshot", "ollama_session", "ollama_auto", "ollama_snapshot",
					"system_prompt",
					"opencode_group_unchanged", "opencode_auto", "opencode_snapshot",
					"grok_observed_models", "upstream_model_metadata",
				}).AddRow(true, false, true, nil, nil, nil, nil, nil, nil, test.locked, false, nil, nil, nil, nil))
			account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "test"},
				Extra: map[string]any{"new_admin_setting": true, service.AccountExtraSystemPromptKey: map[string]any{"mode": "custom", "prompt_id": "stale"}}}

			extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)

			require.NoError(t, err)
			require.Equal(t, true, extra["new_admin_setting"])
			if test.want == nil {
				require.NotContains(t, extra, service.AccountExtraSystemPromptKey)
			} else {
				require.Equal(t, test.want, extra[service.AccountExtraSystemPromptKey])
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateExtraMetadataKeepsAtomicWriteAndOutbox(t *testing.T) {
	for _, outboxFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[outboxFails], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			updates := map[string]any{
				service.UpstreamModelMetadataExtraKey: map[string]any{"fresh": true},
			}
			payload, err := json.Marshal(updates)
			require.NoError(t, err)
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) || $1::jsonb")).
				WithArgs(string(payload), int64(27)).WillReturnResult(sqlmock.NewResult(0, 1))
			outbox := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
				WithArgs(service.SchedulerOutboxEventAccountChanged, int64(27), nil, nil, sqlmock.AnyArg())
			if outboxFails {
				outbox.WillReturnError(errors.New("outbox failed"))
				mock.ExpectRollback()
			} else {
				outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}

			err = newAccountRepositoryWithSQL(client, db, nil).UpdateExtra(context.Background(), 27, updates)

			if outboxFails {
				require.EqualError(t, err, "outbox failed")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
