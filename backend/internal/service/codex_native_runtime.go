package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/codexruntime/tickets"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

var ErrNativeCodexPolicyDisabled = errors.New("native Codex policy is disabled for this account")

type nativeCodexModule interface {
	Invoke(context.Context, extensionv1.Invocation) (extensionv1.Result, error)
	ApplyConfig(context.Context, json.RawMessage) error
	Start(context.Context) error
	Stop(context.Context) error
}

type nativeCodexSnapshot struct {
	metadata *NativeCodexMetadata
	raw      json.RawMessage
	config   tickets.Config
	module   nativeCodexModule
	host     *nativeCodexHost
	ctx      context.Context
	cancel   context.CancelFunc
}

type NativeCodexRuntime struct {
	mu            sync.Mutex
	root          context.Context
	started       bool
	gateway       *OpenAIGatewayService
	repo          NativeCodexRepository
	factory       NativeCodexConfigFactory
	activity      *QuotaActivityService
	snapshot      atomic.Pointer[nativeCodexSnapshot]
	codexDemandMu sync.Mutex
	codexDemandAt map[string]time.Time
}

var nativeCodexPolicyRuntime atomic.Pointer[NativeCodexRuntime]

func NewNativeCodexRuntime(gateway *OpenAIGatewayService, repo NativeCodexRepository, factory NativeCodexConfigFactory) *NativeCodexRuntime {
	r := &NativeCodexRuntime{gateway: gateway, repo: repo, factory: factory}
	if gateway != nil {
		gateway.SetNativeCodexRuntime(r)
	}
	nativeCodexPolicyRuntime.Store(r)
	return r
}

func (s *OpenAIGatewayService) SetNativeCodexRuntime(runtime *NativeCodexRuntime) {
	s.nativeCodexRuntime = runtime
}

// Historical extension jobs must name the actual retired Codex installation,
// never an arbitrary third-party plugin exposing a same-named operation.
func (s *OpenAIGatewayService) ValidateLegacyCodexJobSource(ctx context.Context, pluginID int64) error {
	if s == nil || s.nativeCodexRuntime == nil || pluginID <= 0 {
		return ErrNativeCodexRuntimeUnavailable
	}
	reader, ok := s.nativeCodexRuntime.repo.(interface {
		ValidateLegacyCodexJobSource(context.Context, int64) error
	})
	if !ok {
		return ErrNativeCodexRuntimeUnavailable
	}
	return reader.ValidateLegacyCodexJobSource(ctx, pluginID)
}

func (r *NativeCodexRuntime) SetQuotaActivity(activity *QuotaActivityService) { r.activity = activity }

func NormalizeNativeCodexConfig(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return tickets.NewModule().ValidateConfig(ctx, raw)
}

func (r *NativeCodexRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	r.root = ctx
	if err := r.reloadLocked(ctx); err != nil {
		return err
	}
	r.started = true
	return nil
}

func (r *NativeCodexRuntime) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return ErrNativeCodexRuntimeUnavailable
	}
	return r.reloadLocked(ctx)
}

// UpdateConfig is the sole native settings write path: validate/encrypt first,
// drain the old epoch, then persist source/hash/generation in one transaction.
func (r *NativeCodexRuntime) UpdateConfig(ctx context.Context, raw json.RawMessage, encryptor SecretEncryptor) error {
	raw, err := NormalizeNativeCodexConfig(ctx, raw)
	if err != nil {
		return err
	}
	if encryptor == nil {
		return ErrNativeCodexRuntimeUnavailable
	}
	cipher, err := encryptor.Encrypt(string(raw))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.repo == nil {
		return ErrNativeCodexRuntimeUnavailable
	}
	previous := r.snapshot.Load()
	if previous != nil && previous.ctx.Err() == nil && previous.metadata.ConfigSHA256 == hash {
		current, loadErr := r.repo.LoadNativeCodexMetadata(ctx)
		if loadErr != nil {
			return loadErr
		}
		if *current == *previous.metadata {
			_, saveErr := r.repo.StoreNativeCodexConfig(ctx, cipher, hash)
			return saveErr
		}
	}
	if err = r.stopSnapshot(ctx, previous); err != nil {
		return err
	}
	metadata, err := r.repo.StoreNativeCodexConfig(ctx, cipher, hash)
	if err != nil {
		r.restoreSnapshot(ctx, previous)
		return err
	}
	return r.publishLocked(ctx, raw, metadata)
}

func (r *NativeCodexRuntime) stopSnapshot(ctx context.Context, snapshot *nativeCodexSnapshot) error {
	if snapshot == nil {
		return nil
	}
	snapshot.cancel()
	if err := snapshot.module.Stop(ctx); err != nil {
		return err
	}
	return snapshot.host.drain(ctx)
}

