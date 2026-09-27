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
	"time"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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

func reserveCodexQualityAcquisition(req *http.Request, accountID int64) error {
	e := codexQualityExecutionFromContext(req.Context())
	if e == nil {
		return nil
	}
	if e.accountID != accountID || e.stage != "acquire" {
		return codexQualityUnavailable("acquisition for account %d does not match the %s stage of account %d", accountID, e.stage, e.accountID)
	}
	run, err := e.current(req.Context())
	if err != nil {
		return err
	}
	account, err := e.runtime.account(req.Context(), run)
	if err != nil {
		return err
	}
	return reserveCodexQualitySend(req, account, nil)
}

// Called after local request preparation, immediately before model IO. Every
// generation path of a quality run (acquire, verify and business) spends here.
func reserveCodexQualitySend(req *http.Request, account *Account, q *extensionv1.CodexRoutingQualification) error {
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
	if model != codexQualityModel || effort != codexQualityEffort {
		return codexQualityUnavailable("the outbound body asks for model %q effort %q; a quality diagnosis sends %s with %s", model, effort, codexQualityModel, codexQualityEffort)
	}
	if e.stage == "business" {
		current, err := e.runtime.qualification(req.Context(), run)
		switch {
		case err != nil:
			return err
		case q == nil:
			return codexQualityUnavailable("the business request carries no route qualification")
		case current.Bundle.Key != q.Bundle.Key || current.Bundle.Revision != q.Bundle.Revision:
			return codexQualityUnavailable("the route changed to bundle %s revision %d while the request used %s revision %d", current.Bundle.Key, current.Bundle.Revision, q.Bundle.Key, q.Bundle.Revision)
		}
	}
	e.requestModel, e.effort, e.qualification = model, effort, q
	return reserveCodexQualityAttempt(e.runtime.ctx(req.Context()), e.runtime.store, e.runID, e.grantDigest, e.attempt())
}

func (e *codexQualityExecution) attempt() CodexQualityAttempt {
	a := CodexQualityAttempt{Stage: e.stage, TrialID: e.trialID, OperationID: e.operationID, AccountID: e.accountID, RequestModel: e.requestModel, ReasoningEffort: e.effort, ResponseModels: []string{}, HeaderModels: []string{}}
	if q := e.qualification; q != nil {
		if q.Scope.ConnectionLeaseID != "" {
			a.ConnectionFingerprint = codexQualityHash(q.Scope.ConnectionLeaseID)[:16]
		}
		if q.Bundle.Key != "" {
			a.QualificationFingerprint = codexQualityHash(q.Bundle.Key)[:16]
		}
	}
	return a
}

// Read the original response into the diagnostic observer before a protocol
// guard can reject a frame. The guard's outcome never replaces raw evidence.
func (s *OpenAIGatewayService) observeCodexQualityBusinessResponse(request *http.Request, response *http.Response, err error, q *extensionv1.CodexRoutingQualification) {
	observeCodexQualityResponse(request.Context(), response, err, q)
	if err != nil || response == nil || response.Body == nil {
		return
	}
	guard := s.newCodexRoutingObservedBody(request, response, codexQualityModel)
	guard.finish = func(completion codexRoutingCompletion) {
		if completion.observationCode(codexQualityModel, response.StatusCode) == "routing_verified" {
			return
		}
		e := codexQualityExecutionFromContext(request.Context())
		if e == nil || q == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 3*time.Second)
		defer cancel()
		_, _ = mutateCodexQualityRun(e.runtime.ctx(ctx), e.runtime.store, e.runID, func(run *codexQualityRun) error {
			// Raw observation can have refreshed the same connection's cookie
			// revision before Close finishes. A failed guard revokes that lease,
			// but never a separately renewed connection or ordinary eligibility.
			if current := run.Qualification; current != nil && current.Bundle.Key == q.Bundle.Key && current.Scope.ConnectionLeaseID == q.Scope.ConnectionLeaseID && current.Scope.SameOwner(q.Scope) {
				run.Qualification = nil
			}
			return nil
		})
	}
	response.Body = guard
}

