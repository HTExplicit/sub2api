//go:build reasoning_fidelity && reasoning_recovery_diagnostic

package service_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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

const recoveryDiagnosticRunID = "reasoning-replay-recovery-20260907"
const recoveryDiagnosticMaxAttempts = 16

type recoveryDiagnosticBootstrap struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id"`
	ConfigMode    string         `json:"config_mode"`
	SourceSHA     string         `json:"source_sha"`
	Source        fidelitySource `json:"source"`
}
type recoveryDiagnosticCase struct {
	ID                     int
	Model, Effort, Fixture string
}
type recoveryDiagnosticAttempt struct {
	Type               string         `json:"type"`
	CaseID             int            `json:"case_id"`
	Slot               int            `json:"slot"`
	Model              string         `json:"model"`
	Effort             string         `json:"effort"`
	Fixture            string         `json:"fixture"`
	Status             string         `json:"status"`
	ErrorClass         string         `json:"error_class,omitempty"`
	ErrorCode          string         `json:"error_code,omitempty"`
	Attempts           int            `json:"attempts"`
	DurationMS         int64          `json:"duration_ms"`
	HTTPStatus         int            `json:"http_status"`
	Usage              *fidelityUsage `json:"usage"`
	SentBodySHA        string         `json:"sent_body_sha256,omitempty"`
	SentInputSHA       string         `json:"sent_input_sha256,omitempty"`
	SentModelSHA       string         `json:"sent_model_sha256,omitempty"`
	SentReasoningSHA   string         `json:"sent_reasoning_sha256,omitempty"`
	ResponseIDSHA      string         `json:"response_id_sha256,omitempty"`
	ResponseOutputSHA  string         `json:"response_output_sha256,omitempty"`
	EncryptedInputSent int            `json:"encrypted_input_sent"`
}
type recoveryDiagnosticResult struct {
	Type                       string `json:"type"`
	CaseID                     int    `json:"case_id"`
	Model                      string `json:"model"`
	Effort                     string `json:"effort"`
	Fixture                    string `json:"fixture"`
	Status                     string `json:"status"`
	Attempted                  bool   `json:"attempted"`
	AttemptsUsed               int    `json:"attempts_used"`
	Attempts                   int    `json:"attempts"`
	BlockedRetries             int    `json:"blocked_retries"`
	DurationMS                 int64  `json:"duration_ms"`
	Correct                    *bool  `json:"correct"`
	FixtureSHA                 string `json:"fixture_sha256"`
	AnswerSHA                  string `json:"answer_sha256,omitempty"`
	ReasoningConfigPreserved   bool   `json:"reasoning_config_preserved"`
	ResponseModelMatchesSent   bool   `json:"response_model_matches_sent"`
	EncryptedReasoningComplete bool   `json:"encrypted_reasoning_complete"`
	RecoveryObserved           bool   `json:"recovery_observed"`
	NegativeCacheObserved      bool   `json:"negative_cache_observed"`
	OldCipherRemoved           *bool  `json:"old_cipher_removed"`
	NewCipherPreserved         *bool  `json:"new_cipher_preserved"`
}
type recoveryDiagnosticExchange struct {
	result        recoveryDiagnosticResult
	raw           fidelityResponse
	request, sent []byte
	attempts      []recoveryDiagnosticRecord
}
type recoveryDiagnosticRecord struct {
	result   recoveryDiagnosticAttempt
	body     []byte
	response fidelityResponse
}
type recoveryDiagnosticHarness struct {
	boot                 recoveryDiagnosticBootstrap
	input                *bufio.Reader
	output               *json.Encoder
	gateway              *service.OpenAIGatewayService
	upstream             *recoveryDiagnosticUpstream
	ctx                  context.Context
	accountHash, stopped string
	firstSend            time.Time
	results              int
}

