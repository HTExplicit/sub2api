package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexBorrowSourceUsesNormalAccountTestContract(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer synthetic-source"}, "User-Agent": {"synthetic-persisted-identity"}, "Originator": {"codex-tui"}, "Version": {"0.162.1"}}
	for _, name := range []string{"session-id", "thread-id", "session_id", "x-codex-window-id", "x-codex-turn-state", "x-codex-turn-metadata", "x-codex-routing-hint", "Cookie", "Content-Encoding"} {
		headers.Set(name, "target-only-value")
	}
	original := headers.Clone()
	req, err := borrowSourceObservationRequest(context.Background(), headers)
	require.NoError(t, err)
	require.False(t, req.Close, "source acquisition keeps normal connection policy")
	require.Equal(t, original, headers)
	require.Equal(t, "Bearer synthetic-source", req.Header.Get("Authorization"))
	require.Equal(t, "synthetic-persisted-identity", req.Header.Get("User-Agent"))
	for _, name := range []string{"session-id", "thread-id", "session_id", "x-codex-window-id", "x-codex-turn-state", "x-codex-turn-metadata", "x-codex-routing-hint", "Cookie", "Content-Encoding"} {
		require.Empty(t, req.Header.Get(name), name)
	}
	body := borrowCoreBody(t, req)
	want := createOpenAITestPayload("gpt-6-astra", true, "Reply with OK only.")
	require.Equal(t, want["instructions"], gjson.GetBytes(body, "instructions").String())
	require.Equal(t, "Reply with OK only.", gjson.GetBytes(body, "input.0.content.0.text").String())
	require.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(body, "parallel_tool_calls").Exists())
	require.False(t, gjson.GetBytes(body, "include").Exists())
	require.Equal(t, HTTPUpstreamProfileCodexBorrowSource, HTTPUpstreamProfileFromContext(req.Context()))
	require.True(t, IsCodexGatewayBorrowObservation(req.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
}
