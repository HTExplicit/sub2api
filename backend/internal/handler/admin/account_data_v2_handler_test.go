package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dataV2AdminService struct {
	*stubAdminService
	nextAccountID          int64
	nextFolderID           int64
	nextTagID              int64
	folders                []service.AccountManagementFolder
	tags                   []service.AccountManagementTag
	updateInputs           map[int64]*service.UpdateAccountInput
	failNames              map[string]error
	failTaxonomy           error
	getGroupErr            error
	getGroupCalls          int
	enforceProxyReferences bool
	taxonomyCalls          int
	listCalls              int
	groupListCalls         int
	folderListCalls        int
	tagListCalls           int
	consoleFilters         service.AccountConsoleFilters
}

func newDataV2AdminService() *dataV2AdminService {
	stub := newStubAdminService()
	stub.accounts = nil
	stub.proxies = nil
	return &dataV2AdminService{
		stubAdminService: stub,
		nextAccountID:    100,
		nextFolderID:     200,
		nextTagID:        300,
		updateInputs:     make(map[int64]*service.UpdateAccountInput),
		failNames:        make(map[string]error),
	}
}

func (s *dataV2AdminService) ListAccounts(_ context.Context, page, pageSize int, _, _, _, _ string, _ int64, _, _, _ string) ([]service.Account, int64, error) {
	s.listCalls++
	total := len(s.accounts)
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = total
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []service.Account{}, int64(total), nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return append([]service.Account(nil), s.accounts[start:end]...), int64(total), nil
}

func (s *dataV2AdminService) GetGroup(_ context.Context, id int64) (*service.Group, error) {
	s.getGroupCalls++
	if s.getGroupErr != nil {
		return nil, s.getGroupErr
	}
	for index := range s.groups {
		if s.groups[index].ID == id {
			group := s.groups[index]
			return &group, nil
		}
	}
	return nil, service.ErrGroupNotFound
}

func (s *dataV2AdminService) GetAllGroupsIncludingInactive(_ context.Context) ([]service.Group, error) {
	s.groupListCalls++
	return append([]service.Group(nil), s.groups...), nil
}

