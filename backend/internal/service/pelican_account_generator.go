package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/tidwall/gjson"
)

type pelicanGenerationContextKey struct{}
type pelicanFirstOutputPolicyContextKey struct{}

// Retain the existing first-output policy, but arm its header phase only once
// preparation and account/global admission have completed.
type pelicanFirstOutputPolicy struct {
	timeout         time.Duration
	guard           *openAIFirstOutputHeaderGuard
	startedAt       time.Time
	headersOnce     sync.Once
	headersTimedOut bool
	newGuard        func(context.Context, context.CancelFunc, time.Time) (context.Context, *openAIFirstOutputHeaderGuard)
}

func (p *pelicanFirstOutputPolicy) arm(ctx context.Context) context.Context {
	if p == nil || p.timeout <= 0 {
		return ctx
	}
	p.startedAt = PelicanFirstOutputStart(ctx, time.Now())
	create := p.newGuard
	if create == nil {
		create = newOpenAIFirstOutputHeaderGuard
	}
	var guarded context.Context
	guarded, p.guard = create(ctx, func() {}, p.startedAt.Add(p.timeout))
	return guarded
}

func (p *pelicanFirstOutputPolicy) stopHeaderWait() bool {
	if p == nil || p.guard == nil {
		return false
	}
	p.headersOnce.Do(func() { p.headersTimedOut = p.guard.stopHeaderWait() })
	return p.headersTimedOut
}

// PelicanFirstOutputStart is shared by header and semantic-output guards. The
// first actual send remains the origin for ordinary same-account retries.
func PelicanFirstOutputStart(ctx context.Context, fallback time.Time) time.Time {
	if ctx != nil && IsPelicanGeneration(ctx) {
		if state := pelicanExecutionFromContext(ctx); state != nil {
			state.mu.Lock()
			started := state.generationAt
			state.mu.Unlock()
			if !started.IsZero() {
				return started
			}
		}
	}
	return fallback
}

// The capture belongs to one administrator's fixed-account invocation. It does
// not replace gateway dependencies, create an API key, or enter a selector.
type pelicanGenerationCapture struct {
	account        *Account
	concurrency    *ConcurrencyService
	openaiGateway  *OpenAIGatewayService
	ginContext     *gin.Context
	fallbackModel  string
	fallbackEffort string
	mu             sync.Mutex
	attempts       []*pelicanHTTPAttempt
}

type pelicanHTTPAttempt struct {
	protocol    string
	contentType string
	status      int
	model       string
	effort      string
	raw         bytes.Buffer
	err         error
	wsFrames    bool
}

// IsPelicanGeneration is an internal purpose marker. It is installed only by
// the dedicated generator, never from an HTTP header or public request body.
func IsPelicanGeneration(ctx context.Context) bool {
	return pelicanCaptureFromContext(ctx) != nil
}

func pelicanCaptureFromContext(ctx context.Context) *pelicanGenerationCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(pelicanGenerationContextKey{}).(*pelicanGenerationCapture)
	return capture
}

