//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Return independent snapshots, as the real repository does. Sharing the same
// Account pointer would hide a refresh overwriting a concurrent admin edit.
type openAIIdentityMutationRepo struct {
	AccountRepository
	mu       sync.Mutex
	account  *Account
	getErr   error
	casErr   error
	getCalls int
	afterCAS func()
}

func copyOpenAIIdentityMutationAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	copy := *account
	copy.Credentials = shallowCopyMap(account.Credentials)
	copy.Extra = shallowCopyMap(account.Extra)
	return &copy
}

func (r *openAIIdentityMutationRepo) GetByID(context.Context, int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	return copyOpenAIIdentityMutationAccount(r.account), nil
}

func (r *openAIIdentityMutationRepo) Update(_ context.Context, account *Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.account = copyOpenAIIdentityMutationAccount(account)
	return nil
}

func (r *openAIIdentityMutationRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.account.Credentials = shallowCopyMap(credentials)
	return nil
}

func (r *openAIIdentityMutationRepo) UpdateOpenAIOAuthCredentialsIfUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
) (bool, error) {
	r.mu.Lock()
	if r.casErr != nil {
		err := r.casErr
		r.mu.Unlock()
		return false, err
	}
	if r.account.ID != id || !r.account.IsOpenAIOAuth() ||
		!reflect.DeepEqual(r.account.Credentials, expectedCredentials) ||
		!reflect.DeepEqual(r.account.ProxyID, expectedProxyID) {
		r.mu.Unlock()
		return false, nil
	}
	r.account.Credentials = shallowCopyMap(credentials)
	afterCAS := r.afterCAS
	r.afterCAS = nil
	r.mu.Unlock()
	if afterCAS != nil {
		afterCAS()
	}
	return true, nil
}

type openAIIdentityMutationCache struct {
	*openAITokenCacheStub
	getCalls  int
	beforeGet func(int)
}

func (c *openAIIdentityMutationCache) GetAccessToken(ctx context.Context, key string) (string, error) {
	c.getCalls++
	if c.beforeGet != nil {
		c.beforeGet(c.getCalls)
	}
	return c.openAITokenCacheStub.GetAccessToken(ctx, key)
}

type openAIIdentityMutationExecutor struct {
	*refreshAPIExecutorStub
}

func (e *openAIIdentityMutationExecutor) CacheKey(account *Account) string {
	return OpenAITokenCacheKey(account)
}

