package admin

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type batchDeleteAdminService struct {
	*stubAdminService

	mu               sync.Mutex
	active           int
	maxActive        int
	deletedIDs       []int64
	deleteErrorsByID map[int64]error
	accountsByID     map[int64]*service.Account
}

func (s *batchDeleteAdminService) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	accounts := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if s.accountsByID != nil {
			if account, ok := s.accountsByID[id]; ok {
				accounts = append(accounts, account)
			}
			continue
		}
		accounts = append(accounts, &service.Account{ID: id})
	}
	return accounts, nil
}

func (s *batchDeleteAdminService) DeleteAccount(ctx context.Context, id int64) error {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
		return ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErrorsByID[id]
}

func setupAccountBatchDeleteRouter(adminSvc *batchDeleteAdminService) (*gin.Engine, *AccountHandler, *accountJobSubmitRepository) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	jobs := attachAccountJobSubmitter(router, handler)
	router.POST("/api/v1/admin/accounts/batch-delete", handler.BatchDelete)
	return router, handler, jobs
}

func TestAccountHandlerBatchDeleteCreatesStablePerAccountJobItems(t *testing.T) {
	adminSvc := &batchDeleteAdminService{
		stubAdminService: newStubAdminService(),
		deleteErrorsByID: map[int64]error{
			3: errors.New("delete failed"),
		},
	}
	router, handler, jobs := setupAccountBatchDeleteRouter(adminSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/batch-delete",
		bytes.NewBufferString(`{"account_ids":[5,4,3,2,1,2,0,-1]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var payload accountIDsJobPayload
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBatchDelete, &payload)
	require.Equal(t, []int64{1, 2, 3, 4, 5}, payload.AccountIDs)
	require.Len(t, params.Items, 5)

	results := executeSubmittedAccountJobItems(handler, params)
	for index, result := range results {
		if index == 2 {
			require.Equal(t, service.AccountJobItemStatusFailed, result.Status)
			require.Equal(t, "delete_failed", result.ErrorCode)
			continue
		}
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	}
	require.Equal(t, []int64{1, 2, 3, 4, 5}, adminSvc.deletedIDs)
}

func TestAccountHandlerBatchDeleteOrdersParentAndShadowJobItemsDeterministically(t *testing.T) {
	parentID := int64(1)
	adminSvc := &batchDeleteAdminService{
		stubAdminService: newStubAdminService(),
		accountsByID: map[int64]*service.Account{
			1: {ID: 1},
			2: {ID: 2, ParentAccountID: &parentID},
			3: {ID: 3},
		},
	}
	router, _, jobs := setupAccountBatchDeleteRouter(adminSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/batch-delete",
		bytes.NewBufferString(`{"account_ids":[1,2,3]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var payload accountIDsJobPayload
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBatchDelete, &payload)
	require.Equal(t, []int64{1, 2, 3}, payload.AccountIDs)
	require.Len(t, params.Items, 3)
	for index, seed := range params.Items {
		require.Equal(t, int64(index+1), *seed.TargetAccountID)
	}
	require.Empty(t, adminSvc.deletedIDs, "提交请求不得在 HTTP handler 内执行删除")
}

func TestAccountHandlerBatchDeleteRejectsEmptyNormalizedIDs(t *testing.T) {
	adminSvc := &batchDeleteAdminService{
		stubAdminService: newStubAdminService(),
	}
	router, _, _ := setupAccountBatchDeleteRouter(adminSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/batch-delete",
		bytes.NewBufferString(`{"account_ids":[0,-1]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
