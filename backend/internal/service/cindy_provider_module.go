package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"

	cindycatalog "github.com/Wei-Shaw/sub2api/internal/cindyprovider/catalog"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// cindyProviderRuntime answers the Cindy catalog, pricing, health, balance probe
// and group planning operations in process. The cindy-provider plugin served
// them before; its switches now live in the cindy_provider_config setting.
type cindyProviderRuntime struct {
	module       *cindycatalog.Module
	policySHA256 string
	// cache memoizes configuration-derived answers such as the pricing
	// snapshot, which request paths consult on every call. A configuration
	// change installs a new runtime and with it an empty cache.
	cache     sync.Map
	cacheSize atomic.Int64
}

const cindyProviderCacheLimit = 2048

var cindyProvider atomic.Pointer[cindyProviderRuntime]
var cindyProviderPolicyMu sync.RWMutex

type cindyProviderRuntimeKey struct{}

func init() { ConfigureCindyProvider(nil) }

// ConfigureCindyProvider installs the effective switches (startup load, admin
// update, tests). A nil value falls back to the deploy-time rollout flags.
func ConfigureCindyProvider(config *extensionv1.CindyProviderConfig) {
	cindyProviderPolicyMu.Lock()
	defer cindyProviderPolicyMu.Unlock()
	effective := LegacyCindyProviderConfig()
	if config != nil {
		effective = *config
	}
	module := cindycatalog.New()
	raw, _ := json.Marshal(effective)
	if err := module.ApplyConfig(context.Background(), raw); err != nil {
		panic(err) // A marshaled CindyProviderConfig always validates.
	}
	digest := sha256.Sum256(append([]byte("cindy-native-account-policy-v1\x00"), raw...))
	cindyProvider.Store(&cindyProviderRuntime{module: module, policySHA256: hex.EncodeToString(digest[:])})
}

// invokeCindyProvider runs one Cindy provider operation. Tests replace it to
// model provider answers.
var invokeCindyProvider = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	runtime, _ := ctx.Value(cindyProviderRuntimeKey{}).(*cindyProviderRuntime)
	if runtime == nil {
		runtime = cindyProvider.Load()
	}
	return runtime.module.Invoke(ctx, in)
}

// invokeCindyProviderCached is for operations whose answer depends only on the
// configuration and the payload. The account does not change the answer.
func invokeCindyProviderCached(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	runtime := cindyProvider.Load()
	key := in.Capability + "\x00" + in.Operation + "\x00" + string(in.Payload)
	if cached, ok := runtime.cache.Load(key); ok {
		result := cached.(extensionv1.Result)
		result.Payload = append(json.RawMessage(nil), result.Payload...)
		return result, nil
	}
	// A concurrent settings update must not cache a new runtime's answer in an
	// earlier configuration's cache, or combine two configuration snapshots.
	result, err := invokeCindyProvider(context.WithValue(ctx, cindyProviderRuntimeKey{}, runtime), in)
	if err != nil || result.Code != "" || runtime.cacheSize.Load() >= cindyProviderCacheLimit {
		return result, err
	}
	stored := result
	stored.Payload = append(json.RawMessage(nil), result.Payload...)
	if _, loaded := runtime.cache.LoadOrStore(key, stored); !loaded {
		runtime.cacheSize.Add(1)
	}
	return result, nil
}
