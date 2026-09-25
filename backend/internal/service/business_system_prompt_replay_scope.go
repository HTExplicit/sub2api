package service

import (
	"github.com/gin-gonic/gin"
)

// Replay now operates on the clean converted conversation, before placement.
// Bind its cache to the frozen rule selection so a prompt change cannot reuse
// encrypted reasoning produced under another instruction contract.
func (s *OpenAIGatewayService) businessPromptReplayScope(c *gin.Context, account *Account, body []byte, scope OpenAIReasoningCacheScope) (OpenAIReasoningCacheScope, error) {
	if s == nil || s.businessPromptService == nil || account == nil {
		return scope, nil
	}
	service := s.businessPromptService
	snapshot, err := service.snapshotForRequest(c)
	if err != nil {
		return scope, err
	}
	if !snapshot.Enabled || snapshot.RulePolicy == nil || len(snapshot.RulePolicy.Rules) == 0 || (isOpenAIResponsesCompactPath(c) && !snapshot.CompactEnabled) {
		return scope, nil
	}
	target, err := service.freshPromptBinding(c, account, promptSendTarget(c, account, body, BusinessSystemPromptProtocolResponses, isOpenAIResponsesCompactPath(c), ""))
	if err != nil {
		return scope, err
	}
	application, err := planBusinessSystemPrompt(promptPolicyRequestContext(c), snapshot, target)
	if err != nil {
		return scope, err
	}
	if namespace := businessSystemPromptCacheNamespace(application); namespace != "" {
		scope.ScopeHash = openAIReasoningDigest([]byte(scope.ScopeHash + namespace))
	}
	return scope, nil
}