func openAIIdentityMutationCredentials(identity string) map[string]any {
	return map[string]any{
		"access_token":       "synthetic-token-" + identity,
		"refresh_token":      "synthetic-refresh-" + identity,
		"chatgpt_account_id": "synthetic-workspace-" + identity,
		"chatgpt_user_id":    "synthetic-user-" + identity,
		"expires_at":         time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
}

func newOpenAIIdentityMutationRepo() *openAIIdentityMutationRepo {
	return &openAIIdentityMutationRepo{account: &Account{
		ID:          62001,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: openAIIdentityMutationCredentials("a"),
	}}
}

func buildOpenAIIdentityMutationRequest(t *testing.T, gateway *OpenAIGatewayService, account *Account) *http.Request {
	t.Helper()
	ctx := context.Background()
	token, _, err := gateway.GetAccessToken(ctx, account)
	require.NoError(t, err)
	body := []byte(`{"model":"gpt-5.5","input":"synthetic identity fixture"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request, err := gateway.buildUpstreamRequest(ctx, c, account, body, token, true, "", true)
	require.NoError(t, err)
	return request
}

func TestOpenAIIdentity_AdminCredentialUpdateUsesCurrentTokenAndWorkspace(t *testing.T) {
	for _, cacheState := range []string{"already_cached", "old_snapshot_fills_cache_after_update"} {
		t.Run(cacheState, func(t *testing.T) {
			ctx := context.Background()
			repo := newOpenAIIdentityMutationRepo()
			cache := newOpenAITokenCacheStub()
			provider := NewOpenAITokenProvider(repo, cache, nil)
			selectedBeforeEdit, err := repo.GetByID(ctx, repo.account.ID)
			require.NoError(t, err)
			if cacheState == "already_cached" {
				token, err := provider.GetAccessToken(ctx, selectedBeforeEdit)
				require.NoError(t, err)
				require.Equal(t, "synthetic-token-a", token)
			}

			admin := &adminServiceImpl{accountRepo: repo}
			updated, err := admin.UpdateAccount(ctx, selectedBeforeEdit.ID, &UpdateAccountInput{
				Credentials: openAIIdentityMutationCredentials("b"),
			})
			require.NoError(t, err)
			require.Equal(t, "synthetic-token-b", updated.GetOpenAIAccessToken())
			if cacheState == "old_snapshot_fills_cache_after_update" {
				// A request selected before the edit may finish or fail, but it must
				// not populate an A token that a later B request can consume.
				_, _ = provider.GetAccessToken(ctx, selectedBeforeEdit)
			}

			gateway := &OpenAIGatewayService{accountRepo: repo, openAITokenProvider: provider}
			request := buildOpenAIIdentityMutationRequest(t, gateway, updated)
			require.Equal(t, chatgptCodexURL, request.URL.String())
			require.Equal(t, "synthetic-workspace-b", request.Header.Get("Chatgpt-Account-Id"))
			require.Equal(t, "Bearer synthetic-token-b", request.Header.Get("Authorization"),
				"the real Responses builder must pair the current credential with its workspace")
		})
	}

	t.Run("matching_cache_hit_does_not_read_repository", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		account, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		cache := newOpenAITokenCacheStub()
		require.NoError(t, cache.SetAccessToken(ctx, OpenAITokenCacheKey(account), account.GetOpenAIAccessToken(), time.Minute))
		provider := NewOpenAITokenProvider(repo, cache, nil)
		reads := repo.getCalls
		request := buildOpenAIIdentityMutationRequest(t, &OpenAIGatewayService{accountRepo: repo, openAITokenProvider: provider}, account)
		require.Equal(t, "Bearer synthetic-token-a", request.Header.Get("Authorization"))
		require.Equal(t, reads, repo.getCalls)
	})

	t.Run("unverified_different_cache_token_is_ignored", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		admin := &adminServiceImpl{accountRepo: repo}
		updated, err := admin.UpdateAccount(ctx, repo.account.ID, &UpdateAccountInput{Credentials: openAIIdentityMutationCredentials("b")})
		require.NoError(t, err)
		cache := newOpenAITokenCacheStub()
		require.NoError(t, cache.SetAccessToken(ctx, OpenAITokenCacheKey(updated), "synthetic-token-a", time.Minute))
		repo.getErr = errors.New("synthetic repository unavailable")
		provider := NewOpenAITokenProvider(repo, cache, nil)
		request := buildOpenAIIdentityMutationRequest(t, &OpenAIGatewayService{accountRepo: repo, openAITokenProvider: provider}, updated)
		require.Equal(t, "Bearer synthetic-token-b", request.Header.Get("Authorization"))
		require.Equal(t, "synthetic-workspace-b", request.Header.Get("Chatgpt-Account-Id"))
	})

	t.Run("version_check_cannot_adopt_another_workspace", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		selected, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		credentials := openAIIdentityMutationCredentials("b")
		credentials["_token_version"] = int64(2)
		admin := &adminServiceImpl{accountRepo: repo}
		_, err = admin.UpdateAccount(ctx, selected.ID, &UpdateAccountInput{Credentials: credentials})
		require.NoError(t, err)
		provider := NewOpenAITokenProvider(repo, newOpenAITokenCacheStub(), nil)
		token, err := provider.GetAccessToken(ctx, selected)
		require.ErrorIs(t, err, errOpenAITokenIdentityChanged)
		require.Empty(t, token)
	})

	t.Run("lock_wait_cannot_adopt_another_workspace", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		repo.account.Credentials["expires_at"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		selected, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		cache := &openAIIdentityMutationCache{openAITokenCacheStub: newOpenAITokenCacheStub()}
		cache.lockAcquired = false
		cache.beforeGet = func(call int) {
			if call != 2 {
				return
			}
			admin := &adminServiceImpl{accountRepo: repo}
			_, err := admin.UpdateAccount(ctx, selected.ID, &UpdateAccountInput{Credentials: openAIIdentityMutationCredentials("b")})
			require.NoError(t, err)
			require.NoError(t, cache.SetAccessToken(ctx, OpenAITokenCacheKey(selected), "synthetic-token-b", time.Minute))
		}
		provider := NewOpenAITokenProvider(repo, cache, nil)
		token, err := provider.GetAccessToken(ctx, selected)
		require.ErrorIs(t, err, errOpenAITokenIdentityChanged)
		require.Empty(t, token)
	})
}

func TestOpenAIIdentity_LateRefreshCannotOverwriteAdminCredentials(t *testing.T) {
	t.Run("admin_reauthorization_before_cas", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		account, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		admin := &adminServiceImpl{accountRepo: repo}
		lateCredentials := openAIIdentityMutationCredentials("a")
		lateCredentials["access_token"] = "synthetic-late-refresh-a"
		executor := &refreshAPIExecutorStub{
			needsRefresh: true,
			credentials:  lateCredentials,
			onRefresh: func() {
				// Deterministically perform the admin commit after RefreshIfNeeded
				// reads A and before the provider returns A's replacement token.
				updated, err := admin.UpdateAccount(ctx, account.ID, &UpdateAccountInput{
					Credentials: openAIIdentityMutationCredentials("b"),
				})
				require.NoError(t, err)
				require.Equal(t, "synthetic-token-b", updated.GetOpenAIAccessToken())
			},
		}
		api := NewOAuthRefreshAPI(repo, newOpenAITokenCacheStub())
		result, err := api.RefreshIfNeeded(ctx, account, executor, 3*time.Minute)
		require.NoError(t, err)
		durable, err := repo.GetByID(ctx, account.ID)
		require.NoError(t, err)
		require.Equal(t, "synthetic-token-b", durable.GetOpenAIAccessToken(),
			"a successful admin edit must survive an older OAuth refresh finishing later")
		require.Equal(t, "synthetic-workspace-b", durable.GetChatGPTAccountID())
		require.Equal(t, "synthetic-token-b", result.Account.GetOpenAIAccessToken())
		require.False(t, result.Refreshed, "a discarded stale refresh must not be published as committed")
	})

	t.Run("normal_same_workspace_rotation", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		repo.account.Credentials["expires_at"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		selected, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		credentials := openAIIdentityMutationCredentials("a")
		credentials["access_token"] = "synthetic-rotated-token-a"
		cache := newOpenAITokenCacheStub()
		executor := &openAIIdentityMutationExecutor{&refreshAPIExecutorStub{needsRefresh: true, credentials: credentials}}
		provider := NewOpenAITokenProvider(repo, cache, nil)
		provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), executor)
		gateway := &OpenAIGatewayService{accountRepo: repo, openAITokenProvider: provider}
		request := buildOpenAIIdentityMutationRequest(t, gateway, selected)
		require.Equal(t, "Bearer synthetic-rotated-token-a", request.Header.Get("Authorization"))
		require.Equal(t, "synthetic-workspace-a", request.Header.Get("Chatgpt-Account-Id"))
		// A second old scheduling snapshot may consume the verified background
		// rotation from the cache without mixing workspaces or refreshing twice.
		request = buildOpenAIIdentityMutationRequest(t, gateway, selected)
		require.Equal(t, "Bearer synthetic-rotated-token-a", request.Header.Get("Authorization"))
		require.Equal(t, 1, executor.refreshCalls)
	})

	t.Run("refresh_result_cannot_change_the_callers_workspace", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		repo.account.Credentials["expires_at"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		selected, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		admin := &adminServiceImpl{accountRepo: repo}
		executor := &openAIIdentityMutationExecutor{&refreshAPIExecutorStub{
			needsRefresh: true,
			credentials:  openAIIdentityMutationCredentials("a"),
			onRefresh: func() {
				_, err := admin.UpdateAccount(ctx, selected.ID, &UpdateAccountInput{Credentials: openAIIdentityMutationCredentials("b")})
				require.NoError(t, err)
			},
		}}
		cache := newOpenAITokenCacheStub()
		provider := NewOpenAITokenProvider(repo, cache, nil)
		provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), executor)
		token, err := provider.GetAccessToken(ctx, selected)
		require.ErrorIs(t, err, errOpenAITokenIdentityChanged)
		require.Empty(t, token)
		require.Equal(t, "synthetic-token-b", repo.account.GetOpenAIAccessToken())
	})

	t.Run("admin_reauthorization_after_cas", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		selected, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		admin := &adminServiceImpl{accountRepo: repo}
		repo.afterCAS = func() {
			_, err := admin.UpdateAccount(ctx, selected.ID, &UpdateAccountInput{Credentials: openAIIdentityMutationCredentials("b")})
			require.NoError(t, err)
		}
		api := NewOAuthRefreshAPI(repo, nil)
		result, err := api.RefreshIfNeeded(ctx, selected, &refreshAPIExecutorStub{needsRefresh: true, credentials: openAIIdentityMutationCredentials("a")}, time.Minute)
		require.NoError(t, err)
		require.False(t, result.Refreshed)
		require.Nil(t, result.NewCredentials)
		require.Equal(t, "synthetic-token-b", result.Account.GetOpenAIAccessToken())
	})

	t.Run("cas_failure_does_not_publish_or_overwrite", func(t *testing.T) {
		ctx := context.Background()
		repo := newOpenAIIdentityMutationRepo()
		repo.account.Credentials["expires_at"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		selected, err := repo.GetByID(ctx, repo.account.ID)
		require.NoError(t, err)
		repo.casErr = errors.New("synthetic CAS write failure")
		cache := newOpenAITokenCacheStub()
		provider := NewOpenAITokenProvider(repo, cache, nil)
		provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), &openAIIdentityMutationExecutor{&refreshAPIExecutorStub{
			needsRefresh: true, credentials: openAIIdentityMutationCredentials("a"),
		}})
		token, err := provider.GetAccessToken(ctx, selected)
		var containment *providerCycleContainmentRefreshError
		require.ErrorAs(t, err, &containment)
		require.Empty(t, token, "an ambiguous rotating-token persistence result must not fall back to the old bearer")
		require.Equal(t, "synthetic-token-a", repo.account.GetOpenAIAccessToken())
	})
}
