package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

type nativeSameConfigRepository struct {
	*routingMemoryStore
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

func TestNativeCodexSameConfigSaveKeepsActiveEpoch(t *testing.T) {
	raw, err := NormalizeNativeCodexConfig(context.Background(), json.RawMessage(`{"enabled":false}`))
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	metadata := nativeCodexTestMetadata()
	metadata.ConfigSHA256 = hex.EncodeToString(sum[:])
	repo := &nativeSameConfigRepository{routingMemoryStore: &routingMemoryStore{}, metadata: metadata}
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

func TestNativeCodexClosedLedgerReadableWithoutRewritingGeneration(t *testing.T) {
	host, _, store := routingHostFixture()
	s := &OpenAIGatewayService{nativeCodexRuntime: nativeRoutingFixtureRuntime(host, store), accountRepo: &routingAccountRepositoryFixture{account: ticketTestAccount(7)}}
	run := codexQualityRun{RunID: "138bf94a-1bea-4aeb-9e7a-378e9c394bc9", ActorID: 9, AccountID: 7, Status: "closed", MaxSends: 60, UsedSends: 40, RouteRuntimeGeneration: 99, Attempts: make([]CodexQualityAttempt, 40)}
	raw, err := json.Marshal(run)
	require.NoError(t, err)
	key := codexRoutingPrivateNamespace + ":" + codexQualityKey(run.RunID)
	store.values[key] = extensionv1.StateResult{Found: true, Revision: 11, Value: raw}
	view, err := s.ReadCodexQualityRun(context.Background(), 9, 7, run.RunID)
	require.NoError(t, err)
	require.Equal(t, "closed", view.Status)
	require.Equal(t, 40, view.UsedSends)
	require.Equal(t, 60, view.MaxSends)
	require.False(t, view.RouteReady)
	require.True(t, bytes.Equal(raw, store.values[key].Value), "reading a closed ledger must keep its stored bytes")
	require.EqualValues(t, 11, store.values[key].Revision)
	require.Len(t, store.values, 1)
	run.Status = "open"
	run.ExpiresAt = time.Now().Add(time.Minute)
	run.Qualification = &extensionv1.CodexRoutingQualification{}
	rt, err := s.codexQualityRuntime()
	require.NoError(t, err)
	_, err = rt.qualification(context.Background(), run)
	require.ErrorIs(t, err, ErrCodexQualityUnavailable)
	require.True(t, bytes.Equal(raw, store.values[key].Value), "an old active generation is rejected without rewriting its stored bytes")
}

func TestNativeCodexHostKeepsPrivateStateAndCredentialScope(t *testing.T) {
	host, directory, store := routingHostFixture()
	raw, _ := json.Marshal(extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "quality-run.private"})
	_, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostStateRead, Payload: raw})
	require.Error(t, err)
	for _, kind := range []string{AccountTypeAPIKey, "other"} {
		directory.account.Type = kind
		raw, _ = json.Marshal(extensionv1.CodexRoutingQuery{AccountID: directory.account.ID, Transport: "http"})
		_, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingScope, Payload: raw})
		require.Error(t, err)
	}
	require.Zero(t, directory.requests)
	require.Empty(t, store.values)
}
