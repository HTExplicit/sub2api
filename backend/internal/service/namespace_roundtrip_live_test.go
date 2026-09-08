//go:build reasoning_fidelity && namespace_roundtrip

package service_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
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
	namespaceRunID          = "responses-namespace-20260908-r2"
	namespacePriorAttempts  = 1
	namespaceMaxAttempts    = 5
	namespaceMaxDuration    = 15 * time.Minute
	namespaceRequestTimeout = 180 * time.Second
	namespaceFirstFunction  = "namespace_probe_first"
	namespaceSecondFunction = "namespace_probe_second"
	namespaceFirstOutput    = `{"value":"ALPHA"}`
	namespaceSecondOutput   = `{"value":"BETA"}`
	namespaceFinalAnswer    = "NAMESPACE_OK"
)

// These are the two approved test profiles, not a production model policy.
// Ultra is the Desktop scenario label (max plus client orchestration), not an
// API reasoning.effort value. This fixed local-function chain exercises only
// max-effort Responses forwarding; it does not reproduce Desktop sub-agents.
type namespaceProfile struct {
	Model       string
	EffortLabel string
	WireEffort  string
}

func namespaceProfileFor(model, effortLabel string) (namespaceProfile, bool) {
	if (model == "gpt-6-astra" && effortLabel == "ultra") || (model == "gpt-5.6-luna" && effortLabel == "max") {
		return namespaceProfile{Model: model, EffortLabel: effortLabel, WireEffort: "max"}, true
	}
	return namespaceProfile{}, false
}

func (p namespaceProfile) matchesWireEffort(body []byte) bool {
	var fields map[string]json.RawMessage
	var reasoning map[string]json.RawMessage
	var effort string
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["reasoning"], &reasoning) != nil ||
		json.Unmarshal(reasoning["effort"], &effort) != nil || effort != p.WireEffort {
		return false
	}
	_, hasMode := reasoning["mode"]
	return !hasMode
}

type namespaceBootstrap struct {
	SchemaVersion   int            `json:"schema_version"`
	ConfigMode      string         `json:"config_mode"`
	Mode            string         `json:"mode"`
	RunID           string         `json:"run_id"`
	SourceSHA       string         `json:"source_sha"`
	ManifestSHA     string         `json:"manifest_sha256"`
	PriorAttempts   int            `json:"prior_attempts"`
	ParentLedgerSHA string         `json:"parent_ledger_sha256"`
	Source          fidelitySource `json:"source"`
	Ledger          fidelityLedger `json:"ledger"`
}

type namespaceGrant struct {
	Type        string `json:"type"`
	Scenario    string `json:"scenario"`
	Turn        int    `json:"turn"`
	Slot        int    `json:"slot"`
	Fingerprint string `json:"fingerprint"`
	Attempt     int    `json:"attempt"`
	ElapsedMS   int64  `json:"elapsed_ms"`
}

type namespaceResult struct {
	Type                                string          `json:"type"`
	Scenario                            string          `json:"scenario"`
	Turn                                int             `json:"turn"`
	Slot                                int             `json:"slot"`
	Model                               string          `json:"model"`
	Effort                              string          `json:"effort"`
	WireEffort                          string          `json:"wire_effort"`
	Status                              string          `json:"status"`
	ErrorClass                          string          `json:"error_class,omitempty"`
	ErrorDetail                         json.RawMessage `json:"error_detail,omitempty"`
	Completed                           bool            `json:"completed"`
	HTTPStatus                          int             `json:"http_status"`
	Attempted                           bool            `json:"attempted"`
	Attempts                            int             `json:"attempts"`
	BlockedRetries                      int             `json:"blocked_retries"`
	DurationMS                          int64           `json:"duration_ms"`
	NamespaceFields                     int             `json:"namespace_fields"`
	NamespacePresent                    bool            `json:"namespace_present"`
	NamespaceReplayedFields             int             `json:"namespace_replayed_fields"`
	NamespaceReplayedWithoutDeclaration bool            `json:"namespace_replayed_without_declaration"`
	HistoryPreserved                    bool            `json:"history_preserved"`
	RawOutputPreserved                  bool            `json:"raw_output_preserved"`
	ModelMatches                        bool            `json:"model_matches"`
	EffortMatches                       bool            `json:"effort_matches"`
	PromptApplied                       bool            `json:"prompt_applied"`
	CacheKeyLength                      int             `json:"cache_key_length"`
	CacheKeyStable                      bool            `json:"cache_key_stable"`
	TerminalComplete                    bool            `json:"terminal_complete"`
	ToolContractValid                   bool            `json:"tool_contract_valid"`
	SentBodySHA                         string          `json:"sent_body_sha256,omitempty"`
	ResponseOutputSHA                   string          `json:"response_output_sha256,omitempty"`
	CacheKeySHA                         string          `json:"cache_key_sha256,omitempty"`
	RequestIDSHA                        string          `json:"request_id_sha256,omitempty"`
	ResponseIDSHA                       string          `json:"response_id_sha256,omitempty"`
	PromptSHA                           string          `json:"prompt_sha256,omitempty"`
	Usage                               *fidelityUsage  `json:"usage"`
}

