package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountJobBulkUpdateFreezesFilterTargetsBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		name    string
		filters *BulkUpdateAccountFilters
	}{
		{name: "legacy", filters: &BulkUpdateAccountFilters{Status: service.StatusActive}},
		{name: "console", filters: &BulkUpdateAccountFilters{Statuses: service.StatusActive, Tags: "4"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			admin := newBulkJobScopeAdmin(11, 12, 13)
			admin.matches = []int64{11, 12}
			router, handler, repo := newBulkJobScopeRouter(admin)
			req := BulkUpdateAccountsRequest{Filters: test.filters, Name: "updated"}
			recorder := submitBulkJobScopeRequest(t, router, req, "fixed-scope")
			require.Equal(t, http.StatusAccepted, recorder.Code)
			require.Equal(t, 1, len(repo.created))
			params := repo.created[0]
			if len(params.Items) != 2 {
				t.Errorf("seed count = %d, want two frozen accounts", len(params.Items))
			}
			var payload BulkUpdateAccountsRequest
			require.NoError(t, json.Unmarshal([]byte(params.PayloadCipher), &payload))
			require.Equal(t, req.Filters, payload.Filters, "payload must preserve the original idempotency input")
			require.Empty(t, payload.AccountIDs, "frozen IDs belong in seeds, not the request hash")
			admin.matches = []int64{12, 13} // 11 leaves the filter and 13 joins after submission.
			admin.accountsByID[11].Status = "inactive"
			queriesBeforeExecution := admin.filterCalls
			results := executeBulkJobScopeItems(t, handler, params)
			for _, result := range results {
				require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
			}
			require.Equal(t, []int64{11, 12}, admin.updatedIDs, "execution must not shrink/expand the saved account set")
			require.Equal(t, queriesBeforeExecution, admin.filterCalls, "workers must not query live filters")
			for index, seed := range params.Items {
				require.NotNil(t, seed.TargetAccountID)
				require.Equal(t, int64(11+index), *seed.TargetAccountID)
			}
		})
	}
}

func TestAccountJobBulkUpdateReplayDoesNotResolveChangedFilters(t *testing.T) {
	admin := newBulkJobScopeAdmin(11, 12, 13)
	admin.matches = []int64{11, 12}
	router, _, repo := newBulkJobScopeRouter(admin)
	req := BulkUpdateAccountsRequest{Filters: &BulkUpdateAccountFilters{Status: service.StatusActive}, Status: "inactive"}
	recorder := submitBulkJobScopeRequest(t, router, req, "replay-scope")
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, 1, len(repo.created))
	original := repo.created[0]
	require.Equal(t, 2, len(original.Items))
	queriesAfterSubmission := admin.filterCalls
	for _, state := range []struct {
		name    string
		matches []int64
		err     error
	}{
		{name: "different_nonempty_set", matches: []int64{12, 13}},
		{name: "empty_after_update"},
		{name: "filter_query_unavailable", err: errors.New("synthetic filter outage")},
	} {
		t.Run(state.name, func(t *testing.T) {
			admin.matches, admin.filterErr = state.matches, state.err
			replayed := submitBulkJobScopeRequest(t, router, req, "replay-scope")
			require.Equal(t, http.StatusAccepted, replayed.Code)
			require.Equal(t, "true", replayed.Header().Get("Idempotency-Replayed"))
			require.Equal(t, 1, len(repo.created))
			require.Equal(t, original, repo.created[0], "replay must not rewrite the original snapshot or payload")
			require.Equal(t, queriesAfterSubmission, admin.filterCalls)
			require.Empty(t, admin.updatedIDs)
		})
	}
	changed := req
	changed.Name = "different-operation"
	recorder = submitBulkJobScopeRequest(t, router, changed, "replay-scope")
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, 1, len(repo.created))
	require.Equal(t, queriesAfterSubmission, admin.filterCalls, "conflicting reuse must not resolve or enqueue new targets")
}

func TestAccountJobBulkUpdateEmptyAndLegacyUnboundTargetsDoNotMutate(t *testing.T) {
	admin := newBulkJobScopeAdmin(11, 12)
	router, handler, repo := newBulkJobScopeRouter(admin)
	req := BulkUpdateAccountsRequest{Filters: &BulkUpdateAccountFilters{Status: service.StatusActive}, Name: "updated"}
	recorder := submitBulkJobScopeRequest(t, router, req, "empty-scope")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, 0, len(repo.created))
	require.Empty(t, admin.updatedIDs)

	// Historical filter-only jobs have no trustworthy submission-time ID set.
	admin.matches = []int64{11, 12}
	queriesBeforeExecution := admin.filterCalls
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	results := executeBulkJobScopeItems(t, handler, service.CreateAccountJobParams{
		Kind: service.AccountJobKindBulkUpdate, PayloadCipher: string(raw),
		Items: ordinalAccountJobSeeds(1),
	})
	require.Equal(t, service.AccountJobItemStatusFailed, results[0].Status)
	require.Equal(t, "target_missing", results[0].ErrorCode)
	require.Equal(t, queriesBeforeExecution, admin.filterCalls)
	require.Empty(t, admin.updatedIDs)
}

