package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPluginPolicyCancellationSurvivesUpstreamDetachWithoutCancelingBilling(t *testing.T) {
	for _, stream := range []bool{false, true} {
		parent, disconnect := context.WithCancel(context.Background())
		first, second := &pluginRuntime{}, &pluginRuntime{}
		ctx, releaseFirst, err := first.bindPolicyContext(parent)
		require.NoError(t, err)
		ctx, releaseSecond, err := second.bindPolicyContext(ctx)
		require.NoError(t, err)
		var upstream context.Context
		var releaseUpstream context.CancelFunc
		if stream {
			upstream, releaseUpstream = detachStreamUpstreamContext(ctx, true)
		} else {
			upstream, releaseUpstream = detachUpstreamContext(ctx)
		}
		billing, releaseBilling := detachedBillingContext(ctx)
		disconnect()
		require.NoError(t, upstream.Err(), "client disconnect must preserve upstream settlement")
		first.beginDrain()
		select {
		case <-upstream.Done():
		case <-time.After(time.Second):
			t.Fatal("plugin stop was detached from upstream IO")
		}
		require.NoError(t, billing.Err(), "already observed usage still commits")
		late, releaseLate := detachUpstreamContext(ctx)
		require.ErrorIs(t, late.Err(), context.Canceled, "a stopped policy cannot start a late request")
		releaseLate()
		releaseBilling()
		releaseUpstream()
		releaseSecond()
		releaseFirst()
	}
}

func TestPluginPolicyContextsStopOnConfigurationChangeAndDisable(t *testing.T) {
	runtime := &pluginRuntime{}
	first, releaseFirst, err := runtime.bindPolicyContext(context.Background())
	require.NoError(t, err)
	defer releaseFirst()
	runtime.configuring.Store(true)
	runtime.cancelPolicyContexts()
	require.ErrorIs(t, first.Err(), context.Canceled)
	_, _, err = runtime.bindPolicyContext(context.Background())
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	runtime.configuring.Store(false)
	second, releaseSecond, err := runtime.bindPolicyContext(context.Background())
	require.NoError(t, err)
	defer releaseSecond()
	require.NoError(t, second.Err())
	runtime.beginDrain()
	require.ErrorIs(t, second.Err(), context.Canceled)
	_, _, err = runtime.bindPolicyContext(context.Background())
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	releaseFirst()
	releaseSecond()
}

func TestSavingIdenticalPluginConfigurationDoesNotCancelHostWork(t *testing.T) {
	client := &normalizingPluginClient{normalized: []byte(`{"enabled":true}`)}
	runtime := &pluginRuntime{api: client}
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{}`)))
	work, release, err := runtime.bindPolicyContext(context.Background())
	require.NoError(t, err)
	defer release()
	client.applied = nil
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{"enabled":true}`)))
	require.Empty(t, client.applied)
	require.NoError(t, work.Err())
	client.normalized = []byte(`{"enabled":false}`)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{"enabled":false}`)))
	require.ErrorIs(t, work.Err(), context.Canceled)
}
