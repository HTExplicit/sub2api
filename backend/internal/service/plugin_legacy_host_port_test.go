package service

import (
	"context"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExtensionRuntimeCannotUseUnfencedLegacyStoragePort(t *testing.T) {
	store := newFakePluginKVStore()
	manager := &PluginManager{kvStore: store}
	installation := &PluginInstallation{ID: 7, PluginKey: "fixture.extended", RuntimeGeneration: 2}
	installation.Manifest.Requires.ExtensionAPI = 1
	host, ok := manager.buildHostServices(installation).(*pluginHostServiceServer)
	require.True(t, ok)
	require.NotNil(t, host.extension, "the versioned extension broker remains available")
	_, err := host.KVSet(context.Background(), &pluginv1.KVSetRequest{Namespace: "state", Key: "late", Value: []byte("bypassed-generation")})
	require.Equal(t, codes.Unavailable, status.Code(err), "an extension process must use the versioned state/CAS broker")
	_, found, err := store.Get(context.Background(), installation.PluginKey, "state", "late")
	require.NoError(t, err)
	require.False(t, found)
	_, err = host.KVDelete(context.Background(), &pluginv1.KVDeleteRequest{Namespace: "state", Key: "late"})
	require.Equal(t, codes.Unavailable, status.Code(err))

	legacy := &PluginInstallation{ID: 8, PluginKey: "fixture.legacy"}
	legacyHost, ok := manager.buildHostServices(legacy).(*pluginHostServiceServer)
	require.True(t, ok)
	require.Nil(t, legacyHost.extension)
	_, err = legacyHost.KVSet(context.Background(), &pluginv1.KVSetRequest{Namespace: "state", Key: "same", Value: []byte("official-contract")})
	require.NoError(t, err)
	value, found, err := store.Get(context.Background(), legacy.PluginKey, "state", "same")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "official-contract", string(value))
}
