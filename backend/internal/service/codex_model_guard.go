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
	"sync"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// codexBodyReadLimit bounds an inspected request body and, unless a reader
// sets its own limit, one SSE event.
const codexBodyReadLimit = 128 << 10

// codexUpstreamBodyLimit bounds the upstream error body or failing event kept
// with a failure.
const codexUpstreamBodyLimit = 8 << 10

// ErrCodexModelMismatch fails a response that declares another model than the
// request named.
var ErrCodexModelMismatch = errors.New("upstream Codex response model does not match the requested model")

type codexExpectedModelKey struct{}

// withCodexExpectedModel names the model the response to request must declare
// (see guardCodexResponseModel).
func withCodexExpectedModel(request *http.Request, model string) *http.Request {
	if request == nil {
		return nil
	}
	return request.WithContext(context.WithValue(request.Context(), codexExpectedModelKey{}, strings.TrimSpace(model)))
}

// codexRequestBodyModel is the model a Responses request body names.
func codexRequestBodyModel(body []byte) string {
	return strings.TrimSpace(gjson.GetBytes(body, "model").String())
}

type codexDownstreamContextKey struct{}

// Keep cancellation evidence without reconnecting it to the upstream context:
// existing callers intentionally detach that context to drain usage on cancel.
func withCodexDownstreamContext(request *http.Request, c *gin.Context) *http.Request {
	if request == nil || c == nil || c.Request == nil {
		return request
	}
	return request.WithContext(context.WithValue(request.Context(), codexDownstreamContextKey{}, c.Request.Context()))
}

// guardCodexResponseModel holds an OAuth Codex /responses reply to the model
// its request named (withCodexExpectedModel): a 200 response that declares
// another model fails with ErrCodexModelMismatch. API-key aliases, /compact and
// requests without an expected model are untouched.
func (s *OpenAIGatewayService) guardCodexResponseModel(request *http.Request, account *Account, response *http.Response) {
	if account == nil || !account.IsOpenAIOAuthLike() || request == nil || request.URL == nil ||
		request.URL.Hostname() != "chatgpt.com" || request.URL.Path != "/backend-api/codex/responses" || response == nil || response.Body == nil {
		return
	}
	model, _ := request.Context().Value(codexExpectedModelKey{}).(string)
	if model == "" {
		return
	}
	response.Body = s.newCodexModelGuardBody(request, response, model)
}

func (s *OpenAIGatewayService) newCodexModelGuardBody(request *http.Request, response *http.Response, model string) *codexModelGuardBody {
	completionContext := request.Context()
	if downstream, ok := request.Context().Value(codexDownstreamContextKey{}).(context.Context); ok {
		completionContext = downstream
	}
	body := &codexModelGuardBody{
		ReadCloser:     response.Body,
		ctx:            completionContext,
		sse:            strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream"),
		detectSSE:      strings.TrimSpace(response.Header.Get("Content-Type")) == "",
		maxJSONBytes:   resolveUpstreamResponseReadLimit(s.cfg),
		rejectMismatch: response.StatusCode == http.StatusOK,
		completion:     codexResponseCompletion{RequestedModel: model, maxEventBytes: s.codexModelGuardEventLimit()},
	}
	body.completion.headers(response.Header)
	return body
}

func (s *OpenAIGatewayService) codexModelGuardEventLimit() int {
	maxEventBytes := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxEventBytes = s.cfg.Gateway.MaxLineSize
	}
	return maxEventBytes
}

type codexModelGuardBody struct {
	io.ReadCloser
	sse            bool
	detectSSE      bool
	ctx            context.Context
	maxJSONBytes   int64
	rejectMismatch bool
	completion     codexResponseCompletion
	json           []byte
	once           sync.Once
	mu             sync.Mutex
	done           bool
	finish         func(codexResponseCompletion)
}

func (body *codexModelGuardBody) Read(p []byte) (int, error) {
	body.mu.Lock()
	rejected := body.rejectMismatch && body.completion.Mismatch
	body.mu.Unlock()
	if rejected {
		body.complete()
		return 0, ErrCodexModelMismatch
	}
	n, err := body.ReadCloser.Read(p)
	body.mu.Lock()
	if n > 0 && !body.done {
		if body.sse {
			body.completion.feed(p[:n])
		} else {
			limit := body.maxJSONBytes
			if limit <= 0 {
				limit = defaultUpstreamResponseReadMaxBytes
			}
			if int64(len(body.json))+int64(n) <= limit {
				body.json = append(body.json, p[:n]...)
				if body.detectSSE && bodyHasSSEFraming(body.json) {
					body.sse, body.detectSSE = true, false
					body.completion.feed(body.json)
					body.json = nil
				}
			} else {
				body.completion.fail("incomplete")
				body.json = nil
			}
		}
	}
	if errors.Is(err, context.Canceled) {
		body.completion.fail("cancelled")
	}
	rejected = body.rejectMismatch && body.completion.Mismatch
	body.mu.Unlock()
	if err != nil {
		body.complete()
		body.mu.Lock()
		rejected = body.rejectMismatch && body.completion.Mismatch
		body.mu.Unlock()
	}
	if rejected {
		body.complete()
		return 0, ErrCodexModelMismatch
	}
	return n, err
}

