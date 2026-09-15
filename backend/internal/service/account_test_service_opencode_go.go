package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// testOpenCodeGoConnection probes exactly one native OpenCode Go endpoint,
// matching the model's adaptive protocol (or the account's pinned protocol).
// OpenCode Go is a multi-protocol API-key gateway, so it must not fall through
// to the generic Claude account test path.
func (s *AccountTestService) testOpenCodeGoConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	ctx := c.Request.Context()
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = DefaultOpenCodeGoTestModel
	}
	testModelID = account.GetMappedModel(testModelID)
	protocol := openCodeGoNativeProtocol(account, testModelID)
	baseURL := account.GetCNProtocolBaseURL(protocol)
	if strings.TrimSpace(baseURL) == "" {
		return s.sendErrorAndEnd(c, "No OpenCode Go base URL configured")
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid OpenCode Go base URL: %s", err.Error()))
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	var apiURL string
	var payloadBytes []byte
	switch protocol {
	case APIProtocolAnthropic:
		apiURL = buildOpenAIEndpointURL(normalizedBaseURL, "/v1/messages")
		payload, payloadErr := createTestPayload(testModelID)
		if payloadErr != nil {
			return s.sendErrorAndEnd(c, "Failed to create OpenCode Go Anthropic test payload")
		}
		payloadBytes, err = json.Marshal(payload)
	case APIProtocolResponses:
		apiURL = buildOpenAIResponsesURL(normalizedBaseURL)
		payloadBytes, err = json.Marshal(createOpenAITestPayload(testModelID, false))
	default:
		apiURL = buildOpenAIChatCompletionsURL(normalizedBaseURL)
		payloadBytes, err = json.Marshal(createOpenAIChatCompletionsTestPayload(testModelID, prompt))
		protocol = APIProtocolChatCompletions
	}
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create OpenCode Go test payload")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create OpenCode Go request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	authToken := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if authToken == "" {
		return s.sendErrorAndEnd(c, "No API key available")
	}
	if protocol == APIProtocolAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		setAnthropicAPIKeyAuthHeader(req.Header, account, authToken, normalizedBaseURL)
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	account.ApplyHeaderOverrides(req.Header)
	applyOpenCodeSessionHeader(c, account, apiURL, req.Header, payloadBytes)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.doOpenAIAccountTestUpstream(req, proxyURL, account, true)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("OpenCode Go request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return s.sendErrorAndEnd(c, fmt.Sprintf("OpenCode Go API returned %d: %s", resp.StatusCode, string(body)))
	}
	if protocol == APIProtocolAnthropic {
		return s.processClaudeStream(c, resp.Body)
	}
	if protocol == APIProtocolChatCompletions {
		return s.processOpenAIChatCompletionsStream(c, ctx, account, resp.Body)
	}
	return s.processOpenAIStream(c, ctx, account, resp.Body)
}
