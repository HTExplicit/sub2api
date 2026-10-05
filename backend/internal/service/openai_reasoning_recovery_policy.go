package service

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// openAIReasoningRejection is an upstream error that rejects reasoning
// ciphertext of the request. message is the upstream error text; selection
// reads it only for the id of the rejected item.
type openAIReasoningRejection struct{ code, param, message string }

// parseOpenAIReasoningRejection recognizes an upstream error, response.failed or
// failed response.done payload whose error code is thinking_signature_invalid
// or invalid_encrypted_content. A payload that carries a protected status is
// never one. This reads protocol framing only; whether the request can be
// repaired is decided by selectOpenAIRecoveryIndices.
func parseOpenAIReasoningRejection(payload []byte) (openAIReasoningRejection, bool) {
	if _, err := canonicalReasoningCacheJSON(payload); err != nil {
		return openAIReasoningRejection{}, false
	}
	root := gjson.ParseBytes(payload)
	for _, path := range []string{"status_code", "error.status_code", "error.status", "response.error.status_code", "response.error.status"} {
		if value := root.Get(path); (value.Type == gjson.Number || value.Type == gjson.String) && openAIReasoningRecoveryProtectedStatus(int(value.Int())) {
			return openAIReasoningRejection{}, false
		}
	}
	event, responseStatus := root.Get("type").String(), root.Get("response.status").String()
	if event != "" && event != "error" && event != "response.failed" && event != "response.done" {
		return openAIReasoningRejection{}, false
	}
	if event == "response.done" && responseStatus != "failed" {
		return openAIReasoningRejection{}, false
	}
	if status := root.Get("status").String(); status != "" && status != "failed" {
		return openAIReasoningRejection{}, false
	}
	var source gjson.Result
	switch {
	case root.Get("response.error").IsObject():
		if event != "response.failed" && responseStatus != "failed" {
			return openAIReasoningRejection{}, false
		}
		source = root.Get("response.error")
	case root.Get("error").IsObject():
		source = root.Get("error")
	case event == "error":
		source = root
	default:
		return openAIReasoningRejection{}, false
	}
	code := source.Get("code")
	if code.Type != gjson.String || (code.String() != "thinking_signature_invalid" && code.String() != "invalid_encrypted_content") {
		return openAIReasoningRejection{}, false
	}
	param := source.Get("param")
	if param.Exists() && param.Type != gjson.String && param.Type != gjson.Null {
		return openAIReasoningRejection{}, false
	}
	rejection := openAIReasoningRejection{code: code.String(), param: param.String()}
	if message := source.Get("message"); message.Type == gjson.String {
		rejection.message = message.String()
	}
	return rejection, true
}

// openAIRecoveryMessageNamesItem reports whether the upstream message contains
// a Responses reasoning item id as a whole identifier. The upstream reports a
// ciphertext it cannot verify as "… for item rs_… could not be verified" with a
// null param; the id is the only place the rejected item is identified.
func openAIRecoveryMessageNamesItem(message, id string) bool {
	if !strings.HasPrefix(id, "rs_") || len(id) == len("rs_") {
		return false
	}
	identifier := func(b byte) bool {
		return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for offset := 0; ; {
		at := strings.Index(message[offset:], id)
		if at < 0 {
			return false
		}
		start := offset + at
		end := start + len(id)
		if (start == 0 || !identifier(message[start-1])) && (end == len(message) || !identifier(message[end])) {
			return true
		}
		offset = start + 1
	}
}

var openAIReasoningErrorInputParam = regexp.MustCompile(`^input(?:\[(\d+)\]|\.(\d+))(?:\.encrypted_content)?$`)

// selectOpenAIRecoveryIndices returns the reasoning items of the sent body that
// may lose their ciphertext for the rejection, and the reason the continuation
// diagnostic reports while no stripped retry has been sent: why nothing is
// selected, or recovery_not_dispatched when something is.
func selectOpenAIRecoveryIndices(body []byte, rejection openAIReasoningRejection) ([]int, string) {
	if _, err := canonicalReasoningCacheJSON(body); err != nil {
		return nil, "invalid_request_snapshot"
	}
	// Removing ciphertext cannot repair a locally provable orphan tool result.
	if !openAIReasoningToolHistoryAllowsRecovery(body) {
		return nil, "invalid_tool_history"
	}
	items := openAIReasoningCipherItems(body)
	if len(items) == 0 {
		return nil, "no_reasoning_ciphertext"
	}
	if match := openAIReasoningErrorInputParam.FindStringSubmatch(rejection.param); match != nil {
		text := match[1]
		if text == "" {
			text = match[2]
		}
		if index, err := strconv.Atoi(text); err == nil {
			for _, item := range items {
				if item.index == index {
					return []int{index}, "recovery_not_dispatched"
				}
			}
		}
		return nil, "target_not_reasoning_ciphertext"
	}
	if rejection.param != "" && rejection.param != "input" && rejection.param != "reasoning.encrypted_content" {
		return nil, "unsupported_error_param"
	}
	if openAIRequestHoldsServerContext(body) {
		return nil, "server_held_context"
	}
	indices := make([]int, 0, len(items))
	named := false
	for _, item := range items {
		indices = append(indices, item.index)
		named = named || (rejection.message != "" && openAIRecoveryMessageNamesItem(rejection.message, item.id))
	}
	encryptedFields := 0
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		encryptedFields = countOpenAIEncryptedFields(parsed)
	}
	// Another ciphertext carrier (compaction, an inter-agent message) may be the
	// rejected one, unless the upstream names a reasoning item itself.
	if encryptedFields != len(items) && !named {
		return nil, "ambiguous_encrypted_carriers"
	}
	return indices, "recovery_not_dispatched"
}
