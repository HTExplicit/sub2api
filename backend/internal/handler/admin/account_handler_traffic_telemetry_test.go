package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testextensions"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubAccountTrafficObserveCache struct {
	snapshot      map[service.AccountTrafficProtocol]service.AccountTrafficObserveState
	snapshotCalls int
}

func (s *stubAccountTrafficObserveCache) Begin(context.Context, int64, service.AccountTrafficProtocol) error {
	return nil
}

func (s *stubAccountTrafficObserveCache) Finish(context.Context, int64, service.AccountTrafficProtocol, service.AccountTrafficOutcome) error {
	return nil
}

func (s *stubAccountTrafficObserveCache) Snapshot(context.Context, int64) (map[service.AccountTrafficProtocol]service.AccountTrafficObserveState, error) {
	s.snapshotCalls++
	return s.snapshot, nil
}

type scopedTrafficAdminService struct {
	*stubAdminService
	reads int
}

func (s *scopedTrafficAdminService) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	s.reads++
	return s.stubAdminService.GetAccount(ctx, id)
}

func TestAccountHandlerTrafficTelemetryFollowsTelemetrySwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			t.Cleanup(testextensions.Install)
			service.ConfigureAdminObservability(&extensionv1.AdminObservabilityConfig{TelemetryEnabled: enabled})
			stub := &scopedTrafficAdminService{stubAdminService: newStubAdminService()}
			stub.getAccountResult = &service.Account{ID: 18, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 3, Credentials: map[string]any{"api_key": "never-return-secret"}}
			cache := &stubAccountTrafficObserveCache{snapshot: map[service.AccountTrafficProtocol]service.AccountTrafficObserveState{service.AccountTrafficProtocolHTTP: {Started: 3, Completed2xx: 2}}}
			handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			handler.SetAccountTrafficObserver(service.NewAccountTrafficObserver(cache, nil))
			router := gin.New()
			router.GET("/accounts/:id/traffic-telemetry", handler.GetTrafficTelemetry)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/accounts/18/traffic-telemetry", nil))
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			var response struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, enabled, response.Data["state_available"])
			require.EqualValues(t, 18, response.Data["account_id"])
			require.Equal(t, 1, stub.reads)
			require.NotContains(t, recorder.Body.String(), "never-return-secret")
			if enabled {
				require.Equal(t, 1, cache.snapshotCalls)
				require.Contains(t, response.Data, "protocols")
			} else {
				require.Zero(t, cache.snapshotCalls)
				require.NotContains(t, response.Data, "protocols")
				require.NotContains(t, response.Data, "error", "disabled telemetry keeps the no-data response")
			}
		})
	}
}

func TestAccountHandlerGetTrafficTelemetryReturnsSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	stub.getAccountResult = &service.Account{ID: 18, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 3,
		Credentials: map[string]any{"api_key": "never-return-secret"}}
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.SetAccountTrafficObserver(service.NewAccountTrafficObserver(&stubAccountTrafficObserveCache{
		snapshot: map[service.AccountTrafficProtocol]service.AccountTrafficObserveState{
			service.AccountTrafficProtocolHTTP: {Started: 3, Completed2xx: 2, Upstream429: 1, PeakInFlight: 2, RequestsLast60s: 1},
			service.AccountTrafficProtocolWS:   {},
		},
	}, nil))
	router := gin.New()
	router.GET("/accounts/:id/traffic-telemetry", handler.GetTrafficTelemetry)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/accounts/18/traffic-telemetry", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var payload struct {
		Data struct {
			AccountID             int64                                         `json:"account_id"`
			ConfiguredConcurrency int                                           `json:"configured_concurrency"`
			StateAvailable        bool                                          `json:"state_available"`
			TTLSeconds            int                                           `json:"ttl_seconds"`
			Protocols             map[string]service.AccountTrafficObserveState `json:"protocols"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.EqualValues(t, 18, payload.Data.AccountID)
	require.Equal(t, 3, payload.Data.ConfiguredConcurrency)
	require.True(t, payload.Data.StateAvailable)
	require.Equal(t, service.AccountTrafficObserveTTLSeconds, payload.Data.TTLSeconds)
	require.EqualValues(t, 3, payload.Data.Protocols["http"].Started)
	require.EqualValues(t, 1, payload.Data.Protocols["http"].Upstream429)
	require.Equal(t, 2, payload.Data.Protocols["http"].PeakInFlight)
	require.Contains(t, payload.Data.Protocols, "ws")
	require.NotContains(t, rec.Body.String(), "never-return-secret")
}
