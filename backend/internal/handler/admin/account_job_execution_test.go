package admin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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

func TestAccountJobDataImportUsesCindyMutationRunnerWithoutPreviewDecision(t *testing.T) {
	adminService := newDataV2AdminService()
	runner := &recordingCindyJobMutationRunner{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.cindyJobMutations = runner

	result := executeAccountJobTestItem(t, handler, service.AccountJobKindImportData, DataImportRequest{
		Data: DataPayload{Type: dataType, Version: dataVersion, Proxies: []DataProxy{}, Accounts: []DataAccount{{
			Name: "Cindy", Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey,
			Credentials: cindyJobCredentials(), Concurrency: 1, Priority: 1,
		}}},
	}, service.AccountJobItem{ID: 4, Ordinal: 1})

	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	require.Equal(t, []int64{0}, runner.accountIDs)
}

var _ service.AccountJobCindyMutationRunner = (*recordingCindyJobMutationRunner)(nil)
