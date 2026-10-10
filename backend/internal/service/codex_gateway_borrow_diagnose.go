package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const CodexBorrowDiagnosticMaxRequests = 8

var ErrCodexBorrowDiagnosticBudget = errors.New("diagnostic request budget exhausted (including preparation)")

type codexBorrowDiagnosticContextKey struct{}
type codexBorrowAppliedContextKey struct{}

type codexBorrowDiagnostic struct {
	scenario     string
	serviceTier  string
	history      []any
	turnState    string
	verification atomic.Pointer[CodexGatewayBorrowVerification]
	limit        int32
	onRequest    func(int32)
	requests     *atomic.Int32
	borrow       bool
	transport    string
	session      string
	anchors      codexGatewayBorrowWSAnchorStore
}

func borrowDiagnosticFromContext(ctx context.Context) *codexBorrowDiagnostic {
	if ctx == nil {
		return nil
	}
	d, _ := ctx.Value(codexBorrowDiagnosticContextKey{}).(*codexBorrowDiagnostic)
	return d
}

func consumeBorrowDiagnosticRequest(ctx context.Context) error {
	d := borrowDiagnosticFromContext(ctx)
	if d == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		count := d.requests.Load()
		limit := d.limit
		if limit == 0 {
			limit = CodexBorrowDiagnosticMaxRequests
		}
		if count >= limit {
			return ErrCodexBorrowDiagnosticBudget
		}
		if d.requests.CompareAndSwap(count, count+1) {
			if d.onRequest != nil {
				d.onRequest(count + 1)
			}
			return nil
		}
	}
}

type CodexBorrowDiagnosticRequest struct {
	Comparison   string `json:"comparison,omitempty"`
	Scenario     string `json:"scenario,omitempty"`
	Mode         string `json:"mode,omitempty"`
	ServiceTier  string `json:"service_tier,omitempty"`
	RequestLimit int    `json:"request_limit,omitempty"`
	AccountID    int64  `json:"account_id"`
	Model        string `json:"model"`
	Transport    string `json:"transport"`
}

type CodexBorrowDiagnosticResult struct {
	ProbeOnly     bool                            `json:"probe_only,omitempty"`
	ProbeContext  *CodexBorrowProbeContext        `json:"probe_context,omitempty"`
	Scenario      string                          `json:"scenario,omitempty"`
	FailureStage  string                          `json:"failure_stage,omitempty"`
	FailureReason string                          `json:"failure_reason,omitempty"`
	Verification  *CodexGatewayBorrowVerification `json:"verification,omitempty"`
	ToolRoundTrip bool                            `json:"tool_round_trip,omitempty"`
	StateLength   int                             `json:"state_length,omitempty"`
	ReadError     string                          `json:"read_error,omitempty"`
	Dispatched    bool                            `json:"dispatched"`
	Mode          string                          `json:"mode"`
	Turn          int                             `json:"turn"`
	Applied       bool                            `json:"applied"`
	Completed     bool                            `json:"completed"`
	ResponseID    string                          `json:"response_id,omitempty"`
	ReportedModel string                          `json:"reported_model,omitempty"`
	Answer        string                          `json:"answer"`
	RawResponse   string                          `json:"raw_response"`
	Error         string                          `json:"error,omitempty"`
	DurationMS    int64                           `json:"duration_ms"`
}

type CodexBorrowDiagnosticEvent struct {
	Type     string                       `json:"type"`
	Mode     string                       `json:"mode,omitempty"`
	Requests int32                        `json:"requests"`
	Limit    int                          `json:"limit"`
	Result   *CodexBorrowDiagnosticResult `json:"result,omitempty"`
	Error    string                       `json:"error,omitempty"`
}

