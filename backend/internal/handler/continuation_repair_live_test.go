//go:build continuation_repair_live

package handler

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
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
	continuationRepairRunID       = "responses-continuation-repair-20260909"
	continuationRepairMaxRequests = 4
	continuationRepairMaxAttempts = 6
	continuationRepairMaxBody     = 8 << 20
	continuationRepairDuration    = 15 * time.Minute
	continuationRepairTurnTimeout = 180 * time.Second
	continuationRepairTool        = "continuation_probe_value"
	continuationRepairToolOutput  = `{"value":"ALPHA"}`
	continuationRepairFinal       = "CONTINUATION_OK"
)

type continuationRepairPublication struct {
	Revision      int64                            `json:"revision"`
	Version       service.RemoteSkillBundleVersion `json:"version"`
	Prompt        service.RemoteSkillPromptVersion `json:"prompt"`
	RawBody       string                           `json:"raw_body"`
	EffectiveBody string                           `json:"effective_body"`
}

type continuationRepairSource struct {
	Account             service.Account                      `json:"account"`
	Group               service.Group                        `json:"group"`
	FastPolicy          *service.OpenAIFastPolicySettings    `json:"fast_policy"`
	BusinessPrompt      service.BusinessSystemPromptSnapshot `json:"business_prompt"`
	RegistryPublication *continuationRepairPublication       `json:"registry_publication"`
	Settings            map[string]string                    `json:"settings"`
	ChannelModels       map[string]string                    `json:"channel_models"`
	ChannelImageBridge  *bool                                `json:"channel_image_bridge"`
	Fingerprint         string                               `json:"fingerprint"`
	UserID              int64                                `json:"user_id"`
}

type continuationRepairBootstrap struct {
	SchemaVersion int                      `json:"schema_version"`
	Mode          string                   `json:"mode"`
	ConfigMode    string                   `json:"config_mode"`
	RunID         string                   `json:"run_id"`
	SourceSHA     string                   `json:"source_sha"`
	ManifestSHA   string                   `json:"manifest_sha256"`
	Source        continuationRepairSource `json:"source"`
}

type continuationRepairGrant struct {
	Type          string `json:"type"`
	Scenario      string `json:"scenario"`
	Turn          int    `json:"turn"`
	Request       int    `json:"request"`
	Slot          int    `json:"slot"`
	Attempt       int    `json:"attempt"`
	AttemptInTurn int    `json:"attempt_in_turn"`
	Fingerprint   string `json:"fingerprint"`
	ElapsedMS     int64  `json:"elapsed_ms"`
}

// Every field below is a broker-allowlisted enum, count, boolean, or digest.
// Credential material, request/response bodies, tool IDs, and upstream error
// messages remain in memory and never enter stdout or the persistent ledger.
type continuationRepairIdentity struct {
	Scenario  string `json:"scenario"`
	Turn      int    `json:"turn"`
	Request   int    `json:"request"`
	Model     string `json:"model"`
	AccountID int64  `json:"account_id"`
	WireModel string `json:"wire_model"`
}

type continuationRepairAttempt struct {
	continuationRepairIdentity
	Type               string `json:"type"`
	Slot               int    `json:"slot"`
	AttemptInTurn      int    `json:"attempt_in_turn"`
	UpstreamHTTPStatus int    `json:"upstream_http_status"`
	SignatureRejection bool   `json:"signature_rejection"`
	SemanticOutput     bool   `json:"semantic_output"`
	Uncertain          bool   `json:"uncertain"`
	ErrorClass         string `json:"error_class"`
}

type continuationRepairTurnResult struct {
	continuationRepairIdentity
	Type                   string           `json:"type"`
	Status                 string           `json:"status"`
	Completed              bool             `json:"completed"`
	Terminal               string           `json:"terminal"`
	Attempts               int              `json:"attempts"`
	ClientHTTPStatus       int              `json:"client_http_status"`
	OpsStatus              *int             `json:"ops_status"`
	ToolCallReceived       bool             `json:"tool_call_received"`
	ToolContractValid      bool             `json:"tool_contract_valid"`
	HistoryPreserved       bool             `json:"history_preserved"`
	HeartbeatNormalized    bool             `json:"heartbeat_normalized"`
	AccountHealthUnchanged bool             `json:"account_health_unchanged"`
	BillingRows            int              `json:"billing_rows"`
	OpsRows                int              `json:"ops_rows"`
	ErrorClass             string           `json:"error_class"`
	Usage                  map[string]int64 `json:"usage,omitempty"`
	RequestIDSHA           string           `json:"request_id_sha256,omitempty"`
	ResponseIDSHA          string           `json:"response_id_sha256,omitempty"`
}

type continuationRepairResponse struct {
	Terminal, Status, Model, ID, Text, ErrorClass string
	Output                                        []json.RawMessage
	Usage                                         map[string]int64
	Completed, Semantic, Signature, Invalid       bool
}

type continuationRepairHarness struct {
	boot       continuationRepairBootstrap
	input      *bufio.Reader
	output     *json.Encoder
	ctx        context.Context
	fixture    *continuationRepairFixture
	upstream   *continuationRepairUpstream
	requests   int
	finished   map[string]bool
	stop       string
	sourceHash string
}

// Normal application builds and CI exclude this file. Even a tagged binary
// must receive one exact test selection, the explicit live gate, sealed source
// stdin, and one fsynced broker grant before each individual upstream POST.
func TestContinuationRepairLive(t *testing.T) {
	if os.Getenv("SUB2API_CONTINUATION_REPAIR_LIVE") != "1" {
		t.Skip("explicit sealed runner opt-in required")
	}
	selection := flag.Lookup("test.run")
	if selection == nil || selection.Value.String() != "^TestContinuationRepairLive$" || os.Getenv("SUB2API_REASONING_FIDELITY_LIVE") != "" || os.Getenv("SUB2API_NAMESPACE_ROUNDTRIP_LIVE") != "" {
		t.Fatal("invalid_live_test_selection")
	}
	if !regexp.MustCompile(`^/proc/self/fd/[0-9]+$`).MatchString(os.Getenv("CONFIG_FILE")) {
		t.Fatal("sealed_production_config_required")
	}
	protocol := os.Stdout
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
	out := json.NewEncoder(protocol)
	fallback := func(class string) {
		_ = out.Encode(map[string]any{"type": "summary", "status": "failed", "requests": 0, "attempts": 0,
			"astra_completed": false, "sol_completed": false, "blocked_retries": 0, "error_class": class})
		t.Fail()
	}
	defer func() {
		if recover() != nil {
			fallback("handler_failed")
		}
	}()
	if err := continuationRepairRun(bufio.NewReaderSize(os.Stdin, 64<<10), out); err != nil {
		class := "handler_failed"
		var wireError continuationRepairWireError
		if errors.As(err, &wireError) {
			class = string(wireError)
		}
		fallback(class)
	}
}

