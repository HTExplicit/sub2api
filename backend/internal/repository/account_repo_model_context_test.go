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

func modelContextRepositoryAccount(patch map[string]*int64) *service.Account {
	return &service.Account{
		ID: 27, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials:                map[string]any{"api_key": "sk-test"},
		ModelContextOverridesPatch: patch,
	}
}

func modelContextRepositoryInt64(value int64) *int64 { return &value }

func TestMergeAccountModelContextExtraPreservesLatestManagedValues(t *testing.T) {
	extra := map[string]any{
		"other": "keep",
		service.UpstreamModelContextCapacitiesExtraKey: map[string]any{"stale": true},
		service.UpstreamModelMetadataExtraKey:          map[string]any{"stale": true},
		service.ModelContextOverridesExtraKey:          map[string]any{"stale-model": 300000},
	}
	before, err := json.Marshal(extra)
	require.NoError(t, err)
	currentCapacities := []byte(`{"models":{"dynamic":{"context_window":750000}},"observed_at":"2026-09-07T00:00:00Z"}`)
	currentOverrides := []byte(`{"current-model":1200000}`)
	currentMetadata := []byte(`{"models":{"dynamic":{"reasoning_supported":true,"input_modalities":["text"]}}}`)

	got, err := mergeAccountModelContextExtra(modelContextRepositoryAccount(nil), extra,
		currentCapacities, currentOverrides, currentMetadata)

	require.NoError(t, err)
	require.Equal(t, "keep", got["other"])
	for key, expected := range map[string][]byte{
		service.UpstreamModelContextCapacitiesExtraKey: currentCapacities,
		service.ModelContextOverridesExtraKey:          currentOverrides,
		service.UpstreamModelMetadataExtraKey:          currentMetadata,
	} {
		actual, marshalErr := json.Marshal(got[key])
		require.NoError(t, marshalErr)
		require.JSONEq(t, string(expected), string(actual))
	}
	after, err := json.Marshal(extra)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "merging must not change the stale caller snapshot")
}

func TestMergeAccountModelContextExtraDoesNotResurrectRemovedValues(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(`null`)} {
		extra := map[string]any{
			service.UpstreamModelContextCapacitiesExtraKey: map[string]any{"stale": true},
			service.ModelContextOverridesExtraKey:          map[string]any{"stale": 300000},
			service.UpstreamModelMetadataExtraKey:          map[string]any{"stale": true},
		}
		got, err := mergeAccountModelContextExtra(modelContextRepositoryAccount(nil), extra, raw, raw, raw)
		require.NoError(t, err)
		require.Empty(t, got)
	}
}

func TestMergeAccountModelContextExtraPatchesLatestModelsIndependently(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		patch    map[string]*int64
		expected string
	}{
		{
			name: "set retains another editor's model", current: `{"model-a":500000,"model-b":700000}`,
			patch:    map[string]*int64{"model-a": modelContextRepositoryInt64(900000)},
			expected: `{"model-a":900000,"model-b":700000}`,
		},
		{
			name: "different model additions compose", current: `{"model-a":500000}`,
			patch:    map[string]*int64{"model-b": modelContextRepositoryInt64(700000), "model-c": modelContextRepositoryInt64(1050000)},
			expected: `{"model-a":500000,"model-b":700000,"model-c":1050000}`,
		},
		{
			name: "null deletes only the named model", current: `{"model-a":500000,"model-b":700000}`,
			patch: map[string]*int64{"model-a": nil}, expected: `{"model-b":700000}`,
		},
		{
			name: "nil patch leaves current map", current: `{"model-a":500000,"model-b":700000}`,
			expected: `{"model-a":500000,"model-b":700000}`,
		},
		{
			name: "empty patch leaves current model", current: `{"model-a":500000}`,
			patch: map[string]*int64{}, expected: `{"model-a":500000}`,
		},
		{
			name: "deleting absent model is a no-op", current: `{"model-a":500000}`,
			patch: map[string]*int64{"model-b": nil}, expected: `{"model-a":500000}`,
		},
		{
			name: "last deletion removes the extra key", current: `{"model-a":500000}`,
			patch: map[string]*int64{"model-a": nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stale := map[string]any{service.ModelContextOverridesExtraKey: map[string]any{"stale-model": 123}}
			got, err := mergeAccountModelContextExtra(modelContextRepositoryAccount(tt.patch), stale,
				nil, []byte(tt.current), nil)
			require.NoError(t, err)
			if tt.expected == "" {
				require.NotContains(t, got, service.ModelContextOverridesExtraKey)
				return
			}
			actual, err := json.Marshal(got[service.ModelContextOverridesExtraKey])
			require.NoError(t, err)
			require.JSONEq(t, tt.expected, string(actual))
		})
	}
}

