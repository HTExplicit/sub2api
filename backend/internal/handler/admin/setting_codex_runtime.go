package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) SetNativeCodexConfigEncryptor(encryptor service.SecretEncryptor) {
	h.nativeCodexConfigEncryptor = encryptor
}

func (h *SettingHandler) GetNativeCodexConfiguration(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	raw, err := h.codexTicketGateway.NativeCodexConfiguration()
	if err != nil {
		response.Error(c, 503, "Codex runtime is unavailable")
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}

func (h *SettingHandler) UpdateNativeCodexConfiguration(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var raw json.RawMessage
	if c.ShouldBindJSON(&raw) != nil || len(raw) > 1<<20 {
		response.BadRequest(c, "Invalid Codex routing configuration")
		return
	}
	if _, err := service.NormalizeNativeCodexConfig(c.Request.Context(), raw); err != nil {
		response.BadRequest(c, "Invalid Codex routing configuration")
		return
	}
	if err := h.codexTicketGateway.UpdateNativeCodexConfiguration(c.Request.Context(), raw, h.nativeCodexConfigEncryptor); err != nil {
		if errors.Is(err, service.ErrNativeCodexRuntimeChanged) {
			response.Error(c, 409, "Codex runtime configuration changed; reload and retry")
		} else {
			response.Error(c, 503, "Codex runtime configuration could not be applied")
		}
		return
	}
	h.GetNativeCodexConfiguration(c)
}
