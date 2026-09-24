package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/codexruntime/tickets"
	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type nativeCodexTestLease struct {
	done chan struct{}
	once sync.Once
}

func (l *nativeCodexTestLease) Done() <-chan struct{} { return l.done }
func (*nativeCodexTestLease) Err() error              { return nil }
func (l *nativeCodexTestLease) Release()              { l.once.Do(func() { close(l.done) }) }

func (*routingMemoryStore) ReadNativeCodexStoredConfig(context.Context) (string, bool, error) {
	return "", false, nil
}
func (*routingMemoryStore) LoadNativeCodexMetadata(context.Context) (*NativeCodexMetadata, error) {
	return nativeCodexTestMetadata(), nil
}
func (*routingMemoryStore) StoreNativeCodexConfig(context.Context, string, string) (*NativeCodexMetadata, error) {
	return nativeCodexTestMetadata(), nil
}
func (*routingMemoryStore) SyncNativeCodexConfig(context.Context, string) (*NativeCodexMetadata, error) {
	return nativeCodexTestMetadata(), nil
}
func (*routingMemoryStore) HoldNativeCodexRuntime(ctx context.Context, m *NativeCodexMetadata) (NativeCodexRuntimeLease, error) {
	if !NativeCodexBusinessIORequired(ctx) || m == nil {
		return nil, ErrNativeCodexRuntimeChanged
	}
	return &nativeCodexTestLease{done: make(chan struct{})}, nil
}
func nativeCodexTestMetadata() *NativeCodexMetadata {
	return &NativeCodexMetadata{ID: 1, RuntimeGeneration: 1, ConfigVersion: 1, ConfigSHA256: codexRoutingDigest("fixture-config")}
}

type nativeTicketModuleFixture struct {
	invoke func(extensionv1.Invocation) (extensionv1.Result, error)
}

func (m nativeTicketModuleFixture) Invoke(_ context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	return m.invoke(in)
}
func (nativeTicketModuleFixture) ApplyConfig(context.Context, json.RawMessage) error { return nil }
func (nativeTicketModuleFixture) Start(context.Context) error                        { return nil }
func (nativeTicketModuleFixture) Stop(context.Context) error                         { return nil }

type nativeTicketAccountRepository struct{ AccountRepository }

func (nativeTicketAccountRepository) GetByID(_ context.Context, id int64) (*Account, error) {
	return ticketTestAccount(id), nil
}

func nativeTicketTestRuntime(t *testing.T, cfg config.OpenAICodexTicketConfig, invoke func(extensionv1.Invocation) (extensionv1.Result, error)) *NativeCodexRuntime {
	t.Helper()
	if len(cfg.Models) == 0 {
		cfg.Models = []string{"gpt-6-astra", "gpt-5.6-sol"}
	}
	if invoke == nil {
		invoke = func(extensionv1.Invocation) (extensionv1.Result, error) {
			return extensionv1.Result{Code: "ticket_missing"}, nil
		}
	}
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	r := &NativeCodexRuntime{repo: store, gateway: &OpenAIGatewayService{accountRepo: nativeTicketAccountRepository{}}}
	epoch, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	metadata := nativeCodexTestMetadata()
	host := &nativeCodexHost{key: NativeCodexPluginKey, state: store, repo: store, directory: r.gateway, metadata: metadata, epoch: epoch}
	r.snapshot.Store(&nativeCodexSnapshot{metadata: metadata, config: tickets.Config{Enabled: cfg.Enabled, FailClosed: cfg.FailClosed, Models: cfg.Models, ProxyURL: cfg.HarvestProxyURL}, host: host, module: nativeTicketModuleFixture{invoke: invoke}, ctx: epoch, cancel: cancel})
	return r
}

func nativeRoutingFixtureRuntime(host *nativeCodexHost, store *routingMemoryStore) *NativeCodexRuntime {
	r := &NativeCodexRuntime{repo: store}
	r.snapshot.Store(&nativeCodexSnapshot{metadata: host.metadata, host: host, ctx: host.epoch})
	return r
}