func continuationRepairRun(in *bufio.Reader, out *json.Encoder) error {
	var boot continuationRepairBootstrap
	if continuationRepairReadJSON(in, &boot) != nil || continuationRepairValidateSource(boot) != nil {
		return errors.New("source_changed")
	}
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return errors.New("source_changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), continuationRepairDuration)
	defer cancel()
	h := &continuationRepairHarness{boot: boot, input: in, output: out, ctx: ctx, finished: make(map[string]bool), sourceHash: continuationRepairHashJSON(boot.Source)}
	endpoint, err := continuationRepairEndpoint(&boot.Source.Account)
	if err != nil {
		return err
	}
	if os.Getenv("SUB2API_CONTINUATION_REPAIR_VALIDATE_ONLY") == "1" {
		if err := continuationRepairPreflight(ctx, cfg, boot.Source); err != nil {
			return err
		}
		return out.Encode(map[string]any{"type": "summary", "status": "validated", "requests": 0, "attempts": 0,
			"astra_completed": false, "sol_completed": false, "blocked_retries": 0})
	}
	u := &continuationRepairUpstream{h: h, inner: repository.NewHTTPUpstream(cfg), endpoint: endpoint,
		authorizationHash: continuationRepairHash([]byte("Bearer " + boot.Source.Account.GetOpenAIProtocolAPIKey()))}
	if boot.Source.Account.Proxy != nil {
		u.proxy = boot.Source.Account.Proxy.URL()
	}
	h.upstream = u
	h.fixture, err = continuationRepairBuildFixture(ctx, cfg, boot.Source, u)
	if err != nil {
		return errors.New("source_changed")
	}
	defer h.fixture.close()
	// Use the real Ops capture/parser/sanitization queue, without starting a DB
	// writer. The complete fixture owns this fresh, single-test process.
	opsErrorLogOnce.Do(func() {})
	opsErrorLogMu.Lock()
	opsErrorLogQueue = make(chan opsErrorLogJob, 32)
	opsErrorLogMu.Unlock()
	if err := out.Encode(map[string]any{"type": "ready", "run_id": boot.RunID, "source_sha": boot.SourceSHA,
		"source_sha256": boot.Source.Fingerprint, "endpoint_sha256": continuationRepairHash([]byte(endpoint)), "attempts": 0}); err != nil {
		return errors.New("broker_unavailable")
	}
	for _, scenario := range []string{"astra", "sol"} {
		if h.stop != "" || ctx.Err() != nil {
			break
		}
		firstBody := continuationRepairInitialBody(continuationRepairModel(scenario))
		first, result := h.runTurn(scenario, 1, firstBody)
		if result.Status != "completed" {
			continue
		}
		secondBody, err := continuationRepairContinuation(firstBody, first, scenario)
		if err != nil {
			h.stop = "history_invalid"
			break
		}
		_, result = h.runTurn(scenario, 2, secondBody)
		h.finished[scenario] = result.Status == "completed"
	}
	status := "failed"
	if h.finished["astra"] && h.finished["sol"] && h.stop == "" {
		status = "completed"
	}
	return out.Encode(h.summary(status))
}

func (h *continuationRepairHarness) summary(status string) map[string]any {
	result := map[string]any{"type": "summary", "status": status, "requests": h.requests, "attempts": h.upstream.attempts,
		"astra_completed": h.finished["astra"], "sol_completed": h.finished["sol"], "blocked_retries": h.upstream.blocked}
	if h.stop != "" {
		result["error_class"] = h.stop
	}
	return result
}

func continuationRepairValidateSource(b continuationRepairBootstrap) error {
	if b.SchemaVersion != 1 || b.Mode != "continuation_repair" || b.ConfigMode != "production_env" || b.RunID != continuationRepairRunID ||
		!regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(b.SourceSHA) || b.SourceSHA != os.Getenv("CONTINUATION_REPAIR_SOURCE_SHA") ||
		!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(b.ManifestSHA) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(b.Source.Fingerprint) {
		return errors.New("source_changed")
	}
	a, group := &b.Source.Account, &b.Source.Group
	if a.ID != 16050 || a.Name != "白嫖-dmxapi" || a.Platform != service.PlatformOpenAI || a.Type != service.AccountTypeAPIKey ||
		a.ParentAccountID != nil || a.GetOpenAIProtocolAPIKey() == "" || a.Status != service.StatusActive || !a.Schedulable || a.Concurrency < 1 ||
		group.ID != 35 || group.Platform != service.PlatformOpenAI || group.Status != service.StatusActive || b.Source.UserID != 920000016050 ||
		!a.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityResponses) || service.IsCindyRuntimeCompatibleAPIKeyAccount(a.Platform, a.Type, a.Credentials) {
		return errors.New("source_changed")
	}
	member := false
	for _, id := range a.GroupIDs {
		member = member || id == group.ID
	}
	if !member || (a.ProxyID == nil) != (a.Proxy == nil) || (a.Proxy != nil && (*a.ProxyID != a.Proxy.ID || !a.Proxy.IsActive() || a.Proxy.IsExpired(time.Now()))) {
		return errors.New("source_changed")
	}
	// Live preflight established that the production prompt is disabled. A
	// changed runtime is a new source, not permission to toggle it for a test.
	if b.Source.BusinessPrompt.Enabled || b.Source.RegistryPublication != nil || b.Source.FastPolicy == nil || b.Source.Settings == nil || len(b.Source.ChannelModels) != 2 {
		return errors.New("source_changed")
	}
	for _, scenario := range []string{"astra", "sol"} {
		model := continuationRepairModel(scenario)
		mapped := b.Source.ChannelModels[model]
		if mapped == "" || !continuationRepairSafeWireModel(a.GetMappedModel(mapped)) {
			return errors.New("model_mismatch")
		}
	}
	return nil
}

func continuationRepairEndpoint(account *service.Account) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(account.GetOpenAIBaseURL()), "/")
	if base == "" {
		base = "https://api.openai.com"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("source_changed")
	}
	switch parsed.Path {
	case "":
		parsed.Path = "/v1/responses"
	case "/v1":
		parsed.Path += "/responses"
	case "/v1/responses":
	default:
		return "", errors.New("source_changed")
	}
	return parsed.String(), nil
}

func continuationRepairModel(scenario string) string {
	if scenario == "astra" {
		return "gpt-6-astra"
	}
	return "gpt-5.6-sol"
}

func continuationRepairSafeWireModel(model string) bool {
	return model == "gpt-6-astra" || model == "gpt-6-astra-ssvip" || model == "gpt-5.6-sol"
}

func continuationRepairInitialBody(model string) []byte {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"model": model, "stream": true, "store": false, "reasoning": map[string]any{"effort": "low"},
		"max_output_tokens": 4096, "include": []string{"reasoning.encrypted_content"}, "prompt_cache_key": hex.EncodeToString(key),
		"instructions": "This is an isolated continuation protocol check. The only tool is a read-only constant value. Follow its requested call and then the later heartbeat instructions. Do not use any other tool.",
		"input":        []any{map[string]any{"role": "user", "content": "Call continuation_probe_value exactly once with an empty JSON object. Do not answer yet."}},
		"tools": []any{map[string]any{"type": "function", "name": continuationRepairTool, "description": "Return a fixed constant, without file, shell, network or business actions.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}, "strict": true}},
		"tool_choice": map[string]any{"type": "function", "name": continuationRepairTool}, "parallel_tool_calls": false,
	})
	return body
}

