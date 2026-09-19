package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

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
