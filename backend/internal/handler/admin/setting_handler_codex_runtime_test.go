package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexRuntimeSettingRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *codexRuntimeSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

// The Codex runtime endpoints read and save one switch in the standard
// envelope; a body that is not exactly a request_zstd boolean object is refused
// and stores nothing.
func TestCodexRuntimeSettingsReadAndSaveOneSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := service.EffectiveCodexRuntimeConfig()
	t.Cleanup(func() { service.SetCodexRequestZstdEnabled(old.RequestZstd) })
	service.SetCodexRequestZstdEnabled(true)
	repo := &codexRuntimeSettingRepo{values: map[string]string{}}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), nil, nil, nil, nil, nil, nil)
	call := func(method, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(method, "/api/v1/admin/settings/codex-runtime", strings.NewReader(body))
		if method == http.MethodGet {
			h.GetCodexRuntimeSettings(c)
		} else {
			h.UpdateCodexRuntimeSettings(c)
		}
		return recorder
	}

	read := call(http.MethodGet, "")
	require.Equal(t, http.StatusOK, read.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"request_zstd":true}}`, read.Body.String())

	saved := call(http.MethodPut, `{"request_zstd":false}`)
	require.Equal(t, http.StatusOK, saved.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"request_zstd":false}}`, saved.Body.String())
	require.Equal(t, map[string]string{"codex_runtime_config": `{"request_zstd":false}`}, repo.values)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"request_zstd":false}}`, call(http.MethodGet, "").Body.String())

	for _, body := range []string{`{"request_zstd":"true"}`, `{"request_zstd":null}`, `{"request_zstd":true,"other":true}`, `{"other":true}`, `null`, ``} {
		rejected := call(http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, rejected.Code, body)
		require.Equal(t, map[string]string{"codex_runtime_config": `{"request_zstd":false}`}, repo.values)
		require.False(t, service.EffectiveCodexRuntimeConfig().RequestZstd)
	}

	require.Equal(t, http.StatusOK, call(http.MethodPut, `{}`).Code)
	require.Equal(t, map[string]string{"codex_runtime_config": `{"request_zstd":true}`}, repo.values, "an omitted switch is on")
}
