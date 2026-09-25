package service

import (
	"errors"

	"github.com/gin-gonic/gin"

	coderws "github.com/coder/websocket"
)

// Freeze content when accepting the client turn, while retaining only clean
// messages in the replay accumulator. Placement is resolved after replay.
func (s *OpenAIGatewayService) prepareBusinessPromptWSIngress(c *gin.Context, body []byte, account *Account, protocol string, compact bool) ([]byte, BusinessSystemPromptApplication, error) {
	_, _, err := s.businessSystemPromptSnapshotForRequest(c, account)
	return body, BusinessSystemPromptApplication{}, err
}

func (s *OpenAIGatewayService) finalizeBusinessPromptWSIngress(c *gin.Context, account *Account, body []byte) ([]byte, error) {
	return s.finalizeBusinessPromptForSend(c, account, body, BusinessSystemPromptProtocolResponses, isOpenAIResponsesCompactPath(c))
}

func businessPromptWSCloseError(err error) error {
	if errors.Is(err, ErrPromptDeliveryUnsupported) {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "prompt_delivery_unsupported", err)
	}
	return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "system_prompt_unavailable", err)
}
