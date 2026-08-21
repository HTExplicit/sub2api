package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type codexModelsFailoverAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r codexModelsFailoverAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

func (r codexModelsFailoverAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	accounts := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform && account.IsSchedulable() {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (r codexModelsFailoverAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r codexModelsFailoverAccountRepo) CindyCodexModelsAccountReaderMarker() {}

type codexModelsFailoverHTTPUpstream struct {
	service.HTTPUpstream
	mu          sync.Mutex
	accountIDs  []int64
	firstErr    error
	firstStatus int
	firstBody   string
	statuses    map[int64]int
	bodies      map[int64]string
}

func (u *codexModelsFailoverHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	if body, ok := u.bodies[accountID]; ok {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}

	status, hasStatus := u.statuses[accountID]
	if accountID == 1 || hasStatus {
		if u.firstErr != nil {
			return nil, u.firstErr
		}
		if u.firstBody != "" && !hasStatus {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(u.firstBody)),
			}, nil
		}
		if !hasStatus {
			status = u.firstStatus
		}
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"message":"No available OpenAI accounts","type":"upstream_error"}}`,
			)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"models":[{"slug":"gpt-5.6-sol"}]}`)),
	}, nil
}

func (u *codexModelsFailoverHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestCodexModelsCanceledRequestDoesNotWriteResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil).WithContext(ctx)

	h := &OpenAIGatewayHandler{}
	h.CodexModels(c)

	if c.Writer.Written() {
		t.Fatalf("canceled request wrote an HTTP response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestCodexModelsStrictCindyProjectsVerifiedPublicCatalog(t *testing.T) {
	if !runCodexCatalogEnabledHandlerTest(t) {
		return
	}
	gin.SetMode(gin.TestMode)
	groupID := int64(43)
	accounts := []service.Account{{
		ID: 1, Name: "cindy", Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Priority: 0, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-cindy",
			"base_url": "https://api.laxarouter.ai",
		},
	}}
	upstream := &codexModelsFailoverHTTPUpstream{firstBody: `{"models":[{"slug":"must-not-be-fetched"}]}`}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		codexModelsFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := &OpenAIGatewayHandler{gatewayService: gatewayService, maxAccountSwitches: 3}
	group := &service.Group{
		ID: groupID, Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		StrictCindyKnown: true, StrictCindy: true,
	}
	recorder := performCodexModelsRequestForGroup(t, handler, group)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := upstream.calls(); len(got) != 0 {
		t.Fatalf("strict Cindy manifest must be local; upstream calls=%v", got)
	}
	slugs := decodeCodexModelSlugs(t, recorder.Body.Bytes())
	if got, want := strings.Join(slugs, ","), strings.Join(service.CindyCodexPublicModelIDs(), ","); got != want {
		t.Fatalf("strict Cindy slugs: got %v, want %v", slugs, service.CindyCodexPublicModelIDs())
	}
	assertCodexManifestOmitsNonResponsesCindyIDs(t, recorder.Body.String())

	outdated := performCodexModelsRequestForGroupWithVersion(t, handler, group, "0.146.0")
	if outdated.Code != http.StatusUpgradeRequired {
		t.Fatalf("outdated client status: got %d, want %d; body=%s", outdated.Code, http.StatusUpgradeRequired, outdated.Body.String())
	}
	if !strings.Contains(outdated.Body.String(), service.CindyCodexMinimumClientVersion) {
		t.Fatalf("outdated client response must name minimum version; body=%s", outdated.Body.String())
	}
	if got := upstream.calls(); len(got) != 0 {
		t.Fatalf("outdated strict Cindy request must not reach upstream; calls=%v", got)
	}
}

