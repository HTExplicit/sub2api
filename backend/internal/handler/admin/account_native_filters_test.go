package admin

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeBulkCindyFilterPredicates(t *testing.T) {
	filters, err := toServiceBulkUpdateAccountFilters(&BulkUpdateAccountFilters{CindyOnly: true, CindyHealthStatus: "banned", CindyBalanceStatus: "insufficient"})
	require.NoError(t, err)
	require.NotNil(t, filters.Console)
	require.True(t, filters.Console.CindyOnly)
	require.Equal(t, "banned", filters.Console.CindyHealthStatus)
	require.Equal(t, "insufficient", filters.Console.CindyBalanceStatus)
	_, err = toServiceBulkUpdateAccountFilters(&BulkUpdateAccountFilters{CindyBalanceStatus: "invented"})
	require.Error(t, err)
}

type nativeRecoveryAdmin struct {
	*stubAdminService
	recoveredID int64
}

func (s *nativeRecoveryAdmin) ClearCindyBalanceInsufficient(_ context.Context, id int64) (*service.Account, error) {
	s.recoveredID = id
	return &service.Account{ID: id, Name: "Cindy fixture", Credentials: map[string]any{"api_key": "synthetic-secret"}}, nil
}

func TestNativeCindyRecoveryReturnsAccountDTO(t *testing.T) {
	stub := &nativeRecoveryAdmin{stubAdminService: newStubAdminService()}
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/accounts/:id/recover", handler.ClearCindyBalanceInsufficient)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/accounts/7/recover", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(7), stub.recoveredID)
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, float64(7), envelope.Data["id"])
	require.Contains(t, envelope.Data, "credentials")
	require.NotContains(t, recorder.Body.String(), "synthetic-secret")
}
