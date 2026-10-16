package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type deepSeekHandlerSettings struct {
	service.SettingRepository
	raw string
	err error
}

func (s *deepSeekHandlerSettings) GetValue(context.Context, string) (string, error) {
	return s.raw, nil
}
func (s *deepSeekHandlerSettings) Set(_ context.Context, key, value string) error {
	if s.err != nil {
		return s.err
	}
	s.raw = value
	return nil
}

func TestDeepSeekDefaultAdminConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &deepSeekHandlerSettings{}
	h := NewDeepSeekReasoningHandler(service.NewDeepSeekReasoningService(repo))
	router := gin.New()
	router.GET("/config", h.Get)
	router.PUT("/config", h.Save)
	request := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/config", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		return rec
	}
	rec := request(http.MethodGet, "")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), `"effort":"max"`)
	require.Contains(t, rec.Body.String(), `"enabled":false`)
	rec = request(http.MethodPut, `{"enabled":true,"effort":"max"}`)
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"enabled":true,"effort":"max"}`, repo.raw)
	for _, body := range []string{`{"effort":"medium"}`, `{"enabled":true}`, `{"effort":100}`, `{"enabled":null,"effort":"max"}`, `invalid`} {
		rec = request(http.MethodPut, body)
		require.Equal(t, 400, rec.Code, body)
		require.JSONEq(t, `{"enabled":true,"effort":"max"}`, repo.raw)
	}
	repo.err = errors.New("store unavailable")
	rec = request(http.MethodPut, `{"enabled":false,"effort":"low"}`)
	require.Equal(t, 500, rec.Code)
	rec = request(http.MethodGet, "")
	require.Contains(t, rec.Body.String(), `"enabled":true`)
}
