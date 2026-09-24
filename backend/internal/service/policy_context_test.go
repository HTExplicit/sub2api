package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativePolicyCancellationSurvivesDetachedClientContext(t *testing.T) {
	for _, native := range []bool{false, true} {
		client, cancelClient := context.WithCancel(context.Background())
		policy, cancelPolicy := context.WithCancel(context.Background())
		parent := context.WithValue(client, policyCancellationSignalsKey{}, []context.Context{policy})
		if native {
			parent = context.WithValue(client, nativeCodexPolicySignalsKey{}, []context.Context{policy})
		}
		detached, release := detachPolicyContext(parent)
		cancelClient()
		require.NoError(t, detached.Err(), "a client disconnect does not terminate detached work")
		cancelPolicy()
		select {
		case <-detached.Done():
		case <-time.After(time.Second):
			t.Fatal("native policy cancellation did not reach detached work")
		}
		require.ErrorIs(t, detached.Err(), context.Canceled)
		release()
	}
}
