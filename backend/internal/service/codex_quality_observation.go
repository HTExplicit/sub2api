package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/klauspost/compress/zstd"
	"github.com/tidwall/gjson"
)

func codexQualityWireFields(req *http.Request) (string, string, error) {
	if req == nil || req.GetBody == nil {
		return "", "", ErrCodexQualityUnavailable
	}
	body, err := req.GetBody()
	if err != nil {
		return "", "", ErrCodexQualityUnavailable
	}
	defer body.Close()
	var reader io.Reader = body
	if strings.EqualFold(req.Header.Get("Content-Encoding"), "zstd") {
		decoder, err := zstd.NewReader(body, zstd.WithDecoderMaxMemory(8<<20))
		if err != nil {
			return "", "", ErrCodexQualityUnavailable
		}
		defer decoder.Close()
		reader = decoder
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || !gjson.ValidBytes(raw) {
		return "", "", ErrCodexQualityUnavailable
	}
	return gjson.GetBytes(raw, "model").String(), gjson.GetBytes(raw, "reasoning.effort").String(), nil
}

func reserveCodexQualityAcquisition(req *http.Request, accountID int64) error {
	e := codexQualityExecutionFromContext(req.Context())
	if e == nil {
		return nil
	}
	if e.accountID != accountID || e.stage != "acquire" {
		return ErrCodexQualityUnavailable
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
		return ErrCodexQualityUnavailable
	}
	run, err := e.current(req.Context())
	if err != nil {
		return err
	}
	if _, err = e.runtime.account(req.Context(), run); err != nil {
		return err
	}
	model, effort, err := codexQualityWireFields(req)
	if err != nil || model != codexQualityModel || (e.stage == "business" && effort != codexQualityEffort) {
		return ErrCodexQualityUnavailable
	}
	if e.stage == "business" {
		current, err := e.runtime.qualification(req.Context(), run)
		if err != nil || q == nil || current.Bundle.Key != q.Bundle.Key || current.Bundle.Revision != q.Bundle.Revision {
			return ErrCodexQualityUnavailable
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
		finishCodexQualityAttempt(e.runtime.ctx(ctx), e.runtime.store, e.runID, a)
		return
	}
	a.HTTPStatus = response.StatusCode
	observedAt := time.Now().UTC()
	deleted := false
	if e.stage == "business" && q != nil {
		deleted = e.runtime.s.recordCodexRoutingDeletions(ctx, e.runtime.installation, q, response.Header, observedAt)
	}
	for _, name := range []string{"openai-model", "x-openai-model"} {
		for _, value := range response.Header.Values(name) {
			recordCodexQualityHeaderModel(&a, value)
		}
	}
	observer := &codexQualityObservedBody{ReadCloser: response.Body, sse: strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream"), sniff: response.Header.Get("Content-Type") == "", attempt: a}
	observer.finish = func(a CodexQualityAttempt) {
		finishCodexQualityAttempt(e.runtime.ctx(ctx), e.runtime.store, e.runID, a)
		if e.stage == "business" {
			if _, err := e.current(ctx); err != nil {
				return
			}
			matched := a.Completed && a.TerminalModel != nil && *a.TerminalModel == codexQualityModel && !deleted
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

func qualitySafeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if len(model) > 128 || !strings.HasPrefix(model, "gpt-") {
		return "other"
	}
	for _, r := range model {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return "other"
		}
	}
	return model
}

func recordCodexQualityHeaderModel(attempt *CodexQualityAttempt, value string) {
	model := qualitySafeModel(value)
	if model == "" {
		return
	}
	if !slices.Contains(attempt.HeaderModels, model) && len(attempt.HeaderModels) < 8 {
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
type codexQualityObservedBody struct {
	io.ReadCloser
	sse, sniff, failed, oversized bool
	pending                       []byte
	attempt                       CodexQualityAttempt
	once                          sync.Once
	finish                        func(CodexQualityAttempt)
}

func (b *codexQualityObservedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.feed(p[:n])
	}
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
	if b.sniff && (bytes.HasPrefix(bytes.TrimSpace(b.pending), []byte("data:")) || bytes.HasPrefix(bytes.TrimSpace(b.pending), []byte("event:"))) {
		b.sse, b.sniff = true, false
	}
	if b.sse {
		b.pending = bytes.ReplaceAll(b.pending, []byte("\r\n"), []byte("\n"))
		for {
			end := bytes.Index(b.pending, []byte("\n\n"))
			if end < 0 {
				break
			}
			var data []string
			for _, line := range strings.Split(string(b.pending[:end]), "\n") {
				if strings.HasPrefix(line, "data:") {
					data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				}
			}
			b.event([]byte(strings.Join(data, "\n")))
			b.pending = b.pending[end+2:]
		}
	}
	if len(b.pending) > codexRoutingProbeReadLimit {
		b.oversized, b.failed, b.pending = true, true, nil
	}
}

func (b *codexQualityObservedBody) event(raw []byte) {
	if !gjson.ValidBytes(raw) {
		return
	}
	kind := gjson.GetBytes(raw, "type").String()
	root := gjson.ParseBytes(raw)
	for _, path := range []string{"response.headers.openai-model", "response.headers.x-openai-model", "response.metadata.headers.openai-model", "response.metadata.headers.x-openai-model", "headers.openai-model", "headers.x-openai-model", "metadata.headers.openai-model", "metadata.headers.x-openai-model"} {
		value := root.Get(path)
		if value.Type == gjson.String {
			recordCodexQualityHeaderModel(&b.attempt, value.String())
		} else if value.IsArray() {
			for _, item := range value.Array() {
				if item.Type == gjson.String {
					recordCodexQualityHeaderModel(&b.attempt, item.String())
				}
			}
		}
	}
	r := root.Get("response")
	if !r.Exists() {
		r = root
	}
	model := qualitySafeModel(r.Get("model").String())
	if model != "" && !slices.Contains(b.attempt.ResponseModels, model) && len(b.attempt.ResponseModels) < 8 {
		b.attempt.ResponseModels = append(b.attempt.ResponseModels, model)
	}
	if kind == "response.created" && model != "" {
		b.attempt.CreatedModel = &model
	}
	if kind == "error" || kind == "response.failed" || kind == "response.incomplete" || (root.Get("error").Exists() && root.Get("error").Type != gjson.Null) {
		b.failed = true
		b.attempt.ErrorCode = "upstream_error"
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
		if !b.sse && !b.oversized {
			b.event(b.pending)
		}
		b.attempt.Completed = b.attempt.Completed && !b.failed && b.attempt.HTTPStatus == http.StatusOK
		b.attempt.State = "unknown"
		if b.attempt.Completed {
			b.attempt.State = "complete"
		} else if b.failed || b.attempt.HTTPStatus >= 400 {
			b.attempt.State, b.attempt.ErrorCode = "error", "upstream_error"
		} else if err != nil && err != io.EOF {
			b.attempt.ErrorCode = "stream_error"
		} else {
			b.attempt.ErrorCode = "incomplete"
		}
		b.finish(b.attempt)
	})
}

func (b *codexQualityObservedBody) Close() error { b.complete(nil); return b.ReadCloser.Close() }
