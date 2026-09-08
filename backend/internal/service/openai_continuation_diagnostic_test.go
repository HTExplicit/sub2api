package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func continuationDiagnosticTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func continuationDiagnosticTestContext(body []byte) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	return c
}

func continuationDiagnosticTestRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://upstream.invalid/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func continuationDiagnosticTestDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func continuationDiagnosticTestEncoded(t *testing.T, diagnostic *OpenAIContinuationDiagnostic) (string, gjson.Result) {
	t.Helper()
	if diagnostic == nil {
		t.Fatal("diagnostic is missing")
	}
	encoded := continuationDiagnosticTestJSON(t, diagnostic)
	if len(encoded) > 6*1024 {
		t.Fatalf("diagnostic exceeds its fixed serialization budget: %d bytes", len(encoded))
	}
	root := gjson.ParseBytes(encoded)
	if root.Get("version").Int() != 1 {
		t.Fatal("diagnostic version is missing or unexpected")
	}
	return string(encoded), root
}

func continuationDiagnosticTestNoPlaintext(t *testing.T, encoded string, values ...string) {
	t.Helper()
	for _, value := range values {
		if value != "" && strings.Contains(encoded, value) {
			t.Fatal("diagnostic retained forbidden plaintext")
		}
	}
}

func TestOpenAIContinuationDiagnosticPrivacy(t *testing.T) {
	t.Run("known_enums_and_protocol_param_only", func(t *testing.T) {
		const message = "encrypted content could not be verified: private-upstream-message-69317"
		upstream := continuationDiagnosticTestJSON(t, map[string]any{
			"error": map[string]any{
				"type": "invalid_request_error", "code": "invalid_encrypted_content",
				"param": "input[12].encrypted_content", "message": message,
				"debug": map[string]any{"credentials": "unlisted-debug-secret-49217"},
			},
		})
		body := []byte(`{"input":[]}`)
		diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, upstream, "invalid_encrypted_content")
		encoded, root := continuationDiagnosticTestEncoded(t, diagnostic)
		for path, expected := range map[string]string{
			"classification":                   "invalid_encrypted_content",
			"upstream_error.error_type.value":  "invalid_request_error",
			"upstream_error.error_code.value":  "invalid_encrypted_content",
			"upstream_error.error_param.value": "input[12].encrypted_content",
			"upstream_error.message.sha256":    continuationDiagnosticTestDigest(message),
		} {
			if root.Get(path).String() != expected {
				t.Errorf("diagnostic contract mismatch at %s", path)
			}
		}
		if root.Get("upstream_error.message.value").Exists() {
			t.Fatal("upstream message must never have a plaintext value")
		}
		continuationDiagnosticTestNoPlaintext(t, encoded, "private-upstream-message-69317", "unlisted-debug-secret-49217")
	})

	t.Run("namespace_param_whitelist", func(t *testing.T) {
		for _, tc := range []struct {
			param   string
			allowed bool
		}{
			{"input[11].namespace", true},
			{"input.11.namespace", true},
			{"tools[0].namespace", true},
			{"input[11].namespace.private-field-51937", false},
			{"input[11].namespace?token=private-token-71359", false},
		} {
			t.Run(tc.param, func(t *testing.T) {
				body := []byte(`{"input":[]}`)
				upstream := continuationDiagnosticTestJSON(t, map[string]any{"error": map[string]any{
					"type": "invalid_request_error", "param": tc.param,
					"message": "Missing namespace: private-upstream-message-31957",
				}})
				diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, upstream, "request_validation")
				for _, candidate := range []*OpenAIContinuationDiagnostic{diagnostic, sanitizeOpenAIContinuationDiagnostic(diagnostic)} {
					encoded, root := continuationDiagnosticTestEncoded(t, candidate)
					param := root.Get("upstream_error.error_param")
					if param.Get("sha256").String() != continuationDiagnosticTestDigest(tc.param) {
						t.Fatal("namespace parameter must retain its complete fingerprint")
					}
					if tc.allowed {
						if param.Get("value").String() != tc.param {
							t.Fatal("safe indexed namespace parameter was lost")
						}
					} else {
						if param.Get("value").Exists() {
							t.Fatal("non-protocol namespace suffix retained a plaintext value")
						}
						continuationDiagnosticTestNoPlaintext(t, encoded, tc.param)
					}
					continuationDiagnosticTestNoPlaintext(t, encoded, "private-upstream-message-31957")
				}
			})
		}
	})

	t.Run("non_json", func(t *testing.T) {
		const message = "unknown parameter: prompt_cache_key; private-non-json-secret-71241"
		body := []byte(`{"input":[]}`)
		diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, []byte(message), "unclassified")
		encoded, root := continuationDiagnosticTestEncoded(t, diagnostic)
		if root.Get("upstream_error.message.sha256").String() != continuationDiagnosticTestDigest(message) ||
			root.Get("upstream_error.message.bytes").Int() != int64(len(message)) ||
			root.Get("upstream_error.message.characters").Int() != int64(utf8.RuneCountInString(message)) {
			t.Fatal("non-JSON upstream error must retain complete message fingerprint and lengths")
		}
		for _, field := range []string{"error_type", "error_code", "error_param", "message"} {
			if root.Get("upstream_error." + field + ".value").Exists() {
				t.Errorf("non-JSON upstream %s must not contain a plaintext value", field)
			}
		}
		hints := make(map[string]bool)
		root.Get("upstream_error.hints").ForEach(func(_, hint gjson.Result) bool {
			hints[hint.String()] = true
			return true
		})
		if !hints["unknown parameter"] || !hints["prompt_cache_key"] {
			t.Fatal("non-JSON upstream error lost the fixed diagnostic hint categories")
		}
		continuationDiagnosticTestNoPlaintext(t, encoded, message, "private-non-json-secret-71241")
	})

	for _, tc := range []struct {
		name  string
		value any
	}{
		{"api_key", "sk-privatecredential481735"},
		{"unknown_identifier", "private_credential_identifier_58493"},
		{"bearer", "Bearer private-bearer-material-59314"},
		{"email", "diagnostic-private-481735@example.invalid"},
		{"url", "https://example.invalid/error?access_token=private-query-59317"},
		{"crlf", "private-first-line-51973\r\nAuthorization: Bearer private-second-line-71935"},
		{"overlong_unicode", strings.Repeat("私密字段", 4096)},
		{"object", map[string]any{"unexpected": "private-object-value-59317"}},
		{"array", []any{"private-array-value-59317"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := continuationDiagnosticTestJSON(t, map[string]any{"error": map[string]any{
				"type": tc.value, "code": tc.value, "param": tc.value, "message": tc.value,
			}})
			body := []byte(`{"input":[]}`)
			diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, upstream, "private-untrusted-classification-59317")
			encoded, root := continuationDiagnosticTestEncoded(t, diagnostic)
			if root.Get("classification").String() != "unclassified" {
				t.Fatal("untrusted classification must become a fixed fallback")
			}
			for _, field := range []string{"error_type", "error_code", "error_param", "message"} {
				if root.Get("upstream_error." + field + ".value").Exists() {
					t.Errorf("unknown upstream %s retained a plaintext value", field)
				}
			}
			continuationDiagnosticTestNoPlaintext(t, encoded, "private-untrusted-classification-59317", "private-object-value-59317", "private-array-value-59317")
			if value, ok := tc.value.(string); ok {
				continuationDiagnosticTestNoPlaintext(t, encoded, value)
				if root.Get("upstream_error.error_code.bytes").Int() != int64(len(value)) ||
					root.Get("upstream_error.error_code.characters").Int() != int64(utf8.RuneCountInString(value)) {
					t.Fatal("unknown string length evidence was lost")
				}
				if root.Get("upstream_error.error_code.sha256").String() != continuationDiagnosticTestDigest(value) {
					t.Fatal("unknown string fingerprint must cover its complete original value")
				}
			}
		})
	}
}

