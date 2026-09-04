package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountJobDataImportPreparesIdentityStateOnceForOneHundredItems(t *testing.T) {
	adminService := newDataV2AdminService()
	adminService.groups = []service.Group{{ID: 12, Name: "prepared", Platform: service.PlatformOpenAI, Status: service.StatusActive}}
	adminService.proxies = []service.Proxy{{ID: 7, Name: "prepared", Protocol: "http", Host: "prepared.example", Port: 8080}}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	accounts := make([]DataAccount, 0, 100)
	for index := 0; index < 100; index++ {
		accounts = append(accounts, DataAccount{
			Name:        fmt.Sprintf("batch-%03d", index),
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": fmt.Sprintf("key-%03d", index), "base_url": "https://provider.example/v1"},
			Concurrency: 1,
		})
	}
	folderName := "Prepared import"
	groupIDs := []int64{12}
	proxyID := int64(7)
	request := DataImportRequest{
		Data: DataPayload{Type: dataType, Version: dataVersion, Accounts: accounts, Proxies: []DataProxy{}},
		UniformSettings: DataImportUniformSettings{
			ManagementFolder: &folderName,
			GroupIDs:         &groupIDs,
			ProxyID:          &proxyID,
		},
	}
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 901, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()

	preparedResults := make([]service.AccountJobExecutionResult, 0, len(accounts))
	for index := range accounts {
		results, executeErr := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: int64(index + 1), Ordinal: index + 1}})
		require.NoError(t, executeErr)
		require.Len(t, results, 1)
		require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
		preparedResults = append(preparedResults, results[0])
	}

	require.Equal(t, 1, adminService.listCalls, "one import job must build the full identity index only once")
	require.Equal(t, 1, adminService.lastListProxies.calls, "one import job must load proxy metadata only once")
	require.Equal(t, 1, adminService.groupListCalls, "one import job must load group metadata only once")
	require.Zero(t, adminService.getGroupCalls, "prepared references must avoid per-item group lookups")
	require.Equal(t, 1, adminService.folderListCalls, "one import job must load folder metadata only once")
	require.Equal(t, 1, adminService.tagListCalls, "one import job must load tag metadata only once")
	require.Len(t, adminService.createdAccounts, 100)

	baselineService := newDataV2AdminService()
	baselineService.groups = append([]service.Group(nil), adminService.groups...)
	baselineService.proxies = append([]service.Proxy(nil), adminService.proxies...)
	baselineHandler := NewAccountHandler(baselineService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	baselineResults := make([]service.AccountJobExecutionResult, 0, len(accounts))
	for index := range accounts {
		results, executeErr := baselineHandler.ExecuteAccountJob(context.Background(), job, raw, []service.AccountJobItem{{ID: int64(index + 1), Ordinal: index + 1}})
		require.NoError(t, executeErr)
		require.Len(t, results, 1)
		baselineResults = append(baselineResults, results[0])
	}
	require.Equal(t, baselineResults, preparedResults, "prepared execution must preserve the prior per-item result semantics")
	require.GreaterOrEqual(t, baselineService.listCalls, 200, "the legacy path documents two full identity scans per item")
	require.GreaterOrEqual(t, baselineService.lastListProxies.calls, 100)
	require.GreaterOrEqual(t, baselineService.getGroupCalls, 100)
	require.GreaterOrEqual(t, baselineService.folderListCalls, 100)
	require.GreaterOrEqual(t, baselineService.tagListCalls, 100)
}

// hydratingDataV2AdminService mirrors the production AdminService boundary:
// GetAllGroupsIncludingInactive returns lightweight groups, while GetGroup
// supplies the authoritative strict-Cindy marker.
type hydratingDataV2AdminService struct {
	*dataV2AdminService
	strictCindy bool
	returnNil   bool
}

func (s *hydratingDataV2AdminService) GetGroup(ctx context.Context, id int64) (*service.Group, error) {
	if s.returnNil {
		return nil, nil
	}
	group, err := s.dataV2AdminService.GetGroup(ctx, id)
	if err != nil || group == nil {
		return group, err
	}
	if group.Platform == service.PlatformCindy &&
		group.EffectiveWirePlatform() == service.WirePlatformOpenAI &&
		group.EffectiveProviderProfile() == service.ProviderProfileCindyLaxaV1 {
		group.StrictCindyKnown = true
		group.StrictCindy = s.strictCindy
	}
	return group, nil
}

