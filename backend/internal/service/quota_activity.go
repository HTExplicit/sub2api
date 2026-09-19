package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func (s *QuotaActivityService) BeginUnbilled(ctx context.Context, accountID int64, owner string) (string, error) {
	if s == nil || s.store == nil {
		return "", errors.New("quota observation unavailable")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id := owner + "." + hex.EncodeToString(nonce[:])
	if err := s.store.Begin(ctx, accountID, id); err != nil {
		return "", err
	}
	return id, nil
}

func (s *QuotaActivityService) FinishUnbilled(ctx context.Context, accountID int64, owner, id string) error {
	if s == nil || s.store == nil || !strings.HasPrefix(id, owner+".") || len(id) != len(owner)+33 {
		return errors.New("invalid observation ownership")
	}
	return s.store.Finish(ctx, accountID, id, false)
}

type QuotaActivityStamp struct {
	Epoch    string
	Active   int64
	Revision int64
	Gaps     int64
}

type QuotaActivityStore interface {
	Begin(context.Context, int64, string) error
	Refresh(context.Context, int64, string) error
	Finish(context.Context, int64, string, bool) error
	Read(context.Context, int64) (QuotaActivityStamp, error)
}

type QuotaActivityService struct {
	store     QuotaActivityStore
	uncertain atomic.Bool
	mu        sync.Mutex
	local     map[int64]int
	dirty     map[int64]bool
}

func NewQuotaActivityService(store QuotaActivityStore) *QuotaActivityService {
	return &QuotaActivityService{store: store, local: make(map[int64]int), dirty: make(map[int64]bool)}
}
func (s *QuotaActivityService) localActivity(id int64, delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.local[id] += delta
	if s.local[id] == 0 {
		delete(s.local, id)
	}
}
func (s *QuotaActivityService) markUncertain(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty[id] = true
}

type quotaActivityContextKey struct{}
type quotaActivityEntry struct {
	pending int
	logged  bool
	gap     bool
	closed  bool
}
type quotaUsageTaskContextKey struct{}
type quotaUsageTaskReceipt struct {
	accountID int64
	logged    atomic.Bool
}
type quotaActivityTrace struct {
	service  *QuotaActivityService
	id       string
	mu       sync.Mutex
	accounts map[int64]*quotaActivityEntry
	ended    bool
	done     chan struct{}
	stop     sync.Once
}

func (s *QuotaActivityService) Attach(ctx context.Context) (context.Context, func()) {
	if quotaActivity(ctx) != nil {
		return ctx, func() {}
	}
	if s == nil || s.store == nil {
		return ctx, func() {}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		s.uncertain.Store(true)
		return ctx, func() {}
	}
	trace := &quotaActivityTrace{service: s, id: hex.EncodeToString(nonce[:]), accounts: make(map[int64]*quotaActivityEntry), done: make(chan struct{})}
	return context.WithValue(ctx, quotaActivityContextKey{}, trace), trace.finishRequest
}

func quotaActivity(ctx context.Context) *quotaActivityTrace {
	if ctx == nil {
		return nil
	}
	trace, _ := ctx.Value(quotaActivityContextKey{}).(*quotaActivityTrace)
	return trace
}

func CopyQuotaActivityContext(parent, base context.Context) context.Context {
	if trace := quotaActivity(parent); trace != nil {
		base = context.WithValue(base, quotaActivityContextKey{}, trace)
	}
	return base
}

func ObserveQuotaAccount(ctx context.Context, accountID int64) {
	trace := quotaActivity(ctx)
	if trace == nil || accountID <= 0 {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if trace.accounts[accountID] != nil {
		return
	}
	trace.accounts[accountID] = &quotaActivityEntry{}
	trace.service.localActivity(accountID, 1)
	if err := trace.service.store.Begin(ctx, accountID, trace.id); err != nil {
		trace.service.markUncertain(accountID)
	}
	if len(trace.accounts) == 1 {
		go trace.heartbeat()
	}
}

func MarkQuotaLogPersisted(ctx context.Context, accountID int64) {
	if ctx != nil {
		if receipt, ok := ctx.Value(quotaUsageTaskContextKey{}).(*quotaUsageTaskReceipt); ok && receipt.accountID == accountID {
			receipt.logged.Store(true)
		}
	}
	trace := quotaActivity(ctx)
	if trace == nil {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if entry := trace.accounts[accountID]; entry != nil {
		entry.logged = true
	}
}

func MarkQuotaLogFailed(ctx context.Context, accountID int64) {
	trace := quotaActivity(ctx)
	if trace == nil {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if entry := trace.accounts[accountID]; entry != nil {
		entry.gap = true
	}
}

// Pending is registered before queue submission, closing the gap between
// releasing a network slot and a worker eventually committing the usage log.
func TrackQuotaUsageTask(parent context.Context, task UsageRecordTask) (UsageRecordTask, func()) {
	if parent == nil {
		return task, func() {}
	}
	trace := quotaActivity(parent)
	accountID, _ := parent.Value(ctxkey.AccountID).(int64)
	if trace == nil || accountID <= 0 {
		return task, func() {}
	}
	trace.mu.Lock()
	entry := trace.accounts[accountID]
	if entry == nil {
		trace.mu.Unlock()
		return task, func() {}
	}
	entry.pending++
	trace.mu.Unlock()
	var once sync.Once
	receipt := &quotaUsageTaskReceipt{accountID: accountID}
	finish := func() {
		once.Do(func() {
			trace.mu.Lock()
			defer trace.mu.Unlock()
			entry.pending--
			if !receipt.logged.Load() {
				entry.gap = true
			}
			trace.closeSettledLocked()
		})
	}
	return func(ctx context.Context) {
		defer finish()
		task(context.WithValue(CopyQuotaActivityContext(parent, ctx), quotaUsageTaskContextKey{}, receipt))
	}, finish
}

func (t *quotaActivityTrace) finishRequest() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ended = true
	t.closeSettledLocked()
}

func (t *quotaActivityTrace) closeSettledLocked() {
	if !t.ended {
		return
	}
	allClosed := true
	for id, entry := range t.accounts {
		if entry.closed {
			continue
		}
		if entry.pending > 0 {
			allClosed = false
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := t.service.store.Finish(ctx, id, t.id, entry.logged && !entry.gap); err != nil {
			t.service.markUncertain(id)
		}
		t.service.localActivity(id, -1)
		cancel()
		entry.closed = true
	}
	if allClosed {
		t.stop.Do(func() { close(t.done) })
	}
}

func (t *quotaActivityTrace) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
		}
		t.mu.Lock()
		for id, entry := range t.accounts {
			if entry.closed {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := t.service.store.Refresh(ctx, id, t.id); err != nil {
				t.service.markUncertain(id)
			}
			cancel()
		}
		t.mu.Unlock()
	}
}

func (s *QuotaActivityService) Read(ctx context.Context, id int64) (QuotaActivityStamp, bool) {
	if s == nil || s.store == nil || s.uncertain.Load() {
		return QuotaActivityStamp{}, false
	}
	s.mu.Lock()
	if s.dirty[id] {
		if s.local[id] != 0 {
			s.mu.Unlock()
			return QuotaActivityStamp{}, false
		}
		if err := s.store.Finish(ctx, id, "uncertain-observation", false); err != nil {
			s.mu.Unlock()
			return QuotaActivityStamp{}, false
		}
		delete(s.dirty, id)
	}
	local := s.local[id]
	s.mu.Unlock()
	stamp, err := s.store.Read(ctx, id)
	if int64(local) > stamp.Active {
		stamp.Active = int64(local)
	}
	return stamp, err == nil
}

func (s *OpsService) AttachQuotaActivity(ctx context.Context) (context.Context, func()) {
	if s == nil {
		return ctx, func() {}
	}
	return s.quotaActivity.Attach(ctx)
}
