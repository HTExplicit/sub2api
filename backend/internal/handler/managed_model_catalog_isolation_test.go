package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestManagedModelCatalogIsolationSharedPrivateGroupPreservesPublicModelsAndCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, allowlist := range []bool{false, true} {
		name := "no_allowlist"
		if allowlist {
			name = "wide_allowlist"
		}
		t.Run(name, func(t *testing.T) {
			const groupID int64 = 55
			group := &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true}
			if allowlist {
				group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"*"}}
			}
			makeAccount := func(shared bool) service.Account {
				mapping := map[string]any{"Private-Model": "upstream-private"}
				if shared {
					mapping["s2pub-g33-m0123456789abcdef"] = "upstream-public-vip"
					mapping["S2PUB-g23-mabcdef0123456789"] = "upstream-public"
				}
				return service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID, 23, 33},
					Credentials: map[string]any{"api_key": "offline-fixture-never-send", "base_url": "https://example.invalid", "model_mapping": mapping},
					Extra:       map[string]any{service.ModelContextOverridesExtraKey: map[string]int64{"upstream-private": 333333}}}
			}
			for _, codex := range []bool{false, true} {
				catalogName := "ordinary"
				if codex {
					catalogName = "codex"
				}
				t.Run(catalogName, func(t *testing.T) {
					render := func(account service.Account) []byte {
						before, err := json.Marshal(account.Credentials)
						require.NoError(t, err)
						repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{groupID: {account}}}
						h := newGatewayModelsHandlerForTest(repo)
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
						c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
						if codex {
							h.CodexModels(c)
						} else {
							h.Models(c)
						}
						require.Equal(t, http.StatusOK, recorder.Code)
						after, err := json.Marshal(account.Credentials)
						require.NoError(t, err)
						require.Equal(t, before, after, "catalog rendering must not alter private or managed mappings")
						return recorder.Body.Bytes()
					}
					before := render(makeAccount(false))
					after := render(makeAccount(true))
					require.JSONEq(t, string(before), string(after), "adding internal mappings must not change the private catalog")
					require.NotContains(t, string(after), "s2pub-")
					require.NotContains(t, string(after), "S2PUB-")
					listKey, idKey := "data", "id"
					if codex {
						listKey, idKey = "models", "slug"
					}
					require.Equal(t, int64(1), gjson.GetBytes(after, listKey+".#").Int())
					require.Equal(t, "Private-Model", gjson.GetBytes(after, listKey+".0."+idKey).String())
					require.Equal(t, int64(333333), gjson.GetBytes(after, listKey+".0.context_window").Int())
					require.False(t, group.ManagedModelRoutes.Enabled)
				})
			}
		})
	}
}

func TestManagedModelCatalogIsolationOnlyInternalMappingsDoNotAddFallbackModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(55)
	account := service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://example.invalid", "model_mapping": map[string]any{"s2pub-g33-m0123456789abcdef": "upstream-target"}}}
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{groupID: {account}}})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, IsExclusive: true}})
	h.Models(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"object":"list","data":[]}`, recorder.Body.String())
}