// Diagnose runs fixed short questions through the normal account sender. The
// internal observation purpose isolates health/billing; it is never user input.
func (s *CodexGatewayBorrowService) Diagnose(ctx context.Context, request CodexBorrowDiagnosticRequest, emit func(CodexBorrowDiagnosticEvent)) error {
	if request.Scenario != "" && request.Scenario != "codex_session" && request.Scenario != "probe_contract" {
		return errors.New("scenario must be empty, codex_session or probe_contract")
	}
	if request.Mode != "" && request.Mode != "ordinary" && request.Mode != "borrowed" {
		return errors.New("mode must be empty, ordinary or borrowed")
	}
	if request.Scenario == "codex_session" && request.Transport != "http" {
		return errors.New("codex_session uses HTTP; existing WS diagnostics already cover continuation")
	}
	if request.Scenario == "probe_contract" && (request.Transport != "http" || request.Mode == "ordinary") {
		return errors.New("probe_contract requires borrowed HTTP observations")
	}
	if request.Comparison != "" && (request.Scenario != "probe_contract" || (request.Comparison != "body" && request.Comparison != "encoding")) {
		return errors.New("comparison must be body or encoding in probe_contract")
	}
	if request.ServiceTier != "" && request.ServiceTier != "default" && request.ServiceTier != OpenAIFastTierPriority && request.ServiceTier != OpenAIFastTierFlex && request.ServiceTier != OpenAIFastTierUltrafast {
		return errors.New("unsupported service_tier")
	}
	if request.AccountID <= 0 || (request.Transport != "http" && request.Transport != "ws") {
		return errors.New("account_id and transport (http or ws) are required")
	}
	limit := request.RequestLimit
	if limit == 0 {
		limit = CodexBorrowDiagnosticMaxRequests
	}
	if limit < 1 || limit > CodexBorrowDiagnosticMaxRequests {
		return errors.New("request_limit must be between 1 and 8")
	}
	cfg := s.ConfigSnapshot()
	if !cfg.Enabled || !slices.Contains(cfg.TargetAccountIDs, request.AccountID) || !slices.Contains(cfg.Models, request.Model) {
		return errors.New("select a saved, enabled target and model")
	}
	account, err := s.accounts.GetByID(ctx, request.AccountID)
	if err != nil || !codexGatewayBorrowAccountSupported(account) {
		return errors.New("selected account is unavailable")
	}
	if account.Status != StatusActive || !account.IsModelSupported(request.Model) || normalizeOpenAIModelForUpstream(account, account.GetMappedModel(request.Model)) != request.Model {
		return errors.New("selected account is inactive or maps the requested model to another model")
	}
	if request.Transport == "ws" && s.gateway.getOpenAIWSProtocolResolver().Resolve(account).Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
		return errors.New("WebSocket is not enabled for this account; settings were not changed")
	}
	s.mu.Lock()
	revisionCtx := s.revisionCtx
	revision := s.revision
	s.mu.Unlock()
	ctx, cancel := borrowRevisionContext(WithAccountObservation(ctx), revisionCtx, 10*time.Minute)
	defer cancel()
	if request.Scenario == "probe_contract" {
		return s.diagnoseProbeContract(ctx, revision, revisionCtx, request, limit, emit)
	}
	var requests atomic.Int32
	modes := []bool{false, true}
	if request.Mode == "borrowed" || (request.Scenario == "codex_session" && request.Mode == "") {
		modes = []bool{true}
	} else if request.Mode == "ordinary" {
		modes = []bool{false}
	}
	for _, borrowed := range modes {
		if err := ctx.Err(); err != nil {
			return err
		}
		mode := "ordinary"
		if borrowed {
			mode = "borrowed"
		}
		d := &codexBorrowDiagnostic{limit: int32(limit), onRequest: func(count int32) {
			emit(CodexBorrowDiagnosticEvent{Type: "request", Mode: mode, Requests: count, Limit: limit})
		}, requests: &requests, borrow: borrowed, transport: request.Transport, session: uuid.NewString(), scenario: request.Scenario, serviceTier: request.ServiceTier}
		operation := context.WithValue(ctx, codexBorrowDiagnosticContextKey{}, d)
		emit(CodexBorrowDiagnosticEvent{Type: "phase", Mode: mode, Requests: requests.Load(), Limit: limit})
		previous := ""
		turns := 1
		if request.Transport == "ws" || request.Scenario == "codex_session" {
			turns = 2
		}
		for turn := 1; turn <= turns; turn++ {
			result := s.diagnoseTurn(operation, account, request.Model, previous)
			result.Mode, result.Turn = mode, turn
			emit(CodexBorrowDiagnosticEvent{Type: "result", Mode: mode, Result: &result, Requests: requests.Load(), Limit: limit})
			if !result.Completed || result.Error != "" {
				break
			}
			previous = result.ResponseID
			if request.Transport == "ws" && previous == "" {
				break
			}
		}
		d.anchors.mu.Lock()
		for key, entry := range d.anchors.entries {
			if entry.connID != "" {
				s.gateway.getOpenAIWSConnPool().evictConn(key.account, entry.connID)
			}
		}
		d.anchors.mu.Unlock()
		if requests.Load() >= int32(limit) {
			break
		}
	}
	emit(CodexBorrowDiagnosticEvent{Type: "done", Requests: requests.Load(), Limit: limit})
	return ctx.Err()
}

