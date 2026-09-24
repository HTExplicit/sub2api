package repository

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"sync"
	"time"
)

const nativeCodexRuntimeLeaseProbeInterval = time.Second
const nativeCodexRuntimeLeaseProbeTimeout = time.Second

type nativeCodexRuntimeLeaseSession interface {
	Ping(context.Context) error
	Close(context.Context) error
}

type nativeCodexRuntimeSessionLease struct {
	conn        nativeCodexRuntimeLeaseSession
	done        chan struct{}
	stopped     chan struct{}
	cancel      context.CancelFunc
	releaseOnce sync.Once
	mu          sync.RWMutex
	releasing   bool
	err         error
}

func newNativeCodexRuntimeSessionLease(conn nativeCodexRuntimeLeaseSession, interval, timeout time.Duration) *nativeCodexRuntimeSessionLease {
	ctx, cancel := context.WithCancel(context.Background())
	lease := &nativeCodexRuntimeSessionLease{conn: conn, done: make(chan struct{}), stopped: make(chan struct{}), cancel: cancel}
	go lease.monitor(ctx, interval, timeout)
	return lease
}

func (l *nativeCodexRuntimeSessionLease) Done() <-chan struct{} { return l.done }
func (l *nativeCodexRuntimeSessionLease) Err() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.err
}
func (l *nativeCodexRuntimeSessionLease) Release() {
	l.releaseOnce.Do(func() {
		l.mu.Lock()
		l.releasing = true
		l.mu.Unlock()
		l.cancel()
	})
	<-l.stopped
}

func (l *nativeCodexRuntimeSessionLease) monitor(ctx context.Context, interval, timeout time.Duration) {
	var lost bool
	defer func() {
		l.cancel()
		l.mu.Lock()
		if lost && !l.releasing {
			l.err = service.ErrNativeCodexRuntimeUnavailable
		}
		close(l.done)
		l.mu.Unlock()
		// This goroutine alone owns Ping/Close. Done is published before the
		// bounded close so callers can revoke work without waiting for IO.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = l.conn.Close(closeCtx)
		close(l.stopped)
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		err := l.conn.Ping(probeCtx)
		cancel()
		if err != nil {
			lost = true
			return
		}
	}
}

func (r *nativeCodexRepository) HoldNativeCodexRuntime(ctx context.Context, installation *service.NativeCodexMetadata) (service.NativeCodexRuntimeLease, error) {
	if installation == nil || !service.NativeCodexBusinessIORequired(ctx) {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	pooled, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var config *pgx.ConnConfig
	err = pooled.Raw(func(raw any) error {
		conn, ok := raw.(*stdlib.Conn)
		if !ok {
			return errors.New("plugin runtime leases require PostgreSQL")
		}
		config = conn.Conn().Config().Copy()
		return nil
	})
	_ = pooled.Close()
	if err != nil {
		return nil, err
	}
	// A process-lifetime session must not occupy the business query pool: a
	// small configured pool would deadlock during startup or package replacement.
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["application_name"] = "sub2api-plugin-lease"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	var locked bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared(hashtextextended($1,0))`, nativeCodexRuntimeLockName(installation.ID)).Scan(&locked)
	if err != nil || !locked {
		_ = conn.Close(ctx)
		if err == nil {
			err = service.ErrNativeCodexRuntimeChanged
		}
		return nil, err
	}
	var generation int64
	var state, retired, configRecord string
	err = conn.QueryRow(ctx, `SELECT runtime_generation,state FROM sub2api_plugin_installations WHERE id=$1 AND plugin_key=$2`, installation.ID, service.NativeCodexPluginKey).Scan(&generation, &state)
	stale := err != nil || generation != installation.RuntimeGeneration || state != "disabled"
	if !stale {
		err = conn.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, service.NativeCodexRetirementSettingKey).Scan(&retired)
		stale = err != nil || !nativeCodexRetired(retired)
	}
	if !stale {
		err = conn.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, service.NativeCodexConfigSettingKey).Scan(&configRecord)
		record, decodeErr := nativeCodexConfigRecord(configRecord)
		stale = err != nil || decodeErr != nil || record.ConfigVersion != installation.ConfigVersion || record.ConfigSHA256 != installation.ConfigSHA256
	}

	if stale {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
		if err == nil {
			err = service.ErrNativeCodexRuntimeChanged
		}
		return nil, err
	}
	return newNativeCodexRuntimeSessionLease(conn, nativeCodexRuntimeLeaseProbeInterval, nativeCodexRuntimeLeaseProbeTimeout), nil
}
