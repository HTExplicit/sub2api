package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIReasoningPolicyAdminBoundariesRejectMalformedValuesBeforeMutation(t *testing.T) {
	for _, key := range []string{service.OpenAIChatReasoningReplayEnabledExtraKey, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
		malformedExtra := `"extra":{"` + key + `":"rejected-sensitive-marker"}`
		tests := []struct {
			name, method, path, body string
			mount                    func(*gin.Engine, *AccountHandler)
		}{
			{
				name: "create", method: http.MethodPost, path: "/accounts",
				body:  `{"name":"account","platform":"openai","type":"apikey","credentials":{},` + malformedExtra + `}`,
				mount: func(r *gin.Engine, h *AccountHandler) { r.POST("/accounts", h.Create) },
			},
			{
				name: "update", method: http.MethodPut, path: "/accounts/1", body: `{` + malformedExtra + `}`,
				mount: func(r *gin.Engine, h *AccountHandler) { r.PUT("/accounts/:id", h.Update) },
			},
			{
				name: "bulk", method: http.MethodPost, path: "/accounts/bulk", body: `{"account_ids":[1],` + malformedExtra + `}`,
				mount: func(r *gin.Engine, h *AccountHandler) { r.POST("/accounts/bulk", h.BulkUpdate) },
			},
			{
				name: "batch create", method: http.MethodPost, path: "/accounts/batch",
				body:  `{"accounts":[{"name":"account","platform":"openai","type":"apikey","credentials":{},` + malformedExtra + `}]}`,
				mount: func(r *gin.Engine, h *AccountHandler) { r.POST("/accounts/batch", h.BatchCreate) },
			},
			{
				name: "Codex import", method: http.MethodPost, path: "/accounts/import", body: `{"content":"unused-test-token",` + malformedExtra + `}`,
				mount: func(r *gin.Engine, h *AccountHandler) { r.POST("/accounts/import", h.ImportCodexSession) },
			},
			{
				name: "OAuth apply", method: http.MethodPost, path: "/accounts/1/apply",
				body:  `{"type":"oauth","credentials":{"access_token":"unused-test-token"},` + malformedExtra + `}`,
				mount: func(r *gin.Engine, h *AccountHandler) { r.POST("/accounts/:id/apply", h.ApplyOAuthCredentials) },
			},
		}
		for _, tt := range tests {
			t.Run(key+"/"+tt.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				stub := newStubAdminService()
				stub.getAccountResult = &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
				handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				router := gin.New()
				tt.mount(router, handler)
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				var body struct {
					Reason string `json:"reason"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
				require.Equal(t, "OPENAI_REASONING_POLICY_INVALID", body.Reason)
				require.NotContains(t, recorder.Body.String(), "rejected-sensitive-marker")
				require.Empty(t, stub.createdAccounts)
				require.Zero(t, stub.updateAccountCalls)
				require.Zero(t, stub.updateAccountExtraCalls)
				require.Nil(t, stub.lastBulkUpdateAccountInput)
			})
		}
	}
}

func TestOpenAIReasoningPolicyCodexPATRejectsMalformedValuesBeforeTokenExchange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewOpenAIOAuthHandler(nil, newStubAdminService(), nil, nil)
	router := gin.New()
	router.POST("/pat", handler.CreateAccountFromCodexPAT)
	for _, key := range []string{service.OpenAIChatReasoningReplayEnabledExtraKey, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/pat", bytes.NewBufferString(`{"access_token":"unused-test-token","extra":{"`+key+`":null}}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "OPENAI_REASONING_POLICY_INVALID")
	}
}

func TestOpenAIReasoningPolicyImportMergeDistinguishesStoredValuesFromNewInput(t *testing.T) {
	existing := map[string]any{
		service.OpenAIChatReasoningReplayEnabledExtraKey:        "legacy-invalid",
		service.OpenAIReasoningSignatureRecoveryEnabledExtraKey: false,
		"base_rpm": 20,
	}
	original := maps.Clone(existing)
	for _, incoming := range []map[string]any{
		{"import_source": "codex_session"},
		{service.OpenAIChatReasoningReplayEnabledExtraKey: true},
		{service.OpenAIReasoningSignatureRecoveryEnabledExtraKey: false},
	} {
		inputCopy := maps.Clone(incoming)
		merged := mergeAccountUpdateExtra(existing, incoming)
		require.NoError(t, service.ValidateOpenAIReasoningPolicyExtra(merged))
		require.Equal(t, 20, merged["base_rpm"])
		for _, key := range []string{service.OpenAIChatReasoningReplayEnabledExtraKey, service.OpenAIReasoningSignatureRecoveryEnabledExtraKey} {
			if value, provided := incoming[key]; provided {
				require.Equal(t, value, merged[key])
			} else {
				require.NotContains(t, merged, key, "the service must preserve the stored value, not revalidate it as submitted")
			}
		}
		require.Equal(t, original, existing)
		require.Equal(t, inputCopy, incoming)
	}
	malformed := mergeAccountUpdateExtra(existing, map[string]any{service.OpenAIChatReasoningReplayEnabledExtraKey: "new-invalid"})
	require.Error(t, service.ValidateOpenAIReasoningPolicyExtra(malformed), "new malformed input is not silently discarded")
}

type reasoningPolicyBulkAdminStub struct {
	*stubAdminService
	loadedIDs []int64
}

func (s *reasoningPolicyBulkAdminStub) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	s.loadedIDs = append([]int64(nil), ids...)
	byID := make(map[int64]*service.Account, len(s.accounts))
	for index := range s.accounts {
		byID[s.accounts[index].ID] = &s.accounts[index]
	}
	accounts := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if account := byID[id]; account != nil {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func submitReasoningPolicyBulkTestRequest(t *testing.T, stub *reasoningPolicyBulkAdminStub, req BulkUpdateAccountsRequest) (*httptest.ResponseRecorder, *accountJobSubmitRepository) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	jobs := attachAccountJobSubmitter(router, handler)
	router.POST("/bulk", handler.BulkUpdate)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/bulk", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(request)
	router.ServeHTTP(recorder, request)
	return recorder, jobs
}

func TestOpenAIReasoningPolicyBulkAdminPreflightsAllTargetsBeforeSubmittingJobs(t *testing.T) {
	for _, filtered := range []bool{true, false} {
		stub := &reasoningPolicyBulkAdminStub{stubAdminService: newStubAdminService()}
		stub.accounts = make([]service.Account, 101)
		ids := make([]int64, len(stub.accounts))
		for index := range stub.accounts {
			ids[index] = int64(index + 1)
			stub.accounts[index] = service.Account{ID: ids[index], Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
		}
		stub.accounts[100].Platform = service.PlatformGrok
		req := BulkUpdateAccountsRequest{Extra: map[string]any{service.OpenAIReasoningSignatureRecoveryEnabledExtraKey: false}}
		if filtered {
			req.Filters = &BulkUpdateAccountFilters{Search: "batch"}
		} else {
			req.AccountIDs = ids
		}
		recorder, jobs := submitReasoningPolicyBulkTestRequest(t, stub, req)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "OPENAI_BULK_TARGET_INVALID")
		require.Equal(t, ids, stub.loadedIDs)
		require.Empty(t, jobs.created, "not even the first valid row may be enqueued")
		require.Nil(t, stub.lastBulkUpdateAccountInput)
	}
}

func TestOpenAIReasoningPolicyBulkAdminFreezesFilteredSelection(t *testing.T) {
	ids := make([]int64, 101)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	stub := newBulkJobScopeAdmin(ids...)
	for _, account := range stub.accountsByID {
		account.Type = service.AccountTypeSetupToken
	}
	stub.accountsByID[101] = canonicalCindyJobAccount(101)
	stub.matches = ids
	router, handler, jobs := newBulkJobScopeRouter(stub)
	handler.cindyJobMutations = &recordingCindyJobMutationRunner{}
	req := BulkUpdateAccountsRequest{
		Filters: &BulkUpdateAccountFilters{Search: "batch"},
		Extra:   map[string]any{service.OpenAIChatReasoningReplayEnabledExtraKey: true},
	}
	recorder := submitBulkJobScopeRequest(t, router, req, "reasoning-frozen-scope")
	require.Equal(t, http.StatusAccepted, recorder.Code)
	var submitted BulkUpdateAccountsRequest
	params := requireSubmittedAccountJob(t, jobs.accountJobSubmitRepository, service.AccountJobKindBulkUpdate, &submitted)
	require.Equal(t, req.Filters, submitted.Filters, "the original filter is retained only as the replay source")
	require.Empty(t, submitted.AccountIDs, "resolved IDs must not change the original request hash")
	require.Len(t, params.Items, len(ids))
	for index, seed := range params.Items {
		require.NotNil(t, seed.TargetAccountID)
		require.Equal(t, ids[index], *seed.TargetAccountID)
	}
	queriesAfterSubmission := stub.filterCalls
	require.Empty(t, stub.bulkInputs, "submission remains a background job")

	// Every original account leaves the live filter, and a new invalid target
	// enters it. Execution must still use only the already validated item IDs.
	stub.accountsByID[102] = &service.Account{ID: 102, Platform: service.PlatformGrok}
	stub.matches = []int64{102}
	results := executeBulkJobScopeItems(t, handler, params)
	require.Len(t, results, len(ids))
	for _, result := range results {
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	}
	require.Equal(t, ids, stub.updatedIDs)
	require.Len(t, stub.bulkInputs, len(ids))
	for index, input := range stub.bulkInputs {
		require.Nil(t, input.Filters, "workers must not re-resolve the live filter")
		require.Equal(t, []int64{ids[index]}, input.AccountIDs)
		require.Equal(t, req.Extra, input.Extra)
	}
	require.Equal(t, queriesAfterSubmission, stub.filterCalls)

	for _, matches := range [][]int64{{102}, nil} {
		stub.matches = matches
		replayed := submitBulkJobScopeRequest(t, router, req, "reasoning-frozen-scope")
		require.Equal(t, http.StatusAccepted, replayed.Code)
		require.Equal(t, "true", replayed.Header().Get("Idempotency-Replayed"))
		require.Len(t, jobs.created, 1)
		require.Equal(t, params, jobs.created[0], "replay must preserve the original payload, expiry and frozen items")
		require.Equal(t, queriesAfterSubmission, stub.filterCalls)
		require.Len(t, stub.bulkInputs, len(ids), "replay cannot execute an item again")
	}
	changed := req
	changed.Extra = map[string]any{service.OpenAIChatReasoningReplayEnabledExtraKey: false}
	conflict := submitBulkJobScopeRequest(t, router, changed, "reasoning-frozen-scope")
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Len(t, jobs.created, 1)
	require.Equal(t, queriesAfterSubmission, stub.filterCalls)
}

func TestOpenAIReasoningPolicyBulkAdminRejectsEmptyAndMissingTargets(t *testing.T) {
	for _, req := range []BulkUpdateAccountsRequest{
		{Filters: &BulkUpdateAccountFilters{Search: "missing"}},
		{AccountIDs: []int64{1}},
	} {
		stub := &reasoningPolicyBulkAdminStub{stubAdminService: newStubAdminService()}
		stub.accounts = nil
		req.Extra = map[string]any{service.OpenAIChatReasoningReplayEnabledExtraKey: false}
		recorder, jobs := submitReasoningPolicyBulkTestRequest(t, stub, req)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Empty(t, jobs.created)
	}
}