func TestMergeAccountModelContextExtraRejectsInvalidPatch(t *testing.T) {
	for name, patch := range map[string]map[string]*int64{
		"zero":           {"model-a": modelContextRepositoryInt64(0)},
		"negative":       {"model-a": modelContextRepositoryInt64(-1)},
		"unsafe integer": {"model-a": modelContextRepositoryInt64(9007199254740992)},
		"empty model":    {"": modelContextRepositoryInt64(258000)},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := mergeAccountModelContextExtra(modelContextRepositoryAccount(patch), nil,
				nil, []byte(`{"model-b":700000}`), nil)
			require.Error(t, err)
			require.Nil(t, got)
		})
	}
}

func TestMergeAccountModelContextExtraRevalidatesProtectedAccounts(t *testing.T) {
	for _, account := range []*service.Account{
		{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
		{Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey, ProviderProfile: service.ProviderProfileCindyLaxaV1},
	} {
		t.Run(account.Platform+"/"+account.Type, func(t *testing.T) {
			account.ModelContextOverridesPatch = map[string]*int64{"model-a": modelContextRepositoryInt64(258000)}
			_, err := mergeAccountModelContextExtra(account, nil, nil, nil, nil)
			require.Error(t, err)
			account.ModelContextOverridesPatch = nil
			_, err = mergeAccountModelContextExtra(account, nil, nil, nil, nil)
			require.NoError(t, err, "ordinary protected-account edits must remain supported")
		})
	}
}

func TestMergeAccountModelContextExtraRejectsMalformedStoredJSON(t *testing.T) {
	for _, malformedColumn := range []int{0, 1, 2} {
		values := [3][]byte{}
		values[malformedColumn] = []byte(`{"malformed":`)
		got, err := mergeAccountModelContextExtra(modelContextRepositoryAccount(nil), nil,
			values[0], values[1], values[2])
		require.Error(t, err)
		require.Nil(t, got)
	}
}

func TestLockAndMergeAccountProbeExtraUsesLockedModelContextValues(t *testing.T) {
	for _, identityUnchanged := range []bool{true, false} {
		t.Run(map[bool]string{true: "same identity", false: "changed identity"}[identityUnchanged], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectQuery(`(?s)SELECT.*extra -> 'upstream_model_context_capacities'.*extra -> 'model_context_overrides'.*extra -> 'upstream_model_metadata'.*FOR NO KEY UPDATE`).
				WithArgs(int64(27), service.PlatformOpenAI, service.AccountTypeAPIKey, `{"api_key":"sk-test"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{
					"identity_unchanged", "credential_generation_unchanged", "ollama_group_unchanged", "ollama_proxy_unchanged",
					"enabled", "rate_sync_enabled", "snapshot", "ollama_session", "ollama_auto", "ollama_snapshot",
					"upstream_context_capacities", "model_context_overrides", "upstream_model_metadata",
				}).AddRow(identityUnchanged, identityUnchanged, false, true, nil, nil, nil, nil, nil, nil,
					[]byte(`{"fresh":true}`), []byte(`{"other-model":700000}`), []byte(`{"fresh":true}`)))
			account := modelContextRepositoryAccount(map[string]*int64{"model-a": modelContextRepositoryInt64(1050000)})
			account.Extra = map[string]any{
				service.UpstreamModelContextCapacitiesExtraKey: map[string]any{"stale": true},
				service.ModelContextOverridesExtraKey:          map[string]any{"other-model": 1},
				service.UpstreamModelMetadataExtraKey:          map[string]any{"stale": true},
			}

			got, _, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)

			require.NoError(t, err)
			require.Equal(t, map[string]any{"fresh": true}, got[service.UpstreamModelContextCapacitiesExtraKey])
			require.Equal(t, map[string]any{"fresh": true}, got[service.UpstreamModelMetadataExtraKey])
			require.Equal(t, map[string]int64{"other-model": 700000, "model-a": 1050000}, got[service.ModelContextOverridesExtraKey])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestShouldEnqueueSchedulerOutboxForExtraUpdatesModelContextKeys(t *testing.T) {
	for _, key := range []string{
		service.UpstreamModelContextCapacitiesExtraKey,
		service.ModelContextOverridesExtraKey,
		service.UpstreamModelMetadataExtraKey,
	} {
		t.Run(key, func(t *testing.T) {
			require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{key: map[string]any{}}))
		})
	}
}

func TestUpdateAccountModelContextPatchClearsOnlyAfterSuccessfulWrite(t *testing.T) {
	for _, outboxFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[outboxFails], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery(`(?s)SELECT.*FOR NO KEY UPDATE`).
				WithArgs(int64(27), service.PlatformOpenAI, service.AccountTypeAPIKey, `{"api_key":"sk-test"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{
					"identity_unchanged", "credential_generation_unchanged", "ollama_group_unchanged", "ollama_proxy_unchanged",
					"enabled", "rate_sync_enabled", "snapshot", "ollama_session", "ollama_auto", "ollama_snapshot",
					"upstream_context_capacities", "model_context_overrides", "upstream_model_metadata",
				}).AddRow(true, true, false, true, nil, nil, nil, nil, nil, nil,
					[]byte(`{"fresh":true}`), []byte(`{"other-model":700000}`), []byte(`{"fresh":true}`)))
			mock.ExpectExec(`(?s)UPDATE .*accounts.*SET.*WHERE .*id.*`).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`(?s)SELECT .* FROM "accounts" WHERE "id" = \$1`).
				WithArgs(int64(27)).WillReturnRows(updatedAccountRows(27, `{}`))
			outbox := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox"))
			if outboxFails {
				outbox.WillReturnError(errors.New("outbox failed"))
				mock.ExpectRollback()
			} else {
				outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			patch := map[string]*int64{"model-a": modelContextRepositoryInt64(1050000)}
			account := modelContextRepositoryAccount(patch)
			account.Name = "test"
			account.Concurrency = 1
			account.Priority = 1
			account.Status = service.StatusActive
			account.Schedulable = true

			err = newAccountRepositoryWithSQL(client, db, nil).Update(context.Background(), account)

			if outboxFails {
				require.EqualError(t, err, "outbox failed")
				require.Equal(t, patch, account.ModelContextOverridesPatch)
			} else {
				require.NoError(t, err)
				require.Nil(t, account.ModelContextOverridesPatch)
			}
			require.Equal(t, map[string]int64{"other-model": 700000, "model-a": 1050000}, account.Extra[service.ModelContextOverridesExtraKey])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateExtraModelContextSyncKeepsAtomicWriteAndOutbox(t *testing.T) {
	for _, outboxFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[outboxFails], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			updates := map[string]any{
				service.UpstreamModelContextCapacitiesExtraKey: map[string]any{"fresh": true},
				service.UpstreamModelMetadataExtraKey:          map[string]any{"fresh": true},
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
