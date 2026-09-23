package service

import (
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCodexRoutingIdentityObservationsAreBoundedAndRedacted(t *testing.T) {
	request, err := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"client_metadata":{"session_id":"same-session","thread_id":"body-thread","turn_id":"turn-only"},"input":"secret-prompt"}`))
	require.NoError(t, err)
	request.Header.Set("session-id", "same-session")
	request.Header.Set("thread-id", "different-thread")
	before := request.Body
	observation := inspectCodexIdentityBody(request)
	fields := codexIdentityFieldObservations(request.Header, observation)
	require.Equal(t, before, request.Body)
	remaining, readErr := io.ReadAll(request.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(remaining), "secret-prompt", "inspection must not consume the send body")
	require.Equal(t, "match", fields["session"].Consistency)
	require.Equal(t, "mismatch", fields["thread"].Consistency)
	require.Equal(t, "missing", fields["turn"].Consistency)
	require.Len(t, fields["session"].HeaderDigest, 16)
	require.NotEqual(t, "same-session", fields["session"].HeaderDigest)
	request.GetBody = nil
	fields = codexIdentityFieldObservations(request.Header, inspectCodexIdentityBody(request))
	require.Equal(t, "uninspected", fields["session"].Consistency)
	oversized, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"input":"`+strings.Repeat("x", codexRoutingProbeReadLimit)+`"}`))
	require.False(t, inspectCodexIdentityBody(oversized).Inspected)
}
