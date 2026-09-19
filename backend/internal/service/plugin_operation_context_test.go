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
