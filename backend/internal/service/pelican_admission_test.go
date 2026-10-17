//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type pelicanAdmissionCache struct {
	ConcurrencyCache
	mu        sync.Mutex
	active    map[int64]int
	attempted chan int64
}

func (c *pelicanAdmissionCache) AcquireAccountSlot(_ context.Context, id int64, limit int, _ string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case c.attempted <- id:
	default:
	}
	if c.active[id] >= limit {
		return false, nil
	}
	c.active[id]++
	return true, nil
}
func (c *pelicanAdmissionCache) ReleaseAccountSlot(_ context.Context, id int64, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active[id]--
	return nil
}
func (c *pelicanAdmissionCache) RefreshAccountSlot(context.Context, int64, string) (bool, error) {
	return true, nil
}

func TestPelicanAccountWaitReleasesGlobalCapacityHTTPAndWS(t *testing.T) {
	for _, transport := range []string{"http", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			coordinator := newPelicanExecutionCoordinator(1)
			cache := &pelicanAdmissionCache{active: map[int64]int{1: 1}, attempted: make(chan int64, 100)}
			concurrency := NewConcurrencyService(cache)
			pending := make(chan error, 10)
			for range 10 {
				go func() {
					state := newPelicanExecutionState(ctx, coordinator, time.Minute, time.Now)
					defer state.finish()
					requestCtx := context.WithValue(ctx, pelicanExecutionContextKey{}, state)
					_, release, err := acquirePelicanRequestSlots(requestCtx, concurrency, 1, 1)
					if release != nil {
						release()
					}
					pending <- err
				}()
			}
			for range 10 {
				select {
				case <-cache.attempted:
				case <-ctx.Done():
					t.Fatal("blocked account attempts did not yield global capacity")
				}
			}
			account := pelicanFixtureAccount(2)
			account.Concurrency = 1
			state := newPelicanExecutionState(ctx, coordinator, time.Minute, time.Now)
			defer state.finish()
			requestCtx := context.WithValue(ctx, pelicanExecutionContextKey{}, state)
			requestCtx = context.WithValue(requestCtx, pelicanGenerationContextKey{}, &pelicanGenerationCapture{account: account, concurrency: concurrency, fallbackModel: "model"})
			if transport == "http" {
				req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "https://synthetic.invalid/v1/responses", strings.NewReader("{\"model\":\"model\",\"input\":\"fixture\"}"))
				require.NoError(t, err)
				_, finish, err := PreparePelicanHTTPRequest(req, 2, 1, "http")
				require.NoError(t, err)
				response, err := finish(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("fixture"))}, nil)
				require.NoError(t, err)
				coordinator.mu.Lock()
				active := coordinator.active
				coordinator.mu.Unlock()
				require.Equal(t, 1, active)
				require.NoError(t, response.Body.Close())
				require.NoError(t, response.Body.Close())
			} else {
				_, _, release, err := preparePelicanWSSend(requestCtx, account, "wss://synthetic.invalid/responses", map[string]any{"model": "model"})
				require.NoError(t, err)
				release()
				release()
			}
			cancel()
			for range 10 {
				require.ErrorIs(t, <-pending, context.Canceled)
			}
			coordinator.mu.Lock()
			require.Zero(t, coordinator.active)
			coordinator.mu.Unlock()
			cache.mu.Lock()
			require.Zero(t, cache.active[2])
			require.Equal(t, 1, cache.active[1])
			cache.mu.Unlock()
		})
	}
}
