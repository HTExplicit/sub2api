package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type reasoningRecoverySettingRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *reasoningRecoverySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *reasoningRecoverySettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

// The reasoning recovery endpoints read and save one switch in the standard
// envelope. Without a stored value the switch is on; a body that is not exactly
// an enabled boolean object is refused and stores nothing.
func TestReasoningRecoveryEndpointsReadAndSaveTheSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &reasoningRecoverySettingRepo{values: map[string]string{}}
	recovery := service.NewReasoningRecoveryService(repo)
	h := NewReasoningRecoveryHandler(recovery)
	call := func(method, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(method, "/api/v1/admin/reasoning-recovery", strings.NewReader(body))
		if method == http.MethodGet {
			h.Get(c)
		} else {
			h.Save(c)
		}
		return recorder
	}

	read := call(http.MethodGet, "")
	require.Equal(t, http.StatusOK, read.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":true}}`, read.Body.String())
	require.Empty(t, repo.values)

	saved := call(http.MethodPut, `{"enabled":false}`)
	require.Equal(t, http.StatusOK, saved.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":false}}`, saved.Body.String())
	require.Equal(t, map[string]string{"reasoning_recovery_config": `{"enabled":false}`}, repo.values)
	require.False(t, recovery.Enabled(), "a save applies to this instance at once")
	require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":false}}`, call(http.MethodGet, "").Body.String())

	for _, body := range []string{`{"enabled":"true"}`, `{"enabled":null}`, `{"enabled":true,"other":true}`, `{"other":true}`, `{}`, `null`, ``} {
		rejected := call(http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, rejected.Code, body)
		require.Equal(t, map[string]string{"reasoning_recovery_config": `{"enabled":false}`}, repo.values)
		require.False(t, recovery.Enabled())
	}

	require.Equal(t, http.StatusOK, call(http.MethodPut, `{"enabled":true}`).Code)
	require.Equal(t, map[string]string{"reasoning_recovery_config": `{"enabled":true}`}, repo.values)
	require.True(t, recovery.Enabled())

	// A read answers with the stored value, here one another instance saved,
	// and applies it to this instance.
	repo.values["reasoning_recovery_config"] = `{"enabled":false}`
	require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":false}}`, call(http.MethodGet, "").Body.String())
	require.False(t, recovery.Enabled())
}
