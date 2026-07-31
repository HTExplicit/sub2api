package service

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// writeOpenAIRefusalRecoveryFailureReplacement emits a normal Responses result
// when an upstream policy rejection arrives before it can establish an SSE stream.
// The caller must only invoke it for the explicitly enabled recovery path.
func writeOpenAIRefusalRecoveryFailureReplacement(c *gin.Context, requestBody, failurePayload []byte, replacement string) error {
	if c == nil || c.Writer == nil {
		return errors.New("recovery replacement requires a response writer")
	}
	if c.Writer.Written() {
		return errors.New("cannot write a recovery replacement after response output")
	}
	completedResponse, _, _, err := buildOpenAIRefusalRecoveryCompletedResponse(
		failurePayload,
		nil,
		"",
		"",
		replacement,
	)
	if err != nil {
		return err
	}
	_, stream, _ := extractOpenAIRequestMetaFromBody(requestBody)
	if !stream {
		c.Data(http.StatusOK, "application/json; charset=utf-8", completedResponse)
		return nil
	}

	streamBody, err := buildOpenAIRefusalReplacementSSE(failurePayload, completedResponse, replacement)
	if err != nil {
		return err
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Data(http.StatusOK, "text/event-stream", streamBody)
	return nil
}
