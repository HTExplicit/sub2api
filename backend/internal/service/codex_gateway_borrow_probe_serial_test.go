package service

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestCodexBorrowProbePairCannotInterleaveOnSameAccount(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "business"
		if manual {
			name = "diagnostic"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if manual {
				state := newPelicanExecutionState(ctx, sharedPelicanExecution, time.Minute, time.Now)
				defer state.finish()
				ctx = context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
			}
			account := borrowCoreAccount(92001)
			started, release := make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			var order []string
			s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				id := req.Header.Get("X-Codex-Window-ID")
				phase := "mint"
				if req.Header.Get("X-Codex-Turn-State") != "" {
					phase = "continue"
				}
				mu.Lock()
				order = append(order, id+":"+phase)
				mu.Unlock()
				if id == "first" && phase == "mint" {
					close(started)
					select {
					case <-release:
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
				}
				return borrowCoreResponse("gpt-6-astra", "OK", id), nil
			}, account)
			borrowCoreCandidate(s, time.Now().Add(time.Minute))
			run := func(id string, done chan<- CodexGatewayBorrowVerification) {
				template := &http.Request{Header: http.Header{"X-Codex-Window-Id": []string{id}}}
				done <- s.probeTarget(ctx, template, account, "gpt-6-astra", "", nil, s.candidate)
			}
			first, second := make(chan CodexGatewayBorrowVerification, 1), make(chan CodexGatewayBorrowVerification, 1)
			go run("first", first)
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("first probe did not start")
			}
			go run("second", second)
			queued := false
			deadline := time.Now().Add(time.Second)
			for !queued && time.Now().Before(deadline) {
				sharedPelicanExecution.mu.Lock()
				for _, waiter := range sharedPelicanExecution.waiting {
					queued = queued || waiter.accountID == account.ID
				}
				sharedPelicanExecution.mu.Unlock()
				if !queued {
					time.Sleep(time.Millisecond)
				}
			}
			close(release)
			require.True(t, queued, "second probe must wait on the same account")
			require.True(t, (<-first).Success)
			require.True(t, (<-second).Success)
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, []string{"first:mint", "first:continue", "second:mint", "second:continue"}, order)
		})
	}
}

func TestCodexBorrowProbeLeaseCancellationDoesNotBlockOtherAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	leased, release, err := acquireCodexBorrowObservation(ctx, 92002)
	require.NoError(t, err)
	defer release()
	_, nestedRelease, err := acquireCodexBorrowObservation(leased, 92002)
	require.NoError(t, err)
	nestedRelease() // A nested shot cannot release the whole pair's lease.
	_, releaseOther, err := acquireCodexBorrowObservation(ctx, 92003)
	require.NoError(t, err, "another account can use remaining global capacity")
	releaseOther()
	_, _, err = acquireCodexBorrowObservation(leased, 92003)
	require.Error(t, err, "a pair cannot wait for a source while retaining its target lease")
	queued, cancelQueued := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, releaseQueued, queuedErr := acquireCodexBorrowObservation(queued, 92002)
		if releaseQueued != nil {
			releaseQueued()
		}
		done <- queuedErr
	}()
	cancelQueued()
	require.ErrorIs(t, <-done, context.Canceled)
	release()
	_, releaseAgain, err := acquireCodexBorrowObservation(ctx, 92002)
	require.NoError(t, err, "cancellation and nested releases must not leak account capacity")
	releaseAgain()
}
