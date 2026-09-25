package admin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type batchConnectionAdmin struct {
	service.AdminService
	accounts map[int64]*service.Account
}

func (a *batchConnectionAdmin) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	account := a.accounts[id]
	if account == nil {
		return nil, service.ErrAccountNotFound
	}
	return account, nil
}

type batchConnectionRepo struct {
	service.AccountRepository
	accounts    map[int64]*service.Account
	saved       map[int64]json.RawMessage
	called      []int64
	recovery    *service.SuccessfulTestRecoveryResult
	recoveryErr error
	t           *testing.T
}

func (r *batchConnectionRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	require.NotEmpty(r.t, r.saved[id], "execution plan must be durable before model IO begins")
	r.called = append(r.called, id)
	return r.accounts[id], nil
}
func (r *batchConnectionRepo) RecoverAfterSuccessfulTest(context.Context, int64) (*service.SuccessfulTestRecoveryResult, error) {
	return r.recovery, r.recoveryErr
}

type batchConnectionJobRepo struct {
	service.AccountJobRepository
	saved map[int64]json.RawMessage
	err   error
}

func (r *batchConnectionJobRepo) SaveExecutionSnapshot(_ context.Context, _ int64, itemID int64, raw json.RawMessage) error {
	if r.err != nil {
		return r.err
	}
	r.saved[itemID] = append(json.RawMessage(nil), raw...)
	return nil
}

func TestBatchConnectionWorkerPlanSnapshot(t *testing.T) {
	account := &service.Account{ID: 1, Name: "good", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2", "text-model": "text-model"}}, Extra: map[string]any{"synthetic_ui_test": true}}
	bad := &service.Account{ID: 2, Name: "catalog unavailable", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"base_url": "ftp://invalid"}}
	saved := map[int64]json.RawMessage{}
	accounts := map[int64]*service.Account{1: account, 2: bad}
	repo := &batchConnectionRepo{accounts: accounts, saved: saved, t: t, recovery: &service.SuccessfulTestRecoveryResult{ClearedError: true}}
	jobRepo := &batchConnectionJobRepo{saved: saved}
	tests := service.NewAccountTestService(repo, nil, nil, nil, nil, nil, &config.Config{}, nil)
	h := &AccountHandler{adminService: &batchConnectionAdmin{accounts: accounts}, accountTestService: tests,
		accountJobs: service.NewAccountJobService(jobRepo, accountJobTestEncryptor{}), rateLimitService: service.NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
	raw := json.RawMessage(`{"items":[{"account_id":1,"selection_mode":"auto"},{"account_id":2,"selection_mode":"auto"}]}`)
	run := func(id int64, prior json.RawMessage) service.AccountJobExecutionResult {
		return h.executeBatchConnectionTest(context.Background(), raw, service.AccountJobItem{ID: id, JobID: 88, TargetAccountID: &id, Metadata: prior})
	}
	failed := run(2, nil)
	require.Equal(t, "test_catalog_failed", failed.ErrorCode)
	result := run(1, nil)
	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(result.Metadata, &meta))
	require.Equal(t, "text-model", meta["model_id"])
	require.Equal(t, "recovered", meta["recovery_status"])
	require.NotContains(t, string(result.Metadata), "healthy and interactive")
	frozen := append(json.RawMessage(nil), saved[1]...)
	account.Credentials["model_mapping"] = map[string]any{"aaa-new-default": "aaa-new-default", "text-model": "text-model"}
	repo.recoveryErr = errors.New("private recovery error")
	result = run(1, frozen)
	require.Equal(t, service.AccountJobItemStatusSucceeded, result.Status)
	require.NoError(t, json.Unmarshal(result.Metadata, &meta))
	require.Equal(t, "text-model", meta["model_id"], "retry must not follow a changed default")
	require.Equal(t, "warning", meta["recovery_status"])
	require.NotContains(t, string(result.Metadata), "private recovery error")
	repo.recoveryErr = nil
	repo.recovery = &service.SuccessfulTestRecoveryResult{ManualStatePreserved: true}
	result = run(1, frozen)
	require.NoError(t, json.Unmarshal(result.Metadata, &meta))
	require.Equal(t, "manual_state_preserved", meta["recovery_status"])
	before := len(repo.called)
	account.Credentials["model_mapping"] = map[string]any{"text-model": "changed-wire"}
	result = run(1, frozen)
	require.Equal(t, "test_plan_changed", result.ErrorCode)
	require.Len(t, repo.called, before, "a changed plan must not call the model")
	account.Credentials["model_mapping"] = map[string]any{"text-model": "text-model"}
	jobRepo.err = errors.New("database unavailable")
	result = run(1, nil)
	require.Equal(t, "test_plan_persist_failed", result.ErrorCode)
	require.Len(t, repo.called, before, "an unpersisted selection must not call the model")
}

func TestAccountTestConnectionPurpose(t *testing.T) {
	account := &service.Account{ID: 1, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey}
	plan, err := ordinaryAccountTestPlan(account, []map[string]any{
		{"id": "gemini-3.1-flash-image"}, {"id": "gemini-2.5-flash"},
		{"id": "image-caption-conversation", "output_modalities": []string{"text"}},
		{"id": "audio-only", "output_modalities": []string{"audio"}},
	})
	require.NoError(t, err)
	require.Equal(t, "gemini-2.5-flash", plan.ModeViews["connection"].DefaultModelID)
	require.Equal(t, []string{"gemini-2.5-flash", "image-caption-conversation"}, plan.ModeViews["connection"].ModelIDs)
	require.True(t, accountConnectionModel(account, map[string]any{"id": "gemini-text", "endpoints": []any{"generateContent"}}))
}

func TestBatchConnectionSelectionCancel(t *testing.T) {
	upstream := &blockingBatchCatalogUpstream{entered: make(chan struct{}, 1), release: make(chan struct{})}
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	tests := service.NewAccountTestService(nil, nil, nil, nil, nil, upstream, cfg, nil)
	tests.SetOpenAIGatewayService(gateway)
	a := &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://catalog.example"}}
	h := &AccountHandler{adminService: &batchConnectionAdmin{accounts: map[int64]*service.Account{1: a}}, accountTestService: tests}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan service.AccountJobExecutionResult, 1)
	go func() {
		done <- h.executeBatchConnectionTest(ctx, json.RawMessage(`{"items":[{"account_id":1,"selection_mode":"auto"}]}`), service.AccountJobItem{ID: 1, JobID: 2, TargetAccountID: &a.ID})
	}()
	select {
	case <-upstream.entered:
	case <-time.After(time.Second):
		t.Fatal("catalog did not start")
	}
	cancel()
	select {
	case result := <-done:
		require.Equal(t, service.AccountJobItemStatusCanceled, result.Status)
		require.Empty(t, result.ErrorCode)
		require.NotContains(t, string(result.Metadata), "execution_plan")
	case <-time.After(time.Second):
		t.Fatal("selection did not stop")
	}
}
