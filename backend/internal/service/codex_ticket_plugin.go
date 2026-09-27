package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// The compatibility facade is the only host location that identifies this
// first-party domain. Acquisition, renewal and validation execute in its plugin.
const codexRuntimePluginKey = "codexrip.codex-runtime"

func (s *OpenAIGatewayService) CodexTicketStatuses(account *Account) []OpenAICodexTicketStatus {
	return OpenAICodexTicketStatuses(account, s.openAICodexTicketConfig(), time.Now())
}

func (s *OpenAIGatewayService) openAICodexTicketConfig() config.OpenAICodexTicketConfig {
	cfg := config.OpenAICodexTicketConfig{FailClosed: true, Models: []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}}
	if s == nil || s.nativeCodexRuntime == nil {
		return cfg
	}
	if snapshot := s.nativeCodexRuntime.current(); snapshot != nil {
		cfg.Enabled, cfg.FailClosed, cfg.HarvestProxyURL = snapshot.config.Enabled, snapshot.config.FailClosed, snapshot.config.ProxyURL
		cfg.Models = append([]string(nil), snapshot.config.Models...)
	}
	return cfg
}

func (s *OpenAIGatewayService) openAICodexTicketEnabledContext(context.Context) bool {
	return s.openAICodexTicketConfig().Enabled
}

func (s *OpenAIGatewayService) applyOpenAICodexTicket(ctx context.Context, account *Account, model string, headers http.Header) error {
	if IsCodexQualityRequest(ctx) {
		_, _, err := codexQualityRequestQualification(ctx, account, model)
		return err
	}
	if s == nil || s.nativeCodexRuntime == nil {
		return nil
	}
	if err := s.nativeCodexRuntime.ApplyRequestHeaders(ctx, account, model, headers); err != nil {
		var result *NativeCodexResultError
		if errors.As(err, &result) {
			// The runtime's result code and reason stay inspectable (errors.As).
			return fmt.Errorf("%w: %w", ErrOpenAICodexTicketUnavailable, result)
		}
		// Other causes are kept as text; only the sentinel stays in the chain.
		return fmt.Errorf("%w: extension request prerequisites: %v", ErrOpenAICodexTicketUnavailable, err)
	}
	return nil
}

func (s *OpenAIGatewayService) openAICodexTicketBlocksAccount(account *Account, model string) bool {
	if s != nil && s.nativeCodexRuntime != nil {
		s.nativeCodexRuntime.noteCodexRoutingDemand(account, model)
	}
	return s != nil && s.nativeCodexRuntime != nil && !s.nativeCodexRuntime.SchedulingDecision(account, model, time.Now()).Allowed
}

func codexTicketFailureWithError(code, detail string) CodexTicketResult {
	result := CodexTicketFailure(code)
	result.Error = detail
	return result
}

// codexTicketInvokeFailure keeps the runtime error text and only chooses the
// catalog code that describes where it happened. Only the job's own context
// decides cancellation: a context error while the job is still running comes
// from the runtime stopping or reloading underneath it.
func codexTicketInvokeFailure(ctx context.Context, err error) CodexTicketResult {
	switch {
	case ctx.Err() != nil:
		return codexTicketFailureWithError("ticket_canceled", err.Error())
	case errors.Is(err, context.Canceled):
		return codexTicketFailureWithError("ticket_plugin_unavailable", "the Codex runtime stopped or reloaded during the operation: "+err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return codexTicketFailureWithError("ticket_timeout", err.Error())
	case errors.Is(err, ErrNativeCodexRuntimeUnavailable), errors.Is(err, ErrNativeCodexPolicyDisabled), errors.Is(err, ErrNativeCodexRuntimeChanged):
		return codexTicketFailureWithError("ticket_plugin_unavailable", err.Error())
	}
	return codexTicketFailureWithError("ticket_runtime_error", err.Error())
}

func (s *OpenAIGatewayService) HarvestCodexTicket(ctx context.Context, id int64, model, operation string, jobID int64, force bool) CodexTicketResult {
	if s == nil || s.nativeCodexRuntime == nil {
		return codexTicketFailureWithError("ticket_plugin_unavailable", "native Codex runtime is not configured")
	}
	installation := s.nativeCodexRuntime.metadata()
	if installation == nil {
		return codexTicketFailureWithError("ticket_plugin_unavailable", "native Codex runtime is not loaded")
	}
	if jobID > 0 {
		operation = fmt.Sprintf("job-%d", jobID)
	}
	payload, _ := json.Marshal(map[string]any{"account_id": id, "model": model, "operation_id": operation, "force": force})
	response, err := s.nativeCodexRuntime.Invoke(ctx, PlatformOpenAI, "", extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "harvest", AccountID: id, Payload: payload})
	if err != nil {
		return codexTicketInvokeFailure(ctx, err)
	}
	var result CodexTicketResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return codexTicketFailureWithError("ticket_result_invalid", err.Error())
	}
	if result.Stage == "" {
		result.Stage = CodexTicketFailure(result.Code).Stage
	}
	if observation := result.Observation; observation != nil {
		if result.ResponseModel == "" {
			result.ResponseModel = observation.ResponseModel
		}
		if result.DurationMS == 0 {
			result.DurationMS = observation.DurationMS
		}
		if result.Error == "" && !result.Success {
			result.Error = observation.Summary()
		}
	}
	return result
}

