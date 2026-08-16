package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageStudioAPIKeyRepoStub struct {
	service.APIKeyRepository
	keys      []service.APIKey
	userID    int64
	status    string
	returnErr error
	listCalls int
}

func (s *imageStudioAPIKeyRepoStub) ListAllByUserID(_ context.Context, userID int64, filters service.APIKeyListFilters) ([]service.APIKey, error) {
	s.listCalls++
	s.userID = userID
	s.status = filters.Status
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return append([]service.APIKey(nil), s.keys...), nil
}

type imageStudioAccountRepoStub struct {
	service.AccountRepository
	byGroup map[int64][]service.Account
	errors  map[int64]error
	calls   map[int64]int
}

func (s *imageStudioAccountRepoStub) ListByGroup(_ context.Context, groupID int64) ([]service.Account, error) {
	if s.calls == nil {
		s.calls = make(map[int64]int)
	}
	s.calls[-groupID]++
	if err := s.errors[groupID]; err != nil {
		return nil, err
	}
	return append([]service.Account(nil), s.byGroup[groupID]...), nil
}

func (s *imageStudioAccountRepoStub) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]service.Account, error) {
	if s.calls == nil {
		s.calls = make(map[int64]int)
	}
	s.calls[groupID]++
	if err := s.errors[groupID]; err != nil {
		return nil, err
	}
	return append([]service.Account(nil), s.byGroup[groupID]...), nil
}

func (s *imageStudioAccountRepoStub) CindyGroupIdentityReaderMarker() {}

func newImageStudioEligibleKeysHandler(apiKeyRepo service.APIKeyRepository, accountRepo service.AccountRepository) *GatewayHandler {
	h := newGatewayModelsHandlerForTest(accountRepo)
	h.apiKeyService = service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, nil)
	return h
}

