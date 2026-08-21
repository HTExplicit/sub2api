//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const cindyImageRollbackValidationHelperEnv = "SUB2API_CINDY_IMAGE_ROLLBACK_VALIDATION_HELPER"

type cindyResponsesImageValidationUpstream struct {
	service.HTTPUpstream
	mu     sync.Mutex
	bodies [][]byte
}

func (u *cindyResponsesImageValidationUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.capture(req)
}

func (u *cindyResponsesImageValidationUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.capture(req)
}

func (u *cindyResponsesImageValidationUpstream) capture(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.bodies = append(u.bodies, append([]byte(nil), body...))
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_image_validation","object":"response","status":"completed","model":"gpt-5.6-luna","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}, nil
}

func (u *cindyResponsesImageValidationUpstream) snapshot() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	result := make([][]byte, len(u.bodies))
	for i := range u.bodies {
		result[i] = append([]byte(nil), u.bodies[i]...)
	}
	return result
}

func newCindyResponsesImageValidationHandler(
	t *testing.T,
	account service.Account,
) (*OpenAIGatewayHandler, *cindyResponsesImageValidationUpstream, *concurrencyCacheMock, int64) {
	t.Helper()
	groupID := int64(58100)
	account.Concurrency = 1
	account.Priority = 0
	repo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
	upstream := &cindyResponsesImageValidationUpstream{}
	concurrencyCache := &concurrencyCacheMock{
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	concurrencyService := service.NewConcurrencyService(concurrencyCache)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	billingService := service.NewBillingService(cfg, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrencyService,
		billingService, nil, billingCache, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(
		gateway, concurrencyService, billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	return h, upstream, concurrencyCache, groupID
}

func newCindyResponsesImageValidationContext(
	t *testing.T,
	groupID int64,
	strict bool,
	body []byte,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	apiKey := &service.APIKey{
		ID: 58101, GroupID: &groupID, Status: service.StatusActive,
		User: &service.User{ID: 58102, Status: service.StatusActive},
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI,
			ProviderProfile: service.ProviderProfileCindyLaxaV1, Status: service.StatusActive,
			StrictCindyKnown: true, StrictCindy: strict,
			AllowImageGeneration: true, RateMultiplier: 1,
		},
	}
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.User.ID, Concurrency: 0})
	return c, recorder
}

func cindyResponsesImageValidationAccount(cindy bool) service.Account {
	baseURL := "https://compat.example"
	extra := map[string]any{
		"openai_passthrough":    true,
		"openai_responses_mode": "force_responses",
	}
	if cindy {
		baseURL = "https://api.laxarouter.ai"
		extra = nil
	}
	account := service.Account{
		ID: 58103, Name: "image-validation", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": baseURL},
		Extra:       extra,
	}
	if cindy {
		account.Platform = service.PlatformCindy
		account.WirePlatform = service.WirePlatformOpenAI
		account.ProviderProfile = service.ProviderProfileCindyLaxaV1
	}
	return account
}

func TestStrictCindyResponsesImageRequestsNormalizeVerifiedWireControls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name              string
		body              string
		wantTopLevelModel string
	}{
		{
			name:              "nested image tool",
			body:              `{"model":"gpt-5.6-luna","input":"Create a small red square on white.","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2","size":"1024x1024","quality":"low","n":1}],"tool_choice":{"type":"image_generation"}}`,
			wantTopLevelModel: "openai/gpt-5.6-luna",
		},
		{
			name:              "top level image bridge",
			body:              `{"model":"gpt-image-2","input":"Create a small red square on white.","stream":false,"size":"1024x1024","quality":"low","n":1}`,
			wantTopLevelModel: "openai/gpt-5.6-luna",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h, upstream, _, groupID := newCindyResponsesImageValidationHandler(t, cindyResponsesImageValidationAccount(true))
			c, recorder := newCindyResponsesImageValidationContext(t, groupID, true, []byte(test.body))

			h.Responses(c)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			calls := upstream.snapshot()
			require.Len(t, calls, 1)
			require.Equal(t, test.wantTopLevelModel, gjson.GetBytes(calls[0], "model").String())
			require.Equal(t, "image_generation", gjson.GetBytes(calls[0], "tools.0.type").String())
			require.False(t, gjson.GetBytes(calls[0], "tools.0.action").Exists())
			require.Equal(t, "openai/gpt-image-2", gjson.GetBytes(calls[0], "tools.0.model").String())
			require.False(t, gjson.GetBytes(calls[0], "n").Exists())
			require.False(t, gjson.GetBytes(calls[0], "tools.0.n").Exists())
		})
	}
}

func TestStrictCindyResponsesTopLevelLiveIDRejectsUnverifiedControls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "size", body: `{"model":"openai/gpt-image-2","input":"draw","size":"1536x1024","quality":"low","n":1}`, want: "size"},
		{name: "quality", body: `{"model":"openai/gpt-image-2","input":"draw","size":"1024x1024","quality":"high","n":1}`, want: "quality"},
		{name: "count", body: `{"model":"openai/gpt-image-2","input":"draw","size":"1024x1024","quality":"low","n":2}`, want: "n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h, upstream, _, groupID := newCindyResponsesImageValidationHandler(t, cindyResponsesImageValidationAccount(true))
			c, recorder := newCindyResponsesImageValidationContext(t, groupID, true, []byte(test.body))

			h.Responses(c)

			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
			require.Contains(t, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(), test.want)
			require.Empty(t, upstream.snapshot())
		})
	}
}