// A compiled binary is inert unless invoked by the reviewed broker. Neither
// ordinary go test nor preflight is authorized to issue a POST.
func TestReasoningRecoveryDiagnosticLive(t *testing.T) {
	if os.Getenv("SUB2API_REASONING_RECOVERY_LIVE") != "1" {
		t.Skip("explicit controlled runner opt-in required")
	}
	out := os.Stdout
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("diagnostic_output_initialization_failed")
	}
	os.Stdout, os.Stderr = null, null
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
	if err = recoveryDiagnosticRun(bufio.NewReaderSize(os.Stdin, 64<<10), encoder); err != nil {
		_ = encoder.Encode(map[string]any{"type": "summary", "status": "harness_failed", "error_class": err.Error()})
		t.Fail()
	}
}
func recoveryDiagnosticValidateBootstrap(boot recoveryDiagnosticBootstrap) error {
	if boot.SchemaVersion != 1 || boot.RunID != recoveryDiagnosticRunID || boot.ConfigMode != "production_env" ||
		len(boot.SourceSHA) != 40 || strings.Trim(boot.SourceSHA, "0123456789abcdef") != "" ||
		boot.Source.Account.ID != 15522 || boot.Source.UserID != 920000015522 {
		return errors.New("invalid_bootstrap_contract")
	}
	legacy := fidelityBootstrap{SchemaVersion: 1, Phase: "after", RunID: boot.RunID, ConfigMode: boot.ConfigMode, Source: boot.Source}
	return fidelityValidateBootstrap(legacy)
}
func recoveryDiagnosticRun(input *bufio.Reader, output *json.Encoder) error {
	var boot recoveryDiagnosticBootstrap
	if err := fidelityReadJSON(input, &boot); err != nil {
		return errors.New("invalid_bootstrap")
	}
	if err := recoveryDiagnosticValidateBootstrap(boot); err != nil {
		return err
	}
	if expected := os.Getenv("REASONING_RECOVERY_SOURCE_SHA"); expected != "" && expected != boot.SourceSHA {
		return errors.New("invalid_bootstrap_contract")
	}
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return errors.New("production_config_load_failed")
	}
	h := &recoveryDiagnosticHarness{boot: boot, input: input, output: output, ctx: context.Background()}
	h.accountHash = fidelityHashJSON(&h.boot.Source.Account)
	u := &recoveryDiagnosticUpstream{h: h, inner: repository.NewHTTPUpstream(cfg), used: make(map[int]bool)}
	h.upstream = u
	h.gateway, err = service.ReasoningFidelityGatewayForTest(cfg, u, boot.Source.SystemPrompts, boot.Source.Settings)
	if err != nil {
		return errors.New("unsupported_source_policy")
	}
	service.ReasoningRecoveryDiagnosticAttach(h.gateway)
	probe := fidelityRequestBody("gpt-5.6-sol", "xhigh", fidelitySinglePrompt, false)
	probe, err = fidelityPrepareIngressBody(probe, boot.Source)
	if err != nil {
		return errors.New("source_group_policy_rejected")
	}
	c, _ := h.newContext(h.ctx, probe, recoveryDiagnosticCase{Model: "gpt-5.6-sol", Effort: "xhigh"})
	req, err := service.ReasoningFidelityDirectRequestForTest(c.Request.Context(), h.gateway, c, &h.boot.Source.Account, probe)
	if err != nil {
		return errors.New("source_endpoint_validation_failed")
	}
	if req.URL == nil || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.RawQuery != "" {
		return errors.New("source_endpoint_not_https_responses")
	}
	if !strings.HasSuffix(req.URL.Path, "/responses") {
		return errors.New("source_endpoint_not_responses")
	}
	u.endpoint = req.URL.String()
	u.authorizationHash = fidelityHash([]byte(req.Header.Get("Authorization")))
	_ = req.Body.Close()
	if h.boot.Source.Account.Proxy != nil {
		u.proxy = h.boot.Source.Account.Proxy.URL()
	}
	_ = output.Encode(map[string]any{"type": "ready", "source_fingerprint": boot.Source.Fingerprint, "endpoint_sha256": fidelityHash([]byte(u.endpoint)), "attempts": 0})
	if os.Getenv("SUB2API_REASONING_RECOVERY_VALIDATE_ONLY") == "1" {
		return output.Encode(map[string]any{"type": "summary", "status": "validated", "attempts": 0})
	}
	h.run()
	status := "completed"
	if h.stopped != "" {
		status = h.stopped
	}
	return output.Encode(map[string]any{"type": "summary", "status": status, "attempts": u.attempts, "results_count": h.results, "blocked_retries": u.blocked})
}
func (h *recoveryDiagnosticHarness) newContext(ctx context.Context, body []byte, fixture recoveryDiagnosticCase) (*gin.Context, *httptest.ResponseRecorder) {
	record := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(record)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("User-Agent", "sub2api-reasoning-recovery-diagnostic/1")
	c.Request.Header.Set("X-Client-Request-Id", fidelityNewID())
	service.ReasoningFidelityContextForTest(ctx, c, &h.boot.Source.Group, h.boot.Source.FastPolicy, h.boot.Source.UserID, fixture.Effort)
	service.ReasoningRecoveryDiagnosticTenant(c, &h.boot.Source.Group)
	return c, record
}
func (h *recoveryDiagnosticHarness) run() {
	for index, model := range []string{"gpt-5.6-sol", "gpt-6-astra"} {
		base := index * 8
		effort := []string{"xhigh", "max"}[index]
		fixture := recoveryDiagnosticCase{ID: base + 1, Model: model, Effort: effort, Fixture: "native_single"}
		single := h.runCase(fixture, fidelityRequestBody(model, effort, fidelitySinglePrompt, false), "", nil)
		recoveryCase := recoveryDiagnosticCase{ID: base + 6, Model: model, Effort: effort, Fixture: "invalid_cipher"}
		invalid, oldCipher, ok := recoveryDiagnosticInvalidBody(single)
		if !ok {
			h.skip(recoveryCase, "not_covered")
			h.skip(recoveryDiagnosticCase{ID: base + 8, Model: model, Effort: effort, Fixture: "negative_cache"}, "not_covered")
			continue
		}
		recovered := h.runCase(recoveryCase, invalid, oldCipher, nil)
		negativeCase := recoveryCase
		negativeCase.ID = base + 8
		negativeCase.Fixture = "negative_cache"
		next, newCiphers, ok := recoveryDiagnosticNegativeBody(invalid, recovered)
		if !ok {
			h.skip(negativeCase, "not_covered")
			continue
		}
		h.runCase(negativeCase, next, oldCipher, newCiphers)
	}
}
func (h *recoveryDiagnosticHarness) skip(f recoveryDiagnosticCase, status string) recoveryDiagnosticExchange {
	r := recoveryDiagnosticResult{Type: "case_result", CaseID: f.ID, Model: f.Model, Effort: f.Effort, Fixture: f.Fixture, Status: status, Attempts: h.upstream.attempts, FixtureSHA: recoveryDiagnosticFixtureHash()}
	h.results++
	_ = h.output.Encode(r)
	return recoveryDiagnosticExchange{result: r}
}
func recoveryDiagnosticFixtureHash() string {
	return fidelityHash([]byte(fidelitySinglePrompt))
}
func (h *recoveryDiagnosticHarness) runCase(f recoveryDiagnosticCase, body []byte, oldCipher string, newCiphers []string) recoveryDiagnosticExchange {
	if h.stopped != "" {
		return h.skip(f, "batch_stopped")
	}
	if !h.firstSend.IsZero() && time.Since(h.firstSend) >= fidelityMaxDuration {
		h.stopped = "batch_time_limit"
		return h.skip(f, "batch_stopped")
	}
	original := append([]byte(nil), body...)
	body, err := fidelityPrepareIngressBody(body, h.boot.Source)
	if err != nil {
		return h.skip(f, "unsupported_source_policy")
	}
	ctx, cancel := context.WithTimeout(h.ctx, fidelityRequestTimeout)
	defer cancel()
	c, writer := h.newContext(ctx, body, f)
	u := h.upstream
	u.fixture = f
	u.activeContext = c.Request.Context()
	u.records = nil
	u.caseSent = 0
	u.lastError = ""
	attemptsBefore, blockedBefore := u.attempts, u.blocked
	started := time.Now()
	_, err = h.gateway.Forward(c.Request.Context(), c, &h.boot.Source.Account, body)
	e := recoveryDiagnosticExchange{request: original, attempts: append([]recoveryDiagnosticRecord(nil), u.records...)}
	e.raw = fidelityParseResponse(writer.Body.Bytes(), writer.Header().Get("Content-Type"), writer.Code)
	text := e.raw.Text
	if len(u.records) > 0 {
		e.sent = append([]byte(nil), u.records[len(u.records)-1].body...)
	}
	status := e.raw.Status
	if status == "" {
		status = "no_response"
	}
	if err != nil && status == "completed" {
		status = "request_failed"
	}
	if u.blocked > blockedBefore {
		status = "retry_blocked"
	}
	r := recoveryDiagnosticResult{Type: "case_result", CaseID: f.ID, Model: f.Model, Effort: f.Effort, Fixture: f.Fixture, Status: status, Attempted: u.attempts > attemptsBefore, AttemptsUsed: u.attempts - attemptsBefore, Attempts: u.attempts, BlockedRetries: u.blocked - blockedBefore, DurationMS: time.Since(started).Milliseconds(), FixtureSHA: recoveryDiagnosticFixtureHash(), EncryptedReasoningComplete: e.raw.ReasoningComplete}
	if text != "" {
		r.AnswerSHA = fidelityHash([]byte(text))
	}
	if status == "completed" {
		correct := fidelityScore("single", text)
		r.Correct = &correct
	}
	var sent map[string]json.RawMessage
	_ = json.Unmarshal(e.sent, &sent)
	var reasoning struct {
		Effort string `json:"effort"`
	}
	_ = json.Unmarshal(sent["reasoning"], &reasoning)
	r.ReasoningConfigPreserved = reasoning.Effort == f.Effort
	var sentModel string
	_ = json.Unmarshal(sent["model"], &sentModel)
	r.ResponseModelMatchesSent = e.raw.Model != "" && e.raw.Model == sentModel
	r.RecoveryObserved = f.Fixture == "invalid_cipher" && len(u.records) == 2 && recoveryDiagnosticSignatureCode(u.records[0].result.ErrorCode)
	r.NegativeCacheObserved = f.Fixture == "negative_cache" && len(u.records) == 1 && writer.Header().Get("X-Sub2API-Reasoning-Recovery") == "rejected_history_skipped"
	if oldCipher != "" && len(e.sent) > 0 {
		removed := !recoveryDiagnosticHasCipher(sent["input"], oldCipher)
		r.OldCipherRemoved = &removed
	}
	if len(newCiphers) > 0 {
		preserved := true
		for _, cipher := range newCiphers {
			preserved = preserved && recoveryDiagnosticHasCipher(sent["input"], cipher)
		}
		r.NewCipherPreserved = &preserved
	}
	e.result = r
	h.results++
	_ = h.output.Encode(r)
	return e
}