type namespaceHarness struct {
	boot              namespaceBootstrap
	input             *bufio.Reader
	output            *json.Encoder
	ctx               context.Context
	started           time.Time
	gateway           *service.OpenAIGatewayService
	upstream          *namespaceBudgetUpstream
	sourceSnapshotSHA string
	cacheKeys         map[string]string
	promptSHAs        map[string]string
	results           int
	stopped           string
	coverage          bool
	astraCompleted    bool
	lunaCompleted     bool
}

// Both build tags, the explicit environment gate, and one exact test selection
// are required before source material is read. The broker owns the manifest and
// durable attempt ledger; this test cannot grant itself permission to send.
func TestNamespaceRoundtripLive(t *testing.T) {
	if os.Getenv("SUB2API_NAMESPACE_ROUNDTRIP_LIVE") != "1" {
		t.Skip("explicit controlled runner opt-in required")
	}
	if flag.Lookup("test.run") == nil || flag.Lookup("test.run").Value.String() != "^TestNamespaceRoundtripLive$" ||
		os.Getenv("SUB2API_REASONING_FIDELITY_LIVE") != "" || os.Getenv("SUB2API_NAMESPACE_ROUNDTRIP_REVISION") != "2" {
		t.Fatal("invalid_live_test_selection")
	}
	out := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("diagnostic_output_initialization_failed")
	}
	// Suppress production and testing output before handling any live snapshot.
	// Only the explicit, enum/number/hash-only protocol encoder survives.
	os.Stdout, os.Stderr = devnull, devnull
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	_ = logger.Init(logger.InitOptions{Level: "fatal", Output: logger.OutputOptions{ToStdout: true}})
	logger.SetSink(nil)
	log.SetOutput(io.Discard)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	output := json.NewEncoder(out)
	defer func() {
		if recover() != nil {
			_ = output.Encode(map[string]any{"type": "summary", "status": "failed", "error_class": "internal_panic"})
			t.Fail()
		}
	}()
	if err := namespaceRun(bufio.NewReaderSize(os.Stdin, 64<<10), output); err != nil {
		_ = output.Encode(map[string]any{"type": "summary", "status": "failed", "error_class": err.Error()})
		t.Fail()
	}
}

