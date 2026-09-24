package service

import (
	"errors"

	"github.com/gin-gonic/gin"

	coderws "github.com/coder/websocket"
)

const promptRulesDeferredWSKey = "prompt_rules_deferred_ws"

// The native WS accumulator must retain clean customer history. Validate and
// freeze policy here, but insert explicit messages only after replay assembly.
func (s *OpenAIGatewayService) prepareBusinessPromptWSIngress(c *gin.Context, body []byte, account *Account, protocol string, compact bool) ([]byte, BusinessSystemPromptApplication, error) {
	snapshot, eligible, err := s.businessSystemPromptSnapshotForRequest(c, account)
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, err
	}
	if !eligible || snapshot.RulePolicy == nil {
		return s.applyBusinessSystemPromptForRequest(c, body, account, protocol, compact)
	}
	target := enrichPromptTarget(c, body, businessSystemPromptTargetForAccount(account, protocol, compact))
	application, err := planBusinessSystemPromptWithInvoker(promptPolicyRequestContext(c), body, snapshot, target, promptPlanInvoke)
	if err != nil {
		return nil, application, err
	}
	businessSystemPromptRequestSet(c, promptRulesDeferredWSKey, true)
	return body, application, nil
}

func (s *OpenAIGatewayService) finalizeBusinessPromptWSIngress(c *gin.Context, account *Account, body []byte) ([]byte, error) {
	if deferred, _ := businessSystemPromptRequestGet(c, promptRulesDeferredWSKey); deferred == true {
		// sendAndRelay receives the accumulator's clean array, including on a
		// verified full replay. Reuse frozen policy, not a prior attempt's edits.
		businessSystemPromptRequestDelete(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, BusinessSystemPromptProtocolResponses))
		return s.finalizeBusinessPromptForSend(c, account, body, BusinessSystemPromptProtocolResponses, isOpenAIResponsesCompactPath(c))
	}
	return body, validateBusinessSystemPromptFinal(c, body, BusinessSystemPromptProtocolResponses)
}

func businessPromptWSCloseError(err error) error {
	if errors.Is(err, ErrPromptDeliveryUnsupported) {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "prompt_delivery_unsupported", err)
	}
	return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "system_prompt_unavailable", err)
}
