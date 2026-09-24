package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) SetCodexTicketGateway(gateway *service.OpenAIGatewayService) {
	h.codexTicketGateway = gateway
}

func (h *SettingHandler) TestCodexTicketProxy(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		ProxyURL string `json:"proxy_url"`
		Protocol string `json:"protocol"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "请求格式不正确")
		return
	}
	if h.codexTicketGateway == nil {
		response.Error(c, 503, "票据服务不可用")
		return
	}
	result, err := h.codexTicketGateway.TestCodexTicketProxyWithProtocol(c.Request.Context(), req.ProxyURL, req.Protocol)
	if err != nil {
		response.InternalError(c, "无法完成代理测试")
		return
	}
	response.Success(c, result)
}
