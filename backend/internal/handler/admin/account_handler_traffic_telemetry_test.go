package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubAccountTrafficObserveCache struct {
	snapshot map[service.AccountTrafficProtocol]service.AccountTrafficObserveState
}

func (s *stubAccountTrafficObserveCache) Begin(context.Context, int64, service.AccountTrafficProtocol) error {
	return nil
}

func (s *stubAccountTrafficObserveCache) Finish(context.Context, int64, service.AccountTrafficProtocol, service.AccountTrafficOutcome) error {
	return nil
}

func (s *stubAccountTrafficObserveCache) Snapshot(context.Context, int64) (map[service.AccountTrafficProtocol]service.AccountTrafficObserveState, error) {
	return s.snapshot, nil
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