func (s *dataV2AdminService) CreateAccount(_ context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	if err := s.failNames[input.Name]; err != nil {
		return nil, err
	}
	if s.enforceProxyReferences && input.ProxyID != nil && *input.ProxyID > 0 {
		found := false
		for _, proxy := range s.proxies {
			if proxy.ID == *input.ProxyID {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("proxy reference is unavailable")
		}
	}
	s.createdAccounts = append(s.createdAccounts, input)
	s.nextAccountID++
	account := service.Account{
		ID: s.nextAccountID, Name: input.Name, Notes: input.Notes, Platform: input.Platform, Type: input.Type,
		Credentials: mergeDataImportMaps(nil, input.Credentials), Extra: mergeDataImportMaps(nil, input.Extra),
		ProxyID: input.ProxyID, Concurrency: input.Concurrency, Priority: input.Priority,
		RateMultiplier: input.RateMultiplier, Status: service.StatusActive, Schedulable: true,
		GroupIDs: append([]int64(nil), input.GroupIDs...),
	}
	s.accounts = append(s.accounts, account)
	copy := account
	return &copy, nil
}

func (s *dataV2AdminService) UpdateAccount(_ context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updateInputs[id] = input
	for index := range s.accounts {
		if s.accounts[index].ID != id {
			continue
		}
		account := &s.accounts[index]
		if input.Name != "" {
			account.Name = input.Name
		}
		if input.Notes != nil {
			account.Notes = input.Notes
		}
		if input.Type != "" {
			account.Type = input.Type
		}
		if len(input.Credentials) > 0 {
			account.Credentials = mergeDataImportMaps(account.Credentials, input.Credentials)
		}
		if input.Extra != nil {
			account.Extra = mergeDataImportMaps(nil, input.Extra)
		}
		if input.ProxyID != nil {
			if *input.ProxyID == 0 {
				account.ProxyID = nil
			} else {
				value := *input.ProxyID
				account.ProxyID = &value
			}
		}
		if input.Concurrency != nil {
			account.Concurrency = *input.Concurrency
		}
		if input.Priority != nil {
			account.Priority = *input.Priority
		}
		if input.RateMultiplier != nil {
			account.RateMultiplier = input.RateMultiplier
		}
		if input.Status != "" {
			account.Status = input.Status
		}
		if input.GroupIDs != nil {
			account.GroupIDs = append([]int64(nil), (*input.GroupIDs)...)
		}
		copy := *account
		return &copy, nil
	}
	return nil, errors.New("account not found")
}

func (s *dataV2AdminService) SetAccountSchedulable(_ context.Context, id int64, schedulable bool) (*service.Account, error) {
	for index := range s.accounts {
		if s.accounts[index].ID == id {
			s.accounts[index].Schedulable = schedulable
			copy := s.accounts[index]
			return &copy, nil
		}
	}
	return nil, errors.New("account not found")
}

func (s *dataV2AdminService) ListAccountFolders(context.Context) ([]service.AccountManagementFolder, error) {
	s.folderListCalls++
	return append([]service.AccountManagementFolder(nil), s.folders...), nil
}

func (s *dataV2AdminService) CreateAccountFolder(_ context.Context, input service.AccountTaxonomyInput) (*service.AccountManagementFolder, error) {
	for _, folder := range s.folders {
		if strings.EqualFold(strings.TrimSpace(folder.Name), strings.TrimSpace(input.Name)) {
			copy := folder
			return &copy, nil
		}
	}
	s.nextFolderID++
	folder := service.AccountManagementFolder{ID: s.nextFolderID, Name: strings.TrimSpace(input.Name), SortOrder: input.SortOrder}
	s.folders = append(s.folders, folder)
	return &folder, nil
}

func (s *dataV2AdminService) UpdateAccountFolder(_ context.Context, id int64, input service.AccountTaxonomyInput) (*service.AccountManagementFolder, error) {
	return &service.AccountManagementFolder{ID: id, Name: input.Name, SortOrder: input.SortOrder}, nil
}

func (s *dataV2AdminService) DeleteAccountFolder(_ context.Context, id int64, _ bool) error {
	for index, folder := range s.folders {
		if folder.ID == id {
			s.folders = append(s.folders[:index], s.folders[index+1:]...)
			return nil
		}
	}
	return nil
}

func (s *dataV2AdminService) ListAccountTags(context.Context) ([]service.AccountManagementTag, error) {
	s.tagListCalls++
	return append([]service.AccountManagementTag(nil), s.tags...), nil
}

func (s *dataV2AdminService) CreateAccountTag(_ context.Context, input service.AccountTaxonomyInput) (*service.AccountManagementTag, error) {
	for _, tag := range s.tags {
		if strings.EqualFold(strings.TrimSpace(tag.Name), strings.TrimSpace(input.Name)) {
			copy := tag
			return &copy, nil
		}
	}
	s.nextTagID++
	tag := service.AccountManagementTag{ID: s.nextTagID, Name: strings.TrimSpace(input.Name), SortOrder: input.SortOrder}
	s.tags = append(s.tags, tag)
	return &tag, nil
}

func (s *dataV2AdminService) UpdateAccountTag(_ context.Context, id int64, input service.AccountTaxonomyInput) (*service.AccountManagementTag, error) {
	return &service.AccountManagementTag{ID: id, Name: input.Name, SortOrder: input.SortOrder}, nil
}

func (s *dataV2AdminService) DeleteAccountTag(_ context.Context, id int64) error {
	for index, tag := range s.tags {
		if tag.ID == id {
			s.tags = append(s.tags[:index], s.tags[index+1:]...)
			return nil
		}
	}
	return nil
}

func (s *dataV2AdminService) SetAccountTaxonomy(_ context.Context, accountID int64, assignment service.AccountTaxonomyAssignment) (*service.Account, error) {
	s.taxonomyCalls++
	if s.failTaxonomy != nil {
		return nil, s.failTaxonomy
	}
	for index := range s.accounts {
		if s.accounts[index].ID != accountID {
			continue
		}
		account := &s.accounts[index]
		account.ManagementFolder = nil
		if assignment.FolderID != nil {
			for _, folder := range s.folders {
				if folder.ID == *assignment.FolderID {
					copy := folder
					account.ManagementFolder = &copy
					break
				}
			}
		}
		account.Tags = nil
		for _, tagID := range assignment.TagIDs {
			for _, tag := range s.tags {
				if tag.ID == tagID {
					account.Tags = append(account.Tags, tag)
				}
			}
		}
		copy := *account
		return &copy, nil
	}
	return nil, errors.New("account not found")
}

func (s *dataV2AdminService) ListAccountsConsole(ctx context.Context, page, pageSize int, filters service.AccountConsoleFilters) ([]service.Account, int64, error) {
	s.consoleFilters = filters
	return s.ListAccounts(ctx, page, pageSize, "", "", "", "", 0, "", "", "")
}

func (s *dataV2AdminService) GetAccountConsoleFacets(context.Context, service.AccountConsoleFilters) (*service.AccountConsoleFacets, error) {
	return &service.AccountConsoleFacets{}, nil
}

func setupAccountDataV2Router(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts/data", handler.ExportData)
	return router
}

func TestExportDataUsesCockpitConsoleFilters(t *testing.T) {
	svc := newDataV2AdminService()
	svc.accounts = []service.Account{{
		ID: 1, Name: "Exported", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "secret"}, Status: service.StatusActive,
	}}
	router := setupAccountDataV2Router(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/accounts/data?platforms=openai,grok&statuses=active,error&plans=team&proxies=direct,5&folder=uncategorized&tags=3,4&account_ids=1&group_id=12&search=Exported&sort_by=priority&sort_order=desc&include_proxies=false",
		nil,
	)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, []string{"openai", "grok"}, svc.consoleFilters.Platforms)
	require.Equal(t, []string{"active", "error"}, svc.consoleFilters.Statuses)
	require.Equal(t, []string{"team"}, svc.consoleFilters.Plans)
	require.Equal(t, []int64{5}, svc.consoleFilters.ProxyIDs)
	require.True(t, svc.consoleFilters.IncludeDirect)
	require.True(t, svc.consoleFilters.IncludeUncategorized)
	require.Equal(t, []int64{3, 4}, svc.consoleFilters.TagIDs)
	require.Equal(t, []int64{1}, svc.consoleFilters.AccountIDs)
	require.Equal(t, int64(12), svc.consoleFilters.GroupID)
	require.Equal(t, "Exported", svc.consoleFilters.Search)
	require.Equal(t, "priority", svc.consoleFilters.SortBy)
	require.Equal(t, "desc", svc.consoleFilters.SortOrder)
}

