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
	policy := codexFingerprintPolicyForContext(c, account)
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, account); err != nil {
		return err
	}
	if !policy.enabled && c != nil && c.Request != nil {
		// Earlier builders provide protocol defaults and account UA overrides.
		// With simulation off, restore the caller's identity before filling gaps.
		for _, name := range []string{"user-agent", "originator", "version"} {
			headers.Del(name)
			if value := c.Request.Header.Get(name); value != "" {
				headers.Set(name, value)
			}
		}
		for _, name := range []string{"session-id", "thread-id", "x-client-request-id", "x-codex-window-id", "x-codex-parent-thread-id", "x-codex-turn-metadata"} {
			if value := c.Request.Header.Get(name); value != "" {
				headers.Set(name, value)
			}
		}
		if !isOpenAICompatMessagesBridgeContext(c) {
			ensureCodexIdentityHeaders(headers, policy)
		} else {
			headers.Del("originator")
		}
	}
	enforceCodexIdentityHeadersForAccount(headers, codexAccountIdentitySource(c, account), s.codexIdentityOverrideUA(account), policy)
	if policy.enabled {
		if aligned, changed := alignCodexSandboxJSON(headers.Get("x-codex-turn-metadata"), codexSandboxForUserAgent(headers.Get("user-agent"))); changed {
			headers.Set("x-codex-turn-metadata", aligned)
		}
	}
	return nil
}