// PreparePelicanHTTPRequest is the shared outbound boundary. Ordinary requests
// retain their exact pointer and context. Borrow probes coordinate separately;
// their old observation marker must never start the work-generation budget.
// The returned completion function owns the execution/account leases until the
// response body closes, including error responses and same-account retries.
func PreparePelicanHTTPRequest(req *http.Request, accountID int64, accountConcurrency int, transport string) (*http.Request, func(*http.Response, error) (*http.Response, error), error) {
	finish := func(resp *http.Response, err error) (*http.Response, error) { return resp, err }
	if req == nil {
		return req, finish, nil
	}
	capture := pelicanCaptureFromContext(req.Context())
	if capture == nil || capture.account.ID != accountID || IsCodexGatewayBorrowObservation(req.Context()) {
		return req, finish, nil
	}
	var ctx context.Context
	var release func()
	for {
		requestCtx := req.Context()
		var acquireErr error
		var releaseExecution func()
		ctx, releaseExecution, acquireErr = AcquirePelicanExecution(requestCtx, accountID)
		if acquireErr != nil {
			return nil, finish, acquireErr
		}
		releaseAccount, acquireErr := acquirePelicanBusinessAccountSlot(ctx, capture.concurrency, accountID, accountConcurrency)
		if acquireErr != nil {
			releaseExecution()
			return nil, finish, acquireErr
		}
		var releaseOnce sync.Once
		release = func() { releaseOnce.Do(func() { releaseAccount(); releaseExecution() }) }
		// Admission can outlast a borrowed cookie. Recheck the prepared cache
		// binding after all waits, without a nested probe under this lease.
		prepared, hasPreparation := requestCtx.Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation)
		if hasPreparation && capture.openaiGateway != nil {
			checked, prepareErr := capture.openaiGateway.applyCodexGatewayBorrowHTTP(req.WithContext(ctx), capture.account, prepared.model, prepared.proxy)
			if prepareErr != nil {
				release()
				if prepared.application != nil && !time.Now().Before(prepared.application.ExpiresAt) &&
					capture.openaiGateway.codexGatewayBorrowHTTPConfigured(capture.account, prepared.model) && errors.Is(prepareErr, ErrCodexGatewayBorrowChanged) {
					// Refresh an expired route through the normal preparation path
					// after releasing every account/global execution resource.
					refreshed, _, refreshErr := capture.openaiGateway.prepareCodexGatewayBorrowHTTP(requestCtx, req.WithContext(requestCtx), capture.account, prepared.model, prepared.proxy)
					if refreshErr != nil {
						return nil, finish, refreshErr
					}
					req = refreshed
					continue
				}
				return nil, finish, prepareErr
			}
			req = checked
		}
		break
	}
	req = req.WithContext(ctx)
	fallbackModel := capture.fallbackModel
	if capture.ginContext != nil {
		if observed := capture.ginContext.GetString(OpsUpstreamModelKey); observed != "" {
			fallbackModel = observed
		}
	}
	model, effort, protocol, metadataErr := pelicanFinalHTTPRequest(req, fallbackModel, capture.fallbackEffort)
	if metadataErr != nil {
		release()
		return nil, finish, metadataErr
	}
	ctx = BeginPelicanGeneration(ctx)
	if err := ctx.Err(); err != nil {
		release()
		return nil, finish, err
	}
	firstOutputPolicy, _ := ctx.Value(pelicanFirstOutputPolicyContextKey{}).(*pelicanFirstOutputPolicy)
	ctx = firstOutputPolicy.arm(ctx)
	req = req.WithContext(ctx)
	borrowApplied := false
	if prepared, ok := ctx.Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation); ok {
		borrowApplied = prepared.application != nil && prepared.application.Applied
	}
	invocation := PelicanInvocation{Platform: capture.account.Platform, Model: model, Effort: effort,
		Endpoint: req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath(), Protocol: protocol, Transport: transport, BorrowApplied: borrowApplied}
	RecordPelicanInvocation(ctx, invocation)
	attempt := &pelicanHTTPAttempt{protocol: protocol, model: model, effort: effort}
	capture.mu.Lock()
	capture.attempts = append(capture.attempts, attempt)
	capture.mu.Unlock()
	finish = func(resp *http.Response, sendErr error) (*http.Response, error) {
		firstOutputPolicy.stopHeaderWait()
		attempt.err = sendErr
		if resp == nil || resp.Body == nil {
			release()
			return resp, sendErr
		}
		attempt.status, attempt.contentType = resp.StatusCode, resp.Header.Get("Content-Type")
		if transport == "http" && resp.Proto != "" {
			invocation.Transport = resp.Proto
			RecordPelicanInvocation(ctx, invocation)
		}
		// Gateway error readers can have a smaller diagnostic cap. Capture the
		// actual error body first so the administrator keeps its complete text.
		if resp.StatusCode >= http.StatusBadRequest {
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, codexGatewayBorrowPelicanMaxBody+1))
			_ = resp.Body.Close()
			_, _ = attempt.raw.Write(raw)
			attempt.err = readErr
			release()
			resp.Body = io.NopCloser(bytes.NewReader(raw))
			if int64(len(raw)) > codexGatewayBorrowPelicanMaxBody {
				attempt.err = errors.New("upstream response exceeds the pelican record size limit")
			}
			return resp, sendErr
		}
		resp.Body = &pelicanCapturingBody{ReadCloser: resp.Body, attempt: attempt, release: release}
		return resp, sendErr
	}
	return req, finish, nil
}