func continuationRepairContinuation(initial []byte, response continuationRepairResponse, scenario string) ([]byte, error) {
	call, valid := continuationRepairToolCall(response)
	if !valid {
		return nil, errors.New("tool_contract_invalid")
	}
	var fields map[string]json.RawMessage
	var input []json.RawMessage
	if json.Unmarshal(initial, &fields) != nil || json.Unmarshal(fields["input"], &input) != nil {
		return nil, errors.New("history_invalid")
	}
	// Preserve every original response.output item, including namespace:null,
	// encrypted reasoning, phase, IDs and fields unknown to this test binary.
	input = append(input, response.Output...)
	var callID string
	_ = json.Unmarshal(call["call_id"], &callID)
	// The original call (including its namespace) is replayed above. A new
	// matching result is identified by call_id; do not invent extra namespace
	// metadata on a field shape the normal gateway deliberately normalizes.
	toolOutput := map[string]any{"type": "function_call_output", "call_id": callID, "output": continuationRepairToolOutput}
	raw, _ := json.Marshal(toolOutput)
	input = append(input, raw)
	for i := 0; i < 2; i++ {
		heartbeat := "<heartbeat><automation_id>continuation-repair-" + scenario + "</automation_id><current_time_iso>" + time.Now().UTC().Format(time.RFC3339Nano) +
			"</current_time_iso><instructions>Read the preceding local tool result. If its value is ALPHA, return exactly CONTINUATION_OK. Do not call another tool.</instructions></heartbeat>"
		item := map[string]any{"type": "function_call_output", "namespace": "codex_app", "name": "automation_update", "output": heartbeat}
		if i == 1 {
			item["call_id"] = ""
		}
		raw, _ = json.Marshal(item)
		input = append(input, raw)
	}
	fields["input"], _ = json.Marshal(input)
	fields["tool_choice"] = json.RawMessage(`"none"`)
	return json.Marshal(fields)
}

func continuationRepairToolCall(response continuationRepairResponse) (map[string]json.RawMessage, bool) {
	if !response.Completed {
		return nil, false
	}
	var found map[string]json.RawMessage
	for _, raw := range response.Output {
		item, err := continuationRepairObject(raw)
		if err != nil {
			return nil, false
		}
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		if kind != "function_call" {
			if strings.HasSuffix(kind, "_call") || strings.HasSuffix(kind, "_call_output") || strings.HasPrefix(kind, "mcp_") {
				return nil, false
			}
			continue
		}
		var name, id, args string
		if found != nil || json.Unmarshal(item["name"], &name) != nil || name != continuationRepairTool ||
			json.Unmarshal(item["call_id"], &id) != nil || strings.TrimSpace(id) == "" || json.Unmarshal(item["arguments"], &args) != nil {
			return nil, false
		}
		arguments, err := continuationRepairObject([]byte(args))
		if err != nil || len(arguments) != 0 {
			return nil, false
		}
		found = item
	}
	return found, found != nil
}

func (h *continuationRepairHarness) runTurn(scenario string, turn int, body []byte) (continuationRepairResponse, continuationRepairTurnResult) {
	request := turn
	if scenario == "sol" {
		request += 2
	}
	identity := continuationRepairIdentity{Scenario: scenario, Turn: turn, Request: request, Model: continuationRepairModel(scenario)}
	result := continuationRepairTurnResult{continuationRepairIdentity: identity, Type: "turn_result", Status: "failed", Terminal: "none"}
	if len(body) == 0 || h.requests >= continuationRepairMaxRequests || h.ctx.Err() != nil {
		h.stop = "request_budget_exhausted"
		return continuationRepairResponse{}, result
	}
	if h.output.Encode(map[string]any{"type": "before_turn", "scenario": scenario, "turn": turn, "request": request, "expected_fingerprint": h.boot.Source.Fingerprint}) != nil {
		h.stop = "broker_unavailable"
		return continuationRepairResponse{}, result
	}
	var grant continuationRepairGrant
	if h.readGrant(h.ctx, &grant) != nil || grant.Type != "turn_granted" || grant.Scenario != scenario || grant.Turn != turn || grant.Request != request || grant.Fingerprint != h.boot.Source.Fingerprint {
		h.stop = "broker_denied"
		return continuationRepairResponse{}, result
	}
	h.requests++
	ctx, cancel := context.WithTimeout(h.ctx, continuationRepairTurnTimeout)
	defer cancel()
	u := h.upstream
	u.beginTurn(ctx, identity, body)
	beforeUsage, beforeHealth := len(h.fixture.usageSnapshot()), h.fixture.healthChanges()
	router := gin.New()
	var handlerContext *gin.Context
	router.Use(func(c *gin.Context) {
		h.fixture.decorateContext(c)
		handlerContext = c
		c.Next()
	})
	router.Use(OpsErrorLoggerMiddleware(h.fixture.ops))
	router.POST("/v1/responses", h.fixture.handler.Responses)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "codex_cli_rs/0.149.0 (continuation-repair-isolated-check)")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	u.finishOutstanding()
	parsed := continuationRepairParseResponse(recorder.Body.Bytes(), recorder.Header().Get("Content-Type"), recorder.Code)
	result.Completed, result.Terminal, result.ClientHTTPStatus = parsed.Completed, parsed.Terminal, recorder.Code
	result.Attempts, result.HistoryPreserved, result.HeartbeatNormalized = u.turnAttempts, u.historyPreserved, u.heartbeatNormalized
	result.AccountHealthUnchanged = beforeHealth == h.fixture.healthChanges()
	if handlerContext != nil {
		if id, ok := handlerContext.Get(opsAccountIDKey); ok && id == int64(16050) {
			result.AccountID = 16050
		}
	}
	if u.turnAttempts > 0 {
		result.WireModel = u.identity.WireModel
	}
	_, result.ToolCallReceived = continuationRepairToolCall(parsed)
	result.ToolContractValid = result.ToolCallReceived
	if turn == 2 {
		result.ToolContractValid = parsed.Completed && !continuationRepairHasTool(parsed.Output) && strings.TrimSpace(parsed.Text) == continuationRepairFinal
	}
	usage := h.fixture.usageSnapshot()
	result.BillingRows = len(usage) - beforeUsage
	attributed := result.BillingRows == 1
	if attributed {
		row := usage[len(usage)-1]
		actualModel := row.Model
		if row.UpstreamModel != nil {
			actualModel = *row.UpstreamModel
		}
		attributed = row.AccountID == 16050 && row.UserID == h.boot.Source.UserID && row.GroupID != nil && *row.GroupID == 35 && actualModel == result.WireModel
		if row.RequestID != "" {
			result.RequestIDSHA = continuationRepairHash([]byte(row.RequestID))
		}
	}
	for _, entry := range continuationRepairDrainOps() {
		result.OpsRows++
		status := entry.StatusCode
		result.OpsStatus = &status
	}
	if parsed.ID != "" {
		result.ResponseIDSHA = continuationRepairHash([]byte(parsed.ID))
	}
	result.Usage = parsed.Usage
	switch {
	case u.lastError != "":
		result.ErrorClass = u.lastError
	case u.turnAttempts == 0:
		result.Status, result.ErrorClass = "not_covered", "handler_failed"
	case parsed.Invalid || u.uncertain:
		result.Status, result.ErrorClass = "uncertain", "body_incomplete"
	case !parsed.Completed:
		result.ErrorClass = parsed.ErrorClass
		if result.ErrorClass == "" {
			result.ErrorClass = "response_not_completed"
		}
	case result.AccountID != 16050 || result.WireModel == "":
		result.Status, result.ErrorClass = "not_covered", "model_mismatch"
	case !u.responseModelMatches:
		result.Status, result.ErrorClass = "not_covered", "model_mismatch"
	case !continuationRepairJSONEqual(continuationRepairMarshal(u.completedOutput), continuationRepairMarshal(parsed.Output)):
		result.ErrorClass = "history_invalid"
	case !result.ToolContractValid:
		result.Status, result.ErrorClass = "not_covered", "tool_contract_invalid"
	case !result.HistoryPreserved:
		result.ErrorClass = "history_invalid"
	case turn == 2 && !result.HeartbeatNormalized:
		result.ErrorClass = "heartbeat_not_normalized"
	case !result.AccountHealthUnchanged:
		result.ErrorClass = "account_health_changed"
	case !attributed:
		result.ErrorClass = "billing_attribution_missing"
	default:
		result.Status = "completed"
	}
	if h.output.Encode(result) != nil {
		h.stop = "broker_unavailable"
	}
	return parsed, result
}