func namespaceRun(input *bufio.Reader, output *json.Encoder) error {
	var boot namespaceBootstrap
	if err := fidelityReadJSON(input, &boot); err != nil {
		return errors.New("invalid_bootstrap")
	}
	if err := namespaceValidateBootstrap(boot, os.Getenv("NAMESPACE_ROUNDTRIP_SOURCE_SHA")); err != nil {
		return err
	}
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return errors.New("production_config_load_failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), namespaceMaxDuration)
	defer cancel()
	h := &namespaceHarness{boot: boot, input: input, output: output, ctx: ctx, started: time.Now(),
		cacheKeys: make(map[string]string), promptSHAs: make(map[string]string)}
	h.sourceSnapshotSHA = fidelityHashJSON(&h.boot.Source)
	u := &namespaceBudgetUpstream{h: h, inner: repository.NewHTTPUpstream(cfg), used: make(map[int]bool)}
	h.upstream = u
	h.gateway, err = service.ReasoningFidelityGatewayForTest(cfg, u, boot.Source.BusinessPrompt, boot.Source.RegistryPublication, boot.Source.Settings)
	if err != nil {
		return errors.New("unsupported_source_policy")
	}
	// Construct, but do not send, using the same auth/endpoint/header policy as
	// Forward. This also compiles the one frozen prompt in the isolated process.
	probeProfile, ok := namespaceProfileFor("gpt-6-astra", "ultra")
	if !ok {
		return errors.New("wire_contract_mismatch")
	}
	probe := namespaceInitialBody(probeProfile.Model, probeProfile.EffortLabel, "endpoint-probe", false)
	probe, err = fidelityPrepareIngressBody(probe, boot.Source)
	if err != nil || !probeProfile.matchesWireEffort(probe) {
		return errors.New("source_group_policy_rejected")
	}
	probeContext, _ := h.newContext(ctx, probe, probeProfile.WireEffort)
	request, err := service.ReasoningFidelityDirectRequestForTest(probeContext.Request.Context(), h.gateway, probeContext, &h.boot.Source.Account, probe)
	if err != nil || request == nil || request.URL == nil {
		return errors.New("source_endpoint_validation_failed")
	}
	defer request.Body.Close()
	if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.RawQuery != "" || !strings.HasSuffix(request.URL.Path, "/responses") {
		return errors.New("source_endpoint_not_https_responses")
	}
	u.endpoint = request.URL.String()
	u.authorizationSHA = fidelityHash([]byte(request.Header.Get("Authorization")))
	if h.boot.Source.Account.Proxy != nil {
		u.proxyURL = h.boot.Source.Account.Proxy.URL()
	}
	if err := output.Encode(map[string]any{"type": "ready", "run_id": boot.RunID, "source_sha": boot.SourceSHA,
		"source_sha256": boot.Source.Fingerprint, "endpoint_sha256": fidelityHash([]byte(u.endpoint)), "attempts": 0}); err != nil {
		return errors.New("broker_unavailable")
	}
	if os.Getenv("SUB2API_NAMESPACE_ROUNDTRIP_VALIDATE_ONLY") == "1" {
		return output.Encode(map[string]any{"type": "summary", "status": "validated", "attempts": 0, "total_attempts": boot.PriorAttempts})
	}
	h.runSequences()
	if err := output.Encode(h.summary()); err != nil {
		return errors.New("broker_unavailable")
	}
	return nil
}

func (h *namespaceHarness) summary() map[string]any {
	status := "completed"
	if h.stopped != "" || !h.coverage || !h.astraCompleted || !h.lunaCompleted {
		status = "failed"
	}
	summary := map[string]any{"type": "summary", "status": status,
		"attempts": h.upstream.attempts, "total_attempts": h.boot.PriorAttempts + h.upstream.attempts, "blocked_retries": h.upstream.totalBlocked, "namespace_coverage": h.coverage,
		"astra_completed": h.astraCompleted, "luna_completed": h.lunaCompleted, "results": h.results,
		"source_sha256": h.boot.Source.Fingerprint, "duration_ms": time.Since(h.started).Milliseconds()}
	if h.stopped != "" {
		summary["error_class"] = h.stopped
	}
	return summary
}

func namespaceValidateBootstrap(b namespaceBootstrap, expectedSource string) error {
	if b.SchemaVersion != 1 || b.Mode != "namespace_roundtrip_r2" || b.ConfigMode != "production_env" || b.RunID != namespaceRunID ||
		!regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(b.SourceSHA) || b.SourceSHA != expectedSource ||
		!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(b.ManifestSHA) || b.PriorAttempts != namespacePriorAttempts ||
		!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(b.ParentLedgerSHA) {
		return errors.New("invalid_bootstrap_contract")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(b.Source.Fingerprint) {
		return errors.New("invalid_source_fingerprint")
	}
	a := &b.Source.Account
	if a.ID != 16050 || a.Name != "白嫖-dmxapi" || a.Type != service.AccountTypeAPIKey || a.Platform != service.PlatformOpenAI ||
		a.Credentials == nil || a.GetOpenAIProtocolAPIKey() == "" || a.Status != service.StatusActive || !a.Schedulable || a.Concurrency < 1 {
		return errors.New("invalid_fixed_account")
	}
	if a.ParentAccountID != nil || service.IsCindyRuntimeCompatibleAPIKeyAccount(a.Platform, a.Type, a.Credentials) ||
		!a.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityResponses) {
		return errors.New("unsupported_fixed_account")
	}
	if b.Source.Group.ID < 1 || b.Source.Group.Platform != service.PlatformOpenAI || b.Source.Group.Status != service.StatusActive || b.Source.UserID != 920000016050 {
		return errors.New("invalid_fixed_group")
	}
	member := false
	for _, id := range a.GroupIDs {
		member = member || id == b.Source.Group.ID
	}
	if !member {
		return errors.New("fixed_account_group_mismatch")
	}
	if (a.ProxyID == nil) != (a.Proxy == nil) || (a.Proxy != nil && (*a.ProxyID != a.Proxy.ID || !a.Proxy.IsActive() || a.Proxy.IsExpired(time.Now()))) {
		return errors.New("invalid_fixed_proxy")
	}
	if b.Source.FastPolicy == nil || b.Source.Settings == nil || len(b.Source.ChannelModels) != 2 {
		return errors.New("missing_frozen_policy")
	}
	for model, mapped := range map[string]string{"gpt-6-astra": "gpt-6-astra-ssvip", "gpt-5.6-luna": "gpt-5.6-luna-ssvip"} {
		channelModel := b.Source.ChannelModels[model]
		if channelModel == "" || a.GetMappedModel(channelModel) != mapped {
			return errors.New("fixed_model_mapping_mismatch")
		}
	}
	p := b.Source.BusinessPrompt
	if !p.Enabled || p.ExposeServerPrompt || p.CompactEnabled || p.TemplateID < 1 || p.VersionID < 1 || p.Revision < 1 ||
		strings.TrimSpace(p.Body) == "" || p.SHA256 != fidelityHash([]byte(p.Body)) {
		return errors.New("invalid_frozen_prompt")
	}
	// A consumed run is never resumed: its complete response history exists only
	// in memory, and a restart must not invent a replacement chain.
	if b.Ledger.Attempts != 0 || len(b.Ledger.ConsumedSlots) != 0 || b.Ledger.ElapsedMS != 0 {
		return errors.New("resume_not_allowed")
	}
	return nil
}

func (h *namespaceHarness) newContext(ctx context.Context, body []byte, wireEffort string) (*gin.Context, *httptest.ResponseRecorder) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("User-Agent", "sub2api-namespace-roundtrip-diagnostic/1")
	c.Request.Header.Set("X-Client-Request-Id", fidelityNewID())
	service.ReasoningFidelityContextForTest(ctx, c, &h.boot.Source.Group, h.boot.Source.FastPolicy, h.boot.Source.UserID, wireEffort)
	return c, r
}

