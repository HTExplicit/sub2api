package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/proxytransport"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexProxyParseIsPureAndSelectionErrorsAreShared(t *testing.T) {
	h := &SettingHandler{} // No gateway, database, DNS or transport available.
	call := func(handler gin.HandlerFunc, body map[string]string) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
		ctx.Request.Header.Set("Content-Type", "application/json")
		handler(ctx)
		return rec
	}
	input := "http://fixture-user:fixture-password@first.example:80\nhttp://fixture-user:other-password@second.example:80"
	parsed := call(h.ParseCodexTicketProxy, map[string]string{"proxy_url": input, "protocol": "https"})
	require.Equal(t, http.StatusOK, parsed.Code)
	require.Equal(t, "no-store", parsed.Header().Get("Cache-Control"))
	for _, value := range []string{"fixture-user", "fixture-password", "other-password", "http://"} {
		require.NotContains(t, parsed.Body.String(), value)
	}
	var envelope struct {
		Data proxytransport.ParseResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(parsed.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Candidates, 2)
	for _, selection := range []string{"", "old-selection"} {
		body := map[string]string{"proxy_url": input, "protocol": "https", "proxy_selection_id": selection}
		tested := call(h.TestCodexTicketProxy, body)
		delete(body, "protocol")
		body["proxy_protocol"] = "https"
		saved := call(h.UpdateNativeCodexConfiguration, body)
		require.Equal(t, http.StatusBadRequest, tested.Code)
		require.Equal(t, tested.Code, saved.Code)
		var testError, saveError struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(tested.Body.Bytes(), &testError))
		require.NoError(t, json.Unmarshal(saved.Body.Bytes(), &saveError))
		require.Equal(t, testError, saveError)
		require.False(t, strings.Contains(saved.Body.String(), "fixture-password"))
	}
	accepted := call(h.TestCodexTicketProxy, map[string]string{"proxy_url": input, "protocol": "https", "proxy_selection_id": envelope.Data.Candidates[0].SelectionID})
	require.Equal(t, http.StatusServiceUnavailable, accepted.Code, "a valid selection reaches the unavailable gateway without connecting")
}
