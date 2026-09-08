//go:build reasoning_fidelity && namespace_roundtrip

package service_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Inspect only the already-bounded in-memory response capture. Selection of an
// SSE error envelope never interprets message text; the production diagnostic
// producer and independent sanitizer own every value that can leave the process.
func namespaceSafeErrorDetail(body []byte, contentType string) json.RawMessage {
	if len(body) >= fidelityMaxBody {
		return service.NamespaceRoundtripSafeErrorForTest(nil, true)
	}
	// Error gateways can label a complete JSON rejection as SSE. A valid JSON
	// envelope is already unambiguous and must not be discarded by that header.
	if json.Valid(body) {
		return service.NamespaceRoundtripSafeErrorForTest(body, false)
	}
	trimmed := bytes.TrimSpace(body)
	stream := strings.Contains(strings.ToLower(contentType), "text/event-stream") ||
		bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte(":"))
	if !stream {
		return service.NamespaceRoundtripSafeErrorForTest(body, !json.Valid(body))
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), fidelityMaxBody)
	scanner.Split(fidelitySSELine)
	var data, found []byte
	eventName := ""
	limited := false
	apply := func() {
		if len(data) == 0 {
			return
		}
		payload := bytes.TrimSuffix(data, []byte{'\n'})
		if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
			return
		}
		object, err := fidelityJSONObject(payload)
		if err != nil {
			limited = true
			return
		}
		kind := ""
		if raw, present := object["type"]; present && json.Unmarshal(raw, &kind) != nil {
			limited = true
			return
		}
		if kind == "" {
			kind = eventName
		} else if eventName != "" && eventName != "message" && eventName != kind {
			limited = true
			return
		}
		switch kind {
		case "response.failed", "error", "response.error":
			if found == nil {
				found = append([]byte(nil), payload...)
			} else {
				// More than one failure cannot be represented by a single detail.
				limited = true
			}
		}
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			apply()
			data, eventName = data[:0], ""
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte{':'})
		value = bytes.TrimPrefix(value, []byte{' '})
		switch string(field) {
		case "event":
			eventName = string(value)
		case "data":
			data = append(data, value...)
			data = append(data, '\n')
		}
	}
	limited = limited || scanner.Err() != nil || len(data) > 0 || found == nil
	return service.NamespaceRoundtripSafeErrorForTest(found, limited)
}

type namespaceSafeFingerprint struct {
	Kind       string `json:"kind"`
	Bytes      int    `json:"bytes"`
	Characters int    `json:"characters"`
	SHA256     string `json:"sha256,omitempty"`
	Value      string `json:"value,omitempty"`
}

type namespaceSafeDiagnostic struct {
	ErrorType         namespaceSafeFingerprint `json:"error_type"`
	ErrorCode         namespaceSafeFingerprint `json:"error_code"`
	ErrorParam        namespaceSafeFingerprint `json:"error_param"`
	Hints             []string                 `json:"hints,omitempty"`
	InspectionLimited bool                     `json:"inspection_limited"`
}

const namespaceDiagnosticFixture = `{"error":{"type":"invalid_request_error","code":"missing_required_parameter","param":"input[11].namespace","message":"Missing required parameter: input[11].namespace private-error-secret"}}`

func namespaceReadSafeDiagnostic(t *testing.T, raw json.RawMessage) namespaceSafeDiagnostic {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var safe namespaceSafeDiagnostic
	if decoder.Decode(&safe) != nil || decoder.Decode(new(any)) != io.EOF || bytes.Contains(raw, []byte("private-error-secret")) {
		t.Fatal("unsafe or invalid diagnostic projection")
	}
	return safe
}

func namespaceAssertFixtureDiagnostic(t *testing.T, raw json.RawMessage) {
	t.Helper()
	safe := namespaceReadSafeDiagnostic(t, raw)
	if safe.InspectionLimited || safe.ErrorType.Value != "invalid_request_error" || safe.ErrorCode.Value != "missing_required_parameter" ||
		safe.ErrorParam.Value != "input[11].namespace" || safe.ErrorParam.Kind != "string" || safe.ErrorParam.Bytes != 19 || safe.ErrorParam.Characters != 19 ||
		safe.ErrorParam.SHA256 != "8848fabb11677b65c6829891a90cfc2a4ef9776d6e1e5596b9bfcbd703f57a67" || len(safe.Hints) != 1 || safe.Hints[0] != "missing" {
		t.Fatal("existing allowlisted error identity was not preserved")
	}
}

