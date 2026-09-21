package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func accountViewFenceMetadata() json.RawMessage {
	view := service.AccountJobViewMetadata{AccountViewIdentityV1: extensionv1.AccountViewIdentityV1{Version: 1, PluginID: 3, PluginKey: service.CindyAccountViewPluginKey, PackageSHA256: strings.Repeat("a", 64), ViewID: service.CindyAccountViewID, PresetID: "cindy", ViewDefinitionDigest: strings.Repeat("b", 64)}, RuntimeGeneration: 11, PolicyRevision: 7, NormalizedQueryDigest: strings.Repeat("c", 64)}
	raw, _ := json.Marshal(map[string]any{"plugin_id": 8, "plugin_generation": 4, "account_view": view})
	return raw
}

func TestAccountViewJobFenceLocksBothOwnersInStableOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT runtime_generation,state,package_sha256,revision.*FOR SHARE`).WithArgs(int64(3), service.CindyAccountViewPluginKey).WillReturnRows(sqlmock.NewRows([]string{"generation", "state", "package", "revision"}).AddRow(11, service.PluginStateEnabled, strings.Repeat("a", 64), 7))
	mock.ExpectQuery(`SELECT p.runtime_generation,p.state,.*FOR SHARE`).WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"generation", "state", "enabled"}).AddRow(4, service.PluginStateEnabled, true))
	mock.ExpectRollback()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, lockAccountJobPlugin(context.Background(), tx, accountViewFenceMetadata(), service.AccountJobKindBulkUpdate))
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountViewJobFenceRejectsRevokedOriginBeforeAnyWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT runtime_generation,state,package_sha256,revision.*FOR SHARE`).WithArgs(int64(3), service.CindyAccountViewPluginKey).WillReturnRows(sqlmock.NewRows([]string{"generation", "state", "package", "revision"}).AddRow(11, service.PluginStateDisabled, strings.Repeat("a", 64), 8))
	mock.ExpectRollback()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.ErrorIs(t, lockAccountJobPlugin(context.Background(), tx, accountViewFenceMetadata(), service.AccountJobKindBulkUpdate), service.ErrAccountViewUnavailable)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet(), "no action-owner or business SQL is permitted after origin rejection")
}