func TestCodexModelsMixedGroupDeterministicallyUnionsOrdinaryAndVerifiedCindyResponses(t *testing.T) {
	if !runCodexCatalogEnabledHandlerTest(t) {
		return
	}
	gin.SetMode(gin.TestMode)
	groupID := int64(44)
	accounts := []service.Account{
		{
			ID: 1, Name: "cindy", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 0, Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-cindy",
				"base_url": "https://api.laxarouter.ai",
			},
		},
		{
			ID: 3, Name: "ordinary-later-id", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 1, Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-ordinary-3",
				"base_url": "https://ordinary-3.example/v1",
			},
		},
		{
			ID: 2, Name: "ordinary", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: 1, Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-ordinary",
				"base_url": "https://ordinary.example/v1",
			},
		},
	}
	const ordinaryBody = `{"models":[` +
		`{"slug":"ordinary-model","display_name":"Ordinary"},` +
		`{"slug":"openai/gpt-5.6-sol"},` +
		`{"slug":"gpt-5.4"},` +
		`{"slug":"gpt-5.4-mini"},` +
		`{"slug":"deepseek/deepseek-v4-pro"},` +
		`{"slug":"anthropic/claude-opus-5"},` +
		`{"slug":"x-ai/grok-4.6"},` +
		`{"slug":"google/gemini-3-pro-image"},` +
		`{"slug":"openai/gpt-image-2"}` +
		`]}`
	upstream := &codexModelsFailoverHTTPUpstream{bodies: map[int64]string{
		2: ordinaryBody,
		3: `{"models":[{"slug":"random-ordinary-manifest"}]}`,
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		codexModelsFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := &OpenAIGatewayHandler{gatewayService: gatewayService, maxAccountSwitches: 3}
	group := &service.Group{
		ID: groupID, Platform: service.PlatformOpenAI,
		StrictCindyKnown: true, StrictCindy: false,
	}
	first := performCodexModelsRequestForGroup(t, handler, group)
	second := performCodexModelsRequestForGroup(t, handler, group)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("mixed statuses: first=%d second=%d; first body=%s second body=%s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("mixed manifest changed across identical requests: first=%s second=%s", first.Body.String(), second.Body.String())
	}
	if got := upstream.calls(); !equalInt64Slices(got, []int64{2}) {
		t.Fatalf("mixed manifest must fetch only the ordinary account: got calls %v, want [2]", got)
	}

	slugs := decodeCodexModelSlugs(t, first.Body.Bytes())
	ordinarySlugs := []string{
		"ordinary-model",
		"openai/gpt-5.6-sol",
		"gpt-5.4",
		"gpt-5.4-mini",
		"deepseek/deepseek-v4-pro",
		"anthropic/claude-opus-5",
		"x-ai/grok-4.6",
		"google/gemini-3-pro-image",
		"openai/gpt-image-2",
	}
	want := append(append([]string(nil), ordinarySlugs...), service.CindyCodexPublicModelIDs()...)
	sort.Strings(slugs)
	sort.Strings(want)
	if got, expected := strings.Join(slugs, ","), strings.Join(want, ","); got != expected {
		t.Fatalf("mixed slugs: got %v, want %v", slugs, want)
	}
	for _, slug := range ordinarySlugs {
		if !strings.Contains(first.Body.String(), `"`+slug+`"`) {
			t.Fatalf("mixed manifest dropped ordinary slug %q: %s", slug, first.Body.String())
		}
	}
}

func TestCodexModelsCatalogEnabledNonCindyPreservesLegacyManifest(t *testing.T) {
	if !runCodexCatalogEnabledHandlerTest(t) {
		return
	}
	gin.SetMode(gin.TestMode)
	groupID := int64(45)
	account := service.Account{
		ID: 1, Name: "ordinary", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-ordinary",
			"base_url": "https://ordinary.example/v1",
		},
	}
	const legacyBody = `{"models":[{"slug":"openai/gpt-5.6-sol"},{"slug":"gpt-5.4"},{"slug":"deepseek/deepseek-v4-pro"}],"metadata":{"legacy":true}}`
	upstream := &codexModelsFailoverHTTPUpstream{firstBody: legacyBody}
	gatewayService := service.NewOpenAIGatewayService(
		codexModelsFailoverAccountRepo{accounts: []service.Account{account}},
		nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := &OpenAIGatewayHandler{gatewayService: gatewayService, maxAccountSwitches: 3}
	recorder := performCodexModelsRequestForGroup(t, handler, &service.Group{
		ID: groupID, Platform: service.PlatformOpenAI,
		StrictCindyKnown: true, StrictCindy: false,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := upstream.calls(); !equalInt64Slices(got, []int64{1}) {
		t.Fatalf("ordinary upstream calls: got %v, want [1]", got)
	}
	if got := recorder.Body.String(); got != legacyBody {
		t.Fatalf("catalog-enabled ordinary manifest changed: got %s, want %s", got, legacyBody)
	}
}

const codexCatalogEnabledHandlerTestHelperEnv = "SUB2API_CODEX_MODELS_HANDLER_CATALOG_TEST_HELPER"

func runCodexCatalogEnabledHandlerTest(t *testing.T) bool {
	t.Helper()
	if os.Getenv(codexCatalogEnabledHandlerTestHelperEnv) == "1" {
		return true
	}
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.EqualFold(name, service.CindyCapabilityCatalogEnabledEnv) ||
			strings.EqualFold(name, service.ImageStudioEnabledEnv) ||
			strings.EqualFold(name, service.CindyImageStudioEnabledEnv) ||
			strings.EqualFold(name, codexCatalogEnabledHandlerTestHelperEnv) {
			continue
		}
		environment = append(environment, entry)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(environment,
		service.CindyCapabilityCatalogEnabledEnv+"=true",
		service.ImageStudioEnabledEnv+"=true",
		codexCatalogEnabledHandlerTestHelperEnv+"=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated catalog-enabled %s failed: %v\n%s", t.Name(), err, output)
	}
	return false
}

func decodeCodexModelSlugs(t *testing.T, body []byte) []string {
	t.Helper()
	var manifest struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("decode Codex manifest: %v", err)
	}
	slugs := make([]string, 0, len(manifest.Models))
	for _, model := range manifest.Models {
		slugs = append(slugs, model.Slug)
	}
	return slugs
}

func assertCodexManifestOmitsNonResponsesCindyIDs(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"openai/gpt-5.6-sol",
		"gpt-5.4",
		"gpt-5.4-mini",
		"deepseek-v4-pro",
		"seed-2.1-pro",
		"claude-opus-5",
		"grok-4.6",
		"gemini-3-pro-image",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Codex manifest leaked forbidden ID %q: %s", forbidden, body)
		}
	}
}