func TestNamespaceRoundtripSafeFailureDiagnostic(t *testing.T) {
	t.Run("json_priority_and_complete_sse_envelopes", func(t *testing.T) {
		for _, contentType := range []string{"application/json", "text/event-stream"} {
			namespaceAssertFixtureDiagnostic(t, namespaceSafeErrorDetail([]byte(namespaceDiagnosticFixture), contentType))
		}
		bare := "data: {\"type\":\"error\",\"code\":\"missing_required_parameter\",\"param\":\"input[11].namespace\",\"message\":\"Missing private-error-secret\"}\n\n"
		safeBare := namespaceReadSafeDiagnostic(t, namespaceSafeErrorDetail([]byte(bare), "text/event-stream"))
		if safeBare.InspectionLimited || safeBare.ErrorType.Value != "" || safeBare.ErrorType.SHA256 != fidelityHash([]byte("error")) ||
			safeBare.ErrorCode.Value != "missing_required_parameter" || safeBare.ErrorParam.Value != "input[11].namespace" {
			t.Fatal("bare SSE error lost its safe structured fields")
		}
		var errorObject map[string]json.RawMessage
		_ = json.Unmarshal([]byte(namespaceDiagnosticFixture), &errorObject)
		for _, kind := range []string{"response.failed", "error", "response.error"} {
			payload := `{"type":"` + kind + `","error":` + string(errorObject["error"]) + `}`
			if kind == "response.failed" {
				payload = `{"type":"response.failed","response":{"status":"failed","error":` + string(errorObject["error"]) + `}}`
			}
			for _, newline := range []string{"\n", "\r\n", "\r"} {
				// Event names and multi-line data use the same grammar as the strict
				// response parser. The completion marker contributes no error text.
				payload = strings.Replace(payload, `,"error":`, ",\ndata: \"error\":", 1)
				frame := "event: " + kind + "\ndata: " + payload + "\n\ndata: [DONE]\n\n"
				namespaceAssertFixtureDiagnostic(t, namespaceSafeErrorDetail([]byte(strings.ReplaceAll(frame, "\n", newline)), "text/event-stream"))
			}
		}
	})
	t.Run("unknown_fields_null_and_missing_stay_safe", func(t *testing.T) {
		body := `{"error":{"type":"private-type-secret","code":"private-code-secret","param":"max_output_tokens","message":"private-error-secret"}}`
		raw := namespaceSafeErrorDetail([]byte(body), "application/json")
		safe := namespaceReadSafeDiagnostic(t, raw)
		for index, expected := range []string{"private-type-secret", "private-code-secret", "max_output_tokens"} {
			actual := []namespaceSafeFingerprint{safe.ErrorType, safe.ErrorCode, safe.ErrorParam}[index]
			if actual.Kind != "string" || actual.Value != "" || actual.SHA256 != fidelityHash([]byte(expected)) || actual.Bytes != len(expected) || bytes.Contains(raw, []byte(expected)) {
				t.Fatal("unknown diagnostic text escaped the existing allowlist")
			}
		}
		safe = namespaceReadSafeDiagnostic(t, namespaceSafeErrorDetail([]byte(`{"error":{"type":null,"param":null}}`), "application/json"))
		if safe.ErrorType.Kind != "null" || safe.ErrorParam.Kind != "null" || safe.ErrorCode.Kind != "missing" || safe.ErrorParam.Value != "" {
			t.Fatal("missing or explicit-null fields were invented")
		}
	})
	t.Run("malformed_unterminated_or_unrelated_events_are_limited", func(t *testing.T) {
		for _, body := range []string{
			"data: " + namespaceDiagnosticFixture,
			"event: response.completed\ndata: {\"type\":\"response.failed\",\"response\":" + namespaceDiagnosticFixture + "}\n\n",
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"private-error-secret\"}\n\n",
			"data: {\"type\":\"error\",\"type\":\"response.failed\"}\n\n",
		} {
			safe := namespaceReadSafeDiagnostic(t, namespaceSafeErrorDetail([]byte(body), "text/event-stream"))
			if !safe.InspectionLimited || safe.ErrorParam.Value != "" || safe.ErrorCode.Value != "" {
				t.Fatal("incomplete or unrelated event fabricated an error detail")
			}
		}
		for _, count := range []int{fidelityMaxBody, fidelityMaxBody + 1} {
			safe := namespaceReadSafeDiagnostic(t, namespaceSafeErrorDetail(bytes.Repeat([]byte{'x'}, count), "application/json"))
			if !safe.InspectionLimited || safe.ErrorType.Value != "" || safe.ErrorParam.Value != "" {
				t.Fatal("bounded capture limit was treated as complete")
			}
		}
	})
	t.Run("forward_failure_emits_safe_detail_and_stops_after_one_post", func(t *testing.T) {
		for _, failure := range []struct {
			status            int
			contentType, body string
		}{
			{400, "application/json", namespaceDiagnosticFixture},
			{400, "text/event-stream", namespaceDiagnosticFixture},
			{200, "text/event-stream", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"type\":\"invalid_request_error\",\"code\":\"missing_required_parameter\",\"param\":\"input[11].namespace\",\"message\":\"Missing private-error-secret\"}}}\n\n"},
			{200, "text/event-stream", "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"code\":\"missing_required_parameter\",\"param\":\"input[11].namespace\",\"message\":\"Missing private-error-secret\"}}\n\n"},
		} {
			fake := &namespaceOfflineUpstream{firstFailureBody: failure.body, firstFailureStatus: failure.status, firstFailureType: failure.contentType}
			h, output := namespaceOfflineHarness(t, namespaceOfflineGrants(), fake)
			h.runSequences()
			if h.stopped == "" || h.astraCompleted || h.lunaCompleted || h.upstream.attempts != 1 || fake.calls != 1 || h.summary()["total_attempts"] != 2 {
				t.Fatal("upstream rejection was continued or lost cumulative consumption")
			}
			decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
			results := 0
			for {
				var result namespaceResult
				if err := decoder.Decode(&result); err != nil {
					if err == io.EOF {
						break
					}
					t.Fatal("invalid failure protocol output")
				}
				if result.Type != "attempt_result" {
					continue
				}
				results++
				if result.Completed || result.Status != "failed" || result.Attempts != 1 || result.HTTPStatus != failure.status ||
					result.Effort != "ultra" || result.WireEffort != "max" || result.WireModel != "gpt-6-astra-ssvip" ||
					!result.ModelMatches || !result.EffortMatches || fake.requestedEfforts[0] != "max" {
					t.Fatal("failed attempt reported an invalid terminal result")
				}
				namespaceAssertFixtureDiagnostic(t, result.ErrorDetail)
			}
			if results != 1 || bytes.Contains(output.Bytes(), []byte("private-error-secret")) {
				t.Fatal("failure protocol duplicated a result or leaked provider text")
			}
		}
	})
}
