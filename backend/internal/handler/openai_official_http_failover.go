package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func isOpenAIOfficialHTTPFailover(c *gin.Context, account *service.Account) bool {
	return c != nil && c.Request != nil && service.IsOpenAIOfficialHTTPFailover(c.Request.Context(), account)
}

// Match the official scheduling observation to the actual upstream model, not
// just the request alias or a pre-forward account mapping.
func openAIAccountScheduleModel(c *gin.Context, account *service.Account, forwardModel string, requireCompact bool, result *service.OpenAIForwardResult) string {
	if result != nil {
		if actual := strings.TrimSpace(result.UpstreamModel); actual != "" {
			return actual
		}
	}
	if c != nil {
		if value, ok := c.Get(service.OpsUpstreamModelKey); ok {
			if actual, ok := value.(string); ok && strings.TrimSpace(actual) != "" {
				return strings.TrimSpace(actual)
			}
		}
	}
	return service.ResolveOpenAIAccountUpstreamModelForRequest(account, forwardModel, requireCompact)
}

func (h *OpenAIGatewayHandler) reportOpenAIHTTPAccountScheduleResult(
	c *gin.Context,
	selection *service.AccountSelectionResult,
	account *service.Account,
	forwardModel string,
	requireCompact bool,
	result *service.OpenAIForwardResult,
	success bool,
	firstTokenMs *int,
	observedErr error,
) {
	if isOpenAIOfficialHTTPFailover(c, account) {
		if !success && failoverClientGone(c) {
			return
		}
		h.gatewayService.ReportOpenAIOfficialHTTPAccountScheduleResult(
			c.Request.Context(), account, openAIAccountScheduleModel(c, account, forwardModel, requireCompact, result),
			success, firstTokenMs, observedErr,
		)
		return
	}
	if success {
		h.gatewayService.ReportOpenAIAccountScheduleResultForSelectionWithContext(selection, account.ID, account.GetMappedModel(forwardModel), true, firstTokenMs, c.Request.Context())
		return
	}
	h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, account.GetMappedModel(forwardModel), false, firstTokenMs)
}

// Official HTTP failures are reported before deciding whether to retry, as in
// upstream. The existing selection/probe finalizer remains exclusive to the
// downstream policy, which includes Cindy, OAuth, and managed routes.
func (h *OpenAIGatewayHandler) finalizeOpenAIHTTPFailoverSelection(
	c *gin.Context,
	selection *service.AccountSelectionResult,
	account *service.Account,
	model string,
	failoverErr *service.UpstreamFailoverError,
	action openAIFailoverRetryAction,
) {
	if !isOpenAIOfficialHTTPFailover(c, account) {
		finalizeOpenAIFailoverSelection(h.gatewayService, selection, account, model, failoverErr, action)
	}
}
