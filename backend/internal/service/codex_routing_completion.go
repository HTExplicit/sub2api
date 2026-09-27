package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/tidwall/gjson"
)

const codexRoutingProbeReadLimit = 128 << 10

var ErrCodexRoutingModelMismatch = errors.New("upstream Codex response model does not match the requested model")

// Probe totals remain limited to 128 KiB; business readers explicitly select
// their existing stream/JSON read limits. The upstream error object, the failing
// event (bounded) and the local reason of a failure are kept for administrators.
type codexRoutingCompletion struct {
	Model          string
	RequestedModel string
	Completed      bool
	Failed         bool
	Mismatch       bool
	FailureCode    string
	FailureDetail  string
	UpstreamError  codexUpstreamError
	UpstreamEvent  []byte
	Body           []byte
	maxEventBytes  int
	pending        []byte
	scanFrom       int
}

type codexUpstreamError struct {
	Type, Code, Message, Param string
}

func (e codexUpstreamError) empty() bool {
	return e.Type == "" && e.Code == "" && e.Message == "" && e.Param == ""
}

func codexJSONText(value gjson.Result) string {
	switch {
	case !value.Exists() || value.Type == gjson.Null:
		return ""
	case value.Type == gjson.String:
		return value.String()
	}
	return value.Raw
}

func codexUpstreamErrorObject(value gjson.Result) codexUpstreamError {
	if value.Type == gjson.String {
		return codexUpstreamError{Message: value.String()}
	}
	if !value.IsObject() {
		return codexUpstreamError{}
	}
	return codexUpstreamError{Type: codexJSONText(value.Get("type")), Code: codexJSONText(value.Get("code")), Message: codexJSONText(value.Get("message")), Param: codexJSONText(value.Get("param"))}
}

// codexUpstreamErrorFromBody reads the upstream's own error object from a JSON
// error body ({"error":...}, {"response":{"error":...}} or {"detail":...}).
func codexUpstreamErrorFromBody(body []byte) codexUpstreamError {
	if !gjson.ValidBytes(body) {
		return codexUpstreamError{}
	}
	root := gjson.ParseBytes(body)
	for _, path := range []string{"error", "response.error", "detail"} {
		if found := codexUpstreamErrorObject(root.Get(path)); !found.empty() {
			return found
		}
	}
	return codexUpstreamError{}
}

// boundedCodexUpstreamBody keeps at most CodexRoutingUpstreamBodyLimit bytes
// without splitting a UTF-8 sequence.
func boundedCodexUpstreamBody(raw []byte) []byte {
	limit := extensionv1.CodexRoutingUpstreamBodyLimit
	if len(raw) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(raw[cut]) {
			cut--
		}
		raw = raw[:cut]
	}
	return append([]byte(nil), raw...)
}

func (o *codexRoutingCompletion) fail(code string) {
	o.Failed = true
	if o.FailureCode == "" {
		o.FailureCode = code
	}
}

func (o *codexRoutingCompletion) failWithDetail(code, detail string) {
	if o.FailureDetail == "" && !o.Failed {
		o.FailureDetail = detail
	}
	o.fail(code)
}

// recordUpstream keeps the first upstream failure: its error object and a
// summary of the failing event (see codexRoutingEventSummary).
func (o *codexRoutingCompletion) recordUpstream(found codexUpstreamError, event []byte) {
	if o.UpstreamEvent != nil || !o.UpstreamError.empty() {
		return
	}
	o.UpstreamError = found
	o.UpstreamEvent = codexRoutingEventSummary(event)
}

// codexRoutingEventSummary keeps what identifies an upstream failure: the
// event type, its error fields (top level, error, detail) and the response id,
// status, model, incomplete_details and error. The response instructions,
// tools, input and output of a business request are never copied.
func codexRoutingEventSummary(raw []byte) []byte {
	if !gjson.ValidBytes(raw) {
		return boundedCodexUpstreamBody(raw)
	}
	root := gjson.ParseBytes(raw)
	summary := map[string]any{}
	for _, key := range []string{"type", "code", "message", "param", "error", "detail"} {
		if value := root.Get(key); value.Exists() && value.Type != gjson.Null {
			summary[key] = json.RawMessage(value.Raw)
		}
	}
	if response := root.Get("response"); response.IsObject() {
		kept := map[string]json.RawMessage{}
		for _, key := range []string{"id", "status", "model", "incomplete_details", "error"} {
			if value := response.Get(key); value.Exists() && value.Type != gjson.Null {
				kept[key] = json.RawMessage(value.Raw)
			}
		}
		summary["response"] = kept
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil
	}
	return boundedCodexUpstreamBody(encoded)
}

