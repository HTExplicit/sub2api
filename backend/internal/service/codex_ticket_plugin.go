package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// The compatibility facade is the only host location that identifies this
// first-party domain. Acquisition, renewal and validation execute in its plugin.
const codexRuntimePluginKey = "codexrip.codex-runtime"

func (s *OpenAIGatewayService) CodexTicketStatuses(account *Account) []OpenAICodexTicketStatus {
	return OpenAICodexTicketStatuses(account, s.openAICodexTicketConfig(), time.Now())
}

func (s *OpenAIGatewayService) openAICodexTicketConfig() config.OpenAICodexTicketConfig {
	cfg := config.OpenAICodexTicketConfig{FailClosed: true, Models: []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}}
	if s == nil || s.pluginManager == nil {
		return cfg
	}
	raw, active := s.pluginManager.activeConfig(codexRuntimePluginKey)
	if !active {
		return cfg
	}
	var value struct {
		Enabled    bool     `json:"enabled"`
		FailClosed bool     `json:"fail_closed"`
		ProxyURL   string   `json:"proxy_url"`
		Models     []string `json:"models"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return cfg
	}
	cfg.Enabled = value.Enabled
	cfg.FailClosed = value.FailClosed
	cfg.HarvestProxyURL = value.ProxyURL
	if len(value.Models) > 0 {
		cfg.Models = value.Models
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
	if s == nil || s.pluginManager == nil {
		return nil
	}
	if err := s.pluginManager.ApplyRequestHeaders(ctx, account, model, headers); err != nil {
		return fmt.Errorf("%w: extension request prerequisites", ErrOpenAICodexTicketUnavailable)
	}
	return nil
}

func (s *OpenAIGatewayService) openAICodexTicketBlocksAccount(account *Account, model string) bool {
	if s != nil && s.pluginManager != nil {
		s.pluginManager.noteCodexRoutingDemand(account, model)
	}
	return s != nil && s.pluginManager != nil && !s.pluginManager.SchedulingDecision(account, model, time.Now()).Allowed
}

func (s *OpenAIGatewayService) HarvestCodexTicket(ctx context.Context, id int64, model, operation string, jobID int64, force bool) CodexTicketResult {
	if s == nil || s.pluginManager == nil {
		return CodexTicketFailure("ticket_plugin_unavailable")
	}
	installation, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	if installation == nil {
		return CodexTicketFailure("ticket_plugin_unavailable")
	}
	if jobID > 0 {
		operation = fmt.Sprintf("job-%d", jobID)
	}
	payload, _ := json.Marshal(map[string]any{"account_id": id, "model": model, "operation_id": operation, "force": force})
	response, err := s.pluginManager.InvokeAdminExtension(ctx, installation.ID, id, "harvest", payload)
	if err != nil {
		return CodexTicketFailure("ticket_plugin_unavailable")
	}
	var result CodexTicketResult
	if json.Unmarshal(response.Payload, &result) != nil {
		return CodexTicketFailure("ticket_transport")
	}
	return result
}

func (s *OpenAIGatewayService) StopCodexTicketRenewal(ctx context.Context, id int64, models []string) error {
	if s == nil || s.pluginManager == nil {
		return errors.New("ticket plugin unavailable")
	}
	installation, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	if installation == nil {
		return errors.New("ticket plugin unavailable")
	}
	for _, model := range models {
		raw, _ := json.Marshal(map[string]any{"account_id": id, "model": model})
		response, err := s.pluginManager.InvokeAdminExtension(ctx, installation.ID, id, "stop", raw)
		if err != nil {
			return err
		}
		var result CodexTicketResult
		if json.Unmarshal(response.Payload, &result) != nil || !result.Success {
			return errors.New("ticket stop failed")
		}
	}
	return nil
}

func (s *OpenAIGatewayService) TestCodexTicketProxy(ctx context.Context, raw string) (*CodexTicketProxyTestResult, error) {
	if s == nil || s.pluginManager == nil {
		return nil, errors.New("ticket plugin unavailable")
	}
	installation, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	if installation == nil {
		return nil, errors.New("ticket plugin unavailable")
	}
	payload, _ := json.Marshal(map[string]string{"proxy_url": raw})
	response, err := s.pluginManager.InvokeAdminExtension(ctx, installation.ID, 0, "proxy.test", payload)
	if err != nil {
		return nil, err
	}
	var result CodexTicketProxyTestResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return nil, err
	}
	if result.NetworkReachable && (result.Code == "proxy_reachable" || result.Code == "target_http_status") {
		result.Message = fmt.Sprintf("连接和证书验证正常，目标返回HTTP %d；未发送账号凭据", result.HTTPStatus)
	} else {
		result.Message = "代理连接或证书验证失败"
	}
	return &result, nil
}

func OpenAICodexTicketStatuses(account *Account, cfg config.OpenAICodexTicketConfig, now time.Time) []OpenAICodexTicketStatus {
	if !cfg.Enabled || !isOpenAICodexTicketAccount(account) {
		return nil
	}
	projection := accountPluginProjection(account, codexRuntimePluginKey)
	out := make([]OpenAICodexTicketStatus, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		entry := OpenAICodexTicketStatus{Model: model, RenewalState: "idle"}
		if projection.Identity == CodexTicketAccountIdentity(account) {
			if observation, ok := projection.Observations[model]; ok {
				entry.RenewalState = observation.State
				entry.NextAttemptAt = observation.NextAt
				entry.LastAttemptAt = observation.CheckedAt
				entry.ExpiresAt = observation.ExpiresAt
				entry.Length = observation.Count
				if observation.Code != "" {
					result := CodexTicketFailure(observation.Code)
					if observation.Code == "routing_verified" || observation.Code == "ticket_skipped" {
						result = CodexTicketResult{Code: observation.Code, Success: true, Message: "路由验证记录：业务出口响应完整、模型声明一致；本项未验证质量"}
					}
					entry.LastResult = &result
				}
			}
			if grant, ok := projection.Scheduling[model]; ok && grant.Effect == "allow" && grant.Reason == "routing_verified" && grant.Until != nil && now.Before(*grant.Until) {
				entry.Ready = true
				entry.ExpiresAt = grant.Until
				entry.RemainingSeconds = int64(grant.Until.Sub(now).Seconds())
			}
		}
		entry.Blocked = cfg.FailClosed && !entry.Ready
		out = append(out, entry)
	}
	return out
}
