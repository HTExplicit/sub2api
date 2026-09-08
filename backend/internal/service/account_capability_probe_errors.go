package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

func capabilityProbeFailure(status, classification string) AccountCapabilityProbeAttempt {
	return AccountCapabilityProbeAttempt{Status: status, Classification: classification, Reason: accountCapabilityReason(classification)}
}

func (a *AccountCapabilityProbeAttempt) setFailure(status, classification string) {
	a.Status, a.Classification, a.Reason = status, classification, accountCapabilityReason(classification)
}

func accountCapabilityTransportFailure(err error, ctx context.Context, attempt AccountCapabilityProbeAttempt) AccountCapabilityProbeAttempt {
	var networkError net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		attempt.setFailure("uncertain", "timeout")
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		attempt.setFailure("uncertain", "canceled")
	case errors.As(err, &networkError) && networkError.Timeout():
		attempt.setFailure("uncertain", "timeout")
	default:
		attempt.setFailure("uncertain", "network_error")
	}
	return attempt
}

// Error codes are copied only from a finite allowlist of structured code/type
// fields. Provider messages, arbitrary numeric strings and header values never
// enter durable evidence or the admin response.
func accountCapabilitySafeErrorCode(body []byte) string {
	if !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{"error.code", "error.type", "code", "type", "response.error.code", "response.error.type"} {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String {
			continue
		}
		code := strings.ToLower(strings.TrimSpace(value.String()))
		switch code {
		case "invalid_api_key", "invalid_token", "token_expired", "authentication_error", "unauthorized", "account_deactivated", "account_suspended", "account_banned", "organization_deactivated", "permission_denied", "permission_error", "model_not_found", "model_not_supported", "deployment_not_found", "invalid_request_error", "unsupported_parameter", "unsupported_model", "invalid_model", "context_length_exceeded", "rate_limit_exceeded", "rate_limit_error", "insufficient_quota", "quota_exceeded", "billing_hard_limit_reached", "overloaded_error", "server_error", "internal_server_error", "content_filter", "content_policy_violation", "safety_violation", "max_output_tokens", "max_tokens":
			return code
		}
	}
	return ""
}

func accountCapabilityHTTPFailure(attempt AccountCapabilityProbeAttempt, status int, body []byte, discovery bool) AccountCapabilityProbeAttempt {
	attempt.HTTPStatus = status
	attempt.ErrorCode = accountCapabilitySafeErrorCode(body)
	code := attempt.ErrorCode
	attempt.setFailure("failed", "request_rejected")
	// A clear structured identity failure plus the matching HTTP auth boundary
	// is required before recommending a global scheduling change.
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		switch code {
		case "invalid_api_key", "invalid_token":
			attempt.setFailure("failed", "credential_invalid")
			attempt.AccountFailure = true
			return attempt
		case "account_deactivated", "account_suspended", "account_banned", "organization_deactivated":
			attempt.setFailure("failed", "account_disabled")
			attempt.AccountFailure = true
			return attempt
		}
	}
	if discovery && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented) {
		attempt.setFailure("unsupported", "catalog_unsupported")
		return attempt
	}
	switch {
	case code == "model_not_found" || code == "model_not_supported" || code == "deployment_not_found" || code == "unsupported_model" || code == "invalid_model":
		attempt.setFailure("failed", "model_unavailable")
	case code == "insufficient_quota" || code == "quota_exceeded" || code == "billing_hard_limit_reached":
		attempt.setFailure("failed", "quota_exhausted")
	case code == "content_filter" || code == "content_policy_violation" || code == "safety_violation":
		attempt.setFailure("failed", "safety_rejection")
	case status == http.StatusUnauthorized || code == "authentication_error" || code == "token_expired" || code == "unauthorized":
		attempt.setFailure("failed", "authentication_failed")
	case status == http.StatusForbidden || code == "permission_denied" || code == "permission_error":
		attempt.setFailure("failed", "permission_denied")
	case status == http.StatusTooManyRequests || code == "rate_limit_exceeded" || code == "rate_limit_error":
		attempt.setFailure("failed", "rate_limited")
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
		attempt.setFailure("unsupported", "protocol_unsupported")
	case status >= 500 || code == "server_error" || code == "internal_server_error" || code == "overloaded_error":
		attempt.setFailure("failed", "upstream_unavailable")
	case status >= 300 && status < 400:
		attempt.setFailure("failed", "redirect_blocked")
	}
	return attempt
}