func (h *namespaceHarness) runSequences() {
	scenario := "astra_flat"
	body := namespaceInitialBody("gpt-6-astra", "ultra", fidelityNewID(), false)
	first, result := h.runTurn(scenario, 1, "gpt-6-astra", "ultra", body, namespaceFirstFunction)
	if !result.Completed {
		return
	}
	if result.NamespaceFields == 0 {
		// The prior failed run already consumed one of the six authorized POSTs.
		// The remaining five can cover only this chain and Luna; no replacement
		// namespace declaration or restart is authorized when coverage is absent.
		h.stopped = "namespace_sample_unavailable"
		return
	}
	secondBody, err := namespaceContinuation(body, first, namespaceFirstFunction, namespaceSecondFunction)
	if err != nil {
		h.stopped = "tool_history_invalid"
		return
	}
	second, result := h.runTurn(scenario, 2, "gpt-6-astra", "ultra", secondBody, namespaceSecondFunction)
	if !result.Completed {
		return
	}
	finalBody, err := namespaceContinuation(secondBody, second, namespaceSecondFunction, "")
	if err != nil {
		h.stopped = "tool_history_invalid"
		return
	}
	_, result = h.runTurn(scenario, 3, "gpt-6-astra", "ultra", finalBody, "")
	if !result.Completed {
		return
	}
	h.astraCompleted = true
	body = namespaceInitialBody("gpt-5.6-luna", "max", fidelityNewID(), false)
	first, result = h.runTurn("luna_flat", 1, "gpt-5.6-luna", "max", body, namespaceFirstFunction)
	if !result.Completed {
		return
	}
	finalBody, err = namespaceContinuation(body, first, namespaceFirstFunction, "")
	if err != nil {
		h.stopped = "tool_history_invalid"
		return
	}
	_, result = h.runTurn("luna_flat", 2, "gpt-5.6-luna", "max", finalBody, "")
	h.lunaCompleted = result.Completed
}

func namespaceInitialBody(model, effortLabel, key string, explicitNamespace bool) []byte {
	profile, ok := namespaceProfileFor(model, effortLabel)
	if !ok {
		return nil
	}
	v := map[string]any{"model": profile.Model, "reasoning": map[string]any{"effort": profile.WireEffort}, "stream": true, "store": false,
		"max_output_tokens": 4096, "include": []string{"reasoning.encrypted_content"}, "prompt_cache_key": key,
		"instructions": "This isolated protocol test has only two read-only constant tools. Follow the next requested tool call, then the final-answer instruction. Do not use any other tool.",
		"input":        []any{map[string]any{"role": "user", "content": "Call namespace_probe_first exactly once with an empty JSON object. Do not answer yet."}}}
	namespaceSetTool(v, namespaceFirstFunction, explicitNamespace)
	body, _ := json.Marshal(v)
	return body
}