func TestAccountJobBulkUpdateDeletedFrozenTargetDoesNotChooseReplacement(t *testing.T) {
	admin := newBulkJobScopeAdmin(11, 12, 13)
	admin.matches = []int64{11, 12}
	router, handler, repo := newBulkJobScopeRouter(admin)
	recorder := submitBulkJobScopeRequest(t, router, BulkUpdateAccountsRequest{
		Filters: &BulkUpdateAccountFilters{Status: service.StatusActive}, Name: "updated",
	}, "deleted-target")
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, 1, len(repo.created))
	require.Equal(t, 2, len(repo.created[0].Items))
	delete(admin.accountsByID, 11)
	admin.matches = []int64{12, 13}
	queriesBeforeExecution := admin.filterCalls
	results := executeBulkJobScopeItems(t, handler, repo.created[0])
	require.Equal(t, service.AccountJobItemStatusFailed, results[0].Status)
	require.Equal(t, "bulk_update_failed", results[0].ErrorCode)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[1].Status)
	require.Equal(t, []int64{12}, admin.updatedIDs)
	require.Equal(t, queriesBeforeExecution, admin.filterCalls)
}

func TestAccountJobBulkUpdateExplicitIDsIgnoreFilters(t *testing.T) {
	admin := newBulkJobScopeAdmin(3, 7, 99)
	admin.matches = []int64{99}
	admin.filterErr = errors.New("explicit-ID jobs must not query filters")
	router, handler, repo := newBulkJobScopeRouter(admin)
	recorder := submitBulkJobScopeRequest(t, router, BulkUpdateAccountsRequest{
		AccountIDs: []int64{7, 3, 7}, Filters: &BulkUpdateAccountFilters{Group: "not-an-id"}, Name: "updated",
	}, "explicit-scope")
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, 1, len(repo.created))
	results := executeBulkJobScopeItems(t, handler, repo.created[0])
	require.Equal(t, 2, len(results))
	for _, result := range results {
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	}
	require.Equal(t, []int64{3, 7}, admin.updatedIDs)
	require.Equal(t, 0, admin.filterCalls)
}

func TestAccountJobBulkReplayUsesSubmissionIdentityWithoutWrites(t *testing.T) {
	repo := &bulkJobScopeRepository{accountJobSubmitRepository: &accountJobSubmitRepository{}}
	jobs := service.NewAccountJobService(repo, accountJobTestEncryptor{})
	payload := json.RawMessage(`{"filters":{"status":"active"},"name":"updated"}`)

	job, replayed, err := jobs.Submit(context.Background(), 77, service.AccountJobKindBulkUpdate, "owner-bound", payload, nil, accountJobSeeds([]int64{11}))
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, 1, len(repo.created))
	original := repo.created[0]
	for _, test := range []struct {
		name       string
		ctx        context.Context
		actor      int64
		kind       string
		key        string
		payload    json.RawMessage
		wantReplay bool
		wantErr    error
	}{
		{name: "same_submission", ctx: context.Background(), actor: 77, kind: service.AccountJobKindBulkUpdate, key: "owner-bound", payload: payload, wantReplay: true},
		{name: "other_actor", ctx: context.Background(), actor: 78, kind: service.AccountJobKindBulkUpdate, key: "owner-bound", payload: payload},
		{name: "other_kind", ctx: context.Background(), actor: 77, kind: service.AccountJobKindBatchDelete, key: "owner-bound", payload: payload},
		{name: "changed_payload", ctx: context.Background(), actor: 77, kind: service.AccountJobKindBulkUpdate, key: "owner-bound", payload: json.RawMessage(`{"name":"other"}`), wantErr: service.ErrAccountJobIdempotencyConflict},
		{name: "missing_key", ctx: context.Background(), actor: 77, kind: service.AccountJobKindBulkUpdate, payload: payload, wantErr: service.ErrAccountJobIdempotencyRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, replayed, err := jobs.ReplaySubmission(test.ctx, test.actor, test.kind, test.key, test.payload)
			if test.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.wantErr)
			}
			require.Equal(t, test.wantReplay, replayed)
			if test.wantReplay {
				require.NotNil(t, got)
				require.Equal(t, job.ID, got.ID)
			} else {
				require.Nil(t, got)
			}
			require.Equal(t, 1, len(repo.created))
			require.Equal(t, original, repo.created[0], "lookup must not create, reseed or extend expiry")
		})
	}
	replayedJob, replayed, err := jobs.Submit(context.Background(), 77, service.AccountJobKindBulkUpdate, "owner-bound", payload, nil, accountJobSeeds([]int64{13}))
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, job.ID, replayedJob.ID)
	require.Equal(t, original, repo.created[0], "resubmission must retain the originally frozen target, not the newly supplied seeds")
	require.Equal(t, 1, len(repo.created))
	legacy := original
	legacy.Metadata = json.RawMessage(`{"plugin_id":7,"plugin_generation":2,"target_count":1}`)
	legacyRepo := &bulkJobScopeRepository{accountJobSubmitRepository: &accountJobSubmitRepository{created: []service.CreateAccountJobParams{legacy}}}
	legacyJobs := service.NewAccountJobService(legacyRepo, accountJobTestEncryptor{})
	_, _, err = legacyJobs.ReplaySubmission(context.Background(), 77, service.AccountJobKindBulkUpdate, "owner-bound", payload)
	require.ErrorIs(t, err, service.ErrAccountJobIdempotencyConflict, "a native request cannot silently claim a historical plugin-owned submission")
	require.Equal(t, []service.CreateAccountJobParams{legacy}, legacyRepo.created)

}