func TestCompositeCodexModelsReusesExistingManifestSelection(t *testing.T) {
	handler, upstream, groupID := newCodexModelsFailoverTestHandler(http.StatusServiceUnavailable)

	recorder := performCodexModelsRequestForPlatform(t, handler, groupID, service.PlatformComposite)

	if got, want := upstream.calls(), []int64{1, 2}; !equalInt64Slices(got, want) {
		t.Fatalf("upstream account calls: got %v, want %v", got, want)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestCodexModelsFailsOverFromRetryableUpstreamStatus(t *testing.T) {
	retryableStatuses := []int{
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	}
	for _, status := range retryableStatuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler, upstream, groupID := newCodexModelsFailoverTestHandler(status)
			recorder := performCodexModelsRequest(t, handler, groupID)

			want := []int64{1, 2}
			if got := upstream.calls(); !equalInt64Slices(got, want) {
				t.Fatalf("upstream account calls: got %v, want %v", got, want)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if got, want := recorder.Body.String(), `{"models":[{"slug":"gpt-5.6-sol"}]}`; got != want {
				t.Fatalf("body: got %q, want %q", got, want)
			}
		})
	}
}

func TestCodexModelsFailsOverFromUpstreamTransportError(t *testing.T) {
	handler, upstream, groupID := newCodexModelsFailoverTestHandler(http.StatusServiceUnavailable)
	upstream.firstErr = &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: errors.New("connection reset"),
	}
	recorder := performCodexModelsRequest(t, handler, groupID)

	if got, want := upstream.calls(), []int64{1, 2}; !equalInt64Slices(got, want) {
		t.Fatalf("upstream account calls: got %v, want %v", got, want)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestCodexModelsFailsOverFromInvalidManifestEnvelope(t *testing.T) {
	handler, upstream, groupID := newCodexModelsFailoverTestHandler(http.StatusOK)
	upstream.firstBody = `{"object":"list","data":[]}`
	recorder := performCodexModelsRequest(t, handler, groupID)

	if got, want := upstream.calls(), []int64{1, 2}; !equalInt64Slices(got, want) {
		t.Fatalf("upstream account calls: got %v, want %v", got, want)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got, want := recorder.Body.String(), `{"models":[{"slug":"gpt-5.6-sol"}]}`; got != want {
		t.Fatalf("body: got %q, want %q", got, want)
	}
}

func TestCodexModelsDoesNotFailOverFromPermanentUpstreamStatus(t *testing.T) {
	statuses := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		600,
	}
	for _, status := range statuses {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			handler, upstream, groupID := newCodexModelsFailoverTestHandler(status)
			recorder := performCodexModelsRequest(t, handler, groupID)

			if got, want := upstream.calls(), []int64{1}; !equalInt64Slices(got, want) {
				t.Fatalf("upstream account calls: got %v, want %v", got, want)
			}
			if recorder.Code != http.StatusBadGateway {
				t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
			}
		})
	}
}