func TestAccountJobDataImportHydratesExplicitCindyTargetOnce(t *testing.T) {
	groupID := int64(12)
	listedGroup := strictCindyImportGroup(groupID)
	listedGroup.StrictCindyKnown = false
	listedGroup.StrictCindy = false

	adminService := newDataV2AdminService()
	adminService.groups = []service.Group{listedGroup}
	adminService.accounts = []service.Account{*canonicalCindyJobAccount(42)}
	adminService.accounts[0].GroupIDs = []int64{groupID}
	adminService.accounts[0].Credentials["api_key"] = "existing-key"
	handler := NewAccountHandler(&hydratingDataV2AdminService{dataV2AdminService: adminService, strictCindy: true}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = &recordingCindyJobMutationRunner{}

	request := cindyImportRequest(&groupID,
		cindyImportAccount("new-one", service.PlatformOpenAI, "new-key-one", ""),
		cindyImportAccount("new-two", service.PlatformOpenAI, "new-key-two", ""),
	)
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 905, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()

	for ordinal := 1; ordinal <= len(request.Data.Accounts); ordinal++ {
		results, executeErr := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: int64(ordinal), Ordinal: ordinal}})
		require.NoError(t, executeErr)
		require.Len(t, results, 1)
		require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
	}
	require.Equal(t, 1, adminService.groupListCalls)
	require.Equal(t, 1, adminService.getGroupCalls, "the explicit target must be hydrated once per import job")
	require.Len(t, adminService.createdAccounts, 2)
	for _, created := range adminService.createdAccounts {
		require.Equal(t, []int64{groupID}, created.GroupIDs)
	}
}

func TestAccountJobDataImportPreparedMixedCindyTargetStillRejected(t *testing.T) {
	groupID := int64(12)
	mixedGroup := strictCindyImportGroup(groupID)
	mixedGroup.StrictCindyKnown = false
	mixedGroup.StrictCindy = false

	adminService := newDataV2AdminService()
	adminService.groups = []service.Group{mixedGroup}
	adminService.accounts = []service.Account{{
		ID: 42, Name: "ordinary", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "ordinary-key", "base_url": "https://api.openai.com"},
		GroupIDs:    []int64{groupID}, Status: service.StatusActive, Schedulable: true,
	}}
	handler := NewAccountHandler(&hydratingDataV2AdminService{dataV2AdminService: adminService, strictCindy: false}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	request := cindyImportRequest(&groupID, cindyImportAccount("incoming", service.PlatformOpenAI, "new-key", ""))
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 906, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()

	results, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 1, Ordinal: 1}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, service.AccountJobItemStatusFailed, results[0].Status)
	require.Equal(t, dataImportCodeCindyTargetInvalid, results[0].ErrorCode)
	require.Empty(t, adminService.createdAccounts)
}

func TestAccountJobDataImportMissingTargetIsRejectedPerItem(t *testing.T) {
	groupID := int64(12)
	adminService := newDataV2AdminService()
	adminService.groups = nil
	hydratingService := &hydratingDataV2AdminService{dataV2AdminService: adminService, strictCindy: true}
	handler := NewAccountHandler(hydratingService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	request := cindyImportRequest(&groupID, cindyImportAccount("incoming", service.PlatformOpenAI, "new-key", ""))
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 907, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()

	results, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 1, Ordinal: 1}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, service.AccountJobItemStatusFailed, results[0].Status)
	require.Equal(t, dataImportCodeCindyTargetInvalid, results[0].ErrorCode)
	require.Empty(t, adminService.createdAccounts)
}

func TestAccountJobDataImportPreparationPropagatesTargetLookupFailure(t *testing.T) {
	groupID := int64(12)
	adminService := newDataV2AdminService()
	adminService.groups = []service.Group{strictCindyImportGroup(groupID)}
	adminService.getGroupErr = errors.New("target lookup unavailable")
	handler := NewAccountHandler(&hydratingDataV2AdminService{dataV2AdminService: adminService, strictCindy: true}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	request := cindyImportRequest(&groupID, cindyImportAccount("incoming", service.PlatformOpenAI, "new-key", ""))
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 908, Kind: service.AccountJobKindImportData}
	_, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.Nil(t, cleanup)
	require.EqualError(t, err, "target lookup unavailable")
}

func TestAccountJobDataImportPreparedEmptyCindyTargetBootstraps(t *testing.T) {
	groupID := int64(12)
	listedGroup := strictCindyImportGroup(groupID)
	listedGroup.StrictCindyKnown = false
	listedGroup.StrictCindy = false
	adminService := newDataV2AdminService()
	adminService.groups = []service.Group{listedGroup}
	hydratingService := &hydratingDataV2AdminService{dataV2AdminService: adminService, strictCindy: false}
	handler := NewAccountHandler(hydratingService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = &recordingCindyJobMutationRunner{}

	request := cindyImportRequest(&groupID, cindyImportAccount("bootstrap", service.PlatformOpenAI, "new-key", ""))
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 909, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()

	results, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 1, Ordinal: 1}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
	require.Len(t, adminService.createdAccounts, 1)
	require.Equal(t, []int64{groupID}, adminService.createdAccounts[0].GroupIDs)
}