func executeDataImportPayload(t *testing.T, svc service.AdminService, payload any) DataImportResult {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var req DataImportRequest
	require.NoError(t, json.Unmarshal(raw, &req))
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	result, err := handler.importData(context.Background(), req)
	require.NoError(t, err)
	return result
}

func testDataAccount(name, accountID, userID, email string) map[string]any {
	credentials := map[string]any{"access_token": "new-token", "email": email}
	if accountID != "" {
		credentials["chatgpt_account_id"] = accountID
	}
	if userID != "" {
		credentials["chatgpt_user_id"] = userID
	}
	return map[string]any{
		"name": name, "platform": service.PlatformOpenAI, "type": service.AccountTypeOAuth,
		"credentials": credentials, "extra": map[string]any{"imported": true},
		"concurrency": 2, "priority": 10,
	}
}

func TestDataAccountIdentityKeysTreatsClientEmailAsWarningOnly(t *testing.T) {
	keys := dataAccountIdentityKeys("vertex", map[string]any{
		"client_email": "service@example.com",
	}, nil)
	require.Empty(t, keys)
}

func TestDataAccountIdentityKeysUseCindyCredentialFingerprint(t *testing.T) {
	credentials := map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": " key "}
	keys := dataAccountIdentityKeys(service.PlatformCindy, credentials, nil)
	require.Len(t, keys, 1)
	require.Equal(t, "credential_fingerprint", keys[0].Label)
	trimmed := dataAccountIdentityKeys(service.PlatformCindy, map[string]any{
		"base_url": "https://api.laxarouter.ai", "api_key": "key",
	}, nil)
	require.Len(t, trimmed, 1)
	require.NotEqual(t, keys[0].Value, trimmed[0].Value)
}