func continuationRepairDrainOps() []*service.OpsInsertErrorLogInput {
	var entries []*service.OpsInsertErrorLogInput
	for {
		select {
		case job := <-opsErrorLogQueue:
			opsErrorLogQueueLen.Add(-1)
			opsErrorLogQueueBytes.Add(-job.queuedBytes)
			entries = append(entries, job.entry)
		default:
			return entries
		}
	}
}

func (h *continuationRepairHarness) readGrant(ctx context.Context, grant *continuationRepairGrant) error {
	finished := make(chan error, 1)
	go func() { finished <- continuationRepairReadJSON(h.input, grant) }()
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		// No subsequent read is permitted after a timed-out protocol exchange.
		h.stop = "broker_unavailable"
		return errors.New("broker_unavailable")
	}
}

type continuationRepairUpstream struct {
	mu                                    sync.Mutex
	h                                     *continuationRepairHarness
	inner                                 service.HTTPUpstream
	identity                              continuationRepairIdentity
	active                                context.Context
	endpoint, authorizationHash, proxy    string
	body, firstSent                       []byte
	attempts, turnAttempts, blocked       int
	last                                  *continuationRepairAttempt
	outstanding                           *continuationRepairCapture
	historyPreserved, heartbeatNormalized bool
	responseModelMatches                  bool
	completedOutput                       []json.RawMessage
	uncertain                             bool
	lastError                             string
}

func (u *continuationRepairUpstream) beginTurn(ctx context.Context, identity continuationRepairIdentity, body []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.identity, u.active, u.body = identity, ctx, bytes.Clone(body)
	u.identity.WireModel = u.h.boot.Source.Account.GetMappedModel(u.h.boot.Source.ChannelModels[identity.Model])
	u.turnAttempts, u.last, u.outstanding, u.firstSent, u.lastError = 0, nil, nil, nil, ""
	u.historyPreserved, u.heartbeatNormalized, u.uncertain = false, identity.Turn == 1, false
	u.responseModelMatches, u.completedOutput = false, nil
}

func (u *continuationRepairUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	return u.send(req, proxy, accountID, concurrency, nil)
}

func (u *continuationRepairUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.send(req, proxy, accountID, concurrency, profile)
}

func (u *continuationRepairUpstream) send(req *http.Request, proxy string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fail := func(class string) (*http.Response, error) { u.lastError = class; return nil, errors.New(class) }
	ordinal := u.turnAttempts + 1
	if u.identity.Turn < 1 || ordinal > u.identity.Turn || (ordinal == 2 && (u.last == nil || !u.last.SignatureRejection || u.last.SemanticOutput || u.last.Uncertain)) {
		u.blocked++
		return fail("attempt_budget_exhausted")
	}
	if u.attempts >= continuationRepairMaxAttempts || u.h.stop != "" {
		return fail("attempt_budget_exhausted")
	}
	if req == nil || req.URL == nil || req.Method != http.MethodPost || req.URL.String() != u.endpoint ||
		accountID != 16050 || concurrency != u.h.boot.Source.Account.Concurrency || proxy != u.proxy ||
		continuationRepairHash([]byte(req.Header.Get("Authorization"))) != u.authorizationHash || continuationRepairHashJSON(u.h.boot.Source) != u.h.sourceHash {
		u.h.stop = "source_changed"
		return fail("source_changed")
	}
	if u.active == nil || u.active.Err() != nil || req.Context().Err() != nil || req.Body == nil {
		return fail("body_incomplete")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, continuationRepairMaxBody+1))
	_ = req.Body.Close()
	if err != nil || len(body) > continuationRepairMaxBody {
		return fail("body_incomplete")
	}
	if err := u.validateWire(body, ordinal); err != nil {
		return fail(err.Error())
	}
	cause := "initial"
	if ordinal == 2 {
		cause = "signature_recovery"
	}
	slot := u.attempts + 1
	if u.h.output.Encode(map[string]any{"type": "before_send", "scenario": u.identity.Scenario, "turn": u.identity.Turn, "request": u.identity.Request,
		"slot": slot, "attempt_in_turn": ordinal, "cause": cause, "expected_fingerprint": u.h.boot.Source.Fingerprint}) != nil {
		u.h.stop = "broker_unavailable"
		return fail("broker_unavailable")
	}
	var grant continuationRepairGrant
	if u.h.readGrant(u.active, &grant) != nil || grant.Type != "send_granted" || grant.Scenario != u.identity.Scenario || grant.Turn != u.identity.Turn ||
		grant.Request != u.identity.Request || grant.Slot != slot || grant.Attempt != slot || grant.AttemptInTurn != ordinal || grant.Fingerprint != u.h.boot.Source.Fingerprint ||
		grant.ElapsedMS < 0 || grant.ElapsedMS >= continuationRepairDuration.Milliseconds() {
		u.h.stop = "broker_denied"
		return fail("broker_denied")
	}
	u.attempts, u.turnAttempts = slot, ordinal
	if ordinal == 1 {
		u.firstSent = bytes.Clone(body)
	}
	identity := u.identity
	identity.AccountID = accountID
	result := continuationRepairAttempt{continuationRepairIdentity: identity, Type: "attempt_result", Slot: slot, AttemptInTurn: ordinal}
	// Keep transport policy context, but restore the bounded client deadline
	// and cancellation that ordinary usage-draining forwarding may detach.
	ctx := req.Context()
	cancelDeadline := func() {}
	if deadline, ok := u.active.Deadline(); ok {
		ctx, cancelDeadline = context.WithDeadline(ctx, deadline)
	}
	ctx, cancelSend := context.WithCancel(ctx)
	stop := context.AfterFunc(u.active, cancelSend)
	cleanup := func() { stop(); cancelSend(); cancelDeadline() }
	req = req.Clone(service.WithHTTPUpstreamRedirectsDisabled(ctx))
	req.Body, req.GetBody = io.NopCloser(bytes.NewReader(body)), nil
	var response *http.Response
	if profile == nil {
		response, err = u.inner.Do(req, proxy, accountID, concurrency)
	} else {
		response, err = u.inner.DoWithTLS(req, proxy, accountID, concurrency, profile)
	}
	if err != nil || response == nil || response.Body == nil {
		cleanup()
		result.Uncertain, result.ErrorClass = true, "transport_error"
		u.recordLocked(result)
		return fail("transport_error")
	}
	result.UpstreamHTTPStatus = response.StatusCode
	contentType := response.Header.Get("Content-Type")
	capture := &continuationRepairCapture{ReadCloser: response.Body, cleanup: cleanup, finish: func(raw []byte, incomplete, eof bool) {
		parsed := continuationRepairParseResponse(raw, contentType, response.StatusCode)
		result.SignatureRejection, result.SemanticOutput = parsed.Signature, parsed.Semantic
		result.Uncertain = incomplete || parsed.Invalid || parsed.Terminal == "none" || (!eof && parsed.Terminal == "error")
		result.ErrorClass = parsed.ErrorClass
		if result.Uncertain {
			result.ErrorClass = "body_incomplete"
		}
		u.mu.Lock()
		defer u.mu.Unlock()
		if parsed.Completed {
			u.responseModelMatches = parsed.Model == identity.WireModel || parsed.Model == identity.Model
			u.completedOutput = parsed.Output
		}
		u.recordLocked(result)
	}}
	u.outstanding, response.Body = capture, capture
	return response, nil
}

