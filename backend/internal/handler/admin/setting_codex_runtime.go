package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	nativeCodexHostingModeHeader       = "X-Sub2API-Codex-Hosting-Mode"
	nativeCodexConfigVersionHeader     = "X-Sub2API-Codex-Config-Version"
	nativeCodexConfigSHA256Header      = "X-Sub2API-Codex-Config-SHA256"
	nativeCodexRuntimeGenerationHeader = "X-Sub2API-Codex-Runtime-Generation"
)

// SetCodexGateway attaches the gateway that hosts the native Codex runtime.
func (h *SettingHandler) SetCodexGateway(gateway *service.OpenAIGatewayService) {
	h.codexGateway = gateway
}

func (h *SettingHandler) SetNativeCodexConfigEncryptor(encryptor service.SecretEncryptor) {
	h.nativeCodexConfigEncryptor = encryptor
}

func (h *SettingHandler) GetNativeCodexConfiguration(c *gin.Context) {
	writeNativeCodexConfiguration(c, h.codexGateway.NativeCodexConfiguration)
}

func writeNativeCodexConfiguration(c *gin.Context, read func(context.Context) (json.RawMessage, service.NativeCodexMetadata, error)) {
	c.Header("Cache-Control", "no-store")
	for _, name := range [...]string{nativeCodexHostingModeHeader, nativeCodexConfigVersionHeader, nativeCodexConfigSHA256Header, nativeCodexRuntimeGenerationHeader} {
		c.Writer.Header().Del(name)
	}
	raw, metadata, err := read(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Codex runtime is unavailable: "+err.Error())
		return
	}
	c.Header(nativeCodexHostingModeHeader, "native")
	c.Header(nativeCodexConfigVersionHeader, strconv.FormatInt(metadata.ConfigVersion, 10))
	c.Header(nativeCodexConfigSHA256Header, metadata.ConfigSHA256)
	c.Header(nativeCodexRuntimeGenerationHeader, strconv.FormatInt(metadata.RuntimeGeneration, 10))
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}

func (h *SettingHandler) UpdateNativeCodexConfiguration(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var raw json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		response.BadRequest(c, "Invalid Codex runtime configuration: "+err.Error())
		return
	}
	if len(raw) > 1<<20 {
		response.BadRequest(c, fmt.Sprintf("Invalid Codex runtime configuration: %d bytes exceed the 1 MiB limit", len(raw)))
		return
	}
	normalized, err := service.NormalizeNativeCodexConfig(c.Request.Context(), raw)
	if err != nil {
		response.BadRequest(c, "Invalid Codex runtime configuration: "+err.Error())
		return
	}
	if h.codexGateway == nil {
		response.Error(c, 503, "Codex runtime is unavailable")
		return
	}
	if err := h.codexGateway.UpdateNativeCodexConfiguration(c.Request.Context(), normalized, h.nativeCodexConfigEncryptor); err != nil {
		if errors.Is(err, service.ErrNativeCodexRuntimeChanged) {
			response.Error(c, 409, "Codex runtime configuration changed; reload and retry: "+err.Error())
		} else {
			response.Error(c, 503, "Codex runtime configuration could not be applied: "+err.Error())
		}
		return
	}
	h.GetNativeCodexConfiguration(c)
}
