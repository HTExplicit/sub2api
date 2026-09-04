package service

import (
	"net/http"
	"regexp"
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

var (
	modelNotSupportedMessagePattern   = regexp.MustCompile(`(?i)\b(?:the\s+)?(?:requested\s+)?model(?:\s+(?:['"][^'"\r\n]{1,256}['"]|[A-Za-z0-9._:/-]{1,128}))?\s+(?:is\s+)?(?:temporarily\s+)?(?:not\s+supported|unsupported|unavailable)\b|\bunsupported\s+model\b`)
	modelNotSupportedQualifierPattern = regexp.MustCompile(`(?i)^(?:parameter|output|format|feature|tool|input|field|option|setting|modality|capability|endpoint)\b`)
)

// OpenAIModelNotSupportedCode and OpenAIModelNotSupportedClientMessage are
// deliberately stable client-facing values. The upstream response can contain
// provider-specific model names and account state, neither of which should
// escape when every eligible account has rejected the same request.
const OpenAIModelNotSupportedCode = "model_not_supported"
const OpenAIModelNotSupportedClientMessage = "The requested model is temporarily unavailable. Please try again later."

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
		code := errorObject.Get("code")
		if !code.Exists() || (code.Type != gjson.String && code.Type != gjson.Number) ||
			strings.TrimSpace(code.String()) != "400" {
			continue
		}
		message := errorObject.Get("message")
		if message.Exists() && message.Type != gjson.String {
			continue
		}
		messageText := message.String()
		if strings.TrimSpace(messageText) == "" {
			messageText = upstreamMsg
		}
		// Do not turn an unrelated parameter-validation 400 into a key
		// failover merely because a buggy upstream reused this type. The error
		// object itself remains the authority; this only rejects an internally
		// contradictory structured payload.
		if hasModelNotSupportedMessage(messageText) {
			return true
		}
	}
	return false
}

func hasModelNotSupportedMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" || !strings.Contains(lower, "model") {
		return false
	}
	// Keep the message check narrow enough that a provider reusing the
	// model_not_supported type for a parameter/feature rejection cannot poison
	// an account's model cooldown. The model token must be the subject of the
	// unsupported/unavailable phrase, while allowing the quoted model spelling
	// used by Laxa and the normal "requested model" variants.
	for _, match := range modelNotSupportedMessagePattern.FindAllStringIndex(lower, -1) {
		// "model is unsupported parameter" and "unsupported model output
		// format" describe a rejected request field, not an unavailable model.
		// Ignore a match when its immediate qualifier makes that distinction
		// explicit; otherwise the structured type is sufficient evidence.
		remainder := strings.TrimSpace(lower[match[1]:])
		if modelNotSupportedQualifierPattern.MatchString(remainder) {
			continue
		}
		return true
	}
	return false
}

// isOpenAIModelNotSupportedPayload is the event-form equivalent used by
// Responses SSE and WebSocket code.  It intentionally delegates to the same
// strict structural matcher so HTTP and in-band paths cannot drift.
func isOpenAIModelNotSupportedPayload(payload []byte) bool {
	return isOpenAIModelNotSupportedError(http.StatusBadRequest, "", payload)
}

func newOpenAIModelNotSupportedFailoverError(responseHeaders http.Header, responseBody []byte) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:        http.StatusBadRequest,
		ResponseBody:      append([]byte(nil), responseBody...),
		ResponseHeaders:   responseHeaders.Clone(),
		Scope:             GatewayFailureScopeAccount,
		Reason:            openAIModelNotSupportedReason,
		NextAccountAction: NextAccountRetry,
		ClientStatusCode:  http.StatusBadRequest,
		ClientMessage:     OpenAIModelNotSupportedClientMessage,
		// This signal is scoped to the account/model pair. Do not let adaptive
		// account health or account-wide breakers remove the credential for other
		// models while the persistent model cooldown is active.
		SuppressAccountHealthPenalty: true,
	}
}

// IsOpenAIModelNotSupported identifies a structured, account/model-scoped
// capability failure after it has crossed the service/handler boundary.
func (e *UpstreamFailoverError) IsOpenAIModelNotSupported() bool {
	return e != nil && e.Reason == openAIModelNotSupportedReason
}
