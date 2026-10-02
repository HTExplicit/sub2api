package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"github.com/tidwall/gjson"
)

func codexQualityWireFields(req *http.Request) (string, string, error) {
	if req == nil {
		return "", "", codexQualityUnavailable("no outbound request")
	}
	// Reasoning recovery deliberately clears GetBody to prevent transparent POST
	// replay. Inspect the final encoded body without restoring that capability.
	body := req.Body
	independent := req.GetBody != nil
	if independent {
		var err error
		body, err = req.GetBody()
		if err != nil {
			if body != nil {
				_ = body.Close()
			}
			return "", "", codexQualityUnavailable("copy the outbound body: %v", err)
		}
	}
	if body == nil {
		return "", "", codexQualityUnavailable("the outbound request has no body")
	}
	wire, readErr := io.ReadAll(io.LimitReader(body, (2<<20)+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || len(wire) > 2<<20 {
		if !independent {
			// The original close chain has run. Neither a consumed prefix nor an
			// unread suffix may become a later request after a failed snapshot.
			req.Body = io.NopCloser(codexQualityRejectedWireBody{})
		}
		switch {
		case readErr != nil:
			return "", "", codexQualityUnavailable("read the outbound body: %v", readErr)
		case closeErr != nil:
			return "", "", codexQualityUnavailable("close the outbound body: %v", closeErr)
		}
		return "", "", codexQualityUnavailable("the outbound body exceeds 2 MiB")
	}
	if !independent {
		req.Body = io.NopCloser(bytes.NewReader(wire))
	}
	var reader io.Reader = bytes.NewReader(wire)
	if strings.EqualFold(req.Header.Get("Content-Encoding"), "zstd") {
		decoder, err := zstd.NewReader(reader, zstd.WithDecoderMaxMemory(8<<20), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return "", "", codexQualityUnavailable("zstd decoder: %v", err)
		}
		defer decoder.Close()
		reader = decoder
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	switch {
	case err != nil:
		return "", "", codexQualityUnavailable("decode the outbound body: %v", err)
	case len(raw) > 1<<20:
		return "", "", codexQualityUnavailable("the decoded outbound body exceeds 1 MiB")
	case !gjson.ValidBytes(raw):
		return "", "", codexQualityUnavailable("the outbound body is not valid JSON")
	}
	return gjson.GetBytes(raw, "model").String(), gjson.GetBytes(raw, "reasoning.effort").String(), nil
}

type codexQualityRejectedWireBody struct{}

func (codexQualityRejectedWireBody) Read([]byte) (int, error) {
	return 0, ErrCodexQualityUnavailable
}

// reserveCodexQualitySend runs on the account's ordinary send path after the
// final wire request exists and immediately before upstream IO. It rechecks
// the run and the account, requires the final body to carry the bound model
// and reasoning effort, and then spends one send of the persistent budget.
func reserveCodexQualitySend(req *http.Request, account *Account) error {
	e := codexQualityExecutionFromContext(req.Context())
	if e == nil {
		return nil
	}
	if account == nil || account.ID != e.accountID {
		return codexQualityUnavailable("the outbound account is not account %d of the quality run", e.accountID)
	}
	run, err := e.current(req.Context())
	if err != nil {
		return err
	}
	if _, err = e.runtime.account(req.Context(), run); err != nil {
		return err
	}
	model, effort, err := codexQualityWireFields(req)
	if err != nil {
		return err
	}
	boundModel, boundEffort := run.binding()
	if model != boundModel || effort != boundEffort {
		return codexQualityUnavailable("the outbound body asks for model %q effort %q; quality run %s sends %s with %s", qualityRecordedModel(model), qualityRecordedModel(effort), run.RunID, boundModel, boundEffort)
	}
	e.requestModel, e.effort = model, effort
	return reserveCodexQualityAttempt(e.runtime.ctx(req.Context()), e.runtime.store, e.runID, e.grantDigest, e.attempt())
}

func (e *codexQualityExecution) attempt() CodexQualityAttempt {
	return CodexQualityAttempt{Stage: codexQualityStage, TrialID: e.trialID, AccountID: e.accountID, RequestModel: e.requestModel, ReasoningEffort: e.effort, ResponseModels: []string{}, HeaderModels: []string{}}
}

// Read the original response into the diagnostic observer before the model
// guard can reject a frame. The guard's outcome never replaces raw evidence.
func (s *OpenAIGatewayService) observeCodexQualityBusinessResponse(request *http.Request, response *http.Response, err error) {
	observeCodexQualityResponse(request.Context(), response, err)
	e := codexQualityExecutionFromContext(request.Context())
	if e == nil || err != nil || response == nil || response.Body == nil {
		return
	}
	response.Body = s.newCodexModelGuardBody(request, response, e.requestModel)
}

func observeCodexQualityResponse(ctx context.Context, response *http.Response, err error) {
	e := codexQualityExecutionFromContext(ctx)
	if e == nil {
		return
	}
	a := e.attempt()
	if err != nil || response == nil {
		a.State, a.ErrorCode = "unknown", "transport"
		if err != nil {
			a.ErrorMessage = err.Error()
		} else {
			a.ErrorMessage = "the upstream returned no response"
		}
		finishCodexQualityAttempt(e.runtime.ctx(ctx), e.runtime.store, e.runID, a)
		return
	}
	a.HTTPStatus = response.StatusCode
	a.RequestID, a.CFRay = response.Header.Get("x-request-id"), response.Header.Get("cf-ray")
	for name, values := range response.Header {
		if strings.EqualFold(name, "openai-model") || strings.EqualFold(name, "x-openai-model") {
			for _, value := range values {
				recordCodexQualityHeaderModel(&a, value)
			}
		}
	}
	observer := e.runtime.s.newCodexQualityObservedBody(response, a)
	observer.finish = func(a CodexQualityAttempt) {
		finishCodexQualityAttempt(e.runtime.ctx(ctx), e.runtime.store, e.runID, a)
	}
	response.Body = observer
}

// codexQualityDistinctModels bounds how many distinct declared models one
// attempt keeps; codexQualityModelNameLimit bounds one declared name.
const (
	codexQualityDistinctModels = 32
	codexQualityModelNameLimit = 256
)

// qualityRecordedModel keeps the upstream model declaration verbatim (only
// surrounding whitespace is trimmed); detecting a replaced model needs the name.
// A name over the limit is cut on a rune boundary and marked with its length.
func qualityRecordedModel(model string) string {
	model = strings.TrimSpace(model)
	if len(model) <= codexQualityModelNameLimit {
		return model
	}
	cut := codexQualityModelNameLimit
	for cut > 0 && !utf8.RuneStart(model[cut]) {
		cut--
	}
	return fmt.Sprintf("%s…(truncated, %d bytes)", model[:cut], len(model))
}

func recordCodexQualityHeaderModel(attempt *CodexQualityAttempt, value string) {
	model := qualityRecordedModel(value)
	if model == "" {
		return
	}
	if !slices.Contains(attempt.HeaderModels, model) && len(attempt.HeaderModels) < codexQualityDistinctModels {
		attempt.HeaderModels = append(attempt.HeaderModels, model)
	}
	if len(attempt.HeaderModels) == 1 {
		attempt.HeaderModel = &model
	} else {
		conflict := "conflicting_models"
		attempt.HeaderModel = &conflict
	}
}

// A bounded single-frame observer; it never buffers the whole response and
// always forwards the original bytes without editing model fields or content.
// One SSE event is limited by Gateway.MaxLineSize and a JSON body by
// Gateway.UpstreamResponseReadMaxBytes, the limits of ordinary forwarding.
func (s *OpenAIGatewayService) newCodexQualityObservedBody(response *http.Response, attempt CodexQualityAttempt) *codexQualityObservedBody {
	return &codexQualityObservedBody{
		ReadCloser: response.Body,
		sse:        strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream"),
		sniff:      strings.TrimSpace(response.Header.Get("Content-Type")) == "",
		attempt:    attempt, maxEventBytes: s.codexModelGuardEventLimit(), maxJSONBytes: resolveUpstreamResponseReadLimit(s.cfg),
	}
}

type codexQualityObservedBody struct {
	io.ReadCloser
	detail                        string
	sse, sniff, failed, oversized bool
	pending                       []byte
	scanFrom, maxEventBytes       int
	maxJSONBytes                  int64
	attempt                       CodexQualityAttempt
	once                          sync.Once
	mu                            sync.Mutex
	done                          bool
	finish                        func(CodexQualityAttempt)
}

func (b *codexQualityObservedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if n > 0 && !b.done {
		b.feed(p[:n])
	}
	b.mu.Unlock()
	if err != nil {
		b.complete(err)
	}
	return n, err
}

func (b *codexQualityObservedBody) feed(raw []byte) {
	if b.oversized {
		return
	}
	b.pending = append(b.pending, raw...)
	if b.sniff && bodyHasSSEFraming(b.pending) {
		b.sse, b.sniff = true, false
	}
	limit := int64(b.maxEventBytes)
	if !b.sse {
		limit = b.maxJSONBytes
	}
	if limit <= 0 {
		limit = codexBodyReadLimit
	}
	if b.sse {
		for i := b.scanFrom; i < len(b.pending); i++ {
			if b.pending[i] != '\n' {
				continue
			}
			next := i + 1
			if next < len(b.pending) && b.pending[next] == '\r' {
				next++
			}
			if next >= len(b.pending) || b.pending[next] != '\n' {
				continue
			}
			if int64(i) > limit {
				b.oversized, b.failed, b.pending = true, true, nil
				b.detail = fmt.Sprintf("an SSE event of %d bytes exceeded the %d-byte limit", i, limit)
				return
			}
			var data [][]byte
			for _, line := range bytes.Split(b.pending[:i], []byte{'\n'}) {
				if bytes.HasPrefix(line, []byte("data:")) {
					data = append(data, bytes.TrimSpace(line[len("data:"):]))
				}
			}
			b.event(bytes.Join(data, []byte{'\n'}))
			b.pending = b.pending[next+1:]
			i = -1
		}
		b.scanFrom = max(0, len(b.pending)-2)
	}
	if int64(len(b.pending)) > limit {
		b.detail = fmt.Sprintf("an unterminated event of %d bytes exceeded the %d-byte limit", len(b.pending), limit)
		b.oversized, b.failed, b.pending = true, true, nil
	}
}

// recordUpstream keeps the first upstream failure: its error object and the
// failing event or body (bounded).
func (b *codexQualityObservedBody) recordUpstream(found codexUpstreamError, raw []byte) {
	a := &b.attempt
	if a.UpstreamBody != "" || a.UpstreamErrorType != "" || a.UpstreamErrorCode != "" || a.UpstreamErrorMessage != "" {
		return
	}
	a.UpstreamErrorType, a.UpstreamErrorCode, a.UpstreamErrorMessage, a.UpstreamErrorParam = found.Type, found.Code, found.Message, found.Param
	a.UpstreamBody = string(boundedCodexUpstreamBody(raw))
}

func (b *codexQualityObservedBody) event(raw []byte) {
	if !gjson.ValidBytes(raw) {
		return
	}
	kind := gjson.GetBytes(raw, "type").String()
	root := gjson.ParseBytes(raw)
	for _, path := range []string{"response.headers", "response.metadata.headers", "headers", "metadata.headers"} {
		root.Get(path).ForEach(func(name, value gjson.Result) bool {
			if !strings.EqualFold(name.String(), "openai-model") && !strings.EqualFold(name.String(), "x-openai-model") {
				return true
			}
			if value.Type == gjson.String {
				recordCodexQualityHeaderModel(&b.attempt, value.String())
			} else if value.IsArray() {
				for _, item := range value.Array() {
					if item.Type == gjson.String {
						recordCodexQualityHeaderModel(&b.attempt, item.String())
					}
				}
			}
			return true
		})
	}
	r := root.Get("response")
	if !r.Exists() {
		r = root
	}
	model := qualityRecordedModel(r.Get("model").String())
	if model != "" && !slices.Contains(b.attempt.ResponseModels, model) && len(b.attempt.ResponseModels) < codexQualityDistinctModels {
		b.attempt.ResponseModels = append(b.attempt.ResponseModels, model)
	}
	if kind == "response.created" && model != "" {
		b.attempt.CreatedModel = &model
	}
	if kind == "error" || kind == "response.failed" || kind == "response.incomplete" || (root.Get("error").Exists() && root.Get("error").Type != gjson.Null) {
		b.failed = true
		b.attempt.ErrorCode = "upstream_error"
		found := codexUpstreamErrorObject(root.Get("error"))
		if found.empty() {
			found = codexUpstreamErrorObject(r.Get("error"))
		}
		if found.empty() && kind == "error" {
			found = codexUpstreamError{Code: codexJSONText(root.Get("code")), Message: codexJSONText(root.Get("message")), Param: codexJSONText(root.Get("param"))}
		}
		if found.empty() && kind == "response.incomplete" {
			found = codexUpstreamError{Type: "incomplete", Code: codexJSONText(r.Get("incomplete_details.reason"))}
		}
		b.recordUpstream(found, raw)
	}
	if kind == "response.completed" || (root.Get("object").String() == "response" && r.Get("status").String() == "completed") {
		if model != "" {
			b.attempt.TerminalModel = &model
		}
		b.attempt.Completed = r.Get("status").String() == "completed" && (!r.Get("error").Exists() || r.Get("error").Type == gjson.Null)
		optional := func(path string) *int64 {
			value := r.Get(path)
			if !value.Exists() || value.Type != gjson.Number || value.Int() < 0 {
				return nil
			}
			n := value.Int()
			return &n
		}
		b.attempt.ReasoningTokens = optional("usage.output_tokens_details.reasoning_tokens")
		b.attempt.InputTokens, b.attempt.OutputTokens = optional("usage.input_tokens"), optional("usage.output_tokens")
	}
}

func (b *codexQualityObservedBody) complete(err error) {
	b.once.Do(func() {
		b.mu.Lock()
		if !b.sse && !b.oversized {
			b.event(b.pending)
			if b.attempt.HTTPStatus >= 400 && len(b.pending) > 0 {
				// Non-streaming error bodies may carry {"detail":...} or plain text.
				b.recordUpstream(codexUpstreamErrorFromBody(b.pending), b.pending)
			}
		}
		b.attempt.Completed = b.attempt.Completed && !b.failed && b.attempt.HTTPStatus == http.StatusOK
		b.attempt.State = "unknown"
		if b.attempt.Completed {
			b.attempt.State = "complete"
		} else if b.failed || b.attempt.HTTPStatus >= 400 {
			b.attempt.State, b.attempt.ErrorCode = "error", "upstream_error"
			if b.attempt.ErrorMessage == "" {
				b.attempt.ErrorMessage = b.detail
			}
		} else if err != nil && err != io.EOF {
			b.attempt.ErrorCode = "stream_error"
			b.attempt.ErrorMessage = err.Error()
		} else {
			b.attempt.ErrorCode = "incomplete"
			b.attempt.ErrorMessage = "the response ended without a completed terminal event"
		}
		b.done, b.pending = true, nil
		attempt := b.attempt
		b.mu.Unlock()
		if b.finish != nil {
			b.finish(attempt)
		}
	})
}

func (b *codexQualityObservedBody) Close() error {
	err := b.ReadCloser.Close()
	b.complete(nil)
	return err
}