func TestImageStudioEligibleKeysReturnsOnlyCurrentUserEligibleKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const userID int64 = 7001
	eligibleGroup := &service.Group{
		ID: 7101, Name: "Cindy Images", Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true,
	}
	inactiveGroup := &service.Group{
		ID: 7102, Platform: service.PlatformOpenAI, Status: service.StatusDisabled, AllowImageGeneration: true,
	}
	imageDisabledGroup := &service.Group{
		ID: 7103, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: false,
	}
	ordinaryGroup := &service.Group{
		ID: 7104, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true,
	}
	mixedGroup := &service.Group{
		ID: 7105, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true,
	}
	apiKeyRepo := &imageStudioAPIKeyRepoStub{keys: []service.APIKey{
		{ID: 7201, UserID: userID, Key: "sk-user-image-1", Name: "image-1", Status: service.StatusActive, GroupID: &eligibleGroup.ID, Group: eligibleGroup},
		{ID: 7202, UserID: userID, Key: "sk-user-image-2", Name: "image-2", Status: service.StatusActive, GroupID: &eligibleGroup.ID, Group: eligibleGroup},
		{ID: 7203, UserID: userID, Key: "sk-disabled", Status: service.StatusAPIKeyDisabled, GroupID: &eligibleGroup.ID, Group: eligibleGroup},
		{ID: 7204, UserID: userID + 1, Key: "sk-other-user", Status: service.StatusActive, GroupID: &eligibleGroup.ID, Group: eligibleGroup},
		{ID: 7205, UserID: userID, Key: "sk-inactive-group", Status: service.StatusActive, GroupID: &inactiveGroup.ID, Group: inactiveGroup},
		{ID: 7206, UserID: userID, Key: "sk-image-disabled", Status: service.StatusActive, GroupID: &imageDisabledGroup.ID, Group: imageDisabledGroup},
		{ID: 7207, UserID: userID, Key: "sk-ordinary", Status: service.StatusActive, GroupID: &ordinaryGroup.ID, Group: ordinaryGroup},
		{ID: 7208, UserID: userID, Key: "sk-mixed", Status: service.StatusActive, GroupID: &mixedGroup.ID, Group: mixedGroup},
		{ID: 7209, UserID: userID, Key: "sk-expired", Status: service.StatusActive, ExpiresAt: timePointerForImageStudioTest(time.Now().Add(-time.Minute)), GroupID: &eligibleGroup.ID, Group: eligibleGroup},
		{ID: 7210, UserID: userID, Key: "sk-quota-exhausted", Status: service.StatusActive, Quota: 5, QuotaUsed: 5, GroupID: &eligibleGroup.ID, Group: eligibleGroup},
	}}
	accountRepo := &imageStudioAccountRepoStub{
		byGroup: map[int64][]service.Account{
			eligibleGroup.ID: {cindyGatewayModelAccountForTest(7301)},
			ordinaryGroup.ID: {{
				ID: 7302, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true,
				Credentials: map[string]any{"base_url": "https://ordinary.example.invalid"},
			}},
			mixedGroup.ID: {
				cindyGatewayModelAccountForTest(7303),
				{
					ID: 7304, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true,
					Credentials: map[string]any{"base_url": "https://ordinary.example.invalid"},
				},
			},
		},
	}
	h := newImageStudioEligibleKeysHandler(apiKeyRepo, accountRepo)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/image-studio/eligible-keys", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})

	h.ImageStudioEligibleKeys(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, apiKeyRepo.listCalls)
	require.Equal(t, userID, apiKeyRepo.userID)
	require.Equal(t, service.StatusActive, apiKeyRepo.status)
	require.Equal(t, 1, accountRepo.calls[-eligibleGroup.ID], "keys in one group must share one identity lookup")
	require.Equal(t, 1, accountRepo.calls[eligibleGroup.ID], "strict groups need one schedulability lookup")
	require.Equal(t, 1, accountRepo.calls[-ordinaryGroup.ID])
	require.Zero(t, accountRepo.calls[ordinaryGroup.ID], "ordinary groups must not reach schedulability discovery")
	require.Equal(t, 1, accountRepo.calls[-mixedGroup.ID])
	require.Zero(t, accountRepo.calls[mixedGroup.ID], "mixed groups must not be Image Studio eligible")
	var got struct {
		Data struct {
			Items []struct {
				APIKey       map[string]any                 `json:"api_key"`
				Capabilities []service.CindyModelCapability `json:"capabilities"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	require.Len(t, got.Data.Items, 2)
	require.Equal(t, []any{"sk-user-image-1", "sk-user-image-2"}, []any{
		got.Data.Items[0].APIKey["key"],
		got.Data.Items[1].APIKey["key"],
	})
	for _, item := range got.Data.Items {
		apiKeyFields := make([]string, 0, len(item.APIKey))
		for field := range item.APIKey {
			apiKeyFields = append(apiKeyFields, field)
		}
		require.ElementsMatch(t, []string{"id", "name", "key", "group_id", "group"}, apiKeyFields)
		group, ok := item.APIKey["group"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, map[string]any{"id": float64(eligibleGroup.ID), "name": eligibleGroup.Name}, group)
		require.NotEmpty(t, item.Capabilities)
		for _, capability := range item.Capabilities {
			require.Equal(t, service.CindyModelKindImage, capability.Kind)
		}
	}
	require.NotContains(t, recorder.Body.String(), "live_upstream")
	require.NotContains(t, recorder.Body.String(), "registry_id")
	require.NotContains(t, recorder.Body.String(), "7301")
	require.NotContains(t, recorder.Body.String(), "user_id")
	require.NotContains(t, recorder.Body.String(), "ip_whitelist")
	require.NotContains(t, recorder.Body.String(), "last_used_ip")
	require.NotContains(t, recorder.Body.String(), "quota_used")
	require.NotContains(t, recorder.Body.String(), `"account"`)
	require.NotContains(t, recorder.Body.String(), `"user"`)
}

func timePointerForImageStudioTest(value time.Time) *time.Time {
	return &value
}

func TestImageStudioEligibleKeysFailsClosedOnAccountLookupError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{
		ID: 7401, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true,
	}
	apiKeyRepo := &imageStudioAPIKeyRepoStub{keys: []service.APIKey{{
		ID: 7402, UserID: 7400, Key: "sk-user-image", Status: service.StatusActive, GroupID: &group.ID, Group: group,
	}}}
	accountRepo := &imageStudioAccountRepoStub{errors: map[int64]error{group.ID: errors.New("database unavailable")}}
	h := newImageStudioEligibleKeysHandler(apiKeyRepo, accountRepo)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/image-studio/eligible-keys", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7400})

	h.ImageStudioEligibleKeys(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "database unavailable")
}

func TestImageStudioEligibleKeysRequiresSessionAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/image-studio/eligible-keys", nil)

	newImageStudioEligibleKeysHandler(&imageStudioAPIKeyRepoStub{}, &imageStudioAccountRepoStub{}).ImageStudioEligibleKeys(c)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}
