package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	accountListRoute          = "/api/v1/admin/accounts"
	upstreamBillingRatesRoute = "/api/v1/admin/accounts/upstream-billing-rates"
)

// accountPageCall is one list call as the admin service received it: the scalar arguments of ListAccounts, or the
// filters of ListAccountsConsole when console is set.
type accountPageCall struct {
	page, pageSize                        int
	platform, accountType, status, search string
	groupID                               int64
	privacyMode, sortBy, sortOrder        string
	console                               *service.AccountConsoleFilters
}

// group is the group the call filtered by, whichever list method received it.
func (c accountPageCall) group() int64 {
	if c.console != nil {
		return c.console.GroupID
	}
	return c.groupID
}

// billingRatesAdmin records the account page each request resolves and answers every list call with rows and total.
type billingRatesAdmin struct {
	*accountTaxonomyHandlerStub
	calls   []accountPageCall
	rows    []service.Account
	total   int64
	listErr error
}

func (s *billingRatesAdmin) ListAccounts(_ context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode string, sortBy, sortOrder string) ([]service.Account, int64, error) {
	s.calls = append(s.calls, accountPageCall{
		page: page, pageSize: pageSize, platform: platform, accountType: accountType, status: status, search: search,
		groupID: groupID, privacyMode: privacyMode, sortBy: sortBy, sortOrder: sortOrder,
	})
	return s.rows, s.total, s.listErr
}

func (s *billingRatesAdmin) ListAccountsConsole(_ context.Context, page, pageSize int, filters service.AccountConsoleFilters) ([]service.Account, int64, error) {
	s.calls = append(s.calls, accountPageCall{page: page, pageSize: pageSize, console: &filters})
	return s.rows, s.total, s.listErr
}

func setupUpstreamBillingRatesRouter() (*gin.Engine, *billingRatesAdmin) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := &billingRatesAdmin{accountTaxonomyHandlerStub: newAccountTaxonomyHandlerStub()}
	handler := NewAccountHandler(admin, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET(accountListRoute, handler.List)
	router.GET(upstreamBillingRatesRoute, handler.GetUpstreamBillingRates)
	return router, admin
}

// requestAccountPage sends the query to the route and returns the response with the list calls the request made.
func requestAccountPage(router *gin.Engine, admin *billingRatesAdmin, route, query string) (*httptest.ResponseRecorder, []accountPageCall) {
	admin.calls = nil
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, route+"?"+query, nil))
	return recorder, admin.calls
}

func TestAccountHandlerGetUpstreamBillingRatesResolvesSamePageAsList(t *testing.T) {
	router, admin := setupUpstreamBillingRatesRouter()
	digestTerm := "sha256:" + strings.Repeat("ab", 32)
	over100Bytes := strings.Repeat("k", 120)
	for _, test := range []struct {
		name    string
		query   string
		console bool
	}{
		{name: "no_filter"},
		{name: "search", query: "search=" + digestTerm},
		{name: "search_over_100_bytes", query: "search=" + over100Bytes},
		{name: "group", query: "group_id=12"},
		{name: "ungrouped", query: "group_id=ungrouped"},
		{name: "privacy_and_billing_rate_sort", query: "privacy_mode=training_off&sort_by=upstream_billing_rate&sort_order=desc"},
		{name: "scalar_filters", query: "platform=openai&type=apikey&status=active"},
		{name: "page", query: "page=3&page_size=50"},
		{name: "statuses", query: "statuses=active,error", console: true},
		{name: "console_ungrouped", query: "plans=plus&group_id=ungrouped", console: true},
		{name: "console_search_over_100_bytes", query: "tags=3&search=" + over100Bytes, console: true},
		{name: "console_page", query: "page=2&page_size=5&account_ids=11,12", console: true},
		{
			name: "full_console",
			query: "platforms=openai,anthropic&types=apikey,oauth&statuses=active,error&plans=plus,team" +
				"&proxies=direct,4&folder=uncategorized,7&tags=3,9&account_ids=11,12,13&group_id=12" +
				"&privacy_mode=training_off&search=" + digestTerm + "&sort_by=upstream_billing_rate&sort_order=desc",
			console: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			listResponse, listed := requestAccountPage(router, admin, accountListRoute, test.query)
			ratesResponse, rated := requestAccountPage(router, admin, upstreamBillingRatesRoute, test.query)

			require.Equal(t, http.StatusOK, listResponse.Code)
			require.Equal(t, http.StatusOK, ratesResponse.Code)
			require.Len(t, listed, 1)
			require.Equal(t, test.console, listed[0].console != nil, "the query must take the list path the case is about")
			require.Equal(t, listed, rated)
		})
	}
}

