package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type preconditionPluginRepository struct {
	service.PluginRepository
	installation *service.PluginInstallation
	reads        int
}

func (r *preconditionPluginRepository) GetByID(context.Context, int64) (*service.PluginInstallation, error) {
	r.reads++
	copy := *r.installation
	return &copy, nil
}
func (r *preconditionPluginRepository) List(context.Context) ([]*service.PluginInstallation, error) {
	return []*service.PluginInstallation{r.installation}, nil
}
func (*preconditionPluginRepository) UpdateConfigReturningRevision(context.Context, int64, string, string) (int64, error) {
	panic("stale HTTP precondition must not reach a configuration write")
}

type preconditionCipher struct{}

func (preconditionCipher) Encrypt(raw string) (string, error) { return raw, nil }
func (preconditionCipher) Decrypt(raw string) (string, error) { return raw, nil }

func pluginPreconditionContext(method, body string) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Request = httptest.NewRequest(method, "/admin/plugins/7", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, response
}

func TestPluginMutationHTTPRequiresOriginalPrecondition(t *testing.T) {
	handler := NewPluginHandler(nil)
	for name, handle := range map[string]func(*gin.Context){"enable": handler.Enable, "disable": handler.Disable, "save": handler.SaveConfig, "test": handler.Test, "delete": handler.Delete, "action": handler.InvokeAdmin, "job": handler.SubmitJob} {
		t.Run(name, func(t *testing.T) {
			ctx, result := pluginPreconditionContext(http.MethodPost, `{}`)
			handle(ctx)
			require.Equal(t, http.StatusPreconditionRequired, result.Code)
			require.Contains(t, result.Body.String(), "PLUGIN_PRECONDITION_REQUIRED")
		})
	}
}

func TestPluginMutationHTTPRejectsMalformedPrecondition(t *testing.T) {
	for name, revision := range map[string]string{"zero": "0", "negative": "-1", "whitespace_inner": "1 2", "overflow": "9223372036854775808"} {
		t.Run(name, func(t *testing.T) {
			ctx, result := pluginPreconditionContext(http.MethodPost, `{}`)
			ctx.Request.Header.Set(pluginRevisionHeader, revision)
			ctx.Request.Header.Set(pluginPackageHeader, strings.Repeat("a", 64))
			NewPluginHandler(nil).Disable(ctx)
			require.Equal(t, http.StatusBadRequest, result.Code)
		})
	}
	ctx, result := pluginPreconditionContext(http.MethodPost, `{}`)
	ctx.Request.Header.Set(pluginRevisionHeader, "4")
	ctx.Request.Header.Set(pluginPackageHeader, "not-a-digest")
	NewPluginHandler(nil).Disable(ctx)
	require.Equal(t, http.StatusBadRequest, result.Code)
}

func TestPluginMutationHTTPRejectsStaleReadSnapshot(t *testing.T) {
	for _, mismatch := range []string{"revision", "package"} {
		t.Run(mismatch, func(t *testing.T) {
			repo := &preconditionPluginRepository{installation: &service.PluginInstallation{ID: 7, Revision: 5, State: service.PluginStateDisabled, ConfigEncrypted: `{"enabled":false}`, PackageSHA256: strings.Repeat("a", 64)}}
			manager := service.NewPluginManager(repo, preconditionCipher{}, nil, service.PluginHostInfo{}, nil)
			handler := NewPluginHandler(manager)
			for name, handle := range map[string]func(*gin.Context){"enable": handler.Enable, "disable": handler.Disable, "save": handler.SaveConfig, "test": handler.Test, "delete": handler.Delete} {
				t.Run(name, func(t *testing.T) {
					ctx, result := pluginPreconditionContext(http.MethodPost, `{}`)
					ctx.Request.Header.Set(pluginRevisionHeader, "5")
					ctx.Request.Header.Set(pluginPackageHeader, strings.Repeat("a", 64))
					if mismatch == "revision" {
						ctx.Request.Header.Set(pluginRevisionHeader, "4")
					} else {
						ctx.Request.Header.Set(pluginPackageHeader, strings.Repeat("b", 64))
					}
					handle(ctx)
					require.Equal(t, http.StatusConflict, result.Code)
					require.Contains(t, result.Body.String(), "PLUGIN_STATE_CHANGED")
				})
			}
		})
	}
}

func TestPluginConfigHTTPReadReturnsOneRawSnapshotAndNoStore(t *testing.T) {
	repo := &preconditionPluginRepository{installation: &service.PluginInstallation{ID: 7, Revision: 8, PackageSHA256: strings.Repeat("a", 64), ConfigEncrypted: `{"code":17,"enabled":false}`}}
	handler := NewPluginHandler(service.NewPluginManager(repo, preconditionCipher{}, nil, service.PluginHostInfo{}, nil))
	ctx, result := pluginPreconditionContext(http.MethodGet, "")
	ctx.Request.Header.Set(pluginPackageHeader, strings.Repeat("a", 64))
	handler.GetConfig(ctx)
	require.Equal(t, 1, repo.reads)
	require.Equal(t, http.StatusOK, result.Code)
	require.Equal(t, "8", result.Header().Get(pluginRevisionHeader))
	require.Equal(t, repo.installation.PackageSHA256, result.Header().Get(pluginPackageHeader))
	require.Contains(t, result.Header().Get("Cache-Control"), "no-store")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &decoded))
	require.Equal(t, float64(17), decoded["code"])
	require.NotContains(t, decoded, "data")
}