func (s *CodexGatewayBorrowService) diagnoseTurn(ctx context.Context, account *Account, model, previous string) CodexBorrowDiagnosticResult {
	start := time.Now()
	result := CodexBorrowDiagnosticResult{}
	state := newPelicanExecutionState(ctx, sharedPelicanExecution, 90*time.Second, time.Now)
	ctx = context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
	defer state.finish()
	BeginPelicanPreparation(ctx)
	capture := &pelicanGenerationCapture{account: account, concurrency: s.gateway.concurrencyService, openaiGateway: s.gateway, fallbackModel: model}
	ctx = context.WithValue(ctx, pelicanGenerationContextKey{}, capture)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	capture.ginContext = c
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/admin/codex-diagnostic", nil).WithContext(ctx)
	d := borrowDiagnosticFromContext(ctx)
	d.verification.Store(nil)
	c.Request.Header.Set("session_id", d.session)
	if d.transport == "ws" {
		SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	} else {
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	}
	payload := map[string]any{"model": model, "stream": true, "store": false, "input": []map[string]any{{"role": "user", "content": []map[string]string{{"type": "input_text", "text": "Reply with exactly OK."}}}}}
	if previous != "" {
		payload["previous_response_id"] = previous
	}
	if levels, _ := AccountTestReasoningOptions(account, model); slices.Contains(levels, "low") {
		payload["reasoning"] = map[string]string{"effort": "low"}
	}
	if d.serviceTier != "" {
		payload["service_tier"] = d.serviceTier
	}
	if d.scenario == "codex_session" {
		d.prepareHTTPSessionTurn(c.Request.Header, payload)
	}
	body, _ := json.Marshal(payload)
	forward, sendErr := s.gateway.Forward(ctx, c, account, body)
	result.Scenario, result.Verification = d.scenario, d.verification.Load()
	if sendErr != nil {
		result.FailureStage, result.FailureReason = codexBorrowFailureStage(sendErr)
		if result.Verification != nil && !result.Verification.Success {
			result.FailureStage, result.FailureReason = "target_validation", result.Verification.Reason
		} else if !IsCodexGatewayBorrowRequestFailure(sendErr) {
			result.FailureStage, result.FailureReason = "upstream", "request_failed"
		}
	}
	result.DurationMS = time.Since(start).Milliseconds()
	if forward != nil {
		result.ResponseID = firstNonEmpty(forward.ResponseID, forward.RequestID)
		result.ReportedModel = forward.UpstreamResponseModel
	}
	last := snapshotPelicanAttempt(capture)
	if last == nil {
		result.RawResponse = w.Body.String()
		if sendErr != nil {
			result.Error = sendErr.Error()
		} else {
			result.Error = "no upstream request was dispatched"
		}
		return result
	}
	result.Dispatched = last.dispatched
	if last.err != nil {
		result.ReadError = last.err.Error()
	}
	result.RawResponse = last.raw.String()
	if d.scenario == "codex_session" && last.dispatched {
		state.mu.Lock()
		result.Applied = state.invocation.BorrowApplied
		state.mu.Unlock()
		result.StateLength = len(extractOpenAICodexTurnState(w.Header()))
		d.turnState = extractOpenAICodexTurnState(w.Header())
		d.completeHTTPSessionTurn(&result, sendErr, last.status)
		return result
	}
	if last.wsFrames && last.raw.Len() == 0 && sendErr != nil {
		var eventErr *openAIWSUpstreamEventError
		if errors.As(sendErr, &eventErr) && len(eventErr.payload) > 0 {
			result.RawResponse = string(eventErr.payload)
			result.Error = string(eventErr.payload)
		} else {
			result.Error = sendErr.Error()
		}
		return result
	}

	var parseErr error
	var limited bool
	if last.wsFrames {
		result.Answer, limited, result.ReportedModel, parseErr = parsePelicanWSFrames(result.RawResponse)
	} else {
		result.Answer, limited, result.ReportedModel, parseErr = parsePelicanTextResponse(last.protocol, result.RawResponse)
	}
	state.mu.Lock()
	result.Applied = state.invocation.BorrowApplied
	state.mu.Unlock()
	// A valid completed terminal with text is authoritative. Read-ahead cleanup
	// (often context cancellation after that terminal) cannot turn it into failure.
	result.Completed = sendErr == nil && parseErr == nil && !limited && last.status >= 200 && last.status < 300 && strings.TrimSpace(result.Answer) != ""
	if sendErr != nil {
		result.Error = sendErr.Error()
	} else if parseErr != nil {
		result.Error = parseErr.Error()
	} else if !result.Completed {
		result.Error = "upstream did not complete with visible text"
	}
	return result
}

func (d *codexBorrowDiagnostic) prepareWS(ctx context.Context, s *CodexGatewayBorrowService, req *http.Request, account *Account, model, previous, proxy string) (*codexGatewayBorrowWSTurn, error) {
	key := codexGatewayBorrowWSAnchorKey{account: account.ID, model: model, scope: "diagnostic:" + d.session}
	turn, err := d.anchors.begin(key, previous, time.Now())
	if err != nil {
		return nil, err
	}
	turn.diagnostic, turn.diagnosticApplied = true, d.borrow
	turn.headers = cloneHeader(req.Header)
	if previous == "" && d.borrow {
		borrowed, application, err := s.Apply(req, account, model, proxy, nil, false)
		if err != nil {
			turn.finish(nil, nil, err)
			return nil, err
		}
		if application == nil || !application.Applied {
			turn.finish(nil, nil, ErrCodexGatewayBorrowUnavailable)
			return nil, ErrCodexGatewayBorrowUnavailable
		}
		turn.headers = cloneHeader(borrowed.Header)
	}
	return turn, nil
}

func (turn *codexGatewayBorrowWSTurn) borrowed() bool {
	return turn != nil && (!turn.diagnostic || turn.diagnosticApplied)
}