func TestAccountHandlerGetUpstreamBillingRatesFailsLikeList(t *testing.T) {
	listErr := errors.New("synthetic list outage")
	for _, test := range []struct {
		name       string
		query      string
		listErr    error
		wantStatus int
		wantCalls  int
	}{
		{name: "invalid_group", query: "group_id=abc", wantStatus: http.StatusBadRequest},
		{name: "negative_group", query: "group_id=-3", wantStatus: http.StatusBadRequest},
		{name: "console_invalid_group", query: "statuses=active&group_id=abc", wantStatus: http.StatusBadRequest},
		{name: "console_invalid_filter", query: "tags=abc", wantStatus: http.StatusBadRequest},
		{name: "list_error", listErr: listErr, wantStatus: http.StatusInternalServerError, wantCalls: 1},
		{name: "console_list_error", query: "statuses=active", listErr: listErr, wantStatus: http.StatusInternalServerError, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, admin := setupUpstreamBillingRatesRouter()
			admin.listErr = test.listErr
			listResponse, _ := requestAccountPage(router, admin, accountListRoute, test.query)
			ratesResponse, rated := requestAccountPage(router, admin, upstreamBillingRatesRoute, test.query)

			require.Equal(t, test.wantStatus, listResponse.Code)
			require.Equal(t, listResponse.Code, ratesResponse.Code)
			require.JSONEq(t, listResponse.Body.String(), ratesResponse.Body.String())
			require.Len(t, rated, test.wantCalls)
		})
	}
}

func TestAccountHandlerGetUpstreamBillingRatesFallsBackToOlderGroupName(t *testing.T) {
	router, admin := setupUpstreamBillingRatesRouter()
	for _, test := range []struct {
		query     string
		wantGroup int64
	}{
		{query: "group=12", wantGroup: 12},
		{query: "group=12&statuses=active", wantGroup: 12},
		{query: "group=12&group_id=73", wantGroup: 73},
		{query: "group=12&group_id=73&statuses=active", wantGroup: 73},
		// An empty group_id is still the name List reads, and means no group filter there.
		{query: "group=12&group_id=", wantGroup: 0},
	} {
		t.Run(test.query, func(t *testing.T) {
			recorder, calls := requestAccountPage(router, admin, upstreamBillingRatesRoute, test.query)

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Len(t, calls, 1)
			require.Equal(t, test.wantGroup, calls[0].group())
		})
	}
}

func TestAccountHandlerGetUpstreamBillingRatesKeepsResponseShapeWithConsoleFilters(t *testing.T) {
	router, admin := setupUpstreamBillingRatesRouter()
	admin.rows = []service.Account{
		{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: map[string]any{
			service.UpstreamBillingProbeExtraKey: map[string]any{
				"status": service.UpstreamBillingProbeStatusOK, "data": map[string]any{"effective_rate_multiplier": 0.045},
				"last_attempt_at": "2026-08-01T00:00:00Z", "next_probe_at": "2026-08-01T00:30:00Z",
			},
		}},
		{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
		{ID: 19, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth},
	}
	admin.total = 57
	const query = "page=2&page_size=3&statuses=active&tags=3&sort_by=upstream_billing_rate&sort_order=desc"

	recorder, calls := requestAccountPage(router, admin, upstreamBillingRatesRoute, query)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, calls, 1)
	require.NotNil(t, calls[0].console)

	var body struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.JSONEq(t, `{
		"items": [
			{"account_id": 42, "snapshot": {
				"status": "ok", "data": {"effective_rate_multiplier": 0.045},
				"last_attempt_at": "2026-08-01T00:00:00Z", "next_probe_at": "2026-08-01T00:30:00Z"
			}},
			{"account_id": 7, "snapshot": null},
			{"account_id": 19, "snapshot": null}
		],
		"total": 57, "page": 2, "page_size": 3
	}`, string(body.Data))
	require.Equal(t, "private, no-cache", recorder.Header().Get("Cache-Control"))
	etag := recorder.Header().Get("ETag")
	require.NotEmpty(t, etag)

	request := httptest.NewRequest(http.MethodGet, upstreamBillingRatesRoute+"?"+query, nil)
	request.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	router.ServeHTTP(notModified, request)
	require.Equal(t, http.StatusNotModified, notModified.Code)
	require.Empty(t, notModified.Body.String())
}
