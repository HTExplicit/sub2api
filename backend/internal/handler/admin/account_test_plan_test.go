package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cindy "github.com/Wei-Shaw/sub2api/internal/cindyprovider/catalog"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testextensions"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type testPlanOuterKey struct{}

type testPlanAdmin struct {
	service.AdminService
	account *service.Account
}

func (s testPlanAdmin) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}
func (s testPlanAdmin) GetAccountsByIDs(context.Context, []int64) ([]*service.Account, error) {
	return []*service.Account{s.account}, nil
}

type testPlanOperations struct {
	rejectAll bool
	calls     []extensionv1.Invocation
	contexts  []any
}

func (f *testPlanOperations) InvokeOperation(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	f.calls = append(f.calls, in)
	f.contexts = append(f.contexts, ctx.Value(testPlanOuterKey{}))
	if f.rejectAll {
		return extensionv1.Result{}, service.ErrExtensionOperationDisabled
	}
	if in.Operation == "cindy.catalog" {
		module := cindy.New()
		if err := module.ApplyConfig(ctx, []byte(`{"catalog_enabled":true}`)); err != nil {
			return extensionv1.Result{}, err
		}
		result, err := module.Invoke(ctx, in)
		result.PluginID = 701
		return result, err
	}
	return extensionv1.Result{}, service.ErrExtensionOperationDisabled
}

func TestAccountTestPlanOrdinaryAndLegacyKeepCoreWithoutPlugins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		baseURL string
		legacy  bool
	}{
		{"ordinary", "https://api.openai.com", false},
		{"legacy_projection", "https://api.laxarouter.ai", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &testPlanOperations{rejectAll: true}
			service.ConfigureNativePolicyOperations(fixture)
			t.Cleanup(testextensions.Install)
			account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": tc.baseURL, "model_mapping": map[string]any{"plain-id": "plain-wire"}}}
			require.False(t, service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials))
			require.Equal(t, tc.legacy, service.IsLegacyCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials))
			// Only local mapping metadata is available: no test/discovery client,
			// model IO route, or live extension is installed in this fixture.
			h := &AccountHandler{adminService: testPlanAdmin{account: account}}
			require.Nil(t, h.accountTestService)
			router := gin.New()
			router.GET("/accounts/:id/models", h.GetAvailableModels)
			read := func(suffix string) json.RawMessage {
				out := httptest.NewRecorder()
				router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/accounts/42/models"+suffix, nil))
				require.Equal(t, http.StatusOK, out.Code, out.Body.String())
				var body struct {
					Data json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
				return body.Data
			}
			oldData := read("")
			require.True(t, len(oldData) > 0 && oldData[0] == '[', "old GET must remain an array")
			var oldModels []map[string]any
			require.NoError(t, json.Unmarshal(oldData, &oldModels))
			require.Len(t, oldModels, 1)
			require.Equal(t, "plain-id", oldModels[0]["id"])
			oldCalls := fixture.calls
			fixture.calls = nil

			var plan accountTestPlanView
			require.NoError(t, json.Unmarshal(read("?view=account-test-plan-v1"), &plan))
			require.Equal(t, 1, plan.SchemaVersion)
			require.Equal(t, int64(42), plan.AccountID)
			require.Equal(t, "openai", plan.WirePlatform)
			require.Equal(t, oldModels, plan.Models)
			require.Equal(t, []string{"plain-id"}, plan.ModeViews["default"].ModelIDs)
			require.Equal(t, "plain-id", plan.ModeViews["default"].DefaultModelID)
			require.Empty(t, plan.PolicyStamp)
			require.Equal(t, oldCalls, fixture.calls, "the view must not add calls or change optional metadata queries relative to the same legacy GET")
			if !tc.legacy {
				require.Empty(t, fixture.calls, "ordinary OpenAI has no Cindy or account-tools dependency")
				return
			}
			// Legacy reasoning metadata already makes best-effort compatibility
			// queries. They may fail without blocking either GET. Requiring zero
			// such calls would wrongly change the existing runtime boundary.
			require.NotEmpty(t, oldCalls)
			for _, invocation := range fixture.calls {
				require.Zero(t, invocation.AccountID, "only the unchanged optional metadata path is allowed")
				switch invocation.Operation {
				case "cindy.catalog":
					var query extensionv1.CindyCatalogQuery
					require.NoError(t, json.Unmarshal(invocation.Payload, &query))
					require.NotEqual(t, extensionv1.CindyAccountTestPlanMethodV1, query.Method)
					require.Contains(t, []string{"CindyCompatibilityMappedUpstreamModel", "CindyCompatibilityRoutingTarget"}, query.Method)
				case "image.features":
					// Existing compatibility metadata input, not a test invocation.
				default:
					t.Fatalf("unexpected operation (including account-tools or model IO): %s", invocation.Operation)
				}
			}
		})
	}
}

func TestAccountTestPlanCoreSelectionSmallMatrix(t *testing.T) {
	models := func(ids ...string) []map[string]any {
		rows := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, map[string]any{"id": id, "display_name": id})
		}
		return rows
	}
	for _, tc := range []struct {
		platform string
		ids      []map[string]any
		want     string
		order    []string
	}{
		{service.PlatformGemini, models("unknown-a", "gemini-2.5-flash-image", "gemini-3.1-flash-image", "unknown-b"), "unknown-a", []string{"gemini-3.1-flash-image", "gemini-2.5-flash-image", "unknown-a", "unknown-b"}},
		{service.PlatformGemini, models("gemini-2.5-pro", "gemini-2.0-flash"), "gemini-2.0-flash", []string{"gemini-2.5-pro", "gemini-2.0-flash"}},
		{service.PlatformAntigravity, models("sonnet-custom", "gemini-3.1-flash-image"), "sonnet-custom", []string{"gemini-3.1-flash-image", "sonnet-custom"}},
		{service.PlatformOpenAI, models("codex-auto-review", "first", "sonnet-custom"), "first", []string{"codex-auto-review", "first", "sonnet-custom"}},
	} {
		plan, err := ordinaryAccountTestPlan(&service.Account{ID: 42, Platform: tc.platform}, tc.ids)
		require.NoError(t, err)
		require.Equal(t, tc.order, plan.ModeViews["default"].ModelIDs)
		require.Equal(t, tc.want, plan.ModeViews["default"].DefaultModelID)
	}
	plan, err := ordinaryAccountTestPlan(&service.Account{ID: 42, Platform: service.PlatformGrok}, models("grok-4.3", "grok", "grok-4.5-custom", "grok-imagine", "grok-imagine-video"))
	require.NoError(t, err)
	require.Equal(t, "grok-4.3", plan.ModeViews["text"].DefaultModelID, "same automatic choice as batch tests: grok-4.5 when listed, else the first text model")
	require.Equal(t, []string{"grok-imagine"}, plan.ModeViews["image"].ModelIDs)
	require.Equal(t, []string{"grok-imagine-video"}, plan.ModeViews["video"].ModelIDs)
	for _, mode := range []string{"search", "tts", "stt", "realtime"} {
		require.Empty(t, plan.ModeViews[mode].ModelIDs)
		require.Empty(t, plan.ModeViews[mode].DefaultModelID)
	}
	_, err = accountTestPlanRequested("unknown-view")
	require.Error(t, err)
	_, err = ordinaryAccountTestPlan(&service.Account{ID: 42}, []map[string]any{{"id": "same"}, {"id": "same"}})
	require.Error(t, err)
}
