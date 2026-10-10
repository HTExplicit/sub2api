package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

func TestCodexBorrowPolicyEditCancelsOldPreparation(t *testing.T) {
	for _, id := range []int64{1, 2} {
		t.Run(map[int64]string{1: "source", 2: "target"}[id], func(t *testing.T) {
			entered := make(chan struct{})
			a := borrowCoreAccount(2)
			s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				close(entered)
				<-req.Context().Done()
				return nil, req.Context().Err()
			}, borrowCoreAccount(1), a)
			if id == 2 {
				borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
			}
			_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6-astra")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { _, _, err := s.Apply(req, a, "gpt-6-astra", "", nil, false); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("probe did not start")
			}
			notifyCodexFingerprintAccountChanged(id)
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(time.Second):
				t.Fatal("old policy continued probing")
			}
			status := s.Status()
			require.False(t, status.Preparing)
			for _, target := range status.Targets {
				require.False(t, target.CacheValid)
				require.NotEqual(t, "validating", target.State)
			}
		})
	}
}