func (u *continuationRepairUpstream) recordLocked(result continuationRepairAttempt) {
	u.last = &result
	u.uncertain = u.uncertain || result.Uncertain
	if u.h.output.Encode(result) != nil {
		u.h.stop = "broker_unavailable"
	}
}

func (u *continuationRepairUpstream) finishOutstanding() {
	u.mu.Lock()
	capture := u.outstanding
	u.mu.Unlock()
	if capture != nil {
		_ = capture.Close()
	}
}

type continuationRepairWireError string

func (e continuationRepairWireError) Error() string { return string(e) }

func (u *continuationRepairUpstream) validateWire(body []byte, ordinal int) error {
	sent, err := continuationRepairObject(body)
	if err != nil {
		return continuationRepairWireError("wire_json_mismatch")
	}
	var model string
	var stream, store bool
	if json.Unmarshal(sent["model"], &model) != nil || model != u.identity.WireModel {
		return continuationRepairWireError("wire_model_mismatch")
	}
	if json.Unmarshal(sent["stream"], &stream) != nil || !stream {
		return continuationRepairWireError("wire_stream_mismatch")
	}
	if json.Unmarshal(sent["store"], &store) != nil || store {
		return continuationRepairWireError("wire_store_mismatch")
	}
	if ordinal == 2 {
		if !continuationRepairOnlyCipherRemoval(u.firstSent, body) {
			return continuationRepairWireError("wire_recovery_mismatch")
		}
		return nil
	}
	before, err := continuationRepairObject(u.body)
	if err != nil || !continuationRepairJSONEqual(before["instructions"], sent["instructions"]) {
		return continuationRepairWireError("wire_instructions_mismatch")
	}
	var expected []json.RawMessage
	if json.Unmarshal(before["input"], &expected) != nil {
		return continuationRepairWireError("wire_input_mismatch")
	}
	if u.identity.Turn == 2 {
		if len(expected) < 4 {
			return continuationRepairWireError("wire_input_mismatch")
		}
		for i := len(expected) - 2; i < len(expected); i++ {
			item, err := continuationRepairObject(expected[i])
			var text string
			if err != nil || json.Unmarshal(item["output"], &text) != nil {
				return continuationRepairWireError("wire_input_mismatch")
			}
			expected[i], _ = json.Marshal(map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}})
		}
	}
	normalized, _ := json.Marshal(expected)
	u.historyPreserved = continuationRepairJSONEqual(normalized, sent["input"])
	u.heartbeatNormalized = u.identity.Turn == 1 || u.historyPreserved
	if !u.historyPreserved {
		return continuationRepairWireError("wire_input_mismatch")
	}
	return nil
}

// A recovery grant cannot be used by a same-account compatibility retry. Its
// only permitted wire change is omission of already-present reasoning
// encrypted_content fields; every other input and request field must match.
func continuationRepairOnlyCipherRemoval(before, after []byte) bool {
	a, err := continuationRepairObject(before)
	if err != nil {
		return false
	}
	b, err := continuationRepairObject(after)
	if err != nil {
		return false
	}
	var ai, bi []json.RawMessage
	if json.Unmarshal(a["input"], &ai) != nil || json.Unmarshal(b["input"], &bi) != nil || len(ai) != len(bi) {
		return false
	}
	delete(a, "input")
	delete(b, "input")
	aRaw, _ := json.Marshal(a)
	bRaw, _ := json.Marshal(b)
	if !continuationRepairJSONEqual(aRaw, bRaw) {
		return false
	}
	removed := 0
	for i := range ai {
		if continuationRepairJSONEqual(ai[i], bi[i]) {
			continue
		}
		oldItem, oldErr := continuationRepairObject(ai[i])
		newItem, newErr := continuationRepairObject(bi[i])
		var kind, cipher string
		_, remaining := newItem["encrypted_content"]
		if oldErr != nil || newErr != nil || remaining || json.Unmarshal(oldItem["type"], &kind) != nil || kind != "reasoning" ||
			json.Unmarshal(oldItem["encrypted_content"], &cipher) != nil || cipher == "" {
			return false
		}
		delete(oldItem, "encrypted_content")
		raw, _ := json.Marshal(oldItem)
		if !continuationRepairJSONEqual(raw, bi[i]) {
			return false
		}
		removed++
	}
	return removed > 0
}

type continuationRepairCapture struct {
	io.ReadCloser
	readMu     sync.Mutex
	mu         sync.Mutex
	raw        []byte
	incomplete bool
	eof        bool
	closing    bool
	once       sync.Once
	closeOnce  sync.Once
	cleanup    func()
	finish     func([]byte, bool, bool)
}

func (c *continuationRepairCapture) Read(p []byte) (int, error) {
	// The production timeout/keepalive scanner may read on another goroutine.
	// Preserve byte order without holding the state lock across a network read.
	c.readMu.Lock()
	defer c.readMu.Unlock()
	c.mu.Lock()
	closing := c.closing
	c.mu.Unlock()
	if closing {
		return 0, io.ErrClosedPipe
	}
	n, err := c.ReadCloser.Read(p)
	c.mu.Lock()
	if n > 0 {
		if len(c.raw)+n > continuationRepairMaxBody {
			c.incomplete = true
			err = errors.New("body_incomplete")
		} else {
			c.raw = append(c.raw, p[:n]...)
		}
	}
	if err == io.EOF {
		c.eof = true
	} else if err != nil && !c.closing {
		c.incomplete = true
	}
	finish := err != nil && !c.closing
	c.mu.Unlock()
	if finish {
		c.complete()
	}
	return n, err
}

func (c *continuationRepairCapture) complete() {
	c.once.Do(func() {
		c.mu.Lock()
		raw, incomplete, eof := bytes.Clone(c.raw), c.incomplete, c.eof
		c.mu.Unlock()
		c.finish(raw, incomplete, eof)
	})
}

func (c *continuationRepairCapture) Close() error {
	var result error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closing = true
		c.mu.Unlock()
		// Never start a second reader to drain a half-open SSE tail. Cancel and
		// close first to unblock an existing scanner, then freeze all its bytes.
		// The observer accepts an explicit complete terminal without requiring
		// TCP EOF; a provisional bare error without EOF remains uncertain.
		c.cleanup()
		result = c.ReadCloser.Close()
		c.readMu.Lock()
		defer c.readMu.Unlock()
		c.complete()
	})
	return result
}