func TestDataIdentityIndexUpdateRemovesStaleKeys(t *testing.T) {
	account := service.Account{
		ID: 17, Name: "before", Platform: service.PlatformOpenAI,
		Credentials: map[string]any{"account_id": "old-account", "user_id": "old-user"},
	}
	index := buildDataIdentityIndex([]service.Account{account})
	require.Len(t, index.Find(dataAccountIdentityKeys(account.Platform, account.Credentials, nil)), 1)

	account.Name = "after"
	account.Credentials = map[string]any{"account_id": "new-account", "user_id": "new-user"}
	index.Add(account)

	require.Empty(t, index.Find(dataAccountIdentityKeys(service.PlatformOpenAI, map[string]any{
		"account_id": "old-account", "user_id": "old-user",
	}, nil)))
	matches := index.Find(dataAccountIdentityKeys(account.Platform, account.Credentials, nil))
	require.Len(t, matches, 1)
	require.Equal(t, int64(17), matches[0].AccountID)
}

func TestImportDataAcceptsV1AndUpdatesStrongIdentityAtExecutionTime(t *testing.T) {
	svc := newDataV2AdminService()
	svc.accounts = []service.Account{{
		ID: 8, Name: "Existing", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-1", "chatgpt_user_id": "user-1"},
		Status:      service.StatusActive, Schedulable: true,
	}}
	payload := map[string]any{"data": map[string]any{
		"type": dataType, "version": dataVersionV1, "proxies": []any{},
		"accounts": []any{testDataAccount("Imported", "workspace-1", "user-1", "new@example.com")},
	}}
	result := executeDataImportPayload(t, svc, payload)
	require.Zero(t, result.AccountSkipped)
	require.Zero(t, result.AccountCreated)
	require.Equal(t, 1, result.AccountUpdated)
	require.Empty(t, svc.createdAccounts)
	require.Contains(t, svc.updateInputs, int64(8))
}

func TestImportDataExecutionTimeDecisionAndUniformSettings(t *testing.T) {
	svc := newDataV2AdminService()
	svc.accounts = []service.Account{{
		ID: 9, Name: "Keep Name", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-1", "chatgpt_user_id": "user-1", "refresh_token": "old"},
		Extra:       map[string]any{"preserved": true}, Concurrency: 9, Priority: 90,
		Status: service.StatusActive, Schedulable: true,
	}}
	payload := map[string]any{
		"data": map[string]any{
			"type": dataType, "version": dataVersion, "proxies": []any{},
			"accounts": []any{
				testDataAccount("Update Source", "workspace-1", "user-1", "two@example.com"),
				testDataAccount("Create Source", "workspace-2", "user-2", "three@example.com"),
			},
		},
		"uniform_settings": map[string]any{"name_prefix": "U-", "concurrency": 5, "priority": 50, "status": "disabled"},
	}
	result := executeDataImportPayload(t, svc, payload)
	require.Zero(t, result.AccountSkipped)
	require.Equal(t, 1, result.AccountUpdated)
	require.Equal(t, 1, result.AccountCreated)
	require.Len(t, result.AccountIDs, 2)

	update := svc.updateInputs[9]
	require.NotNil(t, update)
	require.Equal(t, "U-Update Source", update.Name)
	require.NotNil(t, update.Concurrency)
	require.Equal(t, 5, *update.Concurrency)
	require.NotNil(t, update.Priority)
	require.Equal(t, 50, *update.Priority)
	require.Equal(t, service.StatusDisabled, update.Status)
	require.Equal(t, true, update.Extra["preserved"])
	require.Equal(t, true, update.Extra["imported"])
	require.Equal(t, "new-token", update.Credentials["access_token"])

	require.Len(t, svc.createdAccounts, 1)
	created := svc.createdAccounts[0]
	require.Equal(t, "U-Create Source", created.Name)
	require.Equal(t, 5, created.Concurrency)
	require.Equal(t, 50, created.Priority)
}

