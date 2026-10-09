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
	"github.com/tidwall/gjson"
)

type fingerprintAccountAdmin struct {
	service.AdminService
	accounts map[int64]*service.Account
	updates  map[string]any
}

func (s *fingerprintAccountAdmin) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	return s.accounts[id], nil
}
func (s *fingerprintAccountAdmin) UpdateAccountExtra(_ context.Context, id int64, updates map[string]any) error {
	s.updates = updates
	for key, value := range updates {
		s.accounts[id].Extra[key] = value
	}
	return nil
}

func TestCodexFingerprintAccountHandlerOnlyChangesModeAndUsesParentIdentity(t *testing.T) {
	parentID := int64(1)
	parent := &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{"codex_fingerprint_seed": "ac6a7159-d158-41e8-bd52-3e66fd5c6515", "codex_fingerprint_mode": "device"}}
	shadow := &service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, ParentAccountID: &parentID, Extra: map[string]any{"codex_fingerprint_mode": "full", "preserved": "value"}}
	admin := &fingerprintAccountAdmin{accounts: map[int64]*service.Account{1: parent, 2: shadow}}
	h := &AccountHandler{adminService: admin}
	call := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(method, "/api/v1/admin/accounts/2/codex-fingerprint", strings.NewReader(body))
		c.Params = gin.Params{{Key: "id", Value: "2"}}
		if method == http.MethodGet {
			h.GetCodexFingerprint(c)
		} else {
			h.UpdateCodexFingerprint(c)
		}
		return rec
	}
	read := call(http.MethodGet, "")
	require.Equal(t, http.StatusOK, read.Code)
	require.Equal(t, int64(1), gjson.Get(read.Body.String(), "data.identity.identity_account_id").Int())
	require.NotEmpty(t, gjson.Get(read.Body.String(), "data.device_identity.os_type").String())
	require.False(t, gjson.Get(read.Body.String(), "data.identity.identity_persisted").Bool())
	require.Empty(t, gjson.Get(read.Body.String(), "data.device_identity.generated_at").String())
	require.True(t, gjson.Get(read.Body.String(), "data.identifier_policy").Exists())
	require.Nil(t, admin.updates, "GET must not write derived identities")
	saved := call(http.MethodPut, `{"mode":"off"}`)
	require.Equal(t, http.StatusOK, saved.Code)
	require.Equal(t, map[string]any{"codex_fingerprint_mode": "off"}, admin.updates)
	require.Equal(t, "value", shadow.Extra["preserved"])
	require.Equal(t, "device", parent.Extra["codex_fingerprint_mode"])
	for _, body := range []string{`{"mode":"invalid"}`, `{"mode":"full","codex_fingerprint_seed":"injected"}`, `{"mode":"full","device_identity":{"os_type":"Windows"}}`, `{"mode":null}`} {
		rejected := call(http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, rejected.Code)
		require.Equal(t, "off", shadow.Extra["codex_fingerprint_mode"])
	}
}