func namespaceSetTool(v map[string]any, function string, explicitNamespace bool) {
	if function == "" {
		delete(v, "tools")
		v["tool_choice"] = "none"
		return
	}
	tool := map[string]any{"type": "function", "name": function, "description": "Return one fixed constant; no file, shell, network or business operation occurs.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}, "strict": true}
	choice := map[string]any{"type": "function", "name": function}
	if explicitNamespace {
		v["tools"] = []any{map[string]any{"type": "namespace", "name": "namespace_probe", "tools": []any{tool}}}
		choice["namespace"] = "namespace_probe"
	} else {
		v["tools"] = []any{tool}
	}
	v["tool_choice"] = choice
}

// The model's output items are carried as RawMessage, not through a reduced
// struct. Namespace (including null), identity, encrypted data and unknown
// fields survive unchanged; only our new constant tool output/user turn is added.
func namespaceContinuation(body []byte, response fidelityResponse, called, next string) ([]byte, error) {
	callID, ok := namespaceToolCall(response, called)
	if !ok {
		return nil, errors.New("tool_history_invalid")
	}
	var fields map[string]json.RawMessage
	var input []json.RawMessage
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["input"], &input) != nil {
		return nil, errors.New("tool_history_invalid")
	}
	input = append(input, response.Output...)
	constant := namespaceFirstOutput
	if called == namespaceSecondFunction {
		constant = namespaceSecondOutput
	}
	output, _ := json.Marshal(map[string]any{"type": "function_call_output", "call_id": callID, "output": constant})
	input = append(input, output)
	message := "Return exactly NAMESPACE_OK without quotes, markdown or any other text. Do not call another tool."
	if next != "" {
		message = "Call namespace_probe_second exactly once with an empty JSON object. Do not answer yet."
	}
	user, _ := json.Marshal(map[string]any{"role": "user", "content": message})
	input = append(input, user)
	fields["input"], _ = json.Marshal(input)
	toolFields := make(map[string]any)
	namespaceSetTool(toolFields, next, false)
	delete(fields, "tools")
	for key, value := range toolFields {
		fields[key], _ = json.Marshal(value)
	}
	result, err := json.Marshal(fields)
	if err != nil {
		return nil, errors.New("tool_history_invalid")
	}
	return result, nil
}

func namespaceToolCall(response fidelityResponse, expected string) (string, bool) {
	if !namespaceResponseCompleted(response) || expected == "" {
		return "", false
	}
	callID := ""
	for _, raw := range response.Output {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			return "", false
		}
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		if kind != "function_call" {
			if strings.HasSuffix(kind, "_call") || strings.HasSuffix(kind, "_call_output") || kind == "mcp_approval_request" || kind == "mcp_list_tools" {
				return "", false
			}
			continue
		}
		var name, id, args string
		if json.Unmarshal(item["name"], &name) != nil || name != expected || json.Unmarshal(item["call_id"], &id) != nil || id == "" || callID != "" || json.Unmarshal(item["arguments"], &args) != nil {
			return "", false
		}
		parsed, err := fidelityJSONObject([]byte(args))
		if err != nil || len(parsed) != 0 {
			return "", false
		}
		if namespace, present := item["namespace"]; present && !bytes.Equal(bytes.TrimSpace(namespace), []byte("null")) {
			var value string
			if json.Unmarshal(namespace, &value) != nil {
				return "", false
			}
		}
		callID = id
	}
	return callID, callID != ""
}

func namespaceResponseCompleted(r fidelityResponse) bool {
	return r.Status == "completed" && !r.HasError && !r.HasIncomplete && !r.HasRefusal
}

func namespaceCountFields(items []json.RawMessage) int {
	count := 0
	for _, raw := range items {
		var item map[string]json.RawMessage
		var kind string
		if json.Unmarshal(raw, &item) == nil && json.Unmarshal(item["type"], &kind) == nil && kind == "function_call" {
			if _, present := item["namespace"]; present {
				count++
			}
		}
	}
	return count
}

