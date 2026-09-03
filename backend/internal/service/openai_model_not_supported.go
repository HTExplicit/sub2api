package service

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// openAIModelNotSupportedReason is deliberately distinct from model-not-found
// and generic transient failures.  Laxa can advertise a model in its public
// catalogue while an individual API key is temporarily unable to serve it.
// Cooling only the account/model pair lets the scheduler try another key
// without removing the model from the global catalogue.
const openAIModelNotSupportedReason = GatewayFailureReason("upstream_400_model_not_supported")

const upstreamModelNotSupportedCooldown = 30 * time.Minute

// isOpenAIModelNotSupportedError recognizes the structured 400 emitted by
// OpenAI-compatible upstreams when the selected credential cannot serve a
// model.  The match is intentionally strict: free-form text, unsupported
// parameters, and arbitrary 400s must not trigger account failover.
//
// A caller handling an in-band SSE/WebSocket event should pass
// http.StatusBadRequest as statusCode after it has verified that the event is
// an upstream error.  The transport itself is often HTTP 200 in that case.
func isOpenAIModelNotSupportedError(statusCode int, upstreamMsg string, body []byte) bool {
	if statusCode != http.StatusBadRequest || len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}

	for _, path := range []string{"error", "response.error"} {
		errorObject := gjson.GetBytes(body, path)
		if !errorObject.Exists() || !errorObject.IsObject() {
			continue
		}
		errorType := strings.ToLower(strings.TrimSpace(errorObject.Get("type").String()))
		if errorType != "model_not_supported" {
			continue
		}
		if code := strings.TrimSpace(errorObject.Get("code").String()); code != "" && !isHTTP400Code(code) {
			continue
		}
		message := strings.TrimSpace(errorObject.Get("message").String())
		if message == "" {
			message = strings.TrimSpace(upstreamMsg)
		}
		if hasModelNotSupportedMessage(message) {
			return true
		}
	}
	return false
}

func isHTTP400Code(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	value, err := strconv.Atoi(raw)
	return err == nil && value == http.StatusBadRequest
}

func hasModelNotSupportedMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" || !strings.Contains(lower, "model") {
		return false
	}
	return strings.Contains(lower, "not supported") || strings.Contains(lower, "unsupported")
}

// isOpenAIModelNotSupportedPayload is the event-form equivalent used by
// Responses SSE and WebSocket code.  It intentionally delegates to the same
// strict structural matcher so HTTP and in-band paths cannot drift.
func isOpenAIModelNotSupportedPayload(payload []byte) bool {
	return isOpenAIModelNotSupportedError(http.StatusBadRequest, "", payload)
}