func TestCodexModelsDoesNotFailOverFromUpstreamConfigurationError(t *testing.T) {
	handler, upstream, groupID := newCodexModelsFailoverTestHandler(http.StatusServiceUnavailable)
	upstream.firstErr = errors.New("invalid proxy URL")
	recorder := performCodexModelsRequest(t, handler, groupID)

	if got, want := upstream.calls(), []int64{1}; !equalInt64Slices(got, want) {
		t.Fatalf("upstream account calls: got %v, want %v", got, want)
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
	}
}

func TestCodexModelsReturnsLastUpstreamErrorWhenAccountsAreExhausted(t *testing.T) {
	handler, upstream, groupID := newCodexModelsFailoverTestHandler(http.StatusServiceUnavailable)
	upstream.statuses = map[int64]int{
		1: http.StatusServiceUnavailable,
		2: http.StatusGatewayTimeout,
	}
	recorder := performCodexModelsRequest(t, handler, groupID)

	if got, want := upstream.calls(), []int64{1, 2}; !equalInt64Slices(got, want) {
		t.Fatalf("upstream account calls: got %v, want %v", got, want)
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "upstream error 504") {
		t.Fatalf("body does not preserve the last upstream error: %s", body)
	}
}

func TestCodexModelsHonorsAccountSwitchLimit(t *testing.T) {
	handler, upstream, groupID := newCodexModelsFailoverTestHandlerWithAccountCount(http.StatusServiceUnavailable, 4, 2)
	upstream.statuses = map[int64]int{
		1: http.StatusServiceUnavailable,
		2: http.StatusBadGateway,
		3: http.StatusGatewayTimeout,
		4: http.StatusInternalServerError,
	}
	recorder := performCodexModelsRequest(t, handler, groupID)

	if got, want := upstream.calls(), []int64{1, 2, 3}; !equalInt64Slices(got, want) {
		t.Fatalf("upstream account calls: got %v, want %v", got, want)
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want %d; body=%s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "upstream error 504") {
		t.Fatalf("body does not preserve the limit-ending upstream error: %s", body)
	}
}

func newCodexModelsFailoverTestHandler(firstStatus int) (*OpenAIGatewayHandler, *codexModelsFailoverHTTPUpstream, int64) {
	return newCodexModelsFailoverTestHandlerWithAccountCount(firstStatus, 2, 3)
}

func newCodexModelsFailoverTestHandlerWithAccountCount(firstStatus, accountCount, maxSwitches int) (*OpenAIGatewayHandler, *codexModelsFailoverHTTPUpstream, int64) {
	gin.SetMode(gin.TestMode)
	groupID := int64(42)
	accounts := make([]service.Account, 0, accountCount)
	for i := 1; i <= accountCount; i++ {
		accounts = append(accounts, service.Account{
			ID:          int64(i),
			Name:        fmt.Sprintf("upstream-%d", i),
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Priority:    i - 1,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  fmt.Sprintf("sk-%d", i),
				"base_url": fmt.Sprintf("https://upstream-%d.example/v1", i),
			},
		})
	}
	upstream := &codexModelsFailoverHTTPUpstream{firstStatus: firstStatus}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		codexModelsFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	return &OpenAIGatewayHandler{gatewayService: gatewayService, maxAccountSwitches: maxSwitches}, upstream, groupID
}

func performCodexModelsRequest(t *testing.T, handler *OpenAIGatewayHandler, groupID int64) *httptest.ResponseRecorder {
	return performCodexModelsRequestForPlatform(t, handler, groupID, service.PlatformOpenAI)
}

func performCodexModelsRequestForPlatform(t *testing.T, handler *OpenAIGatewayHandler, groupID int64, platform string) *httptest.ResponseRecorder {
	t.Helper()
	return performCodexModelsRequestForGroup(t, handler, &service.Group{ID: groupID, Platform: platform})
}

func performCodexModelsRequestForGroup(t *testing.T, handler *OpenAIGatewayHandler, group *service.Group) *httptest.ResponseRecorder {
	return performCodexModelsRequestForGroupWithVersion(t, handler, group, service.CindyCodexMinimumClientVersion)
}

func performCodexModelsRequestForGroupWithVersion(t *testing.T, handler *OpenAIGatewayHandler, group *service.Group, clientVersion string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version="+clientVersion, nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		GroupID: &group.ID,
		Group:   group,
	})

	handler.CodexModels(c)
	return recorder
}

func equalInt64Slices(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
