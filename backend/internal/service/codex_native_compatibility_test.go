package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

type nativeSameConfigRepository struct {
	*nativeCodexMemoryStore
	metadata *NativeCodexMetadata
	stored   int
	failure  error
}

func (r *nativeSameConfigRepository) LoadNativeCodexMetadata(context.Context) (*NativeCodexMetadata, error) {
	m := *r.metadata
	return &m, nil
}
func (r *nativeSameConfigRepository) StoreNativeCodexConfig(_ context.Context, _ string, hash string) (*NativeCodexMetadata, error) {
	if hash != r.metadata.ConfigSHA256 {
		return nil, ErrNativeCodexRuntimeChanged
	}
	r.stored++
	return r.metadata, r.failure
}

type nativeConfigEncryptorFixture struct{}

func (nativeConfigEncryptorFixture) Encrypt(value string) (string, error) {
	return "fixture-cipher:" + value, nil
}
func (nativeConfigEncryptorFixture) Decrypt(value string) (string, error) { return value, nil }

// nativeStoredConfigRepository serves one saved configuration and records the
// hash and cipher the runtime writes back.
type nativeStoredConfigRepository struct {
	*nativeCodexMemoryStore
	stored   string
	metadata *NativeCodexMetadata
}

func (r *nativeStoredConfigRepository) ReadNativeCodexStoredConfig(context.Context) (string, bool, error) {
	return r.stored, true, nil
}
func (r *nativeStoredConfigRepository) LoadNativeCodexMetadata(context.Context) (*NativeCodexMetadata, error) {
	m := *r.metadata
	return &m, nil
}
func (r *nativeStoredConfigRepository) SyncNativeCodexConfig(_ context.Context, hash string) (*NativeCodexMetadata, error) {
	if hash != r.metadata.ConfigSHA256 {
		r.metadata = &NativeCodexMetadata{ID: r.metadata.ID, RuntimeGeneration: r.metadata.RuntimeGeneration + 1, ConfigVersion: r.metadata.ConfigVersion + 1, ConfigSHA256: hash}
	}
	m := *r.metadata
	return &m, nil
}
func (r *nativeStoredConfigRepository) StoreNativeCodexConfig(ctx context.Context, cipher, hash string) (*NativeCodexMetadata, error) {
	r.stored = cipher
	return r.SyncNativeCodexConfig(ctx, hash)
}

func TestNativeCodexStoredRetiredSettingsStillBoot(t *testing.T) {
	previous := nativeCodexPolicyRuntime.Load()
	t.Cleanup(func() { nativeCodexPolicyRuntime.Store(previous) })
	ctx := context.Background()
	// A configuration saved before the route-qualification feature was retired
	// holds every one of its settings next to request_zstd.
	stored := `{"routing_schema":2,"enabled":true,"fail_closed":true,"proxy_url":"http://user:fixture-secret@proxy.test:8080","proxy_protocol":"https","proxy_selection_id":"selection","models":["gpt-6-astra","gpt-6-sol","gpt-6-luna","gpt-5.6-sol"],"request_zstd":false}`
	repo := &nativeStoredConfigRepository{nativeCodexMemoryStore: &nativeCodexMemoryStore{values: map[string]extensionv1.StateResult{}}, stored: stored, metadata: nativeCodexTestMetadata()}
	gateway := &OpenAIGatewayService{}
	runtime := ProvideNativeCodexRuntime(gateway, repo, nil, nativeConfigEncryptorFixture{}, &config.Config{}, nil, nil)
	require.NoError(t, runtime.Start(ctx), "the server exits when the runtime cannot start")
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	raw, metadata, err := gateway.NativeCodexConfiguration(ctx)
	require.NoError(t, err)
	require.Equal(t, `{"request_zstd":false}`, string(raw))
	sum := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(sum[:]), metadata.ConfigSHA256)
	require.Equal(t, stored, repo.stored, "booting never rewrites the saved configuration")
	plan := func() extensionv1.CodexTransportPlan {
		result, invokeErr := runtime.Invoke(ctx, PlatformOpenAI, AccountTypeOAuth, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.transport.plan",
			Payload: json.RawMessage(`{"method":"POST","path":"/backend-api/codex/responses","content_type":"application/json","body_present":true}`)})
		require.NoError(t, invokeErr)
		var value extensionv1.CodexTransportPlan
		require.NoError(t, json.Unmarshal(result.Payload, &value))
		return value
	}
	require.False(t, plan().Compress, "the saved request_zstd stays in effect")
	// An older admin page may still send the retired settings: they are dropped
	// and never stored again. Any other unknown setting is still rejected.
	require.NoError(t, runtime.UpdateConfig(ctx, json.RawMessage(`{"enabled":false,"fail_closed":true,"models":["gpt-6-astra"],"proxy_url":"","request_zstd":true}`), nativeConfigEncryptorFixture{}))
	require.Equal(t, `fixture-cipher:{"request_zstd":true}`, repo.stored)
	require.True(t, plan().Compress)
	_, err = NormalizeNativeCodexConfig(ctx, json.RawMessage(`{"request_zstd":true,"harvest":true}`))
	require.ErrorContains(t, err, "unknown codex runtime setting: harvest")
}

func TestNativeCodexSameConfigSaveKeepsActiveEpoch(t *testing.T) {
	raw, err := NormalizeNativeCodexConfig(context.Background(), json.RawMessage(`{"enabled":false}`))
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	metadata := nativeCodexTestMetadata()
	metadata.ConfigSHA256 = hex.EncodeToString(sum[:])
	repo := &nativeSameConfigRepository{nativeCodexMemoryStore: &nativeCodexMemoryStore{}, metadata: metadata}
	epoch, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot := &nativeCodexSnapshot{metadata: metadata, raw: raw, ctx: epoch, cancel: cancel}
	runtime := &NativeCodexRuntime{repo: repo, started: true}
	runtime.snapshot.Store(snapshot)
	require.NoError(t, runtime.UpdateConfig(context.Background(), raw, nativeConfigEncryptorFixture{}))
	require.NoError(t, epoch.Err())
	require.Same(t, snapshot, runtime.current())
	require.Equal(t, 1, repo.stored)
	repo.failure = errors.New("fixture write failed")
	require.ErrorIs(t, runtime.UpdateConfig(context.Background(), raw, nativeConfigEncryptorFixture{}), repo.failure)
	require.NoError(t, epoch.Err())
	require.Same(t, snapshot, runtime.current())
}