type bulkJobScopeAdmin struct {
	*accountTaxonomyHandlerStub
	accountsByID map[int64]*service.Account
	matches      []int64
	filterCalls  int
	filterErr    error
	updatedIDs   []int64
	bulkInputs   []*service.BulkUpdateAccountsInput
}

func newBulkJobScopeAdmin(ids ...int64) *bulkJobScopeAdmin {
	s := &bulkJobScopeAdmin{accountTaxonomyHandlerStub: newAccountTaxonomyHandlerStub(), accountsByID: make(map[int64]*service.Account)}
	for _, id := range ids {
		s.accountsByID[id] = &service.Account{ID: id, Name: "original", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive}
	}
	return s
}

func (s *bulkJobScopeAdmin) listMatches() ([]service.Account, int64, error) {
	s.filterCalls++
	if s.filterErr != nil {
		return nil, 0, s.filterErr
	}
	accounts := make([]service.Account, 0, len(s.matches))
	for _, id := range s.matches {
		if account := s.accountsByID[id]; account != nil {
			accounts = append(accounts, *account)
		}
	}
	return accounts, int64(len(accounts)), nil
}

func (s *bulkJobScopeAdmin) ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]service.Account, int64, error) {
	return s.listMatches()
}

func (s *bulkJobScopeAdmin) ListAccountsConsole(context.Context, int, int, service.AccountConsoleFilters) ([]service.Account, int64, error) {
	return s.listMatches()
}

func (s *bulkJobScopeAdmin) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	if account := s.accountsByID[id]; account != nil {
		return account, nil
	}
	return nil, service.ErrAccountNotFound
}

func (s *bulkJobScopeAdmin) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	accounts := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if account := s.accountsByID[id]; account != nil {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (s *bulkJobScopeAdmin) BulkUpdateAccounts(_ context.Context, input *service.BulkUpdateAccountsInput) (*service.BulkUpdateAccountsResult, error) {
	s.bulkInputs = append(s.bulkInputs, input)
	if len(input.AccountIDs) != 1 || input.Filters != nil {
		return nil, errors.New("fixture requires a single explicit account")
	}
	account := s.accountsByID[input.AccountIDs[0]]
	if account == nil {
		return nil, service.ErrAccountNotFound
	}
	account.Name = input.Name
	s.updatedIDs = append(s.updatedIDs, account.ID)
	return &service.BulkUpdateAccountsResult{Success: 1, SuccessIDs: []int64{account.ID}}, nil
}

type bulkJobScopeRepository struct{ *accountJobSubmitRepository }

func (r *bulkJobScopeRepository) FindIdempotent(_ context.Context, actor int64, kind, key string) (*service.AccountJob, error) {
	for index, params := range r.created {
		if params.CreatedBy == actor && params.Kind == kind && params.IdempotencyKey == key {
			return &service.AccountJob{ID: int64(index + 1), CreatedBy: actor, Kind: kind,
				IdempotencyKey: key, RequestHash: params.RequestHash, Metadata: params.Metadata,
				TargetCount: len(params.Items), Attempt: params.Attempt}, nil
		}
	}
	return nil, service.ErrAccountJobNotFound
}

func newBulkJobScopeRouter(admin *bulkJobScopeAdmin) (*gin.Engine, *AccountHandler, *bulkJobScopeRepository) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &AccountHandler{adminService: admin}
	repo := &bulkJobScopeRepository{accountJobSubmitRepository: attachAccountJobSubmitter(router, handler)}
	handler.SetAccountJobService(service.NewAccountJobService(repo, accountJobTestEncryptor{}))
	router.POST("/bulk", handler.BulkUpdate)
	return router, handler, repo
}

func submitBulkJobScopeRequest(t *testing.T, router *gin.Engine, req BulkUpdateAccountsRequest, key string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/bulk", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func executeBulkJobScopeItems(t *testing.T, handler *AccountHandler, params service.CreateAccountJobParams) []service.AccountJobExecutionResult {
	t.Helper()
	job := &service.AccountJob{ID: 1, Kind: params.Kind, TargetCount: len(params.Items)}
	results := make([]service.AccountJobExecutionResult, 0, len(params.Items))
	for index, seed := range params.Items {
		result, err := handler.ExecuteAccountJob(context.Background(), job, json.RawMessage(params.PayloadCipher), []service.AccountJobItem{{
			ID: int64(index + 1), Ordinal: seed.Ordinal, TargetAccountID: seed.TargetAccountID, Metadata: seed.Metadata,
		}})
		require.NoError(t, err)
		require.Equal(t, 1, len(result))
		results = append(results, result[0])
	}
	return results
}