func recoveryDiagnosticHasCipher(input json.RawMessage, cipher string) bool {
	var items []map[string]json.RawMessage
	_ = json.Unmarshal(input, &items)
	for _, item := range items {
		var actual string
		_ = json.Unmarshal(item["encrypted_content"], &actual)
		if actual == cipher {
			return true
		}
	}
	return false
}
func recoveryDiagnosticInvalidBody(e recoveryDiagnosticExchange) ([]byte, string, bool) {
	if e.result.Status != "completed" || !e.raw.ReasoningComplete || e.raw.HasTool || e.raw.HasRefusal {
		return nil, "", false
	}
	var request map[string]json.RawMessage
	_ = json.Unmarshal(e.request, &request)
	for _, key := range []string{"previous_response_id", "conversation", "context_management"} {
		if _, ok := request[key]; ok {
			return nil, "", false
		}
	}
	var input []json.RawMessage
	if json.Unmarshal(request["input"], &input) != nil {
		return nil, "", false
	}
	items := make([]json.RawMessage, len(e.raw.Output))
	count := 0
	bad := ""
	seenIDs := make(map[string]bool)
	for i, raw := range e.raw.Output {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			return nil, "", false
		}
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		var id string
		_ = json.Unmarshal(item["id"], &id)
		if id != "" {
			if seenIDs[id] {
				return nil, "", false
			}
			seenIDs[id] = true
		}
		if kind == "compaction" {
			return nil, "", false
		}
		if kind == "reasoning" {
			var cipher string
			_ = json.Unmarshal(item["encrypted_content"], &cipher)
			if len(cipher) < 16 {
				return nil, "", false
			}
			if bad == "" {
				// Alter exactly one ciphertext field; other completed reasoning
				// items remain part of the genuine inline history being tested.
				changed := []byte(cipher)
				at := len(changed) / 2
				if changed[at] == 'A' {
					changed[at] = 'B'
				} else {
					changed[at] = 'A'
				}
				bad = string(changed)
				item["encrypted_content"], _ = json.Marshal(bad)
			}
			count++
		}
		items[i], _ = json.Marshal(item)
	}
	if count == 0 {
		return nil, "", false
	}
	input = append(input, items...)
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Check the same arrangement task again and return only the requested JSON."}}})
	input = append(input, msg)
	request["input"], _ = json.Marshal(input)
	body, err := json.Marshal(request)
	return body, bad, err == nil
}
func recoveryDiagnosticNegativeBody(invalid []byte, recovered recoveryDiagnosticExchange) ([]byte, []string, bool) {
	if !recovered.result.RecoveryObserved || recovered.result.Status != "completed" || recovered.raw.HasTool || recovered.raw.HasRefusal {
		return nil, nil, false
	}
	var body map[string]json.RawMessage
	var input []json.RawMessage
	if json.Unmarshal(invalid, &body) != nil || json.Unmarshal(body["input"], &input) != nil {
		return nil, nil, false
	}
	ciphers := []string{}
	for _, raw := range recovered.raw.Output {
		var item map[string]json.RawMessage
		_ = json.Unmarshal(raw, &item)
		var kind, cipher string
		_ = json.Unmarshal(item["type"], &kind)
		_ = json.Unmarshal(item["encrypted_content"], &cipher)
		if kind == "compaction" {
			return nil, nil, false
		}
		if kind == "reasoning" && cipher != "" {
			ciphers = append(ciphers, cipher)
		}
	}
	if len(ciphers) == 0 {
		return nil, nil, false
	}
	input = append(input, recovered.raw.Output...)
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Verify the arrangement total and lexicographic endpoints once more. Return only the same JSON fields."}}})
	input = append(input, msg)
	body["input"], _ = json.Marshal(input)
	raw, err := json.Marshal(body)
	return raw, ciphers, err == nil
}

