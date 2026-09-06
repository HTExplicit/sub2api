//go:build reasoning_fidelity

package service_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	fidelityMaxAttempts    = 24
	fidelityMaxBody        = 8 << 20
	fidelityMaxDuration    = 45 * time.Minute
	fidelityRequestTimeout = 180 * time.Second
)

// The runner is the sole persistence owner. It rereads the fixed production
// source before each grant, then durably consumes a slot before acknowledging
// it. The Go process only receives secrets over stdin/inherited environment.
type fidelityBootstrap struct {
	SchemaVersion int            `json:"schema_version"`
	Phase         string         `json:"phase"`
	RunID         string         `json:"run_id"`
	ConfigMode    string         `json:"config_mode"`
	Source        fidelitySource `json:"source"`
	Ledger        fidelityLedger `json:"ledger"`
}

type fidelitySource struct {
	Account             service.Account                               `json:"account"`
	Group               service.Group                                 `json:"group"`
	FastPolicy          *service.OpenAIFastPolicySettings             `json:"fast_policy"`
	BusinessPrompt      service.BusinessSystemPromptSnapshot          `json:"business_prompt"`
	RegistryPublication *service.ReasoningFidelityPublicationSnapshot `json:"registry_publication"`
	Settings            map[string]string                             `json:"settings"`
	ChannelModels       map[string]string                             `json:"channel_models"`
	Fingerprint         string                                        `json:"fingerprint"`
	UserID              int64                                         `json:"user_id"`
}

type fidelityLedger struct {
	Attempts      int   `json:"attempts"`
	ConsumedSlots []int `json:"consumed_slots"`
	ElapsedMS     int64 `json:"elapsed_ms"`
}

type fidelityGrant struct {
	Type        string `json:"type"`
	Slot        int    `json:"slot"`
	Fingerprint string `json:"fingerprint"`
	Attempt     int    `json:"attempt"`
	ElapsedMS   int64  `json:"elapsed_ms"`
}

type fidelityResult struct {
	Type                       string         `json:"type"`
	Slot                       int            `json:"slot"`
	Phase                      string         `json:"phase"`
	Model                      string         `json:"model"`
	Effort                     string         `json:"effort"`
	Arm                        string         `json:"arm"`
	Fixture                    string         `json:"fixture"`
	Status                     string         `json:"status"`
	ErrorClass                 string         `json:"error_class,omitempty"`
	HTTPStatus                 int            `json:"http_status"`
	Attempted                  bool           `json:"attempted"`
	Attempts                   int            `json:"attempts"`
	BlockedRetries             int            `json:"blocked_retries"`
	Correct                    *bool          `json:"correct"`
	DurationMS                 int64          `json:"duration_ms"`
	FixtureSHA                 string         `json:"fixture_sha256"`
	AnswerSHA                  string         `json:"answer_sha256,omitempty"`
	ResponseIDSHA              string         `json:"response_id_sha256,omitempty"`
	RequestIDSHA               string         `json:"request_id_sha256,omitempty"`
	SentBodySHA                string         `json:"sent_body_sha256,omitempty"`
	SentInputSHA               string         `json:"sent_input_sha256,omitempty"`
	ResponseOutputSHA          string         `json:"response_output_sha256,omitempty"`
	SentModelSHA               string         `json:"sent_model_sha256,omitempty"`
	SentEffortSHA              string         `json:"sent_effort_sha256,omitempty"`
	InputPreserved             bool           `json:"input_preserved"`
	ReasoningConfigPreserved   bool           `json:"reasoning_config_preserved"`
	ModelMatchesRequest        bool           `json:"model_matches_request"`
	ResponseModelMatchesSent   bool           `json:"response_model_matches_sent"`
	EncryptedInputBefore       int            `json:"encrypted_input_before"`
	EncryptedInputSent         int            `json:"encrypted_input_sent"`
	PhaseItemsBefore           int            `json:"phase_items_before"`
	PhaseItemsSent             int            `json:"phase_items_sent"`
	EncryptedReasoningComplete bool           `json:"encrypted_reasoning_complete"`
	Candidate518               bool           `json:"candidate_518n_minus_2"`
	RawOutputPreserved         bool           `json:"raw_output_preserved"`
	Usage                      *fidelityUsage `json:"usage"`
}

type fidelitySlot struct {
	ID                          int
	Model, Effort, Arm, Fixture string
}

type fidelityExchange struct {
	Response    fidelityResponse
	Result      fidelityResult
	RequestBody []byte
	SentBody    []byte
}