func TestAccountJobDataImportUpdatesMutableIdentityIndexAfterEachCommit(t *testing.T) {
	adminService := newDataV2AdminService()
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	account := func(name string) DataAccount {
		return DataAccount{
			Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key": "same-key", "base_url": "https://provider.example/v1",
				"account_id": "same-account", "user_id": "same-user",
			},
			Concurrency: 1,
		}
	}
	request := DataImportRequest{Data: DataPayload{
		Type: dataType, Version: dataVersion, Proxies: []DataProxy{},
		Accounts: []DataAccount{account("first"), account("second")},
	}}
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 902, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()

	first, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 1, Ordinal: 1}})
	require.NoError(t, err)
	require.Equal(t, service.AccountJobItemStatusSucceeded, first[0].Status)
	second, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 2, Ordinal: 2}})
	require.NoError(t, err)
	require.Equal(t, service.AccountJobItemStatusSucceeded, second[0].Status)

	require.Len(t, adminService.createdAccounts, 1)
	require.Len(t, adminService.updateInputs, 1)
}

func TestAccountJobDataImportPreparedUnifiedProxyStrategies(t *testing.T) {
	proxyKey := buildProxyKey("http", "proxy.example", 8080, "", "")
	fileProxy := DataProxy{ProxyKey: proxyKey, Name: "file proxy", Protocol: "http", Host: "proxy.example", Port: 8080}
	newAccount := func(name string) DataAccount {
		return DataAccount{
			Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key": name + "-key", "base_url": "https://provider.example/v1",
				"account_id": name + "-account", "user_id": name + "-user",
			},
			Concurrency: 1,
		}
	}
	run := func(t *testing.T, svc *dataV2AdminService, request DataImportRequest) service.AccountJobExecutionResult {
		t.Helper()
		handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		raw, err := json.Marshal(request)
		require.NoError(t, err)
		job := &service.AccountJob{ID: 903, Kind: service.AccountJobKindImportData}
		preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
		require.NoError(t, err)
		defer cleanup()
		results, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 1, Ordinal: 1}})
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	t.Run("preserve file proxy", func(t *testing.T) {
		svc := newDataV2AdminService()
		account := newAccount("preserve")
		account.ProxyKey = &proxyKey
		result := run(t, svc, DataImportRequest{Data: DataPayload{
			Type: dataType, Version: dataVersion, Proxies: []DataProxy{fileProxy}, Accounts: []DataAccount{account},
		}})
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
		require.NotNil(t, svc.createdAccounts[0].ProxyID)
		require.Equal(t, int64(400), *svc.createdAccounts[0].ProxyID)
	})

	t.Run("uniform direct", func(t *testing.T) {
		svc := newDataV2AdminService()
		direct := int64(0)
		account := newAccount("direct")
		account.ProxyKey = &proxyKey
		result := run(t, svc, DataImportRequest{
			Data:            DataPayload{Type: dataType, Version: dataVersion, Proxies: []DataProxy{fileProxy}, Accounts: []DataAccount{account}},
			UniformSettings: DataImportUniformSettings{ProxyID: &direct},
		})
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
		require.Nil(t, svc.createdAccounts[0].ProxyID)
	})

	t.Run("uniform existing proxy", func(t *testing.T) {
		svc := newDataV2AdminService()
		svc.proxies = []service.Proxy{{ID: 7, Name: "managed", Protocol: "http", Host: "managed.example", Port: 8080}}
		proxyID := int64(7)
		result := run(t, svc, DataImportRequest{
			Data:            DataPayload{Type: dataType, Version: dataVersion, Proxies: []DataProxy{}, Accounts: []DataAccount{newAccount("existing")}},
			UniformSettings: DataImportUniformSettings{ProxyID: &proxyID},
		})
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
		require.NotNil(t, svc.createdAccounts[0].ProxyID)
		require.Equal(t, proxyID, *svc.createdAccounts[0].ProxyID)
	})
}

