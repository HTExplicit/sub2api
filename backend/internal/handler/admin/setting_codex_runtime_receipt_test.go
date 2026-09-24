package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeCodexConfigurationHTTPReceiptPreservesExactBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Deliberate formatting makes re-marshalling or wrapping the snapshot visible.
	raw := json.RawMessage("{\n  \"enabled\": false, \"models\": [\"gpt-6-astra\"]\n}")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	for _, version := range []int64{7, math.MaxInt64} {
		t.Run(strconv.FormatInt(version, 10), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/codex-runtime", nil).WithContext(requestContext)
			reads := 0
			writeNativeCodexConfiguration(c, func(ctx context.Context) (json.RawMessage, service.NativeCodexMetadata, error) {
				require.Same(t, requestContext, ctx)
				reads++
				return raw, service.NativeCodexMetadata{
					ID: 7, ConfigVersion: version, RuntimeGeneration: version, ConfigSHA256: digest,
				}, nil
			})
			require.Equal(t, 1, reads)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, []byte(raw), recorder.Body.Bytes(), "success must stay raw JSON without an envelope or added newline")
			require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.Equal(t, "native", recorder.Header().Get(nativeCodexHostingModeHeader))
			require.Equal(t, strconv.FormatInt(version, 10), recorder.Header().Get(nativeCodexConfigVersionHeader))
			require.Equal(t, strconv.FormatInt(version, 10), recorder.Header().Get(nativeCodexRuntimeGenerationHeader))
			bodyHash := sha256.Sum256(recorder.Body.Bytes())
			require.Equal(t, hex.EncodeToString(bodyHash[:]), recorder.Header().Get(nativeCodexConfigSHA256Header))
		})
	}
}

func TestNativeCodexConfigurationHTTPReceiptUnavailableIsOpaque(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"missing_runtime", "read_failure_with_partial_snapshot"} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			for _, header := range [...]string{nativeCodexHostingModeHeader, nativeCodexConfigVersionHeader, nativeCodexConfigSHA256Header, nativeCodexRuntimeGenerationHeader} {
				recorder.Header().Set(header, "stale-receipt")
			}
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/codex-runtime", nil)
			if name == "missing_runtime" {
				(&SettingHandler{}).GetNativeCodexConfiguration(c)
			} else {
				writeNativeCodexConfiguration(c, func(context.Context) (json.RawMessage, service.NativeCodexMetadata, error) {
					return json.RawMessage(`{"proxy_url":"synthetic-private-config"}`),
						service.NativeCodexMetadata{ConfigVersion: 1, RuntimeGeneration: 2},
						errors.New("synthetic-private-database-detail")
				})
			}
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.JSONEq(t, `{"code":503,"message":"Codex runtime is unavailable"}`, recorder.Body.String())
			for _, header := range [...]string{nativeCodexHostingModeHeader, nativeCodexConfigVersionHeader, nativeCodexConfigSHA256Header, nativeCodexRuntimeGenerationHeader} {
				require.Empty(t, recorder.Header().Values(header), "unavailable reads must not expose a receipt")
			}
			require.NotContains(t, recorder.Body.String(), "synthetic-private")
		})
	}
}