func TestOpenAIContinuationDiagnosticStructure(t *testing.T) {
	const sourceKey = "private-source-cache-481735"
	const wireKey = "private-wire-cache-519357"
	const sourceInstructions = "私密原始提示词-419357"
	const wireInstructions = "私密最终提示词-719357"
	const previous = "resp_private_previous_59317"
	const incomingSession = "private-incoming-session-59317"
	const wireSession = "private-wire-session-51937"
	const encrypted = "private-reasoning-ciphertext-51397"
	const compacted = "private-compaction-ciphertext-71359"
	const output = "private-tool-output-819357"
	items := []any{
		map[string]any{"type": "message", "role": "user", "content": "private-user-content-719357"},
		map[string]any{"type": "reasoning", "encrypted_content": encrypted},
		map[string]any{"type": "compaction", "encrypted_content": compacted},
		map[string]any{"type": "item_reference", "id": "private-reference-719357"},
		map[string]any{"type": "function_call", "call_id": "call-private-paired-51397", "name": "private-function-57391", "arguments": "private-arguments-19357"},
		map[string]any{"type": "function_call", "call_id": "call-private-paired-51397"},
		map[string]any{"type": "function_call", "call_id": "call-private-unpaired-71395"},
		map[string]any{"type": "function_call"},
		map[string]any{"type": "function_call_output", "call_id": "call-private-paired-51397", "output": output},
		map[string]any{"type": "function_call_output", "call_id": "call-private-paired-51397", "output": output},
		map[string]any{"type": "function_call_output", "call_id": "call-private-orphan-15397", "output": output},
		map[string]any{"type": "function_call_output", "output": output},
		map[string]any{"type": "private-unknown-item-type-59137", "content": "private-unknown-content-71359"},
	}
	incoming := continuationDiagnosticTestJSON(t, map[string]any{
		"prompt_cache_key": sourceKey, "instructions": sourceInstructions,
		"previous_response_id": previous, "input": items,
	})
	wire := continuationDiagnosticTestJSON(t, map[string]any{
		"prompt_cache_key": wireKey, "instructions": wireInstructions,
		"previous_response_id": previous, "input": items,
	})
	prepared := []byte(`{"prompt_cache_key":"private-obsolete-prepared-71935","input":[]}`)
	c := continuationDiagnosticTestContext(incoming)
	c.Request.Header.Set("session_id", incomingSession)
	c.Request.Header.Set("conversation_id", "private-incoming-conversation-71359")
	req := continuationDiagnosticTestRequest(t, wire)
	req.Header.Set("session_id", wireSession)
	req.Header.Set("conversation_id", "private-wire-conversation-73915")
	req.Header.Set("Authorization", "Bearer private-authentication-53197")
	liveBody := &continuationDiagnosticTestReadCloser{reader: bytes.NewReader(wire)}
	req.Body = liveBody
	incomingBefore, wireBefore, preparedBefore := bytes.Clone(incoming), bytes.Clone(wire), bytes.Clone(prepared)
	incomingHeaders, upstreamHeaders := c.Request.Header.Clone(), req.Header.Clone()
	upstream := []byte(`{"error":{"message":"private-failure-message-71359"}}`)
	first := buildOpenAIContinuationDiagnostic(c, incoming, req, prepared, upstream, "opaque_tool_chain_400")
	encoded, root := continuationDiagnosticTestEncoded(t, first)
	second := buildOpenAIContinuationDiagnostic(c, incoming, req, prepared, upstream, "opaque_tool_chain_400")
	secondEncoded, _ := continuationDiagnosticTestEncoded(t, second)
	if encoded != secondEncoded {
		t.Fatal("identical inputs must have deterministic diagnostic output")
	}
	for path, expected := range map[string]string{
		"classification":                "opaque_tool_chain_400",
		"incoming.body_source":          "forwarding_entry",
		"wire.body_source":              "actual_request",
		"incoming.prompt_cache.sha256":  continuationDiagnosticTestDigest(sourceKey),
		"wire.prompt_cache.sha256":      continuationDiagnosticTestDigest(wireKey),
		"incoming.instructions.sha256":  continuationDiagnosticTestDigest(sourceInstructions),
		"wire.instructions.sha256":      continuationDiagnosticTestDigest(wireInstructions),
		"wire.previous_response.sha256": continuationDiagnosticTestDigest(previous),
		"incoming.session.sha256":       continuationDiagnosticTestDigest(incomingSession),
		"wire.session.sha256":           continuationDiagnosticTestDigest(wireSession),
	} {
		if root.Get(path).String() != expected {
			t.Errorf("diagnostic contract mismatch at %s", path)
		}
	}
	if root.Get("wire.body_bytes").Int() != int64(len(wire)) || root.Get("wire.inspection_limited").Bool() {
		t.Fatal("wire summary did not inspect the actual request body")
	}
	if root.Get("wire.instructions.bytes").Int() != int64(len(wireInstructions)) ||
		root.Get("wire.instructions.characters").Int() != int64(utf8.RuneCountInString(wireInstructions)) {
		t.Fatal("instruction byte and character lengths were conflated")
	}
	for field, expected := range map[string]int64{
		"items": 13, "messages": 1, "reasoning": 1, "compaction": 1, "references": 1,
		"calls": 4, "outputs": 4, "other": 1, "encrypted": 2,
		"missing_call_ids": 1, "missing_output_ids": 1, "duplicate_call_ids": 1,
		"duplicate_output_ids": 1, "unmatched_outputs": 1, "unpaired_calls": 1,
	} {
		if got := root.Get("wire.history." + field).Int(); got != expected {
			t.Errorf("wire history %s = %d, want %d", field, got, expected)
		}
	}
	for _, field := range []string{"call_ids_sha256", "output_ids_sha256", "encrypted_sha256"} {
		if len(root.Get("wire.history."+field).String()) != 64 {
			t.Errorf("history fingerprint %s is missing or not fixed-size", field)
		}
	}
	continuationDiagnosticTestNoPlaintext(t, encoded, sourceKey, wireKey, sourceInstructions, wireInstructions,
		previous, incomingSession, wireSession, encrypted, compacted, output,
		"private-authentication-53197", "private-user-content-719357", "private-reference-719357",
		"call-private-paired-51397", "call-private-unpaired-71395", "call-private-orphan-15397",
		"private-function-57391", "private-arguments-19357", "private-unknown-item-type-59137",
		"private-unknown-content-71359", "private-failure-message-71359")
	if !bytes.Equal(incoming, incomingBefore) || !bytes.Equal(wire, wireBefore) || !bytes.Equal(prepared, preparedBefore) ||
		!reflect.DeepEqual(c.Request.Header, incomingHeaders) || !reflect.DeepEqual(req.Header, upstreamHeaders) ||
		liveBody.reads != 0 || liveBody.closed {
		t.Fatal("diagnostics must not change bytes, headers, or consume the live request body")
	}

	for _, tc := range []struct{ name, body, kind string }{
		{"missing", `{}`, "missing"},
		{"null", `{"prompt_cache_key":null}`, "null"},
		{"empty", `{"prompt_cache_key":""}`, "string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, nil, "unclassified")
			_, view := continuationDiagnosticTestEncoded(t, diagnostic)
			if view.Get("incoming.prompt_cache.kind").String() != tc.kind {
				t.Fatal("missing, null and empty string fingerprints must remain distinguishable")
			}
		})
	}
}