func (body *codexModelGuardBody) complete() {
	body.once.Do(func() {
		body.mu.Lock()
		if !body.sse && !body.completion.Failed {
			body.completion.json(body.json)
		}
		if body.ctx != nil && errors.Is(body.ctx.Err(), context.Canceled) {
			body.completion.fail("cancelled")
		}
		body.done = true
		body.json, body.completion.pending = nil, nil
		completed := body.completion
		body.mu.Unlock()
		if body.finish != nil {
			body.finish(completed)
		}
	})
}

func (body *codexModelGuardBody) Close() error {
	err := body.ReadCloser.Close()
	body.complete()
	return err
}

// Readers select their existing stream/JSON read limits. The upstream error
// object, the failing event (bounded) and the local reason of a failure are
// kept with the completion.
type codexResponseCompletion struct {
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

// boundedCodexUpstreamBody keeps at most codexUpstreamBodyLimit bytes
// without splitting a UTF-8 sequence.
func boundedCodexUpstreamBody(raw []byte) []byte {
	limit := codexUpstreamBodyLimit
	if len(raw) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(raw[cut]) {
			cut--
		}
		raw = raw[:cut]
	}
	return append([]byte(nil), raw...)
}

func (o *codexResponseCompletion) fail(code string) {
	o.Failed = true
	if o.FailureCode == "" {
		o.FailureCode = code
	}
}

func (o *codexResponseCompletion) failWithDetail(code, detail string) {
	if o.FailureDetail == "" && !o.Failed {
		o.FailureDetail = detail
	}
	o.fail(code)
}

// recordUpstream keeps the first upstream failure: its error object and a
// summary of the failing event (see codexUpstreamEventSummary).
func (o *codexResponseCompletion) recordUpstream(found codexUpstreamError, event []byte) {
	if o.UpstreamEvent != nil || !o.UpstreamError.empty() {
		return
	}
	o.UpstreamError = found
	o.UpstreamEvent = codexUpstreamEventSummary(event)
}

// codexUpstreamEventSummary keeps what identifies an upstream failure: the
// event type, its error fields (top level, error, detail) and the response id,
// status, model, incomplete_details and error. The response instructions,
// tools, input and output of a business request are never copied.
func codexUpstreamEventSummary(raw []byte) []byte {
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

func (o *codexResponseCompletion) observeModel(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if len(model) > 256 {
		o.failWithDetail("incomplete", fmt.Sprintf("response model declaration is %d bytes (limit 256): %s", len(model), model))
		return
	}
	if (o.Model != "" && o.Model != model) || (o.RequestedModel != "" && o.RequestedModel != model) {
		o.Mismatch = true
	}
	o.Model = model
}

func (o *codexResponseCompletion) headers(headers http.Header) {
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
func codexUpstreamFailureCode(raw json.RawMessage) string {
	var value struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &value) == nil {
		for _, code := range []string{value.Code, value.Type} {
			switch code {
			case "server_is_overloaded", "rate_limit_exceeded", "slow_down", "insufficient_quota", "usage_limit_reached":
				return "upstream_capacity"
			case "cyber_policy", "bio_policy", "misalignment_policy_violation", "content_policy_violation":
				return "upstream_policy"
			}
		}
	}
	return "upstream_error"
}

func (o *codexResponseCompletion) json(raw []byte) {
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

func (o *codexResponseCompletion) jsonEvent(raw []byte, eventType string) {
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
		o.fail(codexUpstreamFailureCode(event.Error))
		return
	}
	if event.Response != nil && badError(event.Response.Error) {
		o.recordUpstream(codexUpstreamErrorObject(gjson.ParseBytes(event.Response.Error)), raw)
		o.fail(codexUpstreamFailureCode(event.Response.Error))
		return
	}
	switch event.Type {
	case "response.cancelled", "response.canceled":
		o.recordUpstream(codexUpstreamError{Type: event.Type}, raw)
		o.fail("cancelled")
		return
	case "response.incomplete":
		o.recordUpstream(codexUpstreamError{Type: "incomplete", Code: codexJSONText(gjson.GetBytes(raw, "response.incomplete_details.reason"))}, raw)
		o.fail("incomplete")
		return
	case "error", "response.failed":
		// A streamed error event carries code/message/param at its top level.
		root := gjson.ParseBytes(raw)
		o.recordUpstream(codexUpstreamError{Code: codexJSONText(root.Get("code")), Message: codexJSONText(root.Get("message")), Param: codexJSONText(root.Get("param"))}, raw)
		o.fail("upstream_error")
		return
	}
	terminal := event.Type == "response.completed" && event.Response != nil
	if terminal {
		if event.Response.Status != "completed" {
			o.recordUpstream(codexUpstreamError{Type: "status", Code: event.Response.Status}, raw)
			o.failWithDetail("incomplete", fmt.Sprintf("response.completed carried status %q", event.Response.Status))
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
		o.failWithDetail("incomplete", "terminal response did not declare a model")
	}
}

func (o *codexResponseCompletion) feed(data []byte) {
	if o.Failed {
		return
	}
	limit := o.maxEventBytes
	if limit <= 0 {
		limit = codexBodyReadLimit
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
			o.failWithDetail("incomplete", fmt.Sprintf("SSE event of %d bytes exceeds the %d-byte limit", i, limit))
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
		o.failWithDetail("incomplete", fmt.Sprintf("unterminated SSE event of %d bytes exceeds the %d-byte limit", len(o.pending), limit))
	}
	if o.Failed {
		o.pending = nil
	}
}