func codexQualityModelsMatch(attempt CodexQualityAttempt) bool {
	if !attempt.Completed || attempt.TerminalModel == nil || *attempt.TerminalModel != codexQualityModel {
		return false
	}
	for _, models := range [][]string{attempt.ResponseModels, attempt.HeaderModels} {
		for _, model := range models {
			if model != codexQualityModel {
				return false
			}
		}
	}
	return true
}

func observeCodexQualityResponse(ctx context.Context, response *http.Response, err error, q *extensionv1.CodexRoutingQualification) {
	e := codexQualityExecutionFromContext(ctx)
	if e == nil {
		return
	}
	if q != nil {
		e.qualification = q
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
	observedAt := time.Now().UTC()
	deleted := false
	if e.stage == "business" && q != nil {
		deleted = e.runtime.s.recordCodexRoutingDeletions(ctx, e.runtime.installation, q, response.Header, observedAt)
	}
	for name, values := range response.Header {
		if strings.EqualFold(name, "openai-model") || strings.EqualFold(name, "x-openai-model") {
			for _, value := range values {
				recordCodexQualityHeaderModel(&a, value)
			}
		}
	}
	observer := e.runtime.s.newCodexQualityObservedBody(response, e.stage, a)
	observer.finish = func(a CodexQualityAttempt) {
		finishCodexQualityAttempt(e.runtime.ctx(ctx), e.runtime.store, e.runID, a)
		if e.stage == "business" {
			if _, err := e.current(ctx); err != nil {
				return
			}
			matched := codexQualityModelsMatch(a) && !deleted
			var replacement *extensionv1.CodexRoutingQualification
			if matched {
				replacement = e.runtime.s.refreshObservedCodexCookies(ctx, e.runtime.installation, q, response.Header, extensionv1.CodexRoutingObservation{Stage: "business", Code: "routing_verified", RequestedModel: codexQualityModel, ResponseModel: codexQualityModel, Completed: true, ModelMatched: true, ObservedAt: observedAt})
			}
			finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_, _ = mutateCodexQualityRun(e.runtime.ctx(finish), e.runtime.store, e.runID, func(run *codexQualityRun) error {
				if run.Qualification != nil && e.qualification != nil && run.Qualification.Bundle == e.qualification.Bundle {
					if !matched {
						run.Qualification = nil
					} else if replacement != nil {
						run.Qualification = replacement
					}
				}
				return nil
			})
		}
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
func (s *OpenAIGatewayService) newCodexQualityObservedBody(response *http.Response, stage string, attempt CodexQualityAttempt) *codexQualityObservedBody {
	limit, jsonLimit, totalLimit := codexRoutingProbeReadLimit, int64(codexRoutingProbeReadLimit), int64(codexRoutingProbeReadLimit)
	if stage == "business" {
		limit, jsonLimit, totalLimit = s.codexRoutingBusinessEventLimit(), resolveUpstreamResponseReadLimit(s.cfg), 0
	}
	return &codexQualityObservedBody{
		ReadCloser: response.Body,
		sse:        strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream"),
		sniff:      strings.TrimSpace(response.Header.Get("Content-Type")) == "",
		attempt:    attempt, maxEventBytes: limit, maxJSONBytes: jsonLimit, maxReadBytes: totalLimit,
	}
}

type codexQualityObservedBody struct {
	io.ReadCloser
	detail                                string
	sse, sniff, failed, oversized         bool
	pending                               []byte
	scanFrom, maxEventBytes               int
	maxJSONBytes, maxReadBytes, readBytes int64
	attempt                               CodexQualityAttempt
	once                                  sync.Once
	mu                                    sync.Mutex
	done                                  bool
	finish                                func(CodexQualityAttempt)
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
	if b.maxReadBytes > 0 {
		b.readBytes += int64(len(raw))
		if b.readBytes > b.maxReadBytes {
			b.oversized, b.failed, b.pending = true, true, nil
			b.detail = fmt.Sprintf("the response exceeded the %d-byte observation limit", b.maxReadBytes)
			return
		}
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
		limit = codexRoutingProbeReadLimit
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