type continuationDiagnosticTestReadCloser struct {
	reader io.Reader
	reads  int
	closed bool
}

func (r *continuationDiagnosticTestReadCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func (r *continuationDiagnosticTestReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestOpenAIContinuationDiagnosticResourceBounds(t *testing.T) {
	t.Run("history_scan_has_a_hard_limit", func(t *testing.T) {
		body := []byte(`{"input":[` + strings.TrimSuffix(strings.Repeat(`{"type":"function_call","call_id":"private-large-history-id-57319"},`, 1100), ",") + `]}`)
		diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, nil, "unclassified")
		encoded, root := continuationDiagnosticTestEncoded(t, diagnostic)
		if !root.Get("incoming.history.scan_limited").Bool() || root.Get("incoming.history.items").Int() > 1024 {
			t.Fatal("history inspection must stop at its bounded item count and mark partial evidence")
		}
		continuationDiagnosticTestNoPlaintext(t, encoded, "private-large-history-id-57319")
	})

	t.Run("oversized_incoming_and_actual_clone", func(t *testing.T) {
		body := []byte(`{"instructions":"` + strings.Repeat("private-large-instruction-57319", 300_000) + `"}`)
		if len(body) <= 8*1024*1024 {
			t.Fatal("fixture must exceed the body inspection budget")
		}
		req := continuationDiagnosticTestRequest(t, body)
		liveBody := &continuationDiagnosticTestReadCloser{reader: bytes.NewReader(body)}
		req.Body = liveBody
		diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, req, []byte(`{}`), nil, "unclassified")
		encoded, root := continuationDiagnosticTestEncoded(t, diagnostic)
		if !root.Get("incoming.inspection_limited").Bool() || !root.Get("wire.inspection_limited").Bool() {
			t.Fatal("oversized inputs must be explicitly marked as incompletely inspected")
		}
		if root.Get("wire.body_bytes").Int() != int64(len(body)) {
			t.Fatal("oversized actual request must retain its known complete ContentLength")
		}
		if liveBody.reads != 0 || liveBody.closed {
			t.Fatal("size-limited diagnostics must leave the live request body untouched")
		}
		continuationDiagnosticTestNoPlaintext(t, encoded, "private-large-instruction-57319")
	})

	t.Run("unreadable_actual_clone_is_not_a_request_failure", func(t *testing.T) {
		body := []byte(`{"prompt_cache_key":"private-live-body-cache-57319"}`)
		req := continuationDiagnosticTestRequest(t, body)
		liveBody := &continuationDiagnosticTestReadCloser{reader: bytes.NewReader(body)}
		req.Body = liveBody
		req.GetBody = func() (io.ReadCloser, error) {
			return nil, errors.New("private-clone-error-with-credential-71935")
		}
		diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, req, body, nil, "unclassified")
		encoded, root := continuationDiagnosticTestEncoded(t, diagnostic)
		if !root.Get("wire.inspection_limited").Bool() {
			t.Fatal("an unreadable clone must not look like fully verified wire evidence")
		}
		if liveBody.reads != 0 || liveBody.closed {
			t.Fatal("clone failure must not fall back to consuming the live request body")
		}
		continuationDiagnosticTestNoPlaintext(t, encoded, "private-live-body-cache-57319", "private-clone-error-with-credential-71935")
	})
}

