package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

const cindyBalanceProbeMaxBodyBytes = 64 << 10

// These lowest-positive-input-price free-pool controls are used only by an
// explicitly created durable admin job. Persisted luna/terra stage names are
// retained as historical schema labels and no longer identify these models.
var cindyBalanceProbeModels = [...]string{
	"tencent/hy3",
	"z-ai/glm-5.3-flash",
}

type cindyBalanceProbeOutcome uint8

const (
	cindyBalanceProbeOther cindyBalanceProbeOutcome = iota
	cindyBalanceProbeSuccess
	cindyBalanceProbeExhausted
	cindyBalanceProbeNetworkFailure
	cindyBalanceProbeServerFailure
)

func (s *OpenAIGatewayService) probeCindyBalanceModel(ctx context.Context, account *Account, model string) cindyBalanceProbeOutcome {
	if !CindyBalanceDetectionFeatureEnabled() || s == nil || account == nil || s.httpUpstream == nil {
		return cindyBalanceProbeOther
	}
	body, _ := json.Marshal(map[string]any{
		"model":             model,
		"input":             "Reply OK.",
		"max_output_tokens": 1,
		"stream":            false,
	})
	// The account has already passed strict canonical Cindy identity validation;
	// its operator-controlled endpoint is the intended probe destination.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildOpenAIResponsesURL(account.GetOpenAIBaseURL()), bytes.NewReader(body)) //nolint:gosec
	if err != nil {
		return cindyBalanceProbeOther
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Authorization", "Bearer "+account.GetOpenAIApiKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return cindyBalanceProbeNetworkFailure
	}
	if resp == nil || resp.Body == nil {
		return cindyBalanceProbeNetworkFailure
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, cindyBalanceProbeMaxBodyBytes))
	if err != nil {
		return cindyBalanceProbeNetworkFailure
	}
	if ClassifyCindyBalanceInsufficient(account, resp.StatusCode, responseBody) != CindyBalanceSignalNone {
		return cindyBalanceProbeExhausted
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	looksLikeSSE := strings.Contains(contentType, "text/event-stream") || strings.Contains(string(responseBody), "data:")
	if resp.StatusCode == http.StatusOK && looksLikeSSE {
		return classifyCindyBalanceProbeSSE(account, responseBody)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return cindyBalanceProbeServerFailure
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices &&
		isValidCindyBalanceProbeSuccess(resp.Header, responseBody) {
		return cindyBalanceProbeSuccess
	}
	return cindyBalanceProbeOther
}

func classifyCindyBalanceProbeSSE(account *Account, body []byte) cindyBalanceProbeOutcome {
	terminalCount := 0
	exhaustedCount := 0
	completedCount := 0
	forEachOpenAISSEDataPayload(string(body), func(payload []byte) {
		if !gjson.ValidBytes(payload) {
			return
		}
		eventType := gjson.GetBytes(payload, "type")
		if eventType.Type != gjson.String {
			return
		}
		switch eventType.Str {
		case "response.failed", "error":
			terminalCount++
			if ClassifyCindyBalanceInsufficient(account, http.StatusOK, payload) != CindyBalanceSignalNone {
				exhaustedCount++
			}
		case "response.completed":
			terminalCount++
			if isValidCindyBalanceCompletedResponse(gjson.GetBytes(payload, "response")) {
				completedCount++
			}
		}
	})
	if terminalCount != 1 {
		return cindyBalanceProbeOther
	}
	if exhaustedCount == 1 {
		return cindyBalanceProbeExhausted
	}
	if completedCount == 1 {
		return cindyBalanceProbeSuccess
	}
	return cindyBalanceProbeOther
}

func isValidCindyBalanceProbeSuccess(headers http.Header, body []byte) bool {
	contentType := ""
	if headers != nil {
		contentType = strings.ToLower(strings.TrimSpace(headers.Get("Content-Type")))
	}
	looksLikeSSE := strings.Contains(contentType, "text/event-stream") ||
		strings.Contains(string(body), "data:")
	if looksLikeSSE {
		terminalType, terminalPayload, ok := extractOpenAISSETerminalEvent(string(body))
		if !ok || terminalType != "response.completed" || !gjson.ValidBytes(terminalPayload) {
			return false
		}
		return isValidCindyBalanceCompletedResponse(gjson.GetBytes(terminalPayload, "response"))
	}
	if !gjson.ValidBytes(body) {
		return false
	}
	return isValidCindyBalanceCompletedResponse(gjson.ParseBytes(body))
}

func isValidCindyBalanceCompletedResponse(response gjson.Result) bool {
	if !response.Exists() || !response.IsObject() ||
		strings.TrimSpace(response.Get("id").String()) == "" ||
		strings.TrimSpace(response.Get("object").String()) != "response" ||
		strings.TrimSpace(response.Get("status").String()) != "completed" {
		return false
	}
	output := response.Get("output")
	if !output.IsArray() || len(output.Array()) == 0 {
		return false
	}
	usage := response.Get("usage")
	return usage.IsObject() &&
		usage.Get("input_tokens").Type == gjson.Number &&
		usage.Get("output_tokens").Type == gjson.Number
}
