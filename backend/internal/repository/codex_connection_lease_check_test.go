package repository

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexConnectionLeaseLocalCheckDoesNotDial(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "complete")
	}))
	t.Cleanup(server.Close)
	upstream, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	deadline := time.Now().Add(time.Minute)
	response, id, err := upstream.DoWithCodexConnectionLease(request, "", 71, "fixture-scope", "", deadline, nil)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.NoError(t, upstream.CheckCodexConnectionLease(71, "fixture-scope", id, deadline))
	require.ErrorIs(t, upstream.CheckCodexConnectionLease(72, "fixture-scope", id, deadline), service.ErrCodexConnectionLeaseScope)
	require.ErrorIs(t, upstream.CheckCodexConnectionLease(71, "different-scope", id, deadline), service.ErrCodexConnectionLeaseScope)
	require.ErrorIs(t, upstream.CheckCodexConnectionLease(71, "fixture-scope", id, deadline.Add(time.Second)), service.ErrCodexConnectionLeaseScope)
	restarted, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)
	require.ErrorIs(t, restarted.CheckCodexConnectionLease(71, "fixture-scope", id, deadline), service.ErrCodexConnectionLeaseExpired)
	upstream.mu.RLock()
	lease := upstream.codexConnectionLeases[id]
	upstream.mu.RUnlock()
	lease.transport.CloseIdleConnections()
	require.ErrorIs(t, upstream.CheckCodexConnectionLease(71, "fixture-scope", id, deadline), service.ErrCodexConnectionLeaseExpired)
	require.EqualValues(t, 1, calls.Load())
	upstream.removeCodexConnectionLease(id, lease)
}
