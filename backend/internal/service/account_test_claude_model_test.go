//go:build unit

package service

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestService_ClaudeModelChoiceReachesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selected string
		mapping  map[string]any
		want     string
	}{
		{name: "empty prefers Sonnet 5.5", want: "claude-sonnet-5-5"},
		{name: "empty with only Opus mapped", mapping: map[string]any{"claude-opus-5-5": "claude-opus-5-5"}, want: "claude-opus-5-5"},
		{name: "explicit choice is preserved", selected: "claude-sonnet-4-6", want: "claude-sonnet-4-6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{
				ID: 914, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive,
				Credentials: map[string]any{"api_key": "test-key", "base_url": "https://anthropic.example"},
			}
			if tc.mapping != nil {
				account.Credentials["model_mapping"] = tc.mapping
			}
			svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNAnthropicTestResponse())
			c, recorder := newTestContext()
			require.NoError(t, svc.TestAccountConnection(c, account.ID, tc.selected, "test prompt", AccountTestModeDefault))
			require.Len(t, upstream.requests, 1)
			body, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, tc.want, gjson.GetBytes(body, "model").String())
			require.Contains(t, recorder.Body.String(), `"type":"test_start","model":"`+tc.want+`"`)
		})
	}
}
