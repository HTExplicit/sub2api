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
	if len(encoded) > openAIContinuationDiagnosticJSONLimit {
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

// Administrators see the upstream error fields verbatim (bounded) next to the
// fingerprints of the complete values. This replaces the earlier contract that
// kept only known enum values and hashes.
func TestOpenAIContinuationDiagnosticRetainsBoundedErrorValues(t *testing.T) {
	t.Run("original_error_fields", func(t *testing.T) {
		const message = "encrypted content could not be verified: upstream-message-69317"
		upstream := continuationDiagnosticTestJSON(t, map[string]any{
			"error": map[string]any{
				"type": "invalid_request_error", "code": "invalid_encrypted_content",
				"param": "input[12].encrypted_content", "message": message,
				"debug": map[string]any{"credentials": "unlisted-debug-field-49217"},
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
			"upstream_error.message.value":     message,
			"upstream_error.message.sha256":    continuationDiagnosticTestDigest(message),
		} {
			if root.Get(path).String() != expected {
				t.Errorf("diagnostic contract mismatch at %s", path)
			}
		}
		// Only the error fields are projected; the complete body stays in the
		// attempt's upstream detail.
		continuationDiagnosticTestNoPlaintext(t, encoded, "unlisted-debug-field-49217")
	})

	t.Run("every_param_is_kept", func(t *testing.T) {
		for _, param := range []string{"input[11].namespace", "input.11.namespace", "tools[0].namespace", "input[11].namespace.custom-field-51937", "input[11].namespace?token=query-71359"} {
			t.Run(param, func(t *testing.T) {
				body := []byte(`{"input":[]}`)
				upstream := continuationDiagnosticTestJSON(t, map[string]any{"error": map[string]any{
					"type": "invalid_request_error", "param": param,
					"message": "Missing namespace: upstream-message-31957",
				}})
				diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, upstream, "request_validation")
				for _, candidate := range []*OpenAIContinuationDiagnostic{diagnostic, sanitizeOpenAIContinuationDiagnostic(diagnostic)} {
					_, root := continuationDiagnosticTestEncoded(t, candidate)
					value := root.Get("upstream_error.error_param")
					if value.Get("sha256").String() != continuationDiagnosticTestDigest(param) || value.Get("value").String() != param {
						t.Fatal("the upstream param must keep its value and complete fingerprint")
					}
					if root.Get("upstream_error.message.value").String() != "Missing namespace: upstream-message-31957" {
						t.Fatal("the upstream message must be kept")
					}
				}
			})
		}
	})

	t.Run("non_json", func(t *testing.T) {
		const message = "unknown parameter: prompt_cache_key; upstream-non-json-71241"
		body := []byte(`{"input":[]}`)
		diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, []byte(message), "unclassified")
		_, root := continuationDiagnosticTestEncoded(t, diagnostic)
		if root.Get("upstream_error.message.sha256").String() != continuationDiagnosticTestDigest(message) ||
			root.Get("upstream_error.message.bytes").Int() != int64(len(message)) ||
			root.Get("upstream_error.message.characters").Int() != int64(utf8.RuneCountInString(message)) ||
			root.Get("upstream_error.message.value").String() != message {
			t.Fatal("a non-JSON upstream error must keep its text, complete fingerprint and lengths")
		}
		for _, field := range []string{"error_type", "error_code", "error_param"} {
			if root.Get("upstream_error." + field + ".value").Exists() {
				t.Errorf("a non-JSON upstream error has no structured %s value", field)
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
	})

	for _, tc := range []struct {
		name  string
		value any
	}{
		{"api_key", "sk-credential481735"},
		{"unknown_identifier", "credential_identifier_58493"},
		{"bearer", "Bearer bearer-material-59314"},
		{"email", "diagnostic-481735@example.invalid"},
		{"url", "https://example.invalid/error?access_token=query-59317"},
		{"crlf", "first-line-51973\r\nAuthorization: Bearer second-line-71935"},
		{"overlong_unicode", strings.Repeat("私密字段", 4096)},
		{"object", map[string]any{"unexpected": "object-value-59317"}},
		{"array", []any{"array-value-59317"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := continuationDiagnosticTestJSON(t, map[string]any{"error": map[string]any{
				"type": tc.value, "code": tc.value, "param": tc.value, "message": tc.value,
			}})
			body := []byte(`{"input":[]}`)
			diagnostic := buildOpenAIContinuationDiagnostic(continuationDiagnosticTestContext(body), body, nil, body, upstream, "untrusted-classification-59317")
			_, root := continuationDiagnosticTestEncoded(t, diagnostic)
			if root.Get("classification").String() != "unclassified" {
				t.Fatal("an unknown classification must become the fixed fallback")
			}
			value, ok := tc.value.(string)
			if !ok {
				if root.Get("upstream_error.error_code.value").String() != string(continuationDiagnosticTestJSON(t, tc.value)) {
					t.Fatal("a non-string error field must keep its JSON text")
				}
				return
			}
			if root.Get("upstream_error.error_code.bytes").Int() != int64(len(value)) ||
				root.Get("upstream_error.error_code.characters").Int() != int64(utf8.RuneCountInString(value)) ||
				root.Get("upstream_error.error_code.sha256").String() != continuationDiagnosticTestDigest(value) {
				t.Fatal("fingerprint and lengths must describe the complete original value")
			}
			for field, limit := range map[string]int{"error_code": openAIContinuationDiagnosticCodeValueLimit, "message": openAIContinuationDiagnosticMessageValueLimit} {
				kept := root.Get("upstream_error." + field + ".value").String()
				truncated := root.Get("upstream_error." + field + ".truncated").Bool()
				if len(value) <= limit {
					if kept != value || truncated {
						t.Fatalf("%s must be kept verbatim", field)
					}
					continue
				}
				if !truncated || kept == "" || len(kept) > limit || !strings.HasPrefix(value, kept) || !utf8.ValidString(kept) {
					t.Fatalf("%s over its bound must keep a marked, valid prefix", field)
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
		// The bounded original values sit next to their fingerprints.
		"incoming.prompt_cache.value":     sourceKey,
		"wire.prompt_cache.value":         wireKey,
		"incoming.instructions.value":     sourceInstructions,
		"wire.instructions.value":         wireInstructions,
		"wire.previous_response.value":    previous,
		"incoming.session.value":          incomingSession,
		"wire.session.value":              wireSession,
		"incoming.conversation.value":     "private-incoming-conversation-71359",
		"wire.conversation.value":         "private-wire-conversation-73915",
		"upstream_error.message.value":    "private-failure-message-71359",
		"incoming.previous_response.kind": "string",
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
	// History content and request headers other than session/conversation are
	// summarized by counts and set fingerprints only.
	continuationDiagnosticTestNoPlaintext(t, encoded, encrypted, compacted, output,
		"private-authentication-53197", "private-user-content-719357", "private-reference-719357",
		"call-private-paired-51397", "call-private-unpaired-71395", "call-private-orphan-15397",
		"private-function-57391", "private-arguments-19357", "private-unknown-item-type-59137",
		"private-unknown-content-71359")
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

func TestOpenAIContinuationDiagnosticCountsNestedCiphertext(t *testing.T) {
	history := continuationDiagnosticHistory(gjson.Parse(`[{"type":"reasoning","encrypted_content":"a"},{"type":"agent_message","content":[{"type":"input_text","text":"Payload:"},{"type":"encrypted_content","encrypted_content":"b"}]},{"type":"message","role":"user","content":"text"}]`))
	if history.Other != 1 || history.Encrypted != 1 || history.NestedEncrypted != 1 {
		t.Fatalf("other=%d encrypted=%d nested=%d", history.Other, history.Encrypted, history.NestedEncrypted)
	}
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
		// The entry snapshot keeps its value; the unreadable wire has none, and
		// the clone error is not request evidence.
		if root.Get("incoming.prompt_cache.value").String() != "private-live-body-cache-57319" || root.Get("wire.prompt_cache.value").Exists() {
			t.Fatal("only the inspected entry body can supply values")
		}
		continuationDiagnosticTestNoPlaintext(t, encoded, "private-clone-error-with-credential-71935")
	})

	t.Run("oversized_values_are_truncated_not_dropped", func(t *testing.T) {
		instructions := strings.Repeat("instruction-text-", 64<<10)
		key := strings.Repeat("k", 64<<10)
		body := continuationDiagnosticTestJSON(t, map[string]any{"instructions": instructions, "prompt_cache_key": key, "previous_response_id": key, "input": []any{}})
		upstream := continuationDiagnosticTestJSON(t, map[string]any{"error": map[string]any{"message": strings.Repeat("<message>", 64<<10), "code": key}})
		c := continuationDiagnosticTestContext(body)
		c.Request.Header.Set("session_id", key)
		c.Request.Header.Set("conversation_id", key)
		diagnostic := buildOpenAIContinuationDiagnostic(c, body, nil, body, upstream, "request_validation")
		_, root := continuationDiagnosticTestEncoded(t, diagnostic)
		value := root.Get("incoming.instructions")
		if value.Get("sha256").String() != continuationDiagnosticTestDigest(instructions) || value.Get("bytes").Int() != int64(len(instructions)) {
			t.Fatal("the fingerprint must still describe the complete instructions")
		}
		if !value.Get("truncated").Bool() || value.Get("value").String() == "" || !strings.HasPrefix(instructions, value.Get("value").String()) {
			t.Fatal("oversized instructions must keep a marked prefix instead of disappearing")
		}
		if !root.Get("upstream_error.message.truncated").Bool() || root.Get("upstream_error.message.value").String() == "" {
			t.Fatal("an oversized upstream message must keep a marked prefix")
		}
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
	// Every retained attempt keeps its diagnostic, including attempts older
	// than the body window; the entry byte budget still bounds the array.
	for _, event := range events {
		_, after := continuationDiagnosticTestEncoded(t, event.ContinuationDiagnostic)
		for _, path := range []string{
			"classification", "wire.prompt_cache.sha256", "incoming.prompt_cache.sha256",
			"wire.instructions.sha256", "upstream_error.error_code.value", "upstream_error.error_param.value",
			"wire.prompt_cache.value", "wire.instructions.value", "upstream_error.message.value",
		} {
			if !before.Get(path).Exists() || after.Get(path).String() != before.Get(path).String() {
				t.Errorf("diagnostic field %s was lost in the Ops storage roundtrip", path)
			}
		}
	}
	for _, value := range []string{"private-queue-cache-57319", "private-queue-instructions-73519", "private-queue-message-57319"} {
		if !strings.Contains(*entry.UpstreamErrorsJSON, value) {
			t.Fatal("stored diagnostics must keep the bounded original values")
		}
	}
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
			for _, value := range []string{"private-rejection-cache-57319", "private-rejection-instructions-73519", "private-rejection-message-57319"} {
				if !strings.Contains(*entry.UpstreamErrorsJSON, value) {
					t.Fatal("stored diagnostics must keep the bounded original values")
				}
			}
			stillOriginal, _ := continuationDiagnosticTestEncoded(t, diagnostic)
			if stillOriginal != before {
				t.Fatal("queue sanitization changed the shared source diagnostic")
			}
		})
	}
}
