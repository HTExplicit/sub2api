//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNativeCodexConfigGenerationIsAtomicAndIdempotent(t *testing.T) {
	for _, phase := range []string{"initial", "unchanged", "changed"} {
		t.Run(phase, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			r := &nativeCodexRepository{db: db}
			hash := strings.Repeat("a", 64)
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM sub2api_plugin_installations WHERE plugin_key=$1`)).WithArgs(service.NativeCodexPluginKey).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_xact_lock_shared(hashtextextended($1,0))`)).WithArgs("sub2api-plugin-runtime:7").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,runtime_generation,state FROM sub2api_plugin_installations WHERE plugin_key=$1 FOR UPDATE`)).WithArgs(service.NativeCodexPluginKey).WillReturnRows(sqlmock.NewRows([]string{"id", "generation", "state"}).AddRow(7, 12, "disabled"))
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key=$1`)).WithArgs(service.NativeCodexRetirementSettingKey).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"version":1,"completed":true,"plugins":{"codexrip.codex-runtime":{}}}`))
			read := mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key=$1`)).WithArgs(service.NativeCodexConfigSettingKey)
			if phase == "initial" {
				read.WillReturnError(sql.ErrNoRows)
			} else {
				oldHash := hash
				if phase == "changed" {
					oldHash = strings.Repeat("b", 64)
				}
				read.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"version":1,"config_version":3,"config_sha256":"` + oldHash + `"}`))
			}
			if phase != "unchanged" {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`)).WithArgs("sub2api-plugin-runtime:7").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			}
			if phase == "changed" {
				mock.ExpectQuery(regexp.QuoteMeta(`UPDATE sub2api_plugin_installations SET runtime_generation=runtime_generation+1,updated_at=NOW() WHERE id=$1 AND plugin_key=$2 AND state='disabled' AND runtime_generation=$3 RETURNING runtime_generation`)).WithArgs(int64(7), service.NativeCodexPluginKey, int64(12)).WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(13))
			}
			if phase != "unchanged" {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`)).WithArgs(service.NativeCodexConfigSettingKey, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
			}
			mock.ExpectCommit()
			metadata, err := r.SyncNativeCodexConfig(context.Background(), hash)
			require.NoError(t, err)
			expected := int64(12)
			if phase == "changed" {
				expected = 13
			}
			require.Equal(t, expected, metadata.RuntimeGeneration)
			require.Equal(t, hash, metadata.ConfigSHA256)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestNativeCodexStoredConfigDistinguishesMissingAndReadError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &nativeCodexRepository{db: db}
	query := regexp.QuoteMeta(`SELECT config_encrypted FROM sub2api_plugin_installations WHERE plugin_key=$1`)
	sourceQuery := regexp.QuoteMeta(`SELECT value FROM settings WHERE key=$1`)
	mock.ExpectQuery(sourceQuery).WithArgs(service.NativeCodexSourceSettingKey).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(query).WithArgs(service.NativeCodexPluginKey).WillReturnError(sql.ErrNoRows)
	_, present, err := r.ReadNativeCodexStoredConfig(context.Background())
	require.NoError(t, err)
	require.False(t, present)
	failure := errors.New("fixture storage unavailable")
	mock.ExpectQuery(sourceQuery).WithArgs(service.NativeCodexSourceSettingKey).WillReturnError(failure)
	_, _, err = r.ReadNativeCodexStoredConfig(context.Background())
	require.ErrorIs(t, err, failure, "a read failure cannot select the deployment defaults")
	mock.ExpectQuery(sourceQuery).WithArgs(service.NativeCodexSourceSettingKey).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("native-cipher-fixture"))
	value, present, err := r.ReadNativeCodexStoredConfig(context.Background())
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, "native-cipher-fixture", value)

	require.NoError(t, mock.ExpectationsWereMet())
}