func continuationRepairParseResponse(raw []byte, contentType string, status int) continuationRepairResponse {
	r := continuationRepairResponse{Terminal: "none"}
	if len(raw) == 0 || len(raw) > continuationRepairMaxBody {
		r.Invalid, r.ErrorClass = true, "invalid_response"
		return r
	}
	framed := strings.Contains(strings.ToLower(contentType), "text/event-stream") || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("data:")) || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("event:")) || bytes.HasPrefix(bytes.TrimSpace(raw), []byte(":"))
	if !framed {
		object, err := continuationRepairObject(raw)
		if err != nil {
			r.Invalid = true
		} else {
			r = continuationRepairDocument(object)
			if status >= 400 {
				r.Terminal = "error"
				r.Signature = continuationRepairSignature(object, status)
			}
		}
	} else {
		var data []byte
		event := ""
		done := false
		provisionalError, provisionalSignature := false, false
		apply := func() {
			if len(data) == 0 {
				return
			}
			payload := bytes.TrimSuffix(data, []byte{'\n'})
			if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
				done = true
				return
			}
			object, err := continuationRepairObject(payload)
			if err != nil || done {
				r.Invalid = true
				return
			}
			var kind string
			_ = json.Unmarshal(object["type"], &kind)
			if kind == "" {
				kind = event
			} else if event != "" && event != "message" && event != kind {
				r.Invalid = true
			}
			if r.Terminal != "none" {
				r.Invalid = true
			}
			switch kind {
			case "response.created", "response.queued", "response.in_progress":
				// Explicit non-semantic lifecycle events may precede recovery.
			case "response.completed", "response.failed", "response.incomplete", "response.done":
				response, err := continuationRepairObject(object["response"])
				if err != nil {
					r.Invalid = true
					return
				}
				parsed := continuationRepairDocument(response)
				if kind == "response.done" {
					kind = "response." + parsed.Status
				}
				if kind != "response.completed" && kind != "response.failed" && kind != "response.incomplete" {
					r.Invalid = true
					return
				}
				if kind != "response."+parsed.Status {
					r.Invalid = true
				}
				parsed.Terminal, parsed.Semantic, parsed.Invalid = kind, parsed.Semantic || r.Semantic, parsed.Invalid || r.Invalid
				parsed.Signature = kind == "response.failed" && continuationRepairSignature(object, status)
				r = parsed
			case "error", "response.error":
				// A bare error is provisional until EOF; some upstreams then
				// provide an authoritative completed/failed response document.
				provisionalError, provisionalSignature = true, continuationRepairSignature(object, status)
			default:
				// Treat unknown events conservatively as semantic. A payload
				// must not buy a retry just because the harness cannot name it.
				r.Semantic = true
			}
		}
		scanner := bufio.NewScanner(bytes.NewReader(bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))))
		scanner.Buffer(make([]byte, 4096), continuationRepairMaxBody+1)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				apply()
				data, event = nil, ""
				continue
			}
			if line[0] == ':' {
				continue
			}
			field, value, _ := bytes.Cut(line, []byte{':'})
			value = bytes.TrimPrefix(value, []byte{' '})
			switch string(field) {
			case "data":
				data = append(append(data, value...), '\n')
			case "event":
				event = string(value)
			}
		}
		if scanner.Err() != nil || len(data) != 0 {
			r.Invalid = true
		}
		if r.Terminal == "none" && provisionalError {
			r.Terminal, r.Signature, r.ErrorClass = "error", provisionalSignature, "upstream_error"
		}
	}
	if status < 200 || status >= 300 {
		r.Completed = false
		if status == http.StatusBadRequest {
			r.ErrorClass = "request_rejected"
		} else {
			r.ErrorClass = "upstream_error"
		}
	}
	if r.Signature {
		r.ErrorClass = "signature_rejection"
	}
	if r.Invalid {
		r.Completed, r.Signature, r.ErrorClass = false, false, "invalid_response"
	}
	if r.Terminal == "none" {
		r.Completed = false
	}
	return r
}

func continuationRepairDocument(object map[string]json.RawMessage) continuationRepairResponse {
	r := continuationRepairResponse{Terminal: "none"}
	for name, target := range map[string]*string{"status": &r.Status, "model": &r.Model, "id": &r.ID} {
		if raw, present := object[name]; present && json.Unmarshal(raw, target) != nil {
			r.Invalid = true
		}
	}
	if raw, present := object["output"]; present {
		if json.Unmarshal(raw, &r.Output) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			r.Invalid = true
		}
	}
	r.Completed = r.Status == "completed"
	if r.Completed {
		r.Terminal = "response.completed"
		if _, present := object["output"]; !present {
			r.Invalid = true
		}
	} else if r.Status == "failed" || r.Status == "incomplete" {
		r.Terminal, r.ErrorClass = "response."+r.Status, "upstream_error"
	}
	for _, field := range []string{"error", "incomplete_details"} {
		if value := bytes.TrimSpace(object[field]); len(value) > 0 && !bytes.Equal(value, []byte("null")) {
			r.Completed, r.ErrorClass = false, "upstream_error"
		}
	}
	for _, raw := range r.Output {
		item, err := continuationRepairObject(raw)
		if err != nil {
			r.Invalid = true
			continue
		}
		r.Semantic = true
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		var parts []map[string]json.RawMessage
		if content, present := item["content"]; present && kind == "message" {
			if json.Unmarshal(content, &parts) != nil {
				r.Invalid = true
			}
			for _, part := range parts {
				var kind, text string
				_ = json.Unmarshal(part["type"], &kind)
				_ = json.Unmarshal(part["text"], &text)
				if kind == "output_text" {
					r.Text += text
				}
				if kind == "refusal" {
					r.Completed, r.ErrorClass = false, "upstream_error"
				}
			}
		}
	}
	if raw := object["usage"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		usage, err := continuationRepairObject(raw)
		if err != nil {
			r.Invalid = true
		} else {
			r.Usage = make(map[string]int64)
			for _, name := range []string{"input_tokens", "output_tokens", "total_tokens"} {
				if value, present := usage[name]; present {
					var count int64
					if json.Unmarshal(value, &count) != nil || count < 0 || count > 1_000_000_000 {
						r.Invalid = true
					} else {
						r.Usage[name] = count
					}
				}
			}
		}
	}
	return r
}

func continuationRepairSignature(object map[string]json.RawMessage, status int) bool {
	protected := func(status int) bool {
		return status == 401 || status == 402 || status == 403 || status == 407 || status == 429
	}
	if protected(status) {
		return false
	}
	var kind, state string
	_ = json.Unmarshal(object["type"], &kind)
	_ = json.Unmarshal(object["status"], &state)
	if kind != "" && kind != "error" && kind != "response.failed" && kind != "response.done" || state != "" && state != "failed" {
		return false
	}
	errorObject, _ := continuationRepairObject(object["error"])
	response, _ := continuationRepairObject(object["response"])
	if response != nil {
		var responseStatus string
		_ = json.Unmarshal(response["status"], &responseStatus)
		if kind != "response.failed" && responseStatus != "failed" {
			return false
		}
		errorObject, _ = continuationRepairObject(response["error"])
	} else if errorObject == nil && kind == "error" {
		errorObject = object
	}
	var code string
	if json.Unmarshal(errorObject["code"], &code) != nil || (code != "invalid_encrypted_content" && code != "thinking_signature_invalid") {
		return false
	}
	for _, candidate := range []map[string]json.RawMessage{object, errorObject} {
		for _, field := range []string{"status_code", "status"} {
			var number int
			if json.Unmarshal(candidate[field], &number) == nil && protected(number) {
				return false
			}
			var text string
			if json.Unmarshal(candidate[field], &text) == nil && (text == "401" || text == "402" || text == "403" || text == "407" || text == "429") {
				return false
			}
		}
	}
	return true
}