func (s *OpenAIGatewayService) StopCodexTicketRenewal(ctx context.Context, id int64, models []string) error {
	if s == nil || s.nativeCodexRuntime == nil {
		return errors.New("ticket plugin unavailable")
	}
	installation := s.nativeCodexRuntime.metadata()
	if installation == nil {
		return errors.New("ticket plugin unavailable")
	}
	for _, model := range models {
		raw, _ := json.Marshal(map[string]any{"account_id": id, "model": model})
		response, err := s.nativeCodexRuntime.Invoke(ctx, PlatformOpenAI, "", extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "stop", AccountID: id, Payload: raw})
		if err != nil {
			return fmt.Errorf("stop renewal of %s: %w", model, err)
		}
		var result CodexTicketResult
		if err := json.Unmarshal(response.Payload, &result); err != nil {
			return fmt.Errorf("stop renewal of %s: decode runtime result %s: %w", model, string(response.Payload), err)
		}
		if !result.Success {
			return fmt.Errorf("stop renewal of %s: runtime returned %s: %s", model, result.Code, string(response.Payload))
		}
	}
	return nil
}

func (s *OpenAIGatewayService) TestCodexTicketProxy(ctx context.Context, raw string) (*CodexTicketProxyTestResult, error) {
	return s.TestCodexTicketProxyWithProtocol(ctx, raw, "")
}

func (s *OpenAIGatewayService) TestCodexTicketProxyWithProtocol(ctx context.Context, raw, protocol string) (*CodexTicketProxyTestResult, error) {
	if s == nil || s.nativeCodexRuntime == nil {
		return nil, errors.New("ticket plugin unavailable")
	}
	installation := s.nativeCodexRuntime.metadata()
	if installation == nil {
		return nil, errors.New("ticket plugin unavailable")
	}
	payload, _ := json.Marshal(map[string]string{"proxy_url": raw, "protocol": protocol})
	response, err := s.nativeCodexRuntime.Invoke(ctx, PlatformOpenAI, "", extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "proxy.test", Payload: payload})
	if err != nil {
		return nil, err
	}
	var result CodexTicketProxyTestResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode proxy test result: %w", err)
	}
	if result.NetworkReachable && (result.Code == "proxy_reachable" || result.Code == "target_http_status") {
		result.Message = fmt.Sprintf("连接和证书验证正常，目标返回HTTP %d；未发送账号凭据", result.HTTPStatus)
	} else {
		// The code names the failure category; FailureDetail and the stages
		// keep the original error text.
		result.Message = CodexTicketFailure(result.Code).Message
	}
	return &result, nil
}

// OpenAICodexTicketStatuses projects the recorded routing history. With the
// switch off, models with a recorded observation stay visible (nothing is
// ready or blocked then); models without any history are omitted.
func OpenAICodexTicketStatuses(account *Account, cfg config.OpenAICodexTicketConfig, now time.Time) []OpenAICodexTicketStatus {
	if !isOpenAICodexTicketAccount(account) {
		return nil
	}
	projection := nativeCodexAccountProjection(account)
	owned := projection.Identity == CodexTicketAccountIdentity(account)
	out := make([]OpenAICodexTicketStatus, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		entry := OpenAICodexTicketStatus{Model: model, RenewalState: "idle"}
		observation, observed := projection.Observations[model]
		if !cfg.Enabled && (!owned || !observed) {
			continue
		}
		if owned {
			if observed {
				entry.RenewalState = observation.State
				entry.NextAttemptAt = observation.NextAt
				entry.LastAttemptAt = observation.CheckedAt
				entry.ExpiresAt = observation.ExpiresAt
				entry.Length = observation.Count
				if observation.Code != "" {
					result := CodexTicketFailure(observation.Code)
					result.Success = observation.Code == "routing_verified" || observation.Code == "ticket_skipped"
					result.HTTPStatus = observation.HTTPStatus
					result.ObservedLength = observation.Count
					result.ResponseModel = observation.ResponseModel
					// Message stays the short catalog sentence; Error is the raw text.
					result.Error = observation.Message
					entry.LastResult = &result
				}
			}
			if grant, ok := projection.Scheduling[model]; cfg.Enabled && ok && grant.Effect == "allow" && grant.Reason == "routing_verified" && grant.Until != nil && now.Before(*grant.Until) {
				entry.Ready = true
				entry.ExpiresAt = grant.Until
				entry.RemainingSeconds = int64(grant.Until.Sub(now).Seconds())
			}
		}
		entry.Blocked = cfg.Enabled && cfg.FailClosed && !entry.Ready
		out = append(out, entry)
	}
	return out
}