func TestOpenAIContinuationDiagnosticOpsQueueRoundTrip(t *testing.T) {
	body := []byte(`{"prompt_cache_key":"private-queue-cache-57319","instructions":"private-queue-instructions-73519","input":[]}`)
	upstream := []byte(`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","param":"prompt_cache_key","message":"private-queue-message-57319"}}`)
	diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, upstream, "invalid_encrypted_content")
	_, before := continuationDiagnosticTestEncoded(t, diagnostic)
	entry := &OpsInsertErrorLogInput{}
	const total = 20
	for i := 0; i < total; i++ {
		entry.UpstreamErrors = append(entry.UpstreamErrors, &OpsUpstreamErrorEvent{
			AtUnixMs: int64(i + 1), UpstreamStatusCode: http.StatusBadRequest,
			Kind: "continuation_state", Message: OpenAIContinuationStateUnavailableClientMessage,
			ContinuationDiagnostic: diagnostic,
		})
	}
	if err := SanitizeOpsUpstreamErrorsForQueue(entry); err != nil {
		t.Fatal(err)
	}
	if entry.UpstreamErrors != nil || entry.UpstreamErrorsJSON == nil {
		t.Fatal("queue sanitization must release event pointers and retain serialized evidence")
	}
	events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != total {
		t.Fatal("diagnostics unexpectedly changed the retained event count")
	}
	for i, event := range events {
		if i < total-opsUpstreamErrorsBodyWindow {
			if event.ContinuationDiagnostic != nil {
				t.Fatal("old attempts retained diagnostic payloads outside the body window")
			}
			continue
		}
		_, after := continuationDiagnosticTestEncoded(t, event.ContinuationDiagnostic)
		for _, path := range []string{
			"classification", "wire.prompt_cache.sha256", "incoming.prompt_cache.sha256",
			"wire.instructions.sha256", "upstream_error.error_code.value", "upstream_error.error_param.value",
		} {
			if !before.Get(path).Exists() || after.Get(path).String() != before.Get(path).String() {
				t.Errorf("diagnostic field %s was lost in the Ops storage roundtrip", path)
			}
		}
	}
	continuationDiagnosticTestNoPlaintext(t, *entry.UpstreamErrorsJSON,
		"private-queue-cache-57319", "private-queue-instructions-73519", "private-queue-message-57319")
	_, stillOriginal := continuationDiagnosticTestEncoded(t, diagnostic)
	if stillOriginal.Raw != before.Raw {
		t.Fatal("queue sanitization modified the shared source diagnostic")
	}
}