// apply copies the recorded failure facts into an observation. The upstream
// body is the failing event, or the JSON body of a non-success response.
func (o codexRoutingCompletion) apply(observation *extensionv1.CodexRoutingObservation, status int) {
	if observation.UpstreamErrorType == "" && observation.UpstreamErrorCode == "" && observation.UpstreamErrorMessage == "" {
		observation.UpstreamErrorType, observation.UpstreamErrorCode = o.UpstreamError.Type, o.UpstreamError.Code
		observation.UpstreamErrorMessage, observation.UpstreamErrorParam = o.UpstreamError.Message, o.UpstreamError.Param
	}
	if observation.UpstreamBody == "" {
		if len(o.UpstreamEvent) > 0 {
			observation.UpstreamBody = string(o.UpstreamEvent)
		} else if status != http.StatusOK && status != http.StatusSwitchingProtocols && len(o.Body) > 0 {
			observation.UpstreamBody = string(o.Body)
		}
	}
	if observation.Error == "" {
		observation.Error = o.incompleteReason(status)
	}
}

// incompleteReason explains a local (non-upstream) failure classification.
func (o codexRoutingCompletion) incompleteReason(status int) string {
	switch {
	case o.FailureDetail != "":
		return o.FailureDetail
	case o.Failed || (status != http.StatusOK && status != http.StatusSwitchingProtocols) || (o.Completed && o.Model != ""):
		return ""
	case !o.Completed && o.Model == "":
		return "response ended without a completed terminal event or a model declaration"
	case !o.Completed:
		return "response ended without a completed terminal event"
	}
	return "terminal response did not declare a model"
}

func (o *codexRoutingCompletion) observeModel(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if len(model) > 256 {
		o.failWithDetail("routing_incomplete", fmt.Sprintf("response model declaration is %d bytes (limit 256): %s", len(model), model))
		return
	}
	if (o.Model != "" && o.Model != model) || (o.RequestedModel != "" && o.RequestedModel != model) {
		o.Mismatch = true
	}
	o.Model = model
}

func (o *codexRoutingCompletion) headers(headers http.Header) {
	for name, values := range headers {
		if strings.EqualFold(name, "openai-model") || strings.EqualFold(name, "x-openai-model") {
			for _, value := range values {
				o.observeModel(value)
			}
		}
	}
}

// Classification uses explicit upstream codes, never error prose, account plan,
// STATE length, or the official client's hard-coded safety-reroute warning.
func codexRoutingErrorCode(raw json.RawMessage) string {
	var value struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &value) == nil {
		for _, code := range []string{value.Code, value.Type} {
			switch code {
			case "server_is_overloaded", "rate_limit_exceeded", "slow_down", "insufficient_quota", "usage_limit_reached":
				return "routing_capacity"
			case "cyber_policy", "bio_policy", "misalignment_policy_violation", "content_policy_violation":
				return "routing_policy"
			}
		}
	}
	return "routing_upstream"
}

func (o *codexRoutingCompletion) json(raw []byte) {
	o.Body = boundedCodexUpstreamBody(raw)
	o.jsonEvent(raw, "")
	if !o.Completed && o.UpstreamError.empty() && o.UpstreamEvent == nil {
		// Non-streaming error bodies may carry {"detail":...} instead of an
		// error object; keep that upstream text too.
		if found := codexUpstreamErrorFromBody(raw); !found.empty() {
			o.recordUpstream(found, raw)
		}
	}
}