func continuationRepairHasTool(output []json.RawMessage) bool {
	for _, raw := range output {
		object, err := continuationRepairObject(raw)
		if err != nil {
			return true
		}
		var kind string
		_ = json.Unmarshal(object["type"], &kind)
		if strings.HasSuffix(kind, "_call") || strings.HasSuffix(kind, "_call_output") || strings.HasPrefix(kind, "mcp_") {
			return true
		}
	}
	return false
}

func continuationRepairReadJSON(reader *bufio.Reader, target any) error {
	line, err := reader.ReadBytes('\n')
	if err != nil || len(line) > continuationRepairMaxBody || !hasUniqueJSONMembers(line) {
		return errors.New("broker_unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("broker_unavailable")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errors.New("broker_unavailable")
	}
	return nil
}

func continuationRepairObject(raw []byte) (map[string]json.RawMessage, error) {
	if !hasUniqueJSONMembers(raw) {
		return nil, errors.New("invalid_response")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("invalid_response")
	}
	return object, nil
}

func continuationRepairJSONEqual(left, right []byte) bool {
	decode := func(raw []byte) (any, bool) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value, trailing any
		if decoder.Decode(&value) != nil || decoder.Decode(&trailing) != io.EOF {
			return nil, false
		}
		return value, true
	}
	a, okA := decode(left)
	b, okB := decode(right)
	return okA && okB && reflect.DeepEqual(a, b)
}

func continuationRepairHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func continuationRepairHashJSON(value any) string {
	raw, _ := json.Marshal(value)
	return continuationRepairHash(raw)
}

func continuationRepairMarshal(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

// This is the sole offline harness smoke test. Its upstream has no network
// client, and its broker grants are an in-memory serial transcript. It proves
// the complete fixture can route, normalize, attribute billing and parse Ops
// before a sealed live run spends any of its four approved requests.
func TestContinuationRepairOffline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Run("capture_close_unblocks_existing_reader", func(t *testing.T) {
		body := &continuationRepairOfflineBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
		finished := make(chan bool, 2)
		capture := &continuationRepairCapture{ReadCloser: body, cleanup: func() {}, finish: func(_ []byte, incomplete, eof bool) { finished <- incomplete || eof }}
		go func() { _, _ = capture.Read(make([]byte, 1)) }()
		select {
		case <-body.started:
		case <-ctx.Done():
			t.Fatal("offline capture reader did not start")
		}
		closed := make(chan struct{})
		go func() { _ = capture.Close(); _ = capture.Close(); close(closed) }()
		select {
		case <-closed:
		case <-ctx.Done():
			t.Fatal("offline capture close did not unblock its reader")
		}
		if len(finished) != 1 || <-finished {
			t.Fatal("local close must report one frozen capture without inventing EOF")
		}
	})
	if err := continuationRepairPreflight(ctx, &config.Config{RunMode: config.RunModeStandard}, continuationRepairOfflineSource()); err != nil {
		t.Fatal(err)
	}
}

func continuationRepairOfflineSource() continuationRepairSource {
	return continuationRepairSource{
		Account: service.Account{ID: 16050, Name: "白嫖-dmxapi", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{35},
			Credentials: map[string]any{"api_key": "offline-only", "base_url": "https://continuation.example.test",
				"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra-ssvip", "gpt-5.6-sol": "gpt-5.6-sol"}},
			Extra: map[string]any{"use_responses_api": true}},
		Group:      service.Group{ID: 35, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1},
		FastPolicy: service.DefaultOpenAIFastPolicySettings(), Settings: map[string]string{},
		BusinessPrompt: service.BusinessSystemPromptSnapshot{Revision: 1, CompositionMode: service.BusinessSystemPromptCompositionCodexSkillHybrid},
		ChannelModels:  map[string]string{"gpt-6-astra": "gpt-6-astra", "gpt-5.6-sol": "gpt-5.6-sol"},
		Fingerprint:    strings.Repeat("a", 64), UserID: 920000016050,
	}
}

// Exercise frozen production policy through the complete handler using only
// in-memory grants and synthetic responses. This object cannot open a socket;
// none of its requests, usage rows or counters belong to the live broker.
func continuationRepairPreflight(ctx context.Context, cfg *config.Config, source continuationRepairSource) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint, err := continuationRepairEndpoint(&source.Account)
	if err != nil {
		return err
	}
	var grants, protocol bytes.Buffer
	encoder := json.NewEncoder(&grants)
	for i, scenario := range []string{"astra", "sol"} {
		for turn := 1; turn <= 2; turn++ {
			request := i*2 + turn
			_ = encoder.Encode(map[string]any{"type": "turn_granted", "scenario": scenario, "turn": turn, "request": request, "fingerprint": source.Fingerprint})
			_ = encoder.Encode(map[string]any{"type": "send_granted", "scenario": scenario, "turn": turn, "request": request, "slot": request,
				"attempt": request, "attempt_in_turn": 1, "fingerprint": source.Fingerprint, "elapsed_ms": 0})
		}
	}
	h := &continuationRepairHarness{boot: continuationRepairBootstrap{Source: source}, input: bufio.NewReader(&grants), output: json.NewEncoder(&protocol),
		ctx: ctx, finished: make(map[string]bool), sourceHash: continuationRepairHashJSON(source)}
	fake := &continuationRepairOfflineUpstream{endpoint: endpoint}
	h.upstream = &continuationRepairUpstream{h: h, inner: fake, endpoint: endpoint,
		authorizationHash: continuationRepairHash([]byte("Bearer " + source.Account.GetOpenAIProtocolAPIKey()))}
	if source.Account.Proxy != nil {
		h.upstream.proxy = source.Account.Proxy.URL()
	}
	h.fixture, err = continuationRepairBuildFixture(ctx, cfg, source, h.upstream)
	if err != nil {
		return errors.New("handler_failed")
	}
	defer h.fixture.close()
	opsErrorLogOnce.Do(func() {})
	opsErrorLogMu.Lock()
	opsErrorLogQueue = make(chan opsErrorLogJob, 32)
	opsErrorLogMu.Unlock()
	for _, scenario := range []string{"astra", "sol"} {
		initial := continuationRepairInitialBody(continuationRepairModel(scenario))
		response, result := h.runTurn(scenario, 1, initial)
		if result.Status != "completed" {
			return continuationRepairPreflightError(result.ErrorClass)
		}
		body, err := continuationRepairContinuation(initial, response, scenario)
		if err != nil {
			return errors.New("handler_failed")
		}
		_, result = h.runTurn(scenario, 2, body)
		if result.Status != "completed" || !result.HeartbeatNormalized || !result.HistoryPreserved || result.BillingRows != 1 {
			return continuationRepairPreflightError(result.ErrorClass)
		}
	}
	if h.requests != 4 || h.upstream.attempts != 4 || fake.calls != 4 || h.upstream.blocked != 0 || grants.Len() != 0 || h.stop != "" {
		return errors.New("handler_failed")
	}
	counts := make(map[string]int)
	decoder := json.NewDecoder(&protocol)
	for decoder.More() {
		var event map[string]json.RawMessage
		if decoder.Decode(&event) != nil {
			return errors.New("handler_failed")
		}
		var kind string
		_ = json.Unmarshal(event["type"], &kind)
		counts[kind]++
	}
	for _, kind := range []string{"before_turn", "before_send", "attempt_result", "turn_result"} {
		if counts[kind] != 4 {
			return errors.New("handler_failed")
		}
	}
	return nil
}

