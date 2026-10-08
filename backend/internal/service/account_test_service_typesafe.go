package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
	"github.com/gin-gonic/gin"
)

const (
	typeSafeTestDefaultState    = "Sub2API connection test"
	typeSafeTestQuestionID      = "connection_test"
	typeSafeTestMaxPreviewBytes = 2000
)

// testTypeSafeAccountConnection probes a TypeSafe account with a minimal native
// System One request. TypeSafe accounts never speak the Claude protocol, so
// they must not fall through to testClaudeAccountConnection (which would send
// the key to /v1/messages and could misclassify the account).
// Like every connection test downstream, a failure reports the upstream text
// as it is (the transport error and the whole response body), and success
// needs a valid protocol terminal: the answer to the question this test asks.
func (s *AccountTestService) testTypeSafeAccountConnection(c *gin.Context, account *Account, prompt string) error {
	ctx := c.Request.Context()
	if account.Type != AccountTypeAPIKey {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Unsupported account type: %s", account.Type))
	}
	apiKey := account.GetTypeSafeAPIKey()
	if apiKey == "" {
		return s.sendErrorAndEnd(c, "No API key available")
	}
	baseURL, err := s.validateUpstreamBaseURL(account.GetTypeSafeBaseURL())
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
	}

	state := strings.TrimSpace(prompt)
	if state == "" {
		state = typeSafeTestDefaultState
	}
	payload, err := json.Marshal(map[string]any{
		"model": typesafe.JevLatestModel,
		"state": state,
		"questions": map[string]any{
			typeSafeTestQuestionID: map[string]any{
				"type":         "noul",
				"instructions": "Is this text a connection test?",
			},
		},
	})
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create test payload")
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	s.sendEvent(c, TestEvent{Type: "test_start", Model: typesafe.JevLatestModel})

	req, err := typesafe.NewSystemOneRequest(ctx, baseURL, apiKey, payload)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create request")
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))
		// 401/403 表示 API Key 无效或被上游拒绝，标记为 error 状态。
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && s.accountRepo != nil {
			_ = s.setAccountTestError(ctx, account.ID, errMsg)
		}
		return s.sendErrorAndEnd(c, errMsg)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to read response: %s", err.Error()))
	}
	// A relay or a wrong base URL can answer 200 with any JSON, and a passed
	// test recovers the account's error and rate-limit state. The account
	// passes only when System One answered the question that was asked.
	if _, err := typesafe.NoulScore(body, typeSafeTestQuestionID); err != nil {
		return s.sendTestFailure(c, accountTestTerminal("Invalid System One response: %s: %s", err.Error(), string(body)))
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: truncateString(string(body), typeSafeTestMaxPreviewBytes)})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