func (h *namespaceHarness) runTurn(scenario string, turn int, model, effortLabel string, rawBody []byte, expectedTool string) (fidelityResponse, namespaceResult) {
	u := h.upstream
	slot := u.attempts + 1
	profile, ok := namespaceProfileFor(model, effortLabel)
	r := namespaceResult{Type: "attempt_result", Scenario: scenario, Turn: turn, Slot: slot, Model: model, Effort: effortLabel, WireEffort: profile.WireEffort, Status: "failed"}
	if h.stopped != "" || h.ctx.Err() != nil || slot > namespaceMaxAttempts {
		h.stopped = "batch_limit_reached"
		return fidelityResponse{}, r
	}
	if !ok || !profile.matchesWireEffort(rawBody) {
		h.stopped = "wire_contract_mismatch"
		r.ErrorClass = h.stopped
		_ = h.output.Encode(r)
		return fidelityResponse{}, r
	}
	body, err := fidelityPrepareIngressBody(rawBody, h.boot.Source)
	if err != nil || !profile.matchesWireEffort(body) {
		h.stopped = "source_group_policy_rejected"
		r.ErrorClass = h.stopped
		_ = h.output.Encode(r)
		return fidelityResponse{}, r
	}
	ctx, cancel := context.WithTimeout(h.ctx, namespaceRequestTimeout)
	defer cancel()
	c, recorder := h.newContext(ctx, body, profile.WireEffort)
	u.activeSlot, u.scenario, u.turn = slot, scenario, turn
	u.profile = profile
	u.activeContext, u.inputBody = c.Request.Context(), append([]byte(nil), body...)
	u.sentBody, u.rawResponse = nil, nil
	u.responseContentType, u.lastError = "", ""
	u.responseStatus, u.blocked = 0, 0
	start, attemptsBefore := time.Now(), u.attempts
	_, callErr := h.gateway.Forward(c.Request.Context(), c, &h.boot.Source.Account, body)
	response := fidelityParseResponse(recorder.Body.Bytes(), recorder.Header().Get("Content-Type"), recorder.Code)
	upstreamResponse := fidelityParseResponse(u.rawResponse, u.responseContentType, u.responseStatus)
	// attempt_result counts only this logical turn. The broker uses exactly one
	// send here as its success contract; batch totals belong to summary/grants.
	r.Attempts, r.Attempted, r.BlockedRetries, r.HTTPStatus = u.attempts-attemptsBefore, u.attempts > attemptsBefore, u.blocked, u.responseStatus
	r.DurationMS = time.Since(start).Milliseconds()
	r.Usage = upstreamResponse.Usage
	r.NamespaceFields = namespaceCountFields(upstreamResponse.Output)
	r.NamespacePresent = r.NamespaceFields > 0
	r.TerminalComplete = namespaceResponseCompleted(response) && namespaceResponseCompleted(upstreamResponse)
	a, _ := json.Marshal(response.Output)
	b, _ := json.Marshal(upstreamResponse.Output)
	r.RawOutputPreserved = r.TerminalComplete && len(response.Output) > 0 && namespaceJSONEqual(a, b)
	r.ResponseOutputSHA = fidelityHash(b)
	r.RequestIDSHA = fidelityHash([]byte(c.Request.Header.Get("X-Client-Request-Id")))
	if upstreamResponse.ID != "" {
		r.ResponseIDSHA = fidelityHash([]byte(upstreamResponse.ID))
	}
	if len(u.sentBody) > 0 {
		h.observeRequest(&r, body, u.sentBody)
	}
	if expectedTool != "" {
		_, r.ToolContractValid = namespaceToolCall(response, expectedTool)
	} else {
		r.ToolContractValid = r.TerminalComplete && !response.HasTool && strings.TrimSpace(response.Text) == namespaceFinalAnswer
	}
	r.Completed = callErr == nil && r.Attempted && r.Attempts == 1 && r.HTTPStatus >= 200 && r.HTTPStatus < 300 && r.BlockedRetries == 0 &&
		r.TerminalComplete && r.RawOutputPreserved && r.HistoryPreserved && r.ModelMatches && r.EffortMatches && r.PromptApplied && r.CacheKeyLength == 64 && r.CacheKeyStable && r.ToolContractValid
	if r.Completed {
		r.Status = "completed"
		if r.NamespaceReplayedWithoutDeclaration {
			h.coverage = true
		}
	} else {
		r.ErrorClass = namespaceFailureClass(ctx, callErr, r, u.lastError, upstreamResponse.ErrorClass)
		if len(u.rawResponse) > 0 && (u.responseStatus >= 400 || upstreamResponse.HasError) {
			r.ErrorDetail = namespaceSafeErrorDetail(u.rawResponse, u.responseContentType)
		}
		h.stopped = r.ErrorClass
	}
	h.results++
	if err := h.output.Encode(r); err != nil {
		h.stopped = "broker_unavailable"
	}
	return response, r
}

func namespaceFailureClass(ctx context.Context, callErr error, r namespaceResult, localClass, upstreamClass string) string {
	if ctx.Err() != nil {
		return "request_timeout"
	}
	if localClass != "" {
		return localClass
	}
	// The parser maps provider text to a fixed internal category. Never forward
	// that text itself, even for errors tunneled in an HTTP-200 SSE response.
	switch upstreamClass {
	case "auth":
		return "authentication_stopped"
	case "quota", "rate_limit":
		return "quota_or_rate_limit_stopped"
	case "payment_required":
		return "payment_required_stopped"
	}
	switch r.HTTPStatus {
	case 401, 403:
		return "authentication_stopped"
	case 402:
		return "payment_required_stopped"
	case 429:
		return "quota_or_rate_limit_stopped"
	case 400, 404, 422:
		return "upstream_request_rejected"
	}
	if r.BlockedRetries > 0 {
		return "slot_retry_blocked"
	}
	if callErr != nil || r.HTTPStatus < 200 || r.HTTPStatus >= 300 {
		return "forward_error"
	}
	if !r.TerminalComplete {
		return "terminal_not_complete"
	}
	if !r.RawOutputPreserved || !r.HistoryPreserved {
		return "history_not_preserved"
	}
	if !r.ToolContractValid {
		return "tool_contract_invalid"
	}
	return "wire_contract_mismatch"
}