func TestContinuationRepairPreflightFrozenPolicy(t *testing.T) {
	for _, override := range []bool{false, true} {
		name := "global_bridge_rejected_before_send"
		if override {
			name = "channel_disable_overrides_global"
		}
		t.Run(name, func(t *testing.T) {
			source := continuationRepairOfflineSource()
			source.Group.AllowImageGeneration = true
			if override {
				disabled := false
				source.ChannelImageBridge = &disabled
			}
			cfg := &config.Config{RunMode: config.RunModeStandard, Gateway: config.GatewayConfig{CodexImageGenerationBridgeEnabled: true}}
			beforeSource, beforeConfig := continuationRepairHashJSON(source), continuationRepairHashJSON(cfg)
			err := continuationRepairPreflight(context.Background(), cfg, source)
			if override {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var wireError continuationRepairWireError
				if !errors.As(err, &wireError) || wireError != "wire_instructions_mismatch" {
					t.Fatalf("unexpected preflight classification: %v", err)
				}
			}
			if continuationRepairHashJSON(source) != beforeSource || continuationRepairHashJSON(cfg) != beforeConfig {
				t.Fatal("preflight changed frozen source or configuration")
			}
		})
	}
}

func TestContinuationRepairWireMismatchClassification(t *testing.T) {
	initial := continuationRepairInitialBody("gpt-6-astra")
	t.Run("invalid_json", func(t *testing.T) {
		u := &continuationRepairUpstream{body: initial, identity: continuationRepairIdentity{Turn: 1, WireModel: "gpt-6-astra"}}
		if err := u.validateWire([]byte(`{"model":"gpt-6-astra","model":"gpt-6-astra"}`), 1); err == nil || err.Error() != "wire_json_mismatch" {
			t.Fatalf("unexpected JSON classification: %v", err)
		}
	})
	for _, test := range []struct{ name, field, value, class string }{
		{"model", "model", `"gpt-5.6-sol"`, "wire_model_mismatch"},
		{"stream", "stream", "false", "wire_stream_mismatch"},
		{"store", "store", "true", "wire_store_mismatch"},
		{"instructions", "instructions", `"different"`, "wire_instructions_mismatch"},
		{"unknown_input_field", "input", `[{"role":"user","content":"Call continuation_probe_value exactly once with an empty JSON object. Do not answer yet.","unknown":9007199254740993}]`, "wire_input_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields, _ := continuationRepairObject(initial)
			fields[test.field] = json.RawMessage(test.value)
			u := &continuationRepairUpstream{body: initial, identity: continuationRepairIdentity{Turn: 1, WireModel: "gpt-6-astra"}}
			err := u.validateWire(continuationRepairMarshal(fields), 1)
			if err == nil || err.Error() != test.class {
				t.Fatalf("unexpected wire classification: %v", err)
			}
		})
	}
	first := []byte(`{"model":"gpt-6-astra","stream":true,"store":false,"input":[{"type":"reasoning","encrypted_content":"opaque","unknown":9007199254740993}],"unknown":{"large":9007199254740993}}`)
	u := &continuationRepairUpstream{firstSent: first, identity: continuationRepairIdentity{Turn: 2, WireModel: "gpt-6-astra"}}
	allowed := bytes.Replace(first, []byte(`"encrypted_content":"opaque",`), nil, 1)
	if err := u.validateWire(allowed, 2); err != nil {
		t.Fatal(err)
	}
	for _, changed := range [][]byte{
		bytes.Replace(first, []byte("opaque"), []byte("changed"), 1),
		bytes.Replace(allowed, []byte("9007199254740993"), []byte("9007199254740992"), 1),
		bytes.ReplaceAll(allowed, []byte(`"unknown"`), []byte(`"changed"`)),
	} {
		if err := u.validateWire(changed, 2); err == nil || err.Error() != "wire_recovery_mismatch" {
			t.Fatalf("recovery accepted a change beyond ciphertext omission: %v", err)
		}
	}
}

func continuationRepairPreflightError(class string) error {
	switch class {
	case "wire_json_mismatch", "wire_model_mismatch", "wire_stream_mismatch", "wire_store_mismatch",
		"wire_instructions_mismatch", "wire_input_mismatch", "wire_recovery_mismatch":
		return continuationRepairWireError(class)
	default:
		return errors.New("handler_failed")
	}
}

type continuationRepairOfflineUpstream struct {
	calls    int
	endpoint string
}

func (u *continuationRepairOfflineUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	if req == nil || req.URL == nil || req.URL.String() != u.endpoint || accountID != 16050 || req.GetBody != nil {
		return nil, errors.New("offline_transport_contract")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	fields, err := continuationRepairObject(body)
	if err != nil {
		return nil, err
	}
	var model string
	_ = json.Unmarshal(fields["model"], &model)
	u.calls++
	var output []json.RawMessage
	if u.calls%2 == 1 {
		output = []json.RawMessage{
			json.RawMessage(`{"type":"reasoning","id":"rs_offline","summary":[],"encrypted_content":"offline-opaque"}`),
			json.RawMessage(`{"type":"function_call","id":"fc_offline","call_id":"call_offline","name":"continuation_probe_value","arguments":"{}","status":"completed","namespace":null,"unknown_vendor_field":{"large_integer":9007199254740993}}`),
		}
	} else {
		output = []json.RawMessage{json.RawMessage(`{"type":"message","id":"msg_offline","role":"assistant","status":"completed","content":[{"type":"output_text","text":"CONTINUATION_OK","annotations":[]}]}`)}
	}
	payload := continuationRepairMarshal(map[string]any{"type": "response.completed", "response": map[string]any{
		"id": "resp_offline_" + strconv.Itoa(u.calls), "status": "completed", "model": model, "output": output,
		"usage": map[string]int{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6}}})
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"offline-" + strconv.Itoa(u.calls)}},
		Body: io.NopCloser(bytes.NewReader(append(append([]byte("event: response.completed\ndata: "), payload...), '\n', '\n')))}, nil
}

func (u *continuationRepairOfflineUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

type continuationRepairOfflineBlockingBody struct {
	started, closed chan struct{}
	start, stop     sync.Once
}

func (b *continuationRepairOfflineBlockingBody) Read([]byte) (int, error) {
	b.start.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *continuationRepairOfflineBlockingBody) Close() error {
	b.stop.Do(func() { close(b.closed) })
	return nil
}