type recoveryDiagnosticUpstream struct {
	h                                             *recoveryDiagnosticHarness
	inner                                         service.HTTPUpstream
	fixture                                       recoveryDiagnosticCase
	activeContext                                 context.Context
	endpoint, authorizationHash, proxy, lastError string
	attempts, blocked, caseSent                   int
	used                                          map[int]bool
	records                                       []recoveryDiagnosticRecord
}

func (u *recoveryDiagnosticUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return u.send(req, proxy, id, concurrency, nil)
}
func (u *recoveryDiagnosticUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.send(req, proxy, id, concurrency, profile)
}
func recoveryDiagnosticSignatureCode(code string) bool {
	return code == "thinking_signature_invalid" || code == "invalid_encrypted_content"
}
func (u *recoveryDiagnosticUpstream) send(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	fail := func(code string) (*http.Response, error) { u.lastError = code; return nil, errors.New(code) }
	slot := u.fixture.ID + u.caseSent
	max := 1
	if u.fixture.ID == 6 || u.fixture.ID == 14 {
		max = 2
	}
	if u.caseSent >= max || u.used[slot] || u.caseSent == 1 && (len(u.records) != 1 || !recoveryDiagnosticSignatureCode(u.records[0].result.ErrorCode)) {
		u.blocked++
		return fail("slot_retry_blocked")
	}
	if u.attempts >= recoveryDiagnosticMaxAttempts {
		return fail("attempt_budget_exhausted")
	}
	if u.h.stopped != "" {
		return fail("source_changed")
	}
	if req == nil || req.URL == nil || req.Method != http.MethodPost || req.URL.String() != u.endpoint ||
		id != 15522 || concurrency != u.h.boot.Source.Account.Concurrency || proxy != u.proxy ||
		fidelityHash([]byte(req.Header.Get("Authorization"))) != u.authorizationHash ||
		fidelityHashJSON(&u.h.boot.Source.Account) != u.h.accountHash {
		u.h.stopped = "source_verification_stopped"
		return fail("source_changed")
	}
	ctx := u.activeContext
	if ctx == nil {
		ctx = u.h.ctx
	}
	if req.Context().Err() != nil || ctx.Err() != nil {
		return fail("request_timeout")
	}
	if !u.h.firstSend.IsZero() && time.Since(u.h.firstSend) >= fidelityMaxDuration {
		u.h.stopped = "batch_time_limit"
		return fail("attempt_budget_exhausted")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, fidelityMaxBody+1))
	if err != nil || len(body) > fidelityMaxBody {
		return fail("request_body_unavailable")
	}
	_ = req.Body.Close()
	if err = u.h.output.Encode(map[string]any{"type": "before_send", "case_id": u.fixture.ID, "slot": slot, "expected_fingerprint": u.h.boot.Source.Fingerprint}); err != nil {
		u.h.stopped = "source_verification_stopped"
		return fail("broker_unavailable")
	}
	var grant struct {
		fidelityGrant
		CaseID int `json:"case_id"`
	}
	ch := make(chan error, 1)
	go func() { ch <- fidelityReadJSON(u.h.input, &grant) }()
	select {
	case err = <-ch:
	case <-ctx.Done():
		u.h.stopped = "source_verification_stopped"
		return fail("broker_timeout")
	}
	if err != nil {
		u.h.stopped = "source_verification_stopped"
		return fail("broker_unavailable")
	}
	if grant.Type != "send_granted" || grant.CaseID != u.fixture.ID || grant.Slot != slot ||
		grant.Fingerprint != u.h.boot.Source.Fingerprint || grant.Attempt != u.attempts+1 || grant.Attempt > 16 ||
		grant.ElapsedMS < 0 || grant.ElapsedMS >= fidelityMaxDuration.Milliseconds() {
		u.h.stopped = "source_verification_stopped"
		return fail("invalid_broker_grant")
	}
	if u.h.firstSend.IsZero() {
		u.h.firstSend = time.Now()
	}
	u.used[slot] = true
	u.attempts = grant.Attempt
	u.caseSent++
	started := time.Now()
	sendCtx := req.Context()
	cancelDeadline := func() {}
	if deadline, ok := ctx.Deadline(); ok {
		sendCtx, cancelDeadline = context.WithDeadline(sendCtx, deadline)
	}
	sendCtx, cancelSend := context.WithCancel(sendCtx)
	stop := context.AfterFunc(ctx, cancelSend)
	cleanup := func() { stop(); cancelSend(); cancelDeadline() }
	req = req.Clone(service.WithHTTPUpstreamRedirectsDisabled(sendCtx))
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = nil
	var resp *http.Response
	if profile == nil {
		resp, err = u.inner.Do(req, proxy, id, concurrency)
	} else {
		resp, err = u.inner.DoWithTLS(req, proxy, id, concurrency, profile)
	}
	if err != nil {
		cleanup()
		u.record(slot, body, nil, "", 0, started, "transport_error")
		return fail("transport_error")
	}
	if resp == nil || resp.Body == nil {
		cleanup()
		u.record(slot, body, nil, "", 0, started, "empty_upstream_response")
		return fail("empty_upstream_response")
	}
	code, contentType := resp.StatusCode, resp.Header.Get("Content-Type")
	resp.Body = &recoveryDiagnosticCapture{ReadCloser: resp.Body, cleanup: cleanup, finish: func(data []byte) { u.record(slot, body, data, contentType, code, started, "") }}
	return resp, nil
}
func (u *recoveryDiagnosticUpstream) record(slot int, body, data []byte, contentType string, status int, started time.Time, class string) {
	parsed := fidelityParseResponse(data, contentType, status)
	r := recoveryDiagnosticAttempt{Type: "attempt_result", CaseID: u.fixture.ID, Slot: slot, Model: u.fixture.Model, Effort: u.fixture.Effort, Fixture: u.fixture.Fixture, Status: parsed.Status, ErrorClass: parsed.ErrorClass, ErrorCode: recoveryDiagnosticErrorCode(data, contentType), Attempts: u.attempts, DurationMS: time.Since(started).Milliseconds(), HTTPStatus: status, Usage: parsed.Usage, SentBodySHA: fidelityHash(body)}
	if class != "" {
		r.Status = "request_failed"
		r.ErrorClass = class
	}
	var request map[string]json.RawMessage
	_ = json.Unmarshal(body, &request)
	r.SentInputSHA = fidelityHash(request["input"])
	r.SentModelSHA = fidelityHash(request["model"])
	r.SentReasoningSHA = fidelityHash(request["reasoning"])
	r.EncryptedInputSent, _ = fidelityInputCounts(request["input"])
	if parsed.ID != "" {
		r.ResponseIDSHA = fidelityHash([]byte(parsed.ID))
	}
	if len(parsed.Output) > 0 {
		r.ResponseOutputSHA = fidelityHashJSON(parsed.Output)
	}
	u.records = append(u.records, recoveryDiagnosticRecord{result: r, body: append([]byte(nil), body...), response: parsed})
	if status == 401 || status == 403 || parsed.ErrorClass == "auth" {
		u.h.stopped = "authentication_stopped"
	}
	if status == 402 || status == 429 || parsed.ErrorClass == "quota" || parsed.ErrorClass == "rate_limit" {
		u.h.stopped = "quota_or_rate_limit_stopped"
	}
	_ = u.h.output.Encode(r)
}
func recoveryDiagnosticErrorCode(body []byte, contentType string) string {
	physical := bytes.ReplaceAll(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
	framed := bytes.HasPrefix(physical, []byte("data:")) || bytes.HasPrefix(physical, []byte("event:")) || bytes.Contains(physical, []byte("\ndata:")) || bytes.Contains(physical, []byte("\nevent:"))
	if !strings.Contains(contentType, "text/event-stream") && !framed {
		return service.ReasoningRecoveryDiagnosticRejectionCode(body)
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), fidelityMaxBody)
	scanner.Split(fidelitySSELine)
	var data []byte
	eventName := ""
	found := ""
	apply := func() {
		if len(data) == 0 {
			return
		}
		payload := bytes.TrimSuffix(data, []byte{'\n'})
		object, err := fidelityJSONObject(payload)
		if err != nil {
			found = ""
		} else {
			kind := eventName
			if raw := object["type"]; len(raw) > 0 {
				_ = json.Unmarshal(raw, &kind)
			}
			switch kind {
			case "response.completed", "response.incomplete", "response.cancelled", "response.canceled":
				found = ""
			case "response.failed", "response.done", "error":
				found = service.ReasoningRecoveryDiagnosticRejectionCode(payload)
			}
		}
		data = nil
		eventName = ""
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			apply()
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			value := line[5:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			data = append(data, value...)
			data = append(data, '\n')
		} else if bytes.HasPrefix(line, []byte("event:")) {
			eventName = strings.TrimSpace(string(line[6:]))
		}
	}
	apply()
	if scanner.Err() != nil {
		return ""
	}
	return found
}

type recoveryDiagnosticCapture struct {
	io.ReadCloser
	mu      sync.Mutex
	raw     []byte
	once    sync.Once
	cleanup func()
	finish  func([]byte)
}

func (c *recoveryDiagnosticCapture) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > 0 {
		if len(c.raw)+n > fidelityMaxBody {
			return 0, errors.New("response_body_limit")
		}
		c.raw = append(c.raw, p[:n]...)
	}
	return n, err
}
func (c *recoveryDiagnosticCapture) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(func() { c.cleanup(); c.mu.Lock(); raw := append([]byte(nil), c.raw...); c.mu.Unlock(); c.finish(raw) })
	return err
}
