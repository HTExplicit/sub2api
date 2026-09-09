package admin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImportDataProxiesUsesExplicitReplacementFields(t *testing.T) {
	expiresAt := int64(1900000000)
	backupID := int64(7)
	for _, reused := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			name := "new"
			if reused {
				name = "reused"
			}
			if populated {
				name += " with expiry and backup"
			} else {
				name += " with legacy omitted settings"
			}
			t.Run(name, func(t *testing.T) {
				svc := newStubAdminService()
				svc.proxies = []service.Proxy{{ID: backupID, Name: "backup", Protocol: "http", Host: "backup.example", Port: 8080}}
				item := DataProxy{Name: "primary", Protocol: "http", Host: "primary.example", Port: 8080, Status: "inactive"}
				if populated {
					item.ExpiresAt = &expiresAt
					item.FallbackMode = service.FallbackModeProxy
					item.BackupProxyName = "backup"
					item.ExpiryWarnDays = 5
				}
				if reused {
					oldExpiry := time.Unix(expiresAt-3600, 0).UTC()
					svc.proxies = append(svc.proxies, service.Proxy{
						ID: 11, Name: item.Name, Protocol: item.Protocol, Host: item.Host, Port: item.Port,
						Status: service.StatusActive, ExpiresAt: &oldExpiry, FallbackMode: service.FallbackModeProxy,
						BackupProxyID: &backupID, ExpiryWarnDays: 9,
					})
				}
				handler := &AccountHandler{adminService: svc}
				result := DataImportResult{}
				_, err := handler.importDataProxies(context.Background(), []DataProxy{item}, &result)
				require.NoError(t, err)
				require.Empty(t, result.Errors)
				require.Len(t, svc.updatedProxies, 1)
				update := svc.updatedProxies[0]
				require.Equal(t, "inactive", update.Status)
				require.NotNil(t, update.ExpiryWarnDays)
				require.Equal(t, item.ExpiryWarnDays, *update.ExpiryWarnDays)
				require.Equal(t, !populated, update.ClearExpiresAt)
				require.Equal(t, !populated, update.ClearBackupID)
				if populated {
					require.NotNil(t, update.ExpiresAt)
					require.Equal(t, expiresAt, update.ExpiresAt.Unix())
					require.Equal(t, service.FallbackModeProxy, update.FallbackMode)
					require.Equal(t, &backupID, update.BackupProxyID)
				} else {
					require.Nil(t, update.ExpiresAt)
					require.Nil(t, update.BackupProxyID)
					require.Equal(t, service.FallbackModeNone, update.FallbackMode)
				}
				if reused {
					require.Equal(t, 1, result.ProxyReused)
					require.Empty(t, svc.createdProxies)
				} else {
					require.Equal(t, 1, result.ProxyCreated)
					require.Len(t, svc.createdProxies, 1)
					require.Equal(t, update.ExpiresAt, svc.createdProxies[0].ExpiresAt)
					require.Equal(t, update.BackupProxyID, svc.createdProxies[0].BackupProxyID)
				}
			})
		}
	}
}

func TestAccountJobImportResolvesNewBackupProxyByName(t *testing.T) {
	svc := newDataV2AdminService()
	primaryID := int64(11)
	svc.proxies = []service.Proxy{{
		ID: primaryID, Name: "primary", Protocol: "http", Host: "primary.example", Port: 8080, Status: service.StatusActive,
	}}
	handler := &AccountHandler{adminService: svc}
	primaryKey := buildProxyKey("http", "primary.example", 8080, "", "")
	req := DataImportRequest{Data: DataPayload{
		Type: dataType, Version: dataVersion,
		Proxies: []DataProxy{
			{Name: "new-backup", Protocol: "http", Host: "backup.example", Port: 8080},
			{Name: "primary", Protocol: "http", Host: "primary.example", Port: 8080, Status: "inactive", FallbackMode: service.FallbackModeProxy, BackupProxyName: "new-backup"},
		},
		Accounts: []DataAccount{{
			Name: "imported", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "import-key", "base_url": "https://provider.example/v1"},
			Concurrency: 1, ProxyKey: &primaryKey,
		}},
	}}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 904, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()

	require.Len(t, svc.createdProxies, 1)
	require.Len(t, svc.updatedProxies, 1)
	require.Equal(t, []int64{primaryID}, svc.updatedProxyIDs)
	update := svc.updatedProxies[0]
	require.Equal(t, service.FallbackModeProxy, update.FallbackMode)
	require.NotNil(t, update.BackupProxyID)
	require.Equal(t, int64(400), *update.BackupProxyID)
	require.False(t, update.ClearBackupID)

	results, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 1, Ordinal: 1}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
	require.Len(t, svc.createdAccounts, 1)
	require.Equal(t, &primaryID, svc.createdAccounts[0].ProxyID)
}

func TestImportDataProxiesMissingBackupDowngradesWithoutDroppingWarning(t *testing.T) {
	svc := newStubAdminService()
	svc.proxies = nil
	handler := &AccountHandler{adminService: svc}
	result := DataImportResult{}
	_, err := handler.importDataProxies(context.Background(), []DataProxy{{
		Name: "primary", Protocol: "http", Host: "primary.example", Port: 8080, Status: "inactive",
		FallbackMode: service.FallbackModeProxy, BackupProxyName: "missing-backup",
	}}, &result)
	require.NoError(t, err)
	require.Equal(t, 1, result.ProxyCreated)
	require.Len(t, result.Errors, 1)
	require.Contains(t, result.Errors[0].Message, "fallback_mode downgraded to none")
	require.Len(t, svc.createdProxies, 1)
	require.Equal(t, service.FallbackModeNone, svc.createdProxies[0].FallbackMode)
	require.Nil(t, svc.createdProxies[0].BackupProxyID)
	require.Len(t, svc.updatedProxies, 1)
	require.Equal(t, service.FallbackModeNone, svc.updatedProxies[0].FallbackMode)
	require.True(t, svc.updatedProxies[0].ClearBackupID)
}