func accountCapabilityReason(classification string) string {
	switch classification {
	case "text_completed":
		return "Semantic output and an authoritative completion signal were received."
	case "tool_roundtrip_passed":
		return "The specified tool call, result return and final text completion were verified."
	case "tool_call_completed":
		return "The exact expected tool call completed; the tool-result continuation is evaluated separately."
	case "catalog_discovered":
		return "The configured upstream returned a fresh model catalog."
	case "metadata_available":
		return "The upstream token-count endpoint returned a valid non-negative input token count; model generation was not tested."
	case "catalog_empty":
		return "The configured upstream returned a valid, empty model catalog."
	case "catalog_partial":
		return "The upstream indicates more catalog pages; this result contains only the observed page."
	case "catalog_unsupported":
		return "The configured upstream does not support this model catalog endpoint."
	case "invalid_catalog":
		return "The upstream response was not a valid model catalog."
	case "configuration_error":
		return "The account or outbound HTTP configuration cannot perform this observation."
	case "missing_credentials":
		return "The selected account has no API key configured."
	case "unsupported_account":
		return "This observer supports ordinary API-key accounts only, without identity refresh."
	case "unsupported_protocol", "protocol_unsupported":
		return "The selected upstream protocol is not supported by this account or endpoint."
	case "unsupported_profile":
		return "The selected probe profile is not supported."
	case "invalid_model":
		return "An exact, non-empty upstream model ID is required."
	case "credential_invalid":
		return "The upstream explicitly reports an invalid API key or access token."
	case "account_disabled":
		return "The upstream explicitly reports that the account or organization is disabled."
	case "authentication_failed":
		return "Authentication was rejected; account-wide credential failure is not established."
	case "permission_denied":
		return "This request was forbidden; account-wide failure is not established."
	case "model_unavailable":
		return "The requested model is not available on this account."
	case "quota_exhausted":
		return "The upstream reports an exhausted quota or billing limit."
	case "rate_limited":
		return "The upstream temporarily rate-limited this request."
	case "upstream_unavailable":
		return "The upstream is temporarily unavailable."
	case "safety_rejection":
		return "The upstream rejected this request under its safety policy."
	case "redirect_blocked":
		return "The upstream returned a redirect; no redirect or alternate endpoint was followed."
	case "request_rejected":
		return "The upstream rejected the request; no retry or protocol substitution was attempted."
	case "timeout":
		return "The observation timed out; whether the upstream completed is uncertain."
	case "canceled":
		return "The observation was canceled; no automatic replay will occur."
	case "network_error":
		return "The network request failed; delivery or upstream completion may be uncertain."
	case "invalid_response":
		return "The upstream response could not be fully read or validated."
	case "incomplete_response":
		return "No authoritative completion signal was received; EOF or DONE alone is insufficient."
	case "output_budget_exhausted":
		return "The small probe output budget was exhausted before a complete answer."
	case "no_semantic_output":
		return "The completed response contained no usable text or complete tool call."
	case "tool_contract_mismatch":
		return "The response did not contain exactly the specified tool call and fixed arguments."
	case "tool_history_unavailable":
		return "Complete tool-call history was unavailable; no second request was sent."
	case "tool_continuation_incomplete":
		return "The tool result was returned, but the final response did not complete with text."
	default:
		return "This capability observation did not complete successfully."
	}
}