func (h *namespaceHarness) observeRequest(r *namespaceResult, before, sent []byte) {
	var a, b map[string]json.RawMessage
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(sent, &b) != nil {
		return
	}
	var model, key, prompt, clientPrompt string
	var reasoning struct {
		Effort string `json:"effort"`
	}
	_ = json.Unmarshal(b["model"], &model)
	_ = json.Unmarshal(b["reasoning"], &reasoning)
	_ = json.Unmarshal(b["prompt_cache_key"], &key)
	_ = json.Unmarshal(b["instructions"], &prompt)
	_ = json.Unmarshal(a["instructions"], &clientPrompt)
	var input []json.RawMessage
	_ = json.Unmarshal(b["input"], &input)
	r.HistoryPreserved = namespaceJSONEqual(a["input"], b["input"])
	r.NamespaceReplayedFields = namespaceCountFields(input)
	r.NamespaceReplayedWithoutDeclaration = r.NamespaceReplayedFields > 0 && !namespaceHasDeclaration(b["tools"])
	profile, knownProfile := namespaceProfileFor(r.Model, r.Effort)
	r.ModelMatches = model == r.Model+"-ssvip"
	r.EffortMatches = knownProfile && profile.matchesWireEffort(before) && profile.matchesWireEffort(sent) && reasoning.Effort == r.WireEffort
	r.CacheKeyLength = len(key)
	r.CacheKeySHA, r.PromptSHA, r.SentBodySHA = fidelityHash([]byte(key)), fidelityHash([]byte(prompt)), fidelityHash(sent)
	r.PromptApplied = prompt != clientPrompt && prompt == namespaceExpectedInstructions(h.boot.Source, clientPrompt)
	if previous, ok := h.cacheKeys[r.Scenario]; ok {
		r.CacheKeyStable = previous == key && h.promptSHAs[r.Scenario] == r.PromptSHA
	} else {
		r.CacheKeyStable = true
		h.cacheKeys[r.Scenario], h.promptSHAs[r.Scenario] = key, r.PromptSHA
	}
}

func namespaceExpectedInstructions(source fidelitySource, client string) string {
	server := source.BusinessPrompt.Body
	if source.BusinessPrompt.CompositionMode == service.BusinessSystemPromptCompositionCodexSkillHybrid {
		if source.RegistryPublication == nil {
			return ""
		}
		// The real compiler uses the published effective prompt as the complete
		// server component; it does not append the immutable template body.
		server = source.RegistryPublication.EffectiveBody
	}
	return service.MergeBusinessSystemPromptInstructions(client, server)
}

func namespaceHasDeclaration(raw json.RawMessage) bool {
	var tools []map[string]json.RawMessage
	if len(raw) == 0 {
		return false
	}
	if json.Unmarshal(raw, &tools) != nil {
		return true // Fail closed for an unrecognized tools shape.
	}
	for _, tool := range tools {
		var kind string
		_ = json.Unmarshal(tool["type"], &kind)
		if kind == "namespace" {
			return true
		}
		if _, exists := tool["namespace"]; exists {
			return true
		}
	}
	return false
}

// A normal interface{} JSON decode rounds integers larger than 2^53. Unknown
// response fields are part of the replay contract too, so compare without a
// float64 conversion and reject trailing documents instead of losing evidence.
func namespaceJSONEqual(left, right []byte) bool {
	decode := func(raw []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			return nil, errors.New("trailing_json")
		}
		return value, nil
	}
	a, err := decode(left)
	if err != nil {
		return false
	}
	b, err := decode(right)
	return err == nil && reflect.DeepEqual(a, b)
}

// One broker grant authorizes exactly one actual HTTPUpstream invocation.
// Replays, TLS fallback and gateway compatibility retries hit the used slot
// guard. Nil GetBody and the production redirect-disable context also prevent
// HTTPUpstream or net/http from replaying the body below this boundary.
type namespaceBudgetUpstream struct {
	mu                                          sync.Mutex
	h                                           *namespaceHarness
	inner                                       service.HTTPUpstream
	activeSlot, attempts, blocked, totalBlocked int
	turn                                        int
	scenario                                    string
	profile                                     namespaceProfile
	activeContext                               context.Context
	used                                        map[int]bool
	endpoint, authorizationSHA, proxyURL        string
	inputBody, sentBody, rawResponse            []byte
	responseContentType                         string
	responseStatus                              int
	lastError                                   string
}

func (u *namespaceBudgetUpstream) Do(r *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return u.send(r, proxy, id, concurrency, nil)
}

func (u *namespaceBudgetUpstream) DoWithTLS(r *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.send(r, proxy, id, concurrency, profile)
}