func acquirePelicanBusinessAccountSlot(ctx context.Context, concurrency *ConcurrencyService, accountID int64, limit int) (func(), error) {
	if concurrency == nil || limit <= 0 {
		return func() {}, nil
	}
	finishQueueWait := BeginPelicanQueueWait(ctx)
	defer finishQueueWait()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		lease, acquired, err := concurrency.AcquireObservedAccountLease(ctx, accountID, limit, func(cause error) { CancelPelicanExecution(ctx, cause) })
		if err != nil {
			return nil, fmt.Errorf("acquire account concurrency capacity: %w", err)
		}
		if acquired {
			return lease.Release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// The WS sender calls this after normal route/socket preparation. Generation
// starts separately, immediately before response.create, so a pool wait or
// generate:false prewarm does not spend the work-generation budget.
func preparePelicanWSSend(ctx context.Context, account *Account, endpoint string, payload map[string]any, borrowed bool) (context.Context, func([]byte) error, func(), error) {
	observe := func([]byte) error { return nil }
	capture := pelicanCaptureFromContext(ctx)
	if capture == nil || account == nil || capture.account.ID != account.ID {
		return ctx, observe, func() {}, nil
	}
	ctx, releaseExecution, err := AcquirePelicanExecution(ctx, account.ID)
	if err != nil {
		return ctx, observe, func() {}, err
	}
	releaseAccount, err := acquirePelicanBusinessAccountSlot(ctx, capture.concurrency, account.ID, account.Concurrency)
	if err != nil {
		releaseExecution()
		return ctx, observe, func() {}, err
	}
	var once sync.Once
	release := func() { once.Do(func() { releaseAccount(); releaseExecution() }) }
	model := openAIWSPayloadString(payload, "model")
	effort := gjson.GetBytes(payloadAsJSONBytes(payload), "reasoning.effort").String()
	RecordPelicanInvocation(ctx, PelicanInvocation{Platform: account.Platform, Model: model, Effort: effort,
		Endpoint: endpoint, Protocol: "responses", Transport: "websocket", BorrowApplied: borrowed})
	attempt := &pelicanHTTPAttempt{protocol: "responses", status: http.StatusOK, model: model, effort: effort, wsFrames: true}
	capture.mu.Lock()
	capture.attempts = append(capture.attempts, attempt)
	capture.mu.Unlock()
	observe = func(message []byte) error {
		if attempt.raw.Len()+len(message)+1 > int(codexGatewayBorrowPelicanMaxBody) {
			attempt.err = errors.New("upstream response exceeds the pelican record size limit")
			return attempt.err
		}
		_, _ = attempt.raw.Write(message)
		_ = attempt.raw.WriteByte('\n')
		return nil
	}
	return ctx, observe, release, nil
}

type pelicanCapturingBody struct {
	io.ReadCloser
	attempt *pelicanHTTPAttempt
	release func()
	once    sync.Once
}

func (b *pelicanCapturingBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if n > 0 {
		if b.attempt.raw.Len()+n > int(codexGatewayBorrowPelicanMaxBody) {
			b.attempt.err = errors.New("upstream response exceeds the pelican record size limit")
			return 0, b.attempt.err
		}
		_, _ = b.attempt.raw.Write(data[:n])
	}
	if err != nil && err != io.EOF {
		b.attempt.err = err
	}
	return n, err
}

func (b *pelicanCapturingBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func pelicanFinalHTTPRequest(req *http.Request, fallbackModel, fallbackEffort string) (model, effort, protocol string, err error) {
	model, effort = fallbackModel, fallbackEffort
	path := strings.ToLower(req.URL.Path)
	switch {
	case strings.Contains(path, "chat/completions"):
		protocol = "chat"
	case strings.Contains(path, "responses"):
		protocol = "responses"
	case strings.Contains(path, "generatecontent"):
		protocol = "gemini"
	default:
		protocol = "claude"
	}
	if req.GetBody != nil || req.Body != nil {
		body := req.Body
		if req.GetBody != nil {
			body, err = req.GetBody()
			if err != nil {
				return model, effort, protocol, err
			}
		}
		{
			raw, readErr := io.ReadAll(io.LimitReader(body, codexGatewayBorrowPelicanMaxBody+1))
			_ = body.Close()
			if readErr != nil {
				return model, effort, protocol, readErr
			}
			if int64(len(raw)) > codexGatewayBorrowPelicanMaxBody {
				return model, effort, protocol, errors.New("pelican request body exceeds its record size limit")
			}
			if req.GetBody == nil {
				// Reasoning recovery deliberately disables transparent replay.
				// Preserve that nil GetBody contract; only restore the exact bytes
				// consumed for this fixed request's final model/effort observation.
				req.Body = io.NopCloser(bytes.NewReader(raw))
			}
			if readErr == nil && strings.EqualFold(req.Header.Get("Content-Encoding"), "zstd") {
				if decoder, decodeErr := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(codexGatewayBorrowPelicanMaxBody)); decodeErr == nil {
					raw, readErr = decoder.DecodeAll(raw, nil)
					decoder.Close()
				}
			}
			if readErr == nil && json.Valid(raw) {
				for _, field := range []string{"model", "request.model"} {
					if value := gjson.GetBytes(raw, field).String(); value != "" {
						model = value
						break
					}
				}
				effort = ""
				for _, field := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
					if value := gjson.GetBytes(raw, field).String(); value != "" {
						effort = value
						break
					}
				}
			}
		}
	}
	if protocol == "gemini" {
		if _, after, found := strings.Cut(req.URL.Path, "/models/"); found {
			if id, _, found := strings.Cut(after, ":"); found {
				if unescaped, err := url.PathUnescape(id); err == nil {
					model = unescaped
				}
			}
		}
	}
	return model, effort, protocol, nil
}

// GeneratePelican sends the fixed题面 using the selected account's ordinary
// business sender. Only that sender owns credential refresh and compatibility
// retries. An UpstreamFailoverError is recorded here and never enters selection.
func (s *AccountTestService) GeneratePelican(ctx context.Context, accountID int64, model, effort string) (*CodexGatewayBorrowPelicanResult, error) {
	result := &CodexGatewayBorrowPelicanResult{ModelID: model, Effort: effort, Status: "failed"}
	ctx = WithAccountObservation(ctx)
	if pelicanExecutionFromContext(ctx) == nil {
		state := newPelicanExecutionState(ctx, sharedPelicanExecution, time.Duration(PelicanDefaultGenerationTimeoutSeconds)*time.Second, time.Now)
		ctx = context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
		defer state.finish()
	}
	BeginPelicanPreparation(ctx)
	if s == nil || s.accountRepo == nil {
		result.Error = "account sender is unavailable"
		return result, errors.New(result.Error)
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		result.Error = "load selected account: " + err.Error()
		return result, err
	}
	if model == "" {
		model = PickConnectionTestModel(account)
		result.ModelID = model
	}
	option := PelicanTestModelOptions(account, model)
	if reason := PelicanTestTextCapabilityReason(account, model); reason != "" {
		result.Status, result.Error = "skipped", reason
		return result, nil
	}
	if effort == "" {
		effort = option.DefaultEffort
	}
	if effort != "" && !slices.Contains(option.ReasoningEfforts, effort) {
		result.Error = "reasoning effort is not supported by the selected account model"
		return result, errors.New(result.Error)
	}
	result.Effort = effort
	capture := &pelicanGenerationCapture{account: account, concurrency: s.pelicanConcurrencyService,
		openaiGateway: s.openaiGatewayService, fallbackModel: option.UpstreamModel, fallbackEffort: effort}
	ctx = context.WithValue(ctx, pelicanGenerationContextKey{}, capture)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	capture.ginContext = c
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/admin/pelican-tests", nil).WithContext(ctx)
	// This represents the server's internal generation operation. No client
	// UA, protocol handshake, API-key subject or group policy is synthesized.
	sendErr := s.sendPelicanWithAccount(ctx, c, account, model, option.UpstreamModel, effort)
	capture.mu.Lock()
	var last *pelicanHTTPAttempt
	if len(capture.attempts) > 0 {
		last = capture.attempts[len(capture.attempts)-1]
	}
	capture.mu.Unlock()
	if last == nil {
		result.RawResponse = w.Body.String()
		if sendErr == nil {
			sendErr = errors.New("account sender completed without an upstream generation request")
		}
		result.Error = sendErr.Error()
		return result, sendErr
	}
	result.RawResponse = last.raw.String()
	if !utf8.ValidString(result.RawResponse) || strings.Contains(strings.ToLower(last.contentType), "eventstream") {
		// AWS event-stream envelopes are binary. Keep the complete original
		// bytes in a lossless representation and parse the shared adapter output.
		result.RawResponse = "base64:" + base64.StdEncoding.EncodeToString(last.raw.Bytes())
	}
	result.ModelID, result.Effort = last.model, last.effort
	if last.wsFrames && last.raw.Len() == 0 && sendErr != nil {
		// A normal generate:false prewarm can reject a route before the题面.
		// Its complete native error is useful even though no work was generated.
		var eventErr *openAIWSUpstreamEventError
		if errors.As(sendErr, &eventErr) && len(eventErr.payload) > 0 {
			result.RawResponse = string(eventErr.payload)
			result.Error = string(eventErr.payload)
		} else {
			result.Error = sendErr.Error()
		}
		return result, sendErr
	}
	if last.status >= http.StatusBadRequest {
		result.Error = fmt.Sprintf("API returned %d: %s", last.status, last.raw.String())
		return result, sendErr
	}
	parseBody := last.raw.String()
	if strings.Contains(strings.ToLower(last.contentType), "eventstream") {
		parseBody = w.Body.String()
	}
	answer, limited, responseModel, parseErr := parsePelicanTextResponse(last.protocol, parseBody)
	if last.wsFrames {
		answer, limited, responseModel, parseErr = parsePelicanWSFrames(parseBody)
	}
	result.RawAnswer = answer
	if responseModel != "" {
		result.ModelID = responseModel
	}
	if last.err != nil {
		parseErr = fmt.Errorf("%w: %v", ErrAccountTestIncomplete, last.err)
	}
	if limited {
		parseErr = fmt.Errorf("%w: output limit reached", ErrAccountTestIncomplete)
	}
	if parseErr != nil {
		result.Error = parseErr.Error()
		if errors.Is(parseErr, ErrAccountTestIncomplete) || errors.Is(parseErr, ErrAccountTestEmpty) {
			result.Status = "incomplete"
		}
		return result, sendErr
	}
	if sendErr != nil {
		result.Error = sendErr.Error()
		return result, sendErr
	}
	result.Status = "complete"
	return result, nil
}

func pelicanOutputBudget(account *Account, upstreamModel string) int64 {
	capacity := ResolveAccountModelContextCapacity(account, upstreamModel)
	if capacity.MaxOutputTokens > 0 {
		return capacity.MaxOutputTokens
	}
	return 64000
}

func pelicanMessagesBody(model, upstreamModel, effort string, budget int64) []byte {
	payload := map[string]any{"model": model, "stream": true, "max_tokens": budget,
		"messages": []map[string]any{{"role": "user", "content": CodexGatewayBorrowPelicanPrompt}}}
	if effort != "" {
		payload["output_config"] = map[string]any{"effort": effort}
		if claude.EffortUsesAdaptiveThinking(upstreamModel) {
			payload["thinking"] = map[string]any{"type": "adaptive"}
		}
	}
	body, _ := json.Marshal(payload)
	return body
}

func (s *AccountTestService) sendPelicanWithAccount(ctx context.Context, c *gin.Context, account *Account, model, upstreamModel, effort string) error {
	budget := pelicanOutputBudget(account, upstreamModel)
	switch {
	case account.IsGemini(), account.Platform == PlatformAntigravity && account.Type != AccountTypeUpstream && isAntigravityGeminiModel(upstreamModel):
		payload := map[string]any{"contents": []map[string]any{{"role": "user", "parts": []map[string]any{{"text": CodexGatewayBorrowPelicanPrompt}}}},
			"generationConfig": map[string]any{"maxOutputTokens": budget}}
		body, _ := json.Marshal(payload)
		if account.IsGemini() {
			if s.pelicanGeminiService == nil {
				return errors.New("gemini account sender is unavailable")
			}
			_, err := s.pelicanGeminiService.ForwardNative(ctx, c, account, model, "streamGenerateContent", true, body)
			return err
		}
		if s.antigravityGatewayService == nil {
			return errors.New("antigravity account sender is unavailable")
		}
		_, err := s.antigravityGatewayService.ForwardGemini(ctx, c, account, model, "streamGenerateContent", true, body, false)
		return err
	case account.Platform == PlatformAnthropic:
		if s.pelicanGatewayService == nil {
			return errors.New("anthropic account sender is unavailable")
		}
		body := pelicanMessagesBody(model, upstreamModel, effort, budget)
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
		if err != nil {
			return err
		}
		_, err = s.pelicanGatewayService.Forward(ctx, c, account, parsed)
		return err
	case account.Platform == PlatformAntigravity:
		if s.antigravityGatewayService == nil {
			return errors.New("antigravity account sender is unavailable")
		}
		_, err := s.antigravityGatewayService.Forward(ctx, c, account, pelicanMessagesBody(model, upstreamModel, effort, budget), false)
		return err
	case account.IsOpenAICompatible():
		if s.openaiGatewayService == nil {
			return errors.New("OpenAI-compatible account sender is unavailable")
		}
		payload := map[string]any{"model": model, "stream": true, "max_output_tokens": budget,
			"input": []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": CodexGatewayBorrowPelicanPrompt}}}}}
		if effort != "" {
			payload["reasoning"] = map[string]any{"effort": effort}
		}
		body, _ := json.Marshal(payload)
		_, err := s.openaiGatewayService.Forward(ctx, c, account, body)
		return err
	default:
		return fmt.Errorf("platform %s has no supported text generation sender", account.Platform)
	}
}

func parsePelicanTextResponse(protocol, raw string) (answer string, limited bool, model string, err error) {
	var text strings.Builder
	emit := func(event TestEvent) {
		if event.Type == "content" {
			_, _ = text.WriteString(event.Text)
		}
	}
	if !json.Valid([]byte(raw)) && pelicanHasSSEDataLine(raw) {
		limited, model, err = parseAccountConnectionStream(protocol, strings.NewReader(raw), false, emit)
		return text.String(), limited, model, err
	}
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return "", false, "", fmt.Errorf("%w: %s", ErrAccountTestProtocol, raw)
	}
	p := connectionStreamState{protocol: protocol, emit: emit}
	if protocol == "claude" {
		if payload["error"] != nil {
			return "", false, "", p.upstreamError(payload["error"])
		}
		if value, _ := payload["model"].(string); value != "" {
			p.upstreamModel = value
		}
		content, _ := payload["content"].([]any)
		for _, rawBlock := range content {
			block, _ := rawBlock.(map[string]any)
			if block["type"] == "text" {
				p.text(block["text"])
			}
		}
		p.stopReason, _ = payload["stop_reason"].(string)
		if p.stopReason == "" {
			return text.String(), false, p.upstreamModel, p.incomplete()
		}
		payload = map[string]any{"type": "message_stop"}
	} else if protocol == "responses" && payload["type"] == nil {
		event := "response.completed"
		if payload["status"] == "incomplete" {
			event = "response.incomplete"
		}
		payload = map[string]any{"type": event, "response": payload}
	}
	done, err := p.consume(payload)
	if err == nil && !done {
		err = p.incomplete()
	}
	return text.String(), p.limited, p.upstreamModel, err
}

func pelicanHasSSEDataLine(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "data:") {
			return true
		}
	}
	return false
}

func parsePelicanWSFrames(raw string) (answer string, limited bool, model string, err error) {
	var text strings.Builder
	p := connectionStreamState{protocol: "responses", emit: func(event TestEvent) {
		if event.Type == "content" {
			_, _ = text.WriteString(event.Text)
		}
	}}
	decoder := json.NewDecoder(strings.NewReader(raw))
	for {
		var payload map[string]any
		if err := decoder.Decode(&payload); err != nil {
			if err == io.EOF {
				break
			}
			return text.String(), false, p.upstreamModel, fmt.Errorf("%w: %v", ErrAccountTestProtocol, err)
		}
		done, parseErr := p.consume(payload)
		if done || parseErr != nil {
			return text.String(), p.limited, p.upstreamModel, parseErr
		}
	}
	return text.String(), p.limited, p.upstreamModel, p.incomplete()
}