func (o *codexRoutingCompletion) jsonEvent(raw []byte, eventType string) {
	var event struct {
		Type     string                     `json:"type"`
		Object   string                     `json:"object"`
		Status   string                     `json:"status"`
		Model    string                     `json:"model"`
		Headers  map[string]json.RawMessage `json:"headers"`
		Error    json.RawMessage            `json:"error"`
		Response *struct {
			Status  string                     `json:"status"`
			Model   string                     `json:"model"`
			Headers map[string]json.RawMessage `json:"headers"`
			Error   json.RawMessage            `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	if event.Type == "" {
		event.Type = eventType
	}
	observeHeaders := func(headers map[string]json.RawMessage) {
		for name, value := range headers {
			if strings.EqualFold(name, "openai-model") || strings.EqualFold(name, "x-openai-model") {
				var model string
				if json.Unmarshal(value, &model) == nil {
					o.observeModel(model)
				}
			}
		}
	}
	observeHeaders(event.Headers)
	if event.Response != nil {
		observeHeaders(event.Response.Headers)
		o.observeModel(event.Response.Model)
	} else if event.Object == "response" {
		o.observeModel(event.Model)
	}
	badError := func(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }
	if badError(event.Error) {
		o.recordUpstream(codexUpstreamErrorObject(gjson.ParseBytes(event.Error)), raw)
		o.fail(codexRoutingErrorCode(event.Error))
		return
	}
	if event.Response != nil && badError(event.Response.Error) {
		o.recordUpstream(codexUpstreamErrorObject(gjson.ParseBytes(event.Response.Error)), raw)
		o.fail(codexRoutingErrorCode(event.Response.Error))
		return
	}
	switch event.Type {
	case "response.cancelled", "response.canceled":
		o.recordUpstream(codexUpstreamError{Type: event.Type}, raw)
		o.fail("routing_cancelled")
		return
	case "response.incomplete":
		o.recordUpstream(codexUpstreamError{Type: "incomplete", Code: codexJSONText(gjson.GetBytes(raw, "response.incomplete_details.reason"))}, raw)
		o.fail("routing_incomplete")
		return
	case "error", "response.failed":
		// A streamed error event carries code/message/param at its top level.
		root := gjson.ParseBytes(raw)
		o.recordUpstream(codexUpstreamError{Code: codexJSONText(root.Get("code")), Message: codexJSONText(root.Get("message")), Param: codexJSONText(root.Get("param"))}, raw)
		o.fail("routing_upstream")
		return
	}
	terminal := event.Type == "response.completed" && event.Response != nil
	if terminal {
		if event.Response.Status != "completed" {
			o.recordUpstream(codexUpstreamError{Type: "status", Code: event.Response.Status}, raw)
			o.failWithDetail("routing_incomplete", fmt.Sprintf("response.completed carried status %q", event.Response.Status))
			return
		}
	} else if event.Object == "response" && event.Status == "completed" {
		terminal = true
	} else {
		return
	}
	if terminal && o.Model != "" {
		o.Completed = true
	} else {
		o.failWithDetail("routing_incomplete", "terminal response did not declare a model")
	}
}

func (o *codexRoutingCompletion) feed(data []byte) {
	if o.Failed {
		return
	}
	limit := o.maxEventBytes
	if limit <= 0 {
		limit = codexRoutingProbeReadLimit
	}
	o.pending = append(o.pending, data...)
	for i := o.scanFrom; i < len(o.pending); i++ {
		if o.pending[i] != '\n' {
			continue
		}
		next := i + 1
		if next < len(o.pending) && o.pending[next] == '\r' {
			next++
		}
		if next >= len(o.pending) || o.pending[next] != '\n' {
			continue
		}
		if i > limit {
			o.failWithDetail("routing_incomplete", fmt.Sprintf("SSE event of %d bytes exceeds the %d-byte limit", i, limit))
			break
		}
		var lines [][]byte
		eventType := ""
		for _, line := range bytes.Split(o.pending[:i], []byte{'\n'}) {
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if bytes.HasPrefix(line, []byte("data:")) {
				lines = append(lines, bytes.TrimSpace(line[len("data:"):]))
			} else if bytes.HasPrefix(line, []byte("event:")) {
				eventType = string(bytes.TrimSpace(line[len("event:"):]))
			}
		}
		o.jsonEvent(bytes.Join(lines, []byte{'\n'}), eventType)
		o.pending = o.pending[next+1:]
		i = -1
		if o.Failed {
			break
		}
	}
	o.scanFrom = max(0, len(o.pending)-2)
	if len(o.pending) > limit {
		o.failWithDetail("routing_incomplete", fmt.Sprintf("unterminated SSE event of %d bytes exceeds the %d-byte limit", len(o.pending), limit))
	}
	if o.Failed {
		o.pending = nil
	}
}

func (o *codexRoutingCompletion) observationCode(requested string, status int) string {
	if o.FailureCode != "" {
		return o.FailureCode
	}
	if status != http.StatusOK && status != http.StatusSwitchingProtocols {
		return "routing_upstream"
	}
	if o.Mismatch || (o.Model != "" && o.Model != requested) {
		return "routing_model_mismatch"
	}
	if !o.Completed || o.Failed || o.Model == "" {
		return "routing_incomplete"
	}
	return "routing_verified"
}

func readCodexRoutingCompletion(body io.Reader, contentType, requested string, observation *extensionv1.CodexRoutingObservation, headers ...http.Header) error {
	data, err := io.ReadAll(io.LimitReader(body, codexRoutingProbeReadLimit+1))
	if err != nil || len(data) > codexRoutingProbeReadLimit {
		observation.Code = "routing_incomplete"
		if errors.Is(err, context.Canceled) {
			observation.Code = "routing_cancelled"
		}
		if err != nil {
			observation.Error = "read response body: " + err.Error()
		} else {
			observation.Error = fmt.Sprintf("response body exceeds the %d-byte probe limit", codexRoutingProbeReadLimit)
		}
		observation.UpstreamBody = string(boundedCodexUpstreamBody(data))
		return errCodexRoutingUnavailable
	}
	completed := codexRoutingCompletion{RequestedModel: requested}
	for _, header := range headers {
		completed.headers(header)
	}
	// The ChatGPT edge may omit Content-Type on otherwise valid SSE.
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || (strings.TrimSpace(contentType) == "" && bodyHasSSEFraming(data)) {
		completed.feed(data)
	} else {
		completed.json(data)
	}
	observation.Completed = completed.Completed && !completed.Failed
	observation.ResponseModel = completed.Model
	observation.ModelMatched = observation.Completed && !completed.Mismatch && completed.Model == requested
	observation.Code = completed.observationCode(requested, http.StatusOK)
	if observation.Code != "routing_verified" {
		completed.apply(observation, http.StatusOK)
		if observation.Code == "routing_model_mismatch" && observation.Error == "" {
			observation.Error = fmt.Sprintf("requested model %q, upstream declared %q", requested, completed.Model)
		}
		return errCodexRoutingUnavailable
	}
	return nil
}