func TestImportDataUpdatePreservesUnspecifiedOperationalSettings(t *testing.T) {
	proxyID := int64(77)
	svc := newDataV2AdminService()
	svc.accounts = []service.Account{{
		ID: 10, Name: "Existing", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-2", "chatgpt_user_id": "user-2"},
		Extra:       map[string]any{"preserved": "yes"}, ProxyID: &proxyID, Concurrency: 12, Priority: 80,
		RateMultiplier: func() *float64 { value := 1.5; return &value }(), GroupIDs: []int64{2},
		Status: service.StatusActive, Schedulable: false,
	}}
	payload := map[string]any{
		"data": map[string]any{"type": dataType, "version": dataVersion, "proxies": []any{}, "accounts": []any{testDataAccount("JSON Name", "workspace-2", "user-2", "new@example.com")}},
	}
	result := executeDataImportPayload(t, svc, payload)
	require.Equal(t, 1, result.AccountUpdated)
	update := svc.updateInputs[10]
	require.NotNil(t, update)
	require.Empty(t, update.Name)
	require.Nil(t, update.ProxyID)
	require.Nil(t, update.Concurrency)
	require.Nil(t, update.Priority)
	require.Nil(t, update.RateMultiplier)
	require.Nil(t, update.GroupIDs)
	require.Empty(t, update.Status)
	require.Equal(t, "yes", update.Extra["preserved"])
	require.Equal(t, true, update.Extra["imported"])
}

func TestImportDataCreatesTaxonomyOnlyOnCommitAndReusesCaseInsensitiveNames(t *testing.T) {
	svc := newDataV2AdminService()
	first := testDataAccount("First", "workspace-a", "user-a", "a@example.com")
	first["management_folder"] = " Imported "
	first["tags"] = []string{"Blue"}
	second := testDataAccount("Second", "workspace-b", "user-b", "b@example.com")
	second["management_folder"] = "imported"
	second["tags"] = []string{"blue"}
	payload := map[string]any{"data": map[string]any{
		"type": dataType, "version": dataVersion, "proxies": []any{}, "accounts": []any{first, second},
	}}
	result := executeDataImportPayload(t, svc, payload)
	require.Equal(t, 2, result.AccountCreated)
	require.Len(t, svc.folders, 1)
	require.Len(t, svc.tags, 1)
	require.Equal(t, 2, svc.taxonomyCalls)
}

func TestImportDataCreateTreatsZeroProxyOverrideAsDirect(t *testing.T) {
	svc := newDataV2AdminService()
	payload := map[string]any{
		"data": map[string]any{
			"type": dataType, "version": dataVersion, "proxies": []any{},
			"accounts": []any{testDataAccount("Direct", "workspace-direct", "user-direct", "direct@example.com")},
		},
		"uniform_settings": map[string]any{"proxy_id": 0},
	}
	result := executeDataImportPayload(t, svc, payload)
	require.Equal(t, 1, result.AccountCreated)
	require.Len(t, svc.createdAccounts, 1)
	require.Nil(t, svc.createdAccounts[0].ProxyID)
}

func TestImportDataRemovesNewTaxonomyWhenAssignmentFails(t *testing.T) {
	svc := newDataV2AdminService()
	svc.failTaxonomy = errors.New("synthetic taxonomy failure")
	account := testDataAccount("Created", "workspace-taxonomy", "user-taxonomy", "taxonomy@example.com")
	account["management_folder"] = "New Folder"
	account["tags"] = []string{"New Tag"}
	payload := map[string]any{"data": map[string]any{
		"type": dataType, "version": dataVersion, "proxies": []any{}, "accounts": []any{account},
	}}
	result := executeDataImportPayload(t, svc, payload)
	require.Equal(t, 1, result.AccountCreated)
	require.Len(t, result.Items, 1)
	require.Contains(t, result.Items[0].Warnings, "account was created but taxonomy could not be applied: synthetic taxonomy failure")
	require.Empty(t, svc.folders)
	require.Empty(t, svc.tags)
}

func TestImportDataAllowsPartialSuccessWithPerItemResults(t *testing.T) {
	svc := newDataV2AdminService()
	svc.failNames["Broken"] = errors.New("synthetic create failure")
	payload := map[string]any{"data": map[string]any{
		"type": dataType, "version": dataVersion, "proxies": []any{},
		"accounts": []any{
			testDataAccount("Working", "workspace-ok", "user-ok", "ok@example.com"),
			testDataAccount("Broken", "workspace-bad", "user-bad", "bad@example.com"),
		},
	}}
	result := executeDataImportPayload(t, svc, payload)
	require.Equal(t, 1, result.AccountCreated)
	require.Equal(t, 1, result.AccountFailed)
	require.Len(t, result.AccountIDs, 1)
	require.Len(t, result.Items, 2)
	require.Equal(t, "failed", result.Items[1].Action)
	require.Contains(t, result.Items[1].Error, "synthetic create failure")
}
