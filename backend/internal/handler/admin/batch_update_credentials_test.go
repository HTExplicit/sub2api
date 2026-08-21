//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// failingAdminService 嵌入 stubAdminService，可配置 UpdateAccount 在指定 ID 时失败。
type failingAdminService struct {
	*stubAdminService
	failOnAccountID int64
	updateCallCount atomic.Int64
}

func (f *failingAdminService) UpdateAccount(ctx context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	f.updateCallCount.Add(1)
	if id == f.failOnAccountID {
		return nil, errors.New("database error")
	}
	return f.stubAdminService.UpdateAccount(ctx, id, input)
}

func setupAccountHandlerWithService(adminSvc service.AdminService) (*gin.Engine, *AccountHandler, *accountJobSubmitRepository) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	jobs := attachAccountJobSubmitter(router, handler)
	router.POST("/api/v1/admin/accounts/batch-update-credentials", handler.BatchUpdateCredentials)
	return router, handler, jobs
}

func TestBatchUpdateCredentials_AllSuccess(t *testing.T) {
	svc := &failingAdminService{stubAdminService: newStubAdminService()}
	router, handler, jobs := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{1, 2, 3},
		Field:      "account_uuid",
		Value:      "test-uuid",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, "合法批量更新应创建异步任务")
	var payload BatchUpdateCredentialsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBatchUpdateCredentials, &payload)
	require.Equal(t, []int64{1, 2, 3}, payload.AccountIDs)
	require.Len(t, params.Items, 3)
	results := executeSubmittedAccountJobItems(handler, params)
	for _, result := range results {
		require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	}
	require.Equal(t, int64(3), svc.updateCallCount.Load(), "应调用 3 次 UpdateAccount")
}

func TestBatchUpdateCredentials_PartialFailure(t *testing.T) {
	// 让第 2 个账号（ID=2）更新时失败
	svc := &failingAdminService{
		stubAdminService: newStubAdminService(),
		failOnAccountID:  2,
	}
	router, handler, jobs := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{1, 2, 3},
		Field:      "org_uuid",
		Value:      "test-org",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, "批量更新应先返回任务")
	var payload BatchUpdateCredentialsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBatchUpdateCredentials, &payload)
	results := executeSubmittedAccountJobItems(handler, params)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
	require.Equal(t, service.AccountJobItemStatusFailed, results[1].Status)
	require.Equal(t, "credentials_update_failed", results[1].ErrorCode)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[2].Status)

	// 所有 3 个账号都会被尝试更新（非 fail-fast）
	require.Equal(t, int64(3), svc.updateCallCount.Load(),
		"应调用 3 次 UpdateAccount（逐个尝试，失败后继续）")
}

func TestBatchUpdateCredentials_FirstAccountNotFound(t *testing.T) {
	// GetAccount 在 stubAdminService 中总是成功的，需要创建一个 GetAccount 会失败的 stub
	svc := &getAccountFailingService{
		stubAdminService: newStubAdminService(),
		failOnAccountID:  1,
	}
	router, handler, jobs := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{1, 2, 3},
		Field:      "account_uuid",
		Value:      "test",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, "账号存在性由任务项执行时判定")
	var payload BatchUpdateCredentialsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBatchUpdateCredentials, &payload)
	results := executeSubmittedAccountJobItems(handler, params)
	require.Equal(t, service.AccountJobItemStatusFailed, results[0].Status)
	require.Equal(t, "account_not_found", results[0].ErrorCode)
}

// getAccountFailingService 模拟 GetAccount 在特定 ID 时返回 not found。
type getAccountFailingService struct {
	*stubAdminService
	failOnAccountID int64
}

func (f *getAccountFailingService) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	if id == f.failOnAccountID {
		return nil, errors.New("not found")
	}
	return f.stubAdminService.GetAccount(ctx, id)
}

func TestBatchUpdateCredentials_InterceptWarmupRequests_NonBool(t *testing.T) {
	svc := &failingAdminService{stubAdminService: newStubAdminService()}
	router, _, _ := setupAccountHandlerWithService(svc)

	// intercept_warmup_requests 传入非 bool 类型（string），应返回 400
	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "intercept_warmup_requests",
		"value":       "not-a-bool",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"intercept_warmup_requests 传入非 bool 值应返回 400")
}

func TestBatchUpdateCredentials_InterceptWarmupRequests_ValidBool(t *testing.T) {
	svc := &failingAdminService{stubAdminService: newStubAdminService()}
	router, _, jobs := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "intercept_warmup_requests",
		"value":       true,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code,
		"intercept_warmup_requests 传入合法 bool 值应创建任务")
	var payload BatchUpdateCredentialsRequest
	params := requireSubmittedAccountJob(t, jobs, service.AccountJobKindBatchUpdateCredentials, &payload)
	require.Equal(t, true, payload.Value)
	require.Len(t, params.Items, 1)
}

func TestBatchUpdateCredentials_AccountUUID_NonString(t *testing.T) {
	svc := &failingAdminService{stubAdminService: newStubAdminService()}
	router, _, _ := setupAccountHandlerWithService(svc)

	// account_uuid 传入非 string 类型（number），应返回 400
	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "account_uuid",
		"value":       12345,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"account_uuid 传入非 string 值应返回 400")
}

func TestBatchUpdateCredentials_AccountUUID_NullValue(t *testing.T) {
	svc := &failingAdminService{stubAdminService: newStubAdminService()}
	router, _, jobs := setupAccountHandlerWithService(svc)

	// account_uuid 传入 null（设置为空），应正常通过
	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "account_uuid",
		"value":       nil,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code,
		"account_uuid 传入 null 应创建任务")
	var payload BatchUpdateCredentialsRequest
	requireSubmittedAccountJob(t, jobs, service.AccountJobKindBatchUpdateCredentials, &payload)
	require.Nil(t, payload.Value)
}