func TestAccountJobDataImportFailsWhenPreparedProxyIsDeletedBeforeCommit(t *testing.T) {
	svc := newDataV2AdminService()
	svc.enforceProxyReferences = true
	svc.proxies = []service.Proxy{{ID: 9, Name: "managed", Protocol: "http", Host: "managed.example", Port: 8080}}
	proxyID := int64(9)
	request := DataImportRequest{
		Data: DataPayload{Type: dataType, Version: dataVersion, Proxies: []DataProxy{}, Accounts: []DataAccount{{
			Name: "deleted proxy", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key": "deleted-proxy-key", "base_url": "https://provider.example/v1",
				"account_id": "deleted-proxy-account", "user_id": "deleted-proxy-user",
			},
			Concurrency: 1,
		}}},
		UniformSettings: DataImportUniformSettings{ProxyID: &proxyID},
	}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	job := &service.AccountJob{ID: 904, Kind: service.AccountJobKindImportData}
	preparedCtx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	defer cleanup()
	svc.proxies = nil

	results, err := handler.ExecuteAccountJob(preparedCtx, job, raw, []service.AccountJobItem{{ID: 1, Ordinal: 1}})
	require.NoError(t, err)
	require.Equal(t, service.AccountJobItemStatusFailed, results[0].Status)
	require.Empty(t, svc.createdAccounts)
}

type recordingCindyJobMutationRunner struct {
	accountIDs []int64
}

func (r *recordingCindyJobMutationRunner) Run(
	ctx context.Context,
	accountID int64,
	mutate func(context.Context) (*service.Account, error),
) (*service.Account, error) {
	r.accountIDs = append(r.accountIDs, accountID)
	return mutate(ctx)
}

func canonicalCindyJobAccount(id int64) *service.Account {
	return &service.Account{
		ID: id, Name: "Cindy", Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.laxarouter.ai",
			"api_key":  "test-key",
		},
		Status: service.StatusActive,
	}
}

func cindyJobCredentials() map[string]any {
	return map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "test-key"}
}

func executeAccountJobTestItem(
	t *testing.T,
	handler *AccountHandler,
	kind string,
	payload any,
	item service.AccountJobItem,
) service.AccountJobExecutionResult {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return handler.executeAccountJobItem(context.Background(), kind, raw, item)
}

func TestAccountJobBatchCreateUsesCindyMutationRunner(t *testing.T) {
	adminService := newStubAdminService()
	runner := &recordingCindyJobMutationRunner{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = runner

	result := executeAccountJobTestItem(t, handler, service.AccountJobKindBatchCreate, batchCreateJobPayload{
		Accounts: []CreateAccountRequest{{
			Name: "Cindy", Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey,
			Credentials: cindyJobCredentials(),
		}},
	}, service.AccountJobItem{ID: 1, Ordinal: 1})

	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	require.Equal(t, []int64{0}, runner.accountIDs)
}

func TestAccountJobBatchCredentialUpdateUsesCindyMutationRunner(t *testing.T) {
	adminService := newStubAdminService()
	adminService.getAccountResult = canonicalCindyJobAccount(42)
	runner := &recordingCindyJobMutationRunner{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = runner
	target := int64(42)

	result := executeAccountJobTestItem(t, handler, service.AccountJobKindBatchUpdateCredentials,
		BatchUpdateCredentialsRequest{AccountIDs: []int64{42}, Field: "account_uuid", Value: "new"},
		service.AccountJobItem{ID: 2, Ordinal: 1, TargetAccountID: &target})

	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	require.Equal(t, []int64{42}, runner.accountIDs)
}

func TestAccountJobBulkUpdateUsesCindyMutationRunner(t *testing.T) {
	adminService := newStubAdminService()
	adminService.getAccountResult = canonicalCindyJobAccount(43)
	runner := &recordingCindyJobMutationRunner{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = runner
	target := int64(43)
	priority := 9

	result := executeAccountJobTestItem(t, handler, service.AccountJobKindBulkUpdate,
		BulkUpdateAccountsRequest{AccountIDs: []int64{43}, Priority: &priority},
		service.AccountJobItem{ID: 3, Ordinal: 1, TargetAccountID: &target})

	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	require.Equal(t, []int64{43}, runner.accountIDs)
}

func TestAccountJobDataImportUsesCindyMutationRunnerWithCanonicalDecision(t *testing.T) {
	adminService := newDataV2AdminService()
	groupID := int64(81)
	adminService.groups = []service.Group{strictCindyImportGroup(groupID)}
	runner := &recordingCindyJobMutationRunner{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = runner

	result := executeAccountJobTestItem(t, handler, service.AccountJobKindImportData, DataImportRequest{
		Data: DataPayload{Type: dataType, Version: dataVersion, Proxies: []DataProxy{}, Accounts: []DataAccount{{
			Name: "Cindy", Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey,
			Credentials: cindyJobCredentials(), Concurrency: 1, Priority: 1,
		}}},
		TargetGroupID: &groupID,
	}, service.AccountJobItem{ID: 4, Ordinal: 1})

	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	require.Equal(t, []int64{0}, runner.accountIDs)
	require.Len(t, adminService.createdAccounts, 1)
	require.Equal(t, []int64{groupID}, adminService.createdAccounts[0].GroupIDs)
}

var _ service.AccountJobCindyMutationRunner = (*recordingCindyJobMutationRunner)(nil)