type fidelityHarness struct {
	boot             fidelityBootstrap
	input            *bufio.Reader
	output           *json.Encoder
	sourceAccountSHA string
	started          time.Time
	ctx              context.Context
	upstream         *fidelityBudgetUpstream
	gateway          *service.OpenAIGatewayService
	stopped          string
	results          []fidelityResult
}

// Only this test may use the real HTTP repository. Opt-in is mandatory and
// normal unit/CI runs never start a network request or read a live snapshot.
func TestReasoningFidelityLive(t *testing.T) {
	if os.Getenv("SUB2API_REASONING_FIDELITY_LIVE") != "1" {
		t.Skip("explicit controlled runner opt-in required")
	}
	out := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("diagnostic_output_initialization_failed")
	}
	// Suppress every production logging surface before reading any source.
	// Deliberately keep stdout redirected until process exit: testing's PASS
	// trailer must not leak into the JSON-line broker protocol.
	os.Stdout, os.Stderr = devnull, devnull
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	_ = logger.Init(logger.InitOptions{Level: "fatal", Output: logger.OutputOptions{ToStdout: true}})
	logger.SetSink(nil)
	log.SetOutput(io.Discard)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	encoder := json.NewEncoder(out)
	defer func() {
		if recover() != nil {
			_ = encoder.Encode(map[string]any{"type": "summary", "status": "internal_panic", "attempts_unknown": true})
			t.Fail()
		}
	}()
	if err := fidelityRun(bufio.NewReaderSize(os.Stdin, 64<<10), encoder); err != nil {
		_ = encoder.Encode(map[string]any{"type": "summary", "status": "harness_failed", "error_class": err.Error()})
		t.Fail()
	}
}

func fidelityRun(input *bufio.Reader, output *json.Encoder) error {
	var boot fidelityBootstrap
	if err := fidelityReadJSON(input, &boot); err != nil {
		return errors.New("invalid_bootstrap")
	}
	if err := fidelityValidateBootstrap(boot); err != nil {
		return err
	}
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return errors.New("production_config_load_failed")
	}
	// No network clients other than HTTPUpstream are constructed. In particular
	// database/Redis credentials loaded by the production config stay unused.
	remaining := fidelityMaxDuration - time.Duration(boot.Ledger.ElapsedMS)*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	h := &fidelityHarness{boot: boot, input: input, output: output, started: time.Now(), ctx: ctx}
	h.sourceAccountSHA = fidelityHashJSON(&h.boot.Source.Account)
	u := &fidelityBudgetUpstream{h: h, inner: repository.NewHTTPUpstream(cfg), attempts: boot.Ledger.Attempts, used: map[int]bool{}}
	for _, slot := range boot.Ledger.ConsumedSlots {
		u.used[slot] = true
	}
	h.upstream = u
	h.gateway, err = service.ReasoningFidelityGatewayForTest(cfg, u, boot.Source.BusinessPrompt, boot.Source.RegistryPublication, boot.Source.Settings)
	if err != nil {
		return errors.New("unsupported_source_policy")
	}
	// Resolve the exact production endpoint/header builder without sending.
	probe := fidelityRequestBody("gpt-5.6-sol", "xhigh", fidelitySinglePrompt, false)
	probe, err = fidelityPrepareIngressBody(probe, boot.Source)
	if err != nil {
		return errors.New("source_group_policy_rejected")
	}
	probeContext, _ := h.newContext(ctx, probe, "xhigh")
	req, err := service.ReasoningFidelityDirectRequestForTest(probeContext.Request.Context(), h.gateway, probeContext, &h.boot.Source.Account, probe)
	if err != nil {
		return errors.New("source_endpoint_validation_failed")
	}
	if req.URL == nil || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.RawQuery != "" {
		return errors.New("source_endpoint_not_https_responses")
	}
	u.endpoint = req.URL.String()
	if !strings.HasSuffix(req.URL.Path, "/responses") {
		return errors.New("source_endpoint_not_responses")
	}
	u.authorizationSHA = fidelityHash([]byte(req.Header.Get("Authorization")))
	u.proxyURL = ""
	if h.boot.Source.Account.Proxy != nil {
		u.proxyURL = h.boot.Source.Account.Proxy.URL()
	}
	_ = req.Body.Close()
	_ = output.Encode(map[string]any{"type": "ready", "phase": boot.Phase, "source_sha256": boot.Source.Fingerprint, "endpoint_sha256": fidelityHash([]byte(u.endpoint)), "attempts": u.attempts})
	if os.Getenv("SUB2API_REASONING_FIDELITY_VALIDATE_ONLY") == "1" {
		return output.Encode(map[string]any{"type": "summary", "phase": boot.Phase, "status": "validated", "attempts": u.attempts})
	}
	if boot.Phase == "before" {
		h.runBefore()
	} else {
		h.runAfter()
	}
	status := "completed"
	if h.stopped != "" {
		status = h.stopped
	}
	return output.Encode(map[string]any{"type": "summary", "phase": boot.Phase, "status": status, "attempts": u.attempts, "blocked_retries": u.totalBlocked, "phase_duration_ms": time.Since(h.started).Milliseconds(), "results": len(h.results), "source_sha256": boot.Source.Fingerprint})
}

