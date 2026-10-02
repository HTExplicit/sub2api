package service

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// finalizeCodexOutboundHeaders is shared by the business transports.
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
