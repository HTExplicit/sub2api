package service

import (
	"context"
	"sync"
	"time"
)

// A preparation belongs to its configuration revision, not to the first caller.
// Cancelling a waiter only cancels the work when no other waiter needs it.
type codexBorrowFlights struct {
	mu     sync.Mutex
	active map[string]*codexBorrowFlight
}

type codexBorrowFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	value   any
	err     error
}

func (g *codexBorrowFlights) do(ctx, revision context.Context, key string, timeout time.Duration, run func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.active == nil {
		g.active = make(map[string]*codexBorrowFlight)
	}
	f := g.active[key]
	if f == nil {
		operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		stop := context.AfterFunc(revision, cancel)
		f = &codexBorrowFlight{done: make(chan struct{}), cancel: cancel}
		g.active[key] = f
		go func() {
			defer stop()
			defer cancel()
			f.value, f.err = run(operation)
			g.mu.Lock()
			if g.active[key] == f {
				delete(g.active, key)
			}
			close(f.done)
			g.mu.Unlock()
		}()
	}
	f.waiters++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if g.active[key] == f {
				delete(g.active, key)
			}
		}
		g.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		// A concurrently completed flight cannot outlive this waiter's deadline.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return f.value, f.err
	}
}

type CodexGatewayBorrowSetup struct {
	State      string     `json:"state"`
	Phase      string     `json:"phase"`
	AccountID  int64      `json:"account_id"`
	Model      string     `json:"model"`
	Completed  int        `json:"completed"`
	Total      int        `json:"total"`
	Failed     int        `json:"failed"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func (s *CodexGatewayBorrowService) setSetup(revision uint64, setup CodexGatewayBorrowSetup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision == revision {
		s.setup = setup
	}
}