func fidelityValidateBootstrap(b fidelityBootstrap) error {
	if b.SchemaVersion != 1 || (b.Phase != "before" && b.Phase != "after") || b.ConfigMode != "production_env" || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`).MatchString(b.RunID) {
		return errors.New("invalid_bootstrap_contract")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(b.Source.Fingerprint) {
		return errors.New("invalid_source_fingerprint")
	}
	a := &b.Source.Account
	if a.ID <= 0 || a.Name != "白嫖666" || a.Type != service.AccountTypeAPIKey || a.Platform != service.PlatformOpenAI || a.Credentials == nil || a.GetOpenAIProtocolAPIKey() == "" || a.Status != service.StatusActive || !a.Schedulable || a.Concurrency < 1 {
		return errors.New("invalid_fixed_account")
	}
	if a.ParentAccountID != nil || service.IsCindyRuntimeCompatibleAPIKeyAccount(a.Platform, a.Type, a.Credentials) || !a.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityResponses) {
		return errors.New("unsupported_fixed_account")
	}
	if b.Source.Group.ID <= 0 || b.Source.Group.Platform != service.PlatformOpenAI || b.Source.Group.Status != service.StatusActive {
		return errors.New("invalid_fixed_group")
	}
	member := false
	for _, groupID := range a.GroupIDs {
		if groupID == b.Source.Group.ID {
			member = true
		}
	}
	if !member {
		return errors.New("fixed_account_group_mismatch")
	}
	if (a.ProxyID == nil) != (a.Proxy == nil) || (a.Proxy != nil && (*a.ProxyID != a.Proxy.ID || !a.Proxy.IsActive() || a.Proxy.IsExpired(time.Now()))) {
		return errors.New("invalid_fixed_proxy")
	}
	if b.Source.Settings == nil || b.Source.FastPolicy == nil || len(b.Source.ChannelModels) != 2 || b.Source.ChannelModels["gpt-5.6-sol"] == "" || b.Source.ChannelModels["gpt-6-astra"] == "" {
		return errors.New("missing_frozen_policy")
	}
	if b.Ledger.Attempts < 0 || b.Ledger.Attempts > fidelityMaxAttempts || b.Ledger.ElapsedMS < 0 || b.Ledger.ElapsedMS >= fidelityMaxDuration.Milliseconds() || len(b.Ledger.ConsumedSlots) != b.Ledger.Attempts {
		return errors.New("invalid_budget_ledger")
	}
	used := map[int]bool{}
	for _, slot := range b.Ledger.ConsumedSlots {
		if slot < 1 || slot > fidelityMaxAttempts || used[slot] {
			return errors.New("invalid_budget_slots")
		}
		used[slot] = true
	}
	return nil
}

func fidelityReadJSON(r *bufio.Reader, dst any) error {
	var buf bytes.Buffer
	for {
		line, more, err := r.ReadLine()
		if err != nil {
			return errors.New("broker_input_unavailable")
		}
		if buf.Len()+len(line) > fidelityMaxBody {
			return errors.New("broker_input_limit")
		}
		buf.Write(line)
		if !more {
			break
		}
	}
	dec := json.NewDecoder(&buf)
	if err := dec.Decode(dst); err != nil {
		return errors.New("broker_input_invalid")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return errors.New("broker_input_trailing")
	}
	return nil
}

func (h *fidelityHarness) newContext(ctx context.Context, body []byte, effort string) (*gin.Context, *httptest.ResponseRecorder) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("User-Agent", "sub2api-reasoning-fidelity-diagnostic/1")
	c.Request.Header.Set("X-Client-Request-Id", fidelityNewID())
	service.ReasoningFidelityContextForTest(ctx, c, &h.boot.Source.Group, h.boot.Source.FastPolicy, h.boot.Source.UserID, effort)
	return c, r
}

func (h *fidelityHarness) runBefore() {
	for modelIndex, model := range []string{"gpt-5.6-sol", "gpt-6-astra"} {
		effort := []string{"xhigh", "max"}[modelIndex]
		base := modelIndex*6 + 1
		single := fidelityRequestBody(model, effort, fidelitySinglePrompt, false)
		h.runSlot(fidelitySlot{base, model, effort, "direct", "single"}, single)
		h.runSlot(fidelitySlot{base + 1, model, effort, "gateway", "single"}, single)
		h.runTools(fidelitySlot{base + 2, model, effort, "direct", "tool_first"})
		h.runTools(fidelitySlot{base + 4, model, effort, "gateway", "tool_first"})
	}
}

func (h *fidelityHarness) runAfter() {
	type pair struct {
		direct, gateway fidelityExchange
		model, effort   string
	}
	pairs := make([]pair, 0, 2)
	for modelIndex, model := range []string{"gpt-5.6-sol", "gpt-6-astra"} {
		effort := []string{"xhigh", "max"}[modelIndex]
		base := 13 + modelIndex*4
		single := fidelityRequestBody(model, effort, fidelitySinglePrompt, false)
		d := h.runSlot(fidelitySlot{base, model, effort, "direct", "single"}, single)
		g := h.runSlot(fidelitySlot{base + 1, model, effort, "gateway", "single"}, single)
		h.runTools(fidelitySlot{base + 2, model, effort, "gateway", "tool_first"})
		pairs = append(pairs, pair{d, g, model, effort})
	}
	for i, p := range pairs {
		eligible := fidelityPilotPairEligible(p.direct, p.gateway)
		for armIndex, arm := range []string{"direct", "gateway"} {
			slot := fidelitySlot{21 + i*2 + armIndex, p.model, p.effort, arm, "pilot"}
			if !eligible {
				h.skip(slot, "pilot_ineligible")
				continue
			}
			base := p.direct
			if arm == "gateway" {
				base = p.gateway
			}
			body, ok := fidelityPilotBody(base)
			if !ok {
				h.skip(slot, "pilot_state_unavailable")
				continue
			}
			h.runSlot(slot, body)
		}
	}
}

func (h *fidelityHarness) runTools(firstSlot fidelitySlot) {
	body := fidelityRequestBody(firstSlot.Model, firstSlot.Effort, fidelityOrdersPrompt, true)
	first := h.runSlot(firstSlot, body)
	secondSlot := firstSlot
	secondSlot.ID++
	secondSlot.Fixture = "tool_final"
	next, ok := fidelityToolContinuation(body, first.Response)
	if !ok {
		h.skip(secondSlot, "tool_state_unavailable")
		return
	}
	h.runSlot(secondSlot, next)
}

func (h *fidelityHarness) skip(slot fidelitySlot, status string) fidelityExchange {
	r := fidelityResult{Type: "result", Slot: slot.ID, Phase: h.boot.Phase, Model: slot.Model, Effort: slot.Effort, Arm: slot.Arm, Fixture: slot.Fixture, Status: status, Attempts: h.upstream.attempts, FixtureSHA: fidelityFixtureHash(slot.Fixture)}
	h.results = append(h.results, r)
	_ = h.output.Encode(r)
	return fidelityExchange{Result: r}
}

func (h *fidelityHarness) runSlot(slot fidelitySlot, rawBody []byte) fidelityExchange {
	if h.stopped != "" {
		return h.skip(slot, "batch_stopped")
	}
	if h.upstream.used[slot.ID] {
		return h.skip(slot, "already_consumed")
	}
	if h.ctx.Err() != nil {
		h.stopped = "batch_time_limit"
		return h.skip(slot, "batch_time_limit")
	}
	body, policyErr := fidelityPrepareIngressBody(rawBody, h.boot.Source)
	if policyErr != nil {
		return h.skip(slot, "group_policy_rejected")
	}
	ctx, cancel := context.WithTimeout(h.ctx, fidelityRequestTimeout)
	defer cancel()
	c, recorder := h.newContext(ctx, body, slot.Effort)
	ctx = c.Request.Context()
	h.upstream.activeSlot = slot.ID
	h.upstream.activeContext = ctx
	h.upstream.sentBody = nil
	h.upstream.rawResponse = nil
	h.upstream.responseContentType = ""
	h.upstream.responseStatus = 0
	h.upstream.blocked = 0
	h.upstream.lastError = ""
	attemptsBefore := h.upstream.attempts
	start := time.Now()
	var response fidelityResponse
	var callErr error
	if slot.Arm == "gateway" {
		_, callErr = h.gateway.Forward(ctx, c, &h.boot.Source.Account, body)
		response = fidelityParseResponse(recorder.Body.Bytes(), recorder.Header().Get("Content-Type"), recorder.Code)
	} else {
		var req *http.Request
		req, callErr = service.ReasoningFidelityDirectRequestForTest(ctx, h.gateway, c, &h.boot.Source.Account, body)
		if callErr == nil {
			var resp *http.Response
			resp, callErr = h.upstream.Do(req, h.upstream.proxyURL, h.boot.Source.Account.ID, h.boot.Source.Account.Concurrency)
			if resp != nil {
				data, readErr := io.ReadAll(io.LimitReader(resp.Body, fidelityMaxBody+1))
				_ = resp.Body.Close()
				if readErr != nil {
					callErr = readErr
				}
				response = fidelityParseResponse(data, resp.Header.Get("Content-Type"), resp.StatusCode)
			}
		}
	}
	upstreamResponse := fidelityParseResponse(h.upstream.rawResponse, h.upstream.responseContentType, h.upstream.responseStatus)
	if h.upstream.responseStatus == 401 || h.upstream.responseStatus == 403 || upstreamResponse.ErrorClass == "auth" {
		h.stopped = "authentication_stopped"
	}
	if h.upstream.responseStatus == 402 || h.upstream.responseStatus == 429 || upstreamResponse.ErrorClass == "quota" || upstreamResponse.ErrorClass == "rate_limit" {
		h.stopped = "quota_or_rate_limit_stopped"
	}
	if h.upstream.lastError == "source_changed" || strings.HasPrefix(h.upstream.lastError, "broker_") || h.upstream.lastError == "invalid_broker_grant" {
		h.stopped = "source_verification_stopped"
	}
	r := fidelityResult{Type: "result", Slot: slot.ID, Phase: h.boot.Phase, Model: slot.Model, Effort: slot.Effort, Arm: slot.Arm, Fixture: slot.Fixture, Status: response.Status, ErrorClass: response.ErrorClass, HTTPStatus: h.upstream.responseStatus, Attempted: h.upstream.attempts > attemptsBefore, Attempts: h.upstream.attempts, BlockedRetries: h.upstream.blocked, DurationMS: time.Since(start).Milliseconds(), FixtureSHA: fidelityFixtureHash(slot.Fixture), Usage: upstreamResponse.Usage, EncryptedReasoningComplete: response.ReasoningComplete}
	if callErr != nil {
		if r.Status == "" || r.Status == "completed" {
			r.Status = "request_failed"
		}
		if ctx.Err() != nil {
			r.ErrorClass = "request_timeout"
		} else if h.upstream.lastError != "" {
			r.ErrorClass = h.upstream.lastError
		} else {
			r.ErrorClass = "forward_error"
		}
	}
	if h.upstream.blocked > 0 {
		r.Status = "retry_blocked"
	}
	if r.Status == "" {
		r.Status = "no_response"
	}
	if response.Text != "" {
		r.AnswerSHA = fidelityHash([]byte(response.Text))
	}
	if response.ID != "" {
		r.ResponseIDSHA = fidelityHash([]byte(response.ID))
	}
	r.RequestIDSHA = fidelityHash([]byte(c.Request.Header.Get("X-Client-Request-Id")))
	if len(h.upstream.sentBody) > 0 {
		r.SentBodySHA = fidelityHash(h.upstream.sentBody)
		fidelityObserveRequest(&r, body, h.upstream.sentBody, response.Model)
	}
	if len(response.Output) > 0 {
		r.ResponseOutputSHA = fidelityHashJSON(response.Output)
	}
	r.RawOutputPreserved = response.Status == "completed" && upstreamResponse.Status == "completed" && len(response.Output) > 0 && fidelityHashJSON(response.Output) == fidelityHashJSON(upstreamResponse.Output)
	if r.Usage != nil && r.Usage.ReasoningTokens != nil {
		n := *r.Usage.ReasoningTokens
		r.Candidate518 = n >= 516 && (n+2)%518 == 0
	}
	if r.Status == "completed" && !response.HasError && !response.HasIncomplete && !response.HasRefusal && slot.Fixture != "tool_first" {
		correct := fidelityScore(slot.Fixture, response.Text)
		r.Correct = &correct
	}
	h.results = append(h.results, r)
	_ = h.output.Encode(r)
	return fidelityExchange{Response: response, Result: r, RequestBody: append([]byte(nil), rawBody...), SentBody: append([]byte(nil), h.upstream.sentBody...)}
}

func fidelityRequestBody(model, effort, prompt string, tool bool) []byte {
	v := map[string]any{"model": model, "reasoning": map[string]any{"effort": effort}, "stream": true, "store": false, "include": []string{"reasoning.encrypted_content"}, "instructions": "Use only the supplied task and tool data. Return the requested JSON, without extra prose.", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}}}}
	if tool {
		v["tools"] = []any{map[string]any{"type": "function", "name": "load_orders", "description": "Read the fixed test orders. It is read-only and has no side effects.", "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}, "strict": true}}
		v["tool_choice"] = map[string]any{"type": "function", "name": "load_orders"}
	}
	b, _ := json.Marshal(v)
	return b
}

func fidelityChannelMap(body []byte, mapping map[string]string) []byte {
	var v map[string]json.RawMessage
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	var model string
	_ = json.Unmarshal(v["model"], &model)
	if mapped := mapping[model]; mapped != "" {
		v["model"], _ = json.Marshal(mapped)
	}
	b, _ := json.Marshal(v)
	return b
}

func fidelityPrepareIngressBody(body []byte, source fidelitySource) ([]byte, error) {
	// Match the native handler: client effort policy precedes channel mapping,
	// while Forward applies account mapping and endpoint adaptation afterward.
	group := source.Group
	updated, _, err := service.ApplyOpenAIReasoningEffortPolicy(body, group.MaxReasoningEffort, group.ReasoningEffortMappings, group.MaxReasoningEffortOverLimit)
	if err != nil {
		return nil, err
	}
	return fidelityChannelMap(updated, source.ChannelModels), nil
}

func fidelityToolContinuation(body []byte, r fidelityResponse) ([]byte, bool) {
	if r.Status != "completed" || r.HasError || r.HasIncomplete || r.HasRefusal {
		return nil, false
	}
	callID := ""
	for _, raw := range r.Output {
		var item struct{ Type, CallID, Name, Arguments string }
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, false
		}
		_ = json.Unmarshal(fields["type"], &item.Type)
		if item.Type != "function_call" {
			continue
		}
		_ = json.Unmarshal(fields["call_id"], &item.CallID)
		_ = json.Unmarshal(fields["name"], &item.Name)
		_ = json.Unmarshal(fields["arguments"], &item.Arguments)
		var args map[string]any
		if callID != "" || item.CallID == "" || item.Name != "load_orders" || json.Unmarshal([]byte(item.Arguments), &args) != nil || len(args) != 0 {
			return nil, false
		}
		callID = item.CallID
	}
	if callID == "" {
		return nil, false
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(body, &v) != nil {
		return nil, false
	}
	var input []json.RawMessage
	if json.Unmarshal(v["input"], &input) != nil {
		return nil, false
	}
	input = append(input, r.Output...)
	toolOutput, _ := json.Marshal(map[string]any{"type": "function_call_output", "call_id": callID, "output": fidelityOrdersResult})
	input = append(input, toolOutput)
	v["input"], _ = json.Marshal(input)
	v["tool_choice"] = json.RawMessage(`"none"`)
	b, err := json.Marshal(v)
	return b, err == nil
}

func fidelityPilotPairEligible(a, b fidelityExchange) bool {
	for _, e := range []fidelityExchange{a, b} {
		if e.Result.Status != "completed" || e.Response.Status != "completed" || !e.Response.ReasoningComplete || e.Response.HasTool || e.Response.HasError || e.Response.HasIncomplete || e.Response.HasRefusal || !e.Result.ResponseModelMatchesSent {
			return false
		}
		var req map[string]json.RawMessage
		if json.Unmarshal(e.RequestBody, &req) != nil {
			return false
		}
		for _, key := range []string{"previous_response_id", "conversation", "context_management"} {
			if _, present := req[key]; present {
				return false
			}
		}
		for _, item := range e.Response.Output {
			var v map[string]any
			_ = json.Unmarshal(item, &v)
			if v["type"] == "compaction" {
				return false
			}
		}
	}
	if a.Result.SentModelSHA != b.Result.SentModelSHA || a.Result.SentEffortSHA != b.Result.SentEffortSHA {
		return false
	}
	return (a.Result.Correct != nil && !*a.Result.Correct) || (b.Result.Correct != nil && !*b.Result.Correct) || a.Result.Candidate518 || b.Result.Candidate518
}

func fidelityPilotBody(e fidelityExchange) ([]byte, bool) {
	var v map[string]json.RawMessage
	if json.Unmarshal(e.RequestBody, &v) != nil {
		return nil, false
	}
	var input []json.RawMessage
	if json.Unmarshal(v["input"], &input) != nil || len(e.Response.RawReasoning) == 0 {
		return nil, false
	}
	input = append(input, e.Response.RawReasoning...)
	commentary, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "phase": "commentary", "content": []any{map[string]any{"type": "output_text", "text": "I will continue checking the constraints and verify the calculation before providing the final answer."}}})
	input = append(input, commentary)
	v["input"], _ = json.Marshal(input)
	b, err := json.Marshal(v)
	return b, err == nil
}

func fidelityObserveRequest(r *fidelityResult, before, after []byte, responseModel string) {
	var a, b map[string]json.RawMessage
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil {
		return
	}
	r.InputPreserved = fidelityJSONEqual(a["input"], b["input"])
	r.ReasoningConfigPreserved = fidelityJSONEqual(a["reasoning"], b["reasoning"])
	r.ModelMatchesRequest = fidelityJSONEqual(a["model"], b["model"])
	r.SentInputSHA = fidelityHash(b["input"])
	var sentModel string
	_ = json.Unmarshal(b["model"], &sentModel)
	r.SentModelSHA = fidelityHash([]byte(sentModel))
	r.ResponseModelMatchesSent = sentModel != "" && responseModel == sentModel
	var reasoning map[string]json.RawMessage
	_ = json.Unmarshal(b["reasoning"], &reasoning)
	r.SentEffortSHA = fidelityHash(reasoning["effort"])
	r.EncryptedInputBefore, r.PhaseItemsBefore = fidelityInputCounts(a["input"])
	r.EncryptedInputSent, r.PhaseItemsSent = fidelityInputCounts(b["input"])
}

func fidelityInputCounts(raw []byte) (encrypted, phase int) {
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return
	}
	for _, item := range items {
		var text string
		if json.Unmarshal(item["encrypted_content"], &text) == nil && text != "" {
			encrypted++
		}
		if _, ok := item["phase"]; ok {
			phase++
		}
	}
	return
}

func fidelityJSONEqual(a, b []byte) bool {
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}
func fidelityHash(b []byte) string  { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func fidelityHashJSON(v any) string { b, _ := json.Marshal(v); return fidelityHash(b) }
func fidelityNewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("request_id_entropy_unavailable")
	}
	return hex.EncodeToString(b[:])
}
func fidelityFixtureHash(kind string) string {
	if strings.HasPrefix(kind, "tool_") {
		return fidelityHash([]byte(fidelityOrdersPrompt + "\n" + fidelityOrdersResult))
	}
	return fidelityHash([]byte(fidelitySinglePrompt))
}

// This boundary is reached by real Forward and by direct requests. A grant is
// required exactly once per preallocated slot; Forward retries cannot consume a
// second upstream attempt. GetBody=nil additionally disables Go POST replay.
type fidelityBudgetUpstream struct {
	mu                                          sync.Mutex
	h                                           *fidelityHarness
	inner                                       service.HTTPUpstream
	activeSlot, attempts, blocked, totalBlocked int
	activeContext                               context.Context
	used                                        map[int]bool
	endpoint, authorizationSHA, proxyURL        string
	sentBody, rawResponse                       []byte
	responseContentType                         string
	responseStatus                              int
	lastError                                   string
}

func (u *fidelityBudgetUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return u.send(req, proxyURL, accountID, concurrency, nil)
}
func (u *fidelityBudgetUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.send(req, proxyURL, accountID, concurrency, profile)
}

func (u *fidelityBudgetUpstream) send(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fail := func(class string) (*http.Response, error) { u.lastError = class; return nil, errors.New(class) }
	if u.activeSlot < 1 || u.activeSlot > 24 || u.used[u.activeSlot] {
		u.blocked++
		u.totalBlocked++
		return fail("slot_retry_blocked")
	}
	if u.attempts >= fidelityMaxAttempts {
		return fail("attempt_budget_exhausted")
	}
	if req == nil || req.URL == nil || req.Method != http.MethodPost || req.URL.String() != u.endpoint || accountID != u.h.boot.Source.Account.ID || concurrency != u.h.boot.Source.Account.Concurrency || proxyURL != u.proxyURL || fidelityHash([]byte(req.Header.Get("Authorization"))) != u.authorizationSHA || fidelityHashJSON(&u.h.boot.Source.Account) != u.h.sourceAccountSHA {
		return fail("source_changed")
	}
	slotContext := u.activeContext
	if slotContext == nil {
		slotContext = u.h.ctx
	}
	if req.Context().Err() != nil || slotContext.Err() != nil || u.h.ctx.Err() != nil {
		return fail("request_timeout")
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, fidelityMaxBody+1))
	if err != nil || len(data) > fidelityMaxBody {
		return fail("request_body_unavailable")
	}
	_ = req.Body.Close()
	if err = u.h.output.Encode(map[string]any{"type": "before_send", "slot": u.activeSlot, "expected_fingerprint": u.h.boot.Source.Fingerprint}); err != nil {
		return fail("broker_unavailable")
	}
	var grant fidelityGrant
	// A blocked broker cannot hold an upstream call past its request deadline.
	grantResult := make(chan error, 1)
	go func() { grantResult <- fidelityReadJSON(u.h.input, &grant) }()
	select {
	case err = <-grantResult:
	case <-slotContext.Done():
		return fail("broker_timeout")
	}
	if err != nil {
		return fail("broker_unavailable")
	}
	if grant.Type != "send_granted" {
		return fail("broker_denied")
	}
	if grant.Slot != u.activeSlot || grant.Fingerprint != u.h.boot.Source.Fingerprint || grant.Attempt != u.attempts+1 || grant.Attempt > 24 || grant.ElapsedMS < u.h.boot.Ledger.ElapsedMS || grant.ElapsedMS >= fidelityMaxDuration.Milliseconds() {
		return fail("invalid_broker_grant")
	}
	u.used[u.activeSlot] = true
	u.attempts = grant.Attempt
	u.sentBody = append([]byte(nil), data...)
	// Forward deliberately detaches client cancellation so normal clients can
	// finish usage accounting. A diagnostic is different: the fixed 180-second
	// slot deadline is a hard experiment bound, not a client-disconnect hint.
	// Retain all production transport/context values, reattaching only this
	// test-owned deadline and cancellation at the final outbound boundary.
	sendContext := req.Context()
	var cancelDeadline context.CancelFunc = func() {}
	if deadline, ok := slotContext.Deadline(); ok {
		sendContext, cancelDeadline = context.WithDeadline(sendContext, deadline)
	}
	sendContext, cancelSend := context.WithCancel(sendContext)
	stopCancellation := context.AfterFunc(slotContext, cancelSend)
	cleanup := func() { stopCancellation(); cancelSend(); cancelDeadline() }
	req = req.Clone(service.WithHTTPUpstreamRedirectsDisabled(sendContext))
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = nil
	var resp *http.Response
	if profile == nil {
		resp, err = u.inner.Do(req, proxyURL, accountID, concurrency)
	} else {
		resp, err = u.inner.DoWithTLS(req, proxyURL, accountID, concurrency, profile)
	}
	if err != nil {
		cleanup()
		u.lastError = "transport_error"
		return nil, errors.New("transport_error")
	}
	if resp == nil || resp.Body == nil {
		cleanup()
		return fail("empty_upstream_response")
	}
	u.responseStatus = resp.StatusCode
	u.responseContentType = resp.Header.Get("Content-Type")
	resp.Body = &fidelityCaptureBody{ReadCloser: resp.Body, output: &u.rawResponse, remaining: fidelityMaxBody, cleanup: cleanup}
	return resp, nil
}

type fidelityCaptureBody struct {
	io.ReadCloser
	output    *[]byte
	remaining int
	cleanup   func()
	closeOnce sync.Once
}

func (r *fidelityCaptureBody) Close() error {
	err := r.ReadCloser.Close()
	r.closeOnce.Do(func() {
		if r.cleanup != nil {
			r.cleanup()
		}
	})
	return err
}

func (r *fidelityCaptureBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		take := n
		if take > r.remaining {
			take = r.remaining
		}
		*r.output = append(*r.output, p[:take]...)
		r.remaining -= take
		if take < n {
			return n, errors.New("response_body_limit")
		}
	}
	return n, err
}
