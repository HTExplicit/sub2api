package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type installedPluginUploadRepository struct{ service.PluginRepository }

func (installedPluginUploadRepository) GetByKey(context.Context, string) (*service.PluginInstallation, error) {
	return &service.PluginInstallation{ID: 7, State: service.PluginStateDisabled}, nil
}

func TestPluginUploadExistingIdentityReturnsConflict(t *testing.T) {
	files := map[string][]byte{"bin/plugin": []byte("not a runnable program"), "ui/index.html": []byte("<html></html>")}
	hashes := make(map[string]string)
	for name, content := range files {
		sum := sha256.Sum256(content)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	manifest := service.PluginManifest{
		SchemaVersion: 1, ID: "com.example.upload", Name: "upload fixture", Version: "1.0.0",
		Requires:     service.PluginRequirements{Sub2API: ">=0.1.170 <0.2.0", PluginProtocol: pluginv1.ProtocolVersion, TransportAPI: pluginv1.TransportAPIVersion, UIBridge: pluginv1.UIBridgeVersion},
		Capabilities: []service.PluginCapability{{ID: service.PluginCapabilityOpenAIOAuthOutbound, Platform: service.PlatformOpenAI, AccountType: service.AccountTypeOAuth}},
		Runtimes:     map[string]service.PluginRuntime{service.PluginManifest{}.RuntimeKey(): {Path: "bin/plugin"}},
		UI:           service.PluginUIManifest{Entrypoint: "ui/index.html"}, Files: hashes,
	}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	files["manifest.json"] = raw
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("plugin", "existing.s2plugin")
	require.NoError(t, err)
	_, err = part.Write(archive.Bytes())
	require.NoError(t, err)
	require.NoError(t, form.Close())
	cfg := &config.Config{Plugins: config.PluginConfig{DataDir: t.TempDir(), AllowUnsigned: true, MaxUploadBytes: 1 << 20, MaxUncompressedBytes: 1 << 20}}
	manager := service.NewPluginManager(installedPluginUploadRepository{}, nil, cfg, service.PluginHostInfo{Version: "0.1.179"}, nil)
	handler := NewPluginHandler(manager)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/plugins", &body)
	ctx.Request.Header.Set("Content-Type", form.FormDataContentType())
	handler.Upload(ctx)
	require.Equal(t, http.StatusConflict, response.Code)
	var payload struct {
		Code    int    `json:"code"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, http.StatusConflict, payload.Code)
	require.Equal(t, "PLUGIN_ALREADY_INSTALLED", payload.Reason)
	require.Contains(t, payload.Message, "独立更新")
}