func TestResponsesCindyGroupRejectsUnknownNestedImageModelBeforeConcurrencyAcquire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, upstream, concurrencyCache, groupID := newCindyResponsesImageValidationHandler(t, cindyResponsesImageValidationAccount(true))
	body := []byte(`{"model":"gpt-5.6-luna","input":"draw","tools":[{"type":"image_generation","model":"unknown-image"}],"tool_choice":{"type":"image_generation"}}`)
	c, recorder := newCindyResponsesImageValidationContext(t, groupID, false, body)

	h.Responses(c)

	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	require.Equal(t, "model_not_found", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Empty(t, upstream.snapshot())
	require.Equal(t, int32(0), atomic.LoadInt32(&concurrencyCache.releaseAccountCalled))
}

func TestResponsesCindyGroupRejectsInvalidNestedControlsBeforeConcurrencyAcquire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, upstream, concurrencyCache, groupID := newCindyResponsesImageValidationHandler(t, cindyResponsesImageValidationAccount(true))
	body := []byte(`{"model":"gpt-5.6-luna","input":"draw","tools":[{"type":"image_generation","model":"gpt-image-2","quality":"high"}],"tool_choice":{"type":"image_generation"}}`)
	c, recorder := newCindyResponsesImageValidationContext(t, groupID, false, body)

	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Contains(t, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(), "quality")
	require.Empty(t, upstream.snapshot())
	require.Equal(t, int32(0), atomic.LoadInt32(&concurrencyCache.releaseAccountCalled))
}

func TestResponsesSelectedNonCindyPreservesNestedImageBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := cindyResponsesImageValidationAccount(false)
	body := []byte(`{"model":"gpt-5.6-luna","input":"draw","tools":[{"type":"image_generation","model":"unknown-image","quality":"high"}],"tool_choice":{"type":"image_generation"}}`)
	direct, err := resolveSelectedCindyResponsesImageTools(&account, body)
	require.NoError(t, err)
	require.Equal(t, body, direct)

	h, upstream, concurrencyCache, groupID := newCindyResponsesImageValidationHandler(t, account)
	c, recorder := newCindyResponsesImageValidationContext(t, groupID, false, body)
	apiKeyValue, exists := c.Get(string(middleware2.ContextKeyAPIKey))
	require.True(t, exists)
	apiKey := apiKeyValue.(*service.APIKey)
	apiKey.Group.Platform = service.PlatformOpenAI
	apiKey.Group.WirePlatform = ""
	apiKey.Group.ProviderProfile = ""
	h.Responses(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	calls := upstream.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, "unknown-image", gjson.GetBytes(calls[0], "tools.0.model").String())
	require.Equal(t, "high", gjson.GetBytes(calls[0], "tools.0.quality").String())
	require.Equal(t, int32(1), atomic.LoadInt32(&concurrencyCache.releaseAccountCalled))
}

func TestCindyImageRollbackPreservesLegacyResponsesImageToolRequest(t *testing.T) {
	tests := []struct {
		name           string
		catalogEnabled string
		bridgeEnabled  string
	}{
		{name: "responses image phase disabled", catalogEnabled: "true", bridgeEnabled: "false"},
		{name: "catalog phase disabled keeps responses image independent", catalogEnabled: "false", bridgeEnabled: "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestCindyImageRollbackValidationHelper$")
			cmd.Env = append(withoutCindyImageValidationEnv(os.Environ()),
				service.CindyCapabilityCatalogEnabledEnv+"="+test.catalogEnabled,
				service.CindyResponsesImageBridgeEnabledEnv+"="+test.bridgeEnabled,
				cindyImageRollbackValidationHelperEnv+"=1",
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated image rollback test failed: %v\n%s", err, output)
			}
		})
	}
}

func TestCindyImageRollbackValidationHelper(t *testing.T) {
	if os.Getenv(cindyImageRollbackValidationHelperEnv) == "" {
		t.Skip("subprocess helper")
	}
	gin.SetMode(gin.TestMode)
	account := cindyResponsesImageValidationAccount(true)
	body := []byte(`{"model":"gpt-5.6-luna","input":"draw","tools":[{"type":"image_generation","model":"unknown-image","quality":"high"}],"tool_choice":{"type":"image_generation"}}`)
	h, upstream, concurrencyCache, groupID := newCindyResponsesImageValidationHandler(t, account)
	c, recorder := newCindyResponsesImageValidationContext(t, groupID, false, body)

	h.Responses(c)

	if service.CindyResponsesImageBridgeFeatureEnabled() {
		require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
		require.Equal(t, "model_not_found", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
		require.Empty(t, upstream.snapshot())
		require.Equal(t, int32(1), atomic.LoadInt32(&concurrencyCache.releaseAccountCalled))
		return
	}

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	calls := upstream.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, "unknown-image", gjson.GetBytes(calls[0], "tools.0.model").String())
	require.Equal(t, "high", gjson.GetBytes(calls[0], "tools.0.quality").String())
	require.Equal(t, int32(1), atomic.LoadInt32(&concurrencyCache.releaseAccountCalled))
}

func withoutCindyImageValidationEnv(environment []string) []string {
	blocked := map[string]struct{}{
		strings.ToUpper(service.CindyCapabilityCatalogEnabledEnv):    {},
		strings.ToUpper(service.ImageStudioEnabledEnv):               {},
		strings.ToUpper(service.CindyImageStudioEnabledEnv):          {},
		strings.ToUpper(service.CindyResponsesImageBridgeEnabledEnv): {},
		strings.ToUpper(cindyImageRollbackValidationHelperEnv):       {},
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[strings.ToUpper(key)]; found {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
