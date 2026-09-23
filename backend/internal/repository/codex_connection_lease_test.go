package repository

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexConnectionLeaseReusesPhysicalConnectionAndRejectsScope(t *testing.T) {
	var connections atomic.Int32
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = io.WriteString(w, "complete") }))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	request := func() *http.Request {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		return req
	}
	first, id, err := upstream.DoWithCodexConnectionLease(request(), "", 1, "scope-a", "", time.Now().Add(time.Minute), nil)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	_, err = io.Copy(io.Discard, first.Body)
	require.NoError(t, err)
	require.NoError(t, first.Body.Close())
	second, same, err := upstream.DoWithCodexConnectionLease(request(), "", 1, "scope-a", id, time.Time{}, nil)
	require.NoError(t, err)
	require.Equal(t, id, same)
	_, err = io.Copy(io.Discard, second.Body)
	require.NoError(t, err)
	require.NoError(t, second.Body.Close())
	require.EqualValues(t, 1, connections.Load())
	_, _, err = upstream.DoWithCodexConnectionLease(request(), "", 2, "scope-a", id, time.Time{}, nil)
	require.ErrorIs(t, err, service.ErrCodexConnectionLeaseScope)
	_, _, err = upstream.DoWithCodexConnectionLease(request(), "", 1, "changed-profile-or-route", id, time.Time{}, nil)
	require.ErrorIs(t, err, service.ErrCodexConnectionLeaseScope)
	require.EqualValues(t, 2, calls.Load())
	upstream.removeCodexConnectionLease(id, upstream.codexConnectionLeases[id])
}

func TestCodexConnectionLeaseNeverRedialsAfterUpstreamCloses(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, "complete")
	}))
	t.Cleanup(server.Close)
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	request := func() *http.Request {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		return req
	}
	first, id, err := upstream.DoWithCodexConnectionLease(request(), "", 1, "scope", "", time.Now().Add(time.Minute), nil)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, first.Body)
	require.NoError(t, err)
	require.NoError(t, first.Body.Close())
	_, _, err = upstream.DoWithCodexConnectionLease(request(), "", 1, "scope", id, time.Time{}, nil)
	require.ErrorIs(t, err, service.ErrCodexConnectionLeaseExpired)
	require.EqualValues(t, 1, calls.Load())
}

func TestCodexConnectionLeaseExpiryAndRedirectDoNotSendAnotherRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/other", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	request := func() *http.Request {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		return req
	}
	response, id, err := upstream.DoWithCodexConnectionLease(request(), "", 1, "scope", "", time.Now().Add(time.Minute), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, response.StatusCode)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	upstream.mu.Lock()
	upstream.codexConnectionLeases[id].expiresAt = time.Now().Add(-time.Second)
	upstream.mu.Unlock()
	_, _, err = upstream.DoWithCodexConnectionLease(request(), "", 1, "scope", id, time.Time{}, nil)
	require.ErrorIs(t, err, service.ErrCodexConnectionLeaseExpired)
	require.EqualValues(t, 1, calls.Load())
	upstream.removeCodexConnectionLease(id, upstream.codexConnectionLeases[id])
}