func TestOpenAIContinuationDiagnosticRequestRejectionOpsQueueRoundTrip(t *testing.T) {
	body := []byte(`{"prompt_cache_key":"private-rejection-cache-57319","instructions":"private-rejection-instructions-73519","input":[]}`)
	upstream := []byte(`{"error":{"type":"invalid_request_error","code":"missing_required_parameter","param":"input[11].namespace","message":"private-rejection-message-57319"}}`)
	for _, tc := range []struct {
		name, classification, expected, kind string
	}{
		{"validation", "request_validation", "request_validation", "request_rejected"},
		{"generic_400", "unclassified_bad_request", "unclassified_bad_request", "request_rejected"},
		{"empty", "", "unclassified", "continuation_state"},
		{"legacy", "opaque_tool_chain_400", "opaque_tool_chain_400", "continuation_state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, upstream, tc.classification)
			before, root := continuationDiagnosticTestEncoded(t, diagnostic)
			if root.Get("classification").String() != tc.expected {
				t.Fatal("producer classification did not use the explicit diagnostic contract")
			}
			message := OpenAIRequestRejectedClientMessage
			if tc.kind == "continuation_state" {
				message = OpenAIContinuationStateUnavailableClientMessage
			}
			entry := &OpsInsertErrorLogInput{UpstreamErrors: []*OpsUpstreamErrorEvent{{
				AtUnixMs: 1, UpstreamStatusCode: http.StatusBadRequest,
				Kind: tc.kind, Message: message, ContinuationDiagnostic: diagnostic,
			}}}
			if err := SanitizeOpsUpstreamErrorsForQueue(entry); err != nil {
				t.Fatal(err)
			}
			if entry.UpstreamErrors != nil || entry.UpstreamErrorsJSON == nil {
				t.Fatal("queue sanitization did not retain serialized diagnostic evidence")
			}
			events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Kind != tc.kind || events[0].Message != message {
				t.Fatal("request-rejection or legacy event semantics changed in storage")
			}
			_, after := continuationDiagnosticTestEncoded(t, events[0].ContinuationDiagnostic)
			for path, expected := range map[string]string{
				"classification":                    tc.expected,
				"upstream_error.error_type.value":   "invalid_request_error",
				"upstream_error.error_code.value":   "missing_required_parameter",
				"upstream_error.error_param.value":  "input[11].namespace",
				"upstream_error.error_param.sha256": continuationDiagnosticTestDigest("input[11].namespace"),
				"wire.prompt_cache.sha256":          continuationDiagnosticTestDigest("private-rejection-cache-57319"),
			} {
				if after.Get(path).String() != expected {
					t.Errorf("diagnostic field %s changed in request-rejection queue roundtrip", path)
				}
			}
			continuationDiagnosticTestNoPlaintext(t, *entry.UpstreamErrorsJSON,
				"private-rejection-cache-57319", "private-rejection-instructions-73519", "private-rejection-message-57319")
			stillOriginal, _ := continuationDiagnosticTestEncoded(t, diagnostic)
			if stillOriginal != before {
				t.Fatal("queue sanitization changed the shared source diagnostic")
			}
		})
	}
}
