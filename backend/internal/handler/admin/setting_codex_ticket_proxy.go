package admin

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/proxytransport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type codexTicketProxyInput struct {
	ProxyURL         string `json:"proxy_url"`
	Protocol         string `json:"protocol"`
	ProxySelectionID string `json:"proxy_selection_id"`
}

// ParseCodexTicketProxy only recognizes syntax. It has no gateway, storage,
// certificate, DNS or network dependency and never persists a proxy draft.
func (h *SettingHandler) ParseCodexTicketProxy(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req codexTicketProxyInput
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "请求格式不正确")
		return
	}
	response.Success(c, proxytransport.Parse(req.ProxyURL, req.Protocol))
}

func writeCodexProxySelectionError(c *gin.Context, err error) bool {
	var invalid *proxytransport.SelectionError
	if !errors.As(err, &invalid) {
		return false
	}
	c.JSON(http.StatusBadRequest, response.Response{Code: http.StatusBadRequest, Reason: invalid.Code, Message: invalid.Error(), Data: invalid.Result})
	return true
}

func (h *SettingHandler) SetCodexTicketGateway(gateway *service.OpenAIGatewayService) {
	h.codexTicketGateway = gateway
}

func (h *SettingHandler) TestCodexTicketProxy(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req codexTicketProxyInput
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "请求格式不正确")
		return
	}
	normal, err := proxytransport.Resolve(req.ProxyURL, req.Protocol, req.ProxySelectionID)
	if err == nil && normal == "" {
		err = &proxytransport.SelectionError{Code: "invalid_proxy", Result: proxytransport.Parse(req.ProxyURL, req.Protocol)}
	}
	if writeCodexProxySelectionError(c, err) {
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "票据服务不可用")
		return
	}
	result, err := h.codexTicketGateway.TestCodexTicketProxyWithProtocol(c.Request.Context(), normal, "")
	if err != nil {
		response.InternalError(c, "无法完成代理测试")
		return
	}
	response.Success(c, result)
}