func (r *NativeCodexRuntime) restoreSnapshot(ctx context.Context, previous *nativeCodexSnapshot) {
	if previous != nil {
		if current, err := r.repo.LoadNativeCodexMetadata(ctx); err == nil && *current == *previous.metadata {
			_ = r.publishLocked(ctx, previous.raw, current)
		}
	}
}

func (r *NativeCodexRuntime) reloadLocked(ctx context.Context) error {
	if r.repo == nil || r.factory == nil {
		return ErrNativeCodexRuntimeUnavailable
	}
	raw, err := r.factory(ctx)
	if err != nil {
		return err
	}
	raw, err = NormalizeNativeCodexConfig(ctx, raw)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	previous := r.snapshot.Load()
	if previous != nil && previous.ctx.Err() == nil && previous.metadata.ConfigSHA256 == hash {
		current, loadErr := r.repo.LoadNativeCodexMetadata(ctx)
		if loadErr != nil {
			return loadErr
		}
		if *current == *previous.metadata {
			return nil
		}
	}
	if previous != nil {
		if err = r.stopSnapshot(ctx, previous); err != nil {
			return err
		}
	}
	metadata, err := r.repo.SyncNativeCodexConfig(ctx, hash)
	if err != nil {
		// A failed transaction may resume the old policy only if its persistent fence is unchanged.
		r.restoreSnapshot(ctx, previous)
		return err
	}
	return r.publishLocked(ctx, raw, metadata)
}

func (r *NativeCodexRuntime) publishLocked(ctx context.Context, raw json.RawMessage, metadata *NativeCodexMetadata) error {
	var cfg tickets.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	epoch, cancel := context.WithCancel(r.root)
	module := tickets.NewModule()
	host := &nativeCodexHost{key: NativeCodexPluginKey, state: r.repo, directory: r.gateway, metadata: metadata, repo: r.repo, activity: r.activity, epoch: epoch}
	module.SetHost(host)
	if err := module.ApplyConfig(ctx, raw); err != nil {
		cancel()
		return err
	}
	snapshot := &nativeCodexSnapshot{metadata: metadata, raw: append(json.RawMessage(nil), raw...), config: cfg, module: module, host: host, ctx: epoch, cancel: cancel}
	r.snapshot.Store(snapshot)
	if err := module.Start(epoch); err != nil {
		cancel()
		return err
	}
	return nil
}

func (r *NativeCodexRuntime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = false
	if snapshot := r.snapshot.Load(); snapshot != nil {
		return r.stopSnapshot(ctx, snapshot)
	}
	return nil
}

func (r *NativeCodexRuntime) current() *nativeCodexSnapshot {
	if r == nil {
		return nil
	}
	return r.snapshot.Load()
}

func (r *NativeCodexRuntime) metadata() *NativeCodexMetadata {
	if snapshot := r.current(); snapshot != nil {
		return snapshot.metadata
	}
	return nil
}

func nativeCodexScope(platform, kind string, in extensionv1.Invocation) bool {
	if platform != "" && platform != PlatformOpenAI {
		return false
	}
	return in.Capability == extensionv1.CapabilityRecovery || kind == "" || kind == AccountTypeOAuth || kind == AccountTypeSetupToken
}

func (r *NativeCodexRuntime) Invoke(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if !nativeCodexScope(platform, kind, in) {
		return extensionv1.Result{}, ErrNativeCodexPolicyDisabled
	}
	snapshot := r.current()
	if snapshot == nil || snapshot.ctx.Err() != nil {
		return extensionv1.Result{}, ErrNativeCodexRuntimeUnavailable
	}
	if wanted, ok := NativeCodexExecutionFromContext(ctx); ok && wanted != *snapshot.metadata {
		return extensionv1.Result{}, ErrNativeCodexRuntimeChanged
	}
	if in.AccountID > 0 {
		account, err := r.gateway.accountRepo.GetByID(ctx, in.AccountID)
		if err != nil || account == nil || account.Platform != PlatformOpenAI || (kind != "" && kind != account.Type) || !nativeCodexScope(account.Platform, account.Type, in) {
			return extensionv1.Result{}, ErrNativeCodexPolicyDisabled
		}
	}
	bound, release, err := snapshot.host.bind(ctx)
	if err != nil {
		return extensionv1.Result{}, err
	}
	defer release()
	return snapshot.module.Invoke(bound, in)
}

var invokeNativeCodex = func(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	runtime := nativeCodexPolicyRuntime.Load()
	if runtime == nil {
		return extensionv1.Result{}, ErrNativeCodexPolicyDisabled
	}
	return runtime.Invoke(ctx, platform, kind, in)
}

var bindNativeCodexContext = func(ctx context.Context, platform, kind string, in extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
	if !nativeCodexScope(platform, kind, in) {
		return nil, nil, ErrNativeCodexPolicyDisabled
	}
	runtime := nativeCodexPolicyRuntime.Load()
	snapshot := runtime.current()
	if snapshot == nil {
		return nil, nil, ErrNativeCodexPolicyDisabled
	}
	return snapshot.host.bind(ctx)
}
