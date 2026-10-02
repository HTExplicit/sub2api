package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// nativeCodexMemoryStore is an in-memory native Codex state store.
type nativeCodexMemoryStore struct {
	PluginRepository
	mu     sync.Mutex
	values map[string]extensionv1.StateResult
}

func (store *nativeCodexMemoryStore) ReadExtensionState(_ context.Context, _ string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.values[req.Namespace+":"+req.Key], nil
}

func (store *nativeCodexMemoryStore) CompareSwapExtensionState(_ context.Context, _ string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.values == nil {
		store.values = map[string]extensionv1.StateResult{}
	}
	key := req.Namespace + ":" + req.Key
	old := store.values[key]
	old.Applied = false
	if old.Revision == req.ExpectedRevision {
		old = extensionv1.StateResult{Found: true, Applied: true, Revision: old.Revision + 1, Value: append(json.RawMessage(nil), req.Value...)}
		store.values[key] = old
	}
	return old, nil
}

type codexAccountRepositoryFixture struct {
	AccountRepository
	account *Account
}

func (r *codexAccountRepositoryFixture) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func codexOAuthTestAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "tok", "chatgpt_account_id": "acc-1"}}
}

type nativeCodexTestLease struct {
	done chan struct{}
	once sync.Once
}

func (l *nativeCodexTestLease) Done() <-chan struct{} { return l.done }
func (*nativeCodexTestLease) Err() error              { return nil }
func (l *nativeCodexTestLease) Release()              { l.once.Do(func() { close(l.done) }) }

func (*nativeCodexMemoryStore) ReadNativeCodexStoredConfig(context.Context) (string, bool, error) {
	return "", false, nil
}
func (*nativeCodexMemoryStore) LoadNativeCodexMetadata(context.Context) (*NativeCodexMetadata, error) {
	return nativeCodexTestMetadata(), nil
}
func (*nativeCodexMemoryStore) StoreNativeCodexConfig(context.Context, string, string) (*NativeCodexMetadata, error) {
	return nativeCodexTestMetadata(), nil
}
func (*nativeCodexMemoryStore) SyncNativeCodexConfig(context.Context, string) (*NativeCodexMetadata, error) {
	return nativeCodexTestMetadata(), nil
}
func (*nativeCodexMemoryStore) HoldNativeCodexRuntime(ctx context.Context, m *NativeCodexMetadata) (NativeCodexRuntimeLease, error) {
	if !NativeCodexBusinessIORequired(ctx) || m == nil {
		return nil, ErrNativeCodexRuntimeChanged
	}
	return &nativeCodexTestLease{done: make(chan struct{})}, nil
}
func nativeCodexTestMetadata() *NativeCodexMetadata {
	sum := sha256.Sum256([]byte("fixture-config"))
	return &NativeCodexMetadata{ID: 1, RuntimeGeneration: 1, ConfigVersion: 1, ConfigSHA256: hex.EncodeToString(sum[:])}
}

// nativeCodexModuleFixture answers every runtime invocation with an empty
// result.
type nativeCodexModuleFixture struct{}

func (nativeCodexModuleFixture) Invoke(context.Context, extensionv1.Invocation) (extensionv1.Result, error) {
	return extensionv1.Result{}, nil
}
func (nativeCodexModuleFixture) ApplyConfig(context.Context, json.RawMessage) error { return nil }
func (nativeCodexModuleFixture) Start(context.Context) error                        { return nil }
func (nativeCodexModuleFixture) Stop(context.Context) error                         { return nil }

type nativeCodexTestAccountRepository struct{ AccountRepository }

func (nativeCodexTestAccountRepository) GetByID(_ context.Context, id int64) (*Account, error) {
	return codexOAuthTestAccount(id), nil
}

// nativeCodexTestRuntime is a loaded native runtime with in-memory state.
func nativeCodexTestRuntime(t *testing.T) *NativeCodexRuntime {
	t.Helper()
	store := &nativeCodexMemoryStore{values: map[string]extensionv1.StateResult{}}
	r := &NativeCodexRuntime{repo: store, gateway: &OpenAIGatewayService{accountRepo: nativeCodexTestAccountRepository{}}}
	epoch, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	metadata := nativeCodexTestMetadata()
	host := &nativeCodexHost{metadata: metadata, repo: store, epoch: epoch}
	r.snapshot.Store(&nativeCodexSnapshot{metadata: metadata, host: host, module: nativeCodexModuleFixture{}, ctx: epoch, cancel: cancel})
	return r
}