func (u *namespaceBudgetUpstream) send(request *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fail := func(class string) (*http.Response, error) { u.lastError = class; return nil, errors.New(class) }
	if u.activeSlot < 1 || u.activeSlot > namespaceMaxAttempts || u.used[u.activeSlot] {
		u.blocked++
		u.totalBlocked++
		return fail("slot_retry_blocked")
	}
	if u.attempts >= namespaceMaxAttempts {
		return fail("attempt_budget_exhausted")
	}
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.URL.String() != u.endpoint ||
		id != 16050 || id != u.h.boot.Source.Account.ID || concurrency != u.h.boot.Source.Account.Concurrency || proxy != u.proxyURL ||
		fidelityHash([]byte(request.Header.Get("Authorization"))) != u.authorizationSHA || fidelityHashJSON(&u.h.boot.Source) != u.h.sourceSnapshotSHA {
		return fail("source_changed")
	}
	slotContext := u.activeContext
	if slotContext == nil {
		slotContext = u.h.ctx
	}
	if request.Context().Err() != nil || slotContext.Err() != nil || u.h.ctx.Err() != nil {
		return fail("request_timeout")
	}
	if request.Body == nil {
		return fail("request_body_unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, fidelityMaxBody+1))
	_ = request.Body.Close()
	if err != nil || len(body) > fidelityMaxBody {
		return fail("request_body_unavailable")
	}
	if !u.validWireBody(body) {
		return fail("wire_contract_mismatch")
	}
	if err := u.h.output.Encode(map[string]any{"type": "before_send", "scenario": u.scenario, "turn": u.turn,
		"slot": u.activeSlot, "expected_fingerprint": u.h.boot.Source.Fingerprint}); err != nil {
		return fail("broker_unavailable")
	}
	var grant namespaceGrant
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
	if grant.Slot != u.activeSlot || grant.Scenario != u.scenario || grant.Turn != u.turn || grant.Fingerprint != u.h.boot.Source.Fingerprint ||
		grant.Attempt != u.attempts+1 || grant.Attempt > namespaceMaxAttempts || grant.ElapsedMS < 0 || grant.ElapsedMS >= namespaceMaxDuration.Milliseconds() {
		return fail("invalid_broker_grant")
	}
	u.used[u.activeSlot], u.attempts = true, grant.Attempt
	u.sentBody = append([]byte(nil), body...)
	// Forward can detach client cancellation for normal accounting. Restore the
	// diagnostic's hard deadline without discarding other transport context.
	sendContext := request.Context()
	var cancelDeadline context.CancelFunc = func() {}
	if deadline, ok := slotContext.Deadline(); ok {
		sendContext, cancelDeadline = context.WithDeadline(sendContext, deadline)
	}
	sendContext, cancelSend := context.WithCancel(sendContext)
	stopCancellation := context.AfterFunc(slotContext, cancelSend)
	cleanup := func() { stopCancellation(); cancelSend(); cancelDeadline() }
	request = request.Clone(service.WithHTTPUpstreamRedirectsDisabled(sendContext))
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = nil
	var response *http.Response
	if profile == nil {
		response, err = u.inner.Do(request, proxy, id, concurrency)
	} else {
		response, err = u.inner.DoWithTLS(request, proxy, id, concurrency, profile)
	}
	if err != nil {
		cleanup()
		return fail("transport_error")
	}
	if response == nil || response.Body == nil {
		cleanup()
		return fail("empty_upstream_response")
	}
	u.responseStatus, u.responseContentType = response.StatusCode, response.Header.Get("Content-Type")
	response.Body = &fidelityCaptureBody{ReadCloser: response.Body, output: &u.rawResponse, remaining: fidelityMaxBody, cleanup: cleanup}
	return response, nil
}

func (u *namespaceBudgetUpstream) validWireBody(body []byte) bool {
	var sent, before map[string]json.RawMessage
	if json.Unmarshal(body, &sent) != nil || json.Unmarshal(u.inputBody, &before) != nil {
		return false
	}
	var model, key, instructions, clientInstructions string
	var stream, store bool
	var outputLimit int
	var include []string
	if json.Unmarshal(sent["model"], &model) != nil || model != u.profile.Model+"-ssvip" ||
		!u.profile.matchesWireEffort(body) || !u.profile.matchesWireEffort(u.inputBody) ||
		json.Unmarshal(sent["max_output_tokens"], &outputLimit) != nil || outputLimit != 4096 ||
		json.Unmarshal(sent["stream"], &stream) != nil || !stream || json.Unmarshal(sent["store"], &store) != nil || store ||
		json.Unmarshal(sent["include"], &include) != nil || len(include) != 1 || include[0] != "reasoning.encrypted_content" ||
		json.Unmarshal(sent["prompt_cache_key"], &key) != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(key) ||
		json.Unmarshal(sent["instructions"], &instructions) != nil || json.Unmarshal(before["instructions"], &clientInstructions) != nil ||
		instructions == clientInstructions || instructions != namespaceExpectedInstructions(u.h.boot.Source, clientInstructions) || !namespaceJSONEqual(sent["input"], before["input"]) {
		return false
	}
	if previous, exists := u.h.cacheKeys[u.scenario]; exists && (key != previous || u.h.promptSHAs[u.scenario] != fidelityHash([]byte(instructions))) {
		return false
	}
	for _, field := range []string{"previous_response_id", "conversation", "context_management"} {
		if _, exists := sent[field]; exists {
			return false
		}
	}
	return true
}
