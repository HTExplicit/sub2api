package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type codexRoutingDownstreamContextKey struct{}

// Keep cancellation evidence without reconnecting it to the upstream context:
// existing callers intentionally detach that context to drain usage on cancel.
func withCodexRoutingDownstreamContext(request *http.Request, c *gin.Context) *http.Request {
	if request == nil || c == nil || c.Request == nil {
		return request
	}
	return request.WithContext(context.WithValue(request.Context(), codexRoutingDownstreamContextKey{}, c.Request.Context()))
}

// finalizeCodexOutboundHeaders is shared by probes and business transports.
// Call it after model/tier policy and account header overrides. It only closes
// the identity/workspace/routing contract; it never supplies a reasoning effort
// or replaces a caller's prompt, tools, model, or response protocol.
func (s *OpenAIGatewayService) finalizeCodexOutboundHeaders(ctx context.Context, c *gin.Context, account *Account, headers http.Header, model, serviceTier string) error {
	setOpenAICodexRoutingHint(headers, account, model, serviceTier)
	if account == nil || !account.UsesOpenAICodexProtocol() {
		return nil
	}
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, account); err != nil {
		return err
	}
	return enforceCodexIdentityHeadersForAccountContext(ctx, headers, codexAccountIdentitySource(c, account), s.codexIdentityOverrideUA(account))
}

func codexRoutingProbeEffort(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		// Preserve the existing unspecified probe default. Diagnostics can
		// explicitly request the business effort without changing other probes.
		return "", nil
	}
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return value, nil
	default:
		return "", errCodexRoutingUnavailable
	}
}
