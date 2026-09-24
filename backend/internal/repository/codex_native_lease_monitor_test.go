package repository

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type runtimeLeaseMonitorSession struct {
	entered    chan struct{}
	enterOnce  sync.Once
	block      bool
	pingErr    error
	active     atomic.Int32
	pings      atomic.Int32
	closes     atomic.Int32
	concurrent atomic.Bool
}

func (s *runtimeLeaseMonitorSession) Ping(ctx context.Context) error {
	s.pings.Add(1)
	s.active.Add(1)
	defer s.active.Add(-1)
	s.enterOnce.Do(func() { close(s.entered) })
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.pingErr
}

func (s *runtimeLeaseMonitorSession) Close(context.Context) error {
	s.closes.Add(1)
	if s.active.Load() != 0 {
		s.concurrent.Store(true)
	}
	return nil
}

func TestNativeRuntimeLeaseNormalReleaseCancelsProbeWithoutConcurrentClose(t *testing.T) {
	session := &runtimeLeaseMonitorSession{entered: make(chan struct{}), block: true}
	lease := newNativeCodexRuntimeSessionLease(session, time.Millisecond, time.Second)
	select {
	case <-session.entered:
	case <-time.After(time.Second):
		t.Fatal("monitor did not start its probe")
	}
	var released sync.WaitGroup
	for range 2 {
		released.Add(1)
		go func() { defer released.Done(); lease.Release() }()
	}
	finished := make(chan struct{})
	go func() { released.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Release did not cancel/join the in-flight probe")
	}
	require.NoError(t, lease.Err(), "deliberate cleanup is not session loss")
	require.Equal(t, int32(1), session.pings.Load())
	require.Equal(t, int32(1), session.closes.Load())
	require.False(t, session.concurrent.Load(), "Ping and Close must have one connection owner")
	select {
	case <-lease.Done():
	default:
		t.Fatal("normal release did not finish listeners")
	}
}

func TestNativeRuntimeLeaseLossListenerCanReleaseWithoutDeadlock(t *testing.T) {
	session := &runtimeLeaseMonitorSession{entered: make(chan struct{}), pingErr: errors.New("synthetic lost session")}
	lease := newNativeCodexRuntimeSessionLease(session, time.Millisecond, time.Second)
	listenerDone := make(chan struct{})
	go func() {
		<-lease.Done()
		lease.Release()
		close(listenerDone)
	}()
	select {
	case <-listenerDone:
	case <-time.After(time.Second):
		t.Fatal("loss notification and caller Release formed a wait cycle")
	}
	require.ErrorIs(t, lease.Err(), service.ErrNativeCodexRuntimeUnavailable)
	lease.Release()
	require.Equal(t, int32(1), session.pings.Load(), "loss must not reconnect or re-acquire a lease")
	require.Equal(t, int32(1), session.closes.Load())
	require.False(t, session.concurrent.Load())
}

func TestNativeRuntimeLeaseProbeTimeoutReportsLossWithoutRetry(t *testing.T) {
	session := &runtimeLeaseMonitorSession{entered: make(chan struct{}), block: true}
	lease := newNativeCodexRuntimeSessionLease(session, time.Millisecond, 5*time.Millisecond)
	select {
	case <-lease.Done():
	case <-time.After(time.Second):
		t.Fatal("a stalled session probe did not reach its bounded timeout")
	}
	require.ErrorIs(t, lease.Err(), service.ErrNativeCodexRuntimeUnavailable)
	lease.Release()
	require.Equal(t, int32(1), session.pings.Load())
	require.Equal(t, int32(1), session.closes.Load())
	require.False(t, session.concurrent.Load())
}
