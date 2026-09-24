package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/gin-gonic/gin"
)

type cindyPricingContextKey struct{}
type capturedCindyPricing struct {
	accountID int64
	owner     int64
	snapshot  *extensionv1.CindyPricingSnapshot
	catalog   *CindyCatalogSnapshot
}

// Capture before provider IO. Billing retains this immutable reference when a
// plugin is stopped, replaced, or reconfigured while the response is streaming.
func CaptureCindyPricingContext(ctx context.Context, c *gin.Context, account *Account) (context.Context, error) {
	if account == nil || !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return ctx, nil
	}
	owner, err := cindyProviderAdmissionOwner(ctx, account)
	if err != nil {
		return ctx, err
	}
	if existing, ok := ctx.Value(cindyPricingContextKey{}).(*capturedCindyPricing); ok && existing != nil && existing.accountID == account.ID {
		if existing.catalog == nil || existing.owner != owner {
			return ctx, errors.New("captured Cindy policy owner is unavailable")
		}
		if c != nil && c.Request != nil {
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), cindyPricingContextKey{}, existing))
		}
		return ctx, nil
	}
	images, _ := currentImageToolsConfig()
	query := extensionv1.CindyPricingQuery{}
	if images != (extensionv1.ImageToolsConfig{}) {
		query.Images = &images
	}
	payload, _ := json.Marshal(query)
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeCindyProviderCached(call, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.pricing", AccountID: account.ID, Payload: payload})
	if err != nil || result.Code != "" || result.PluginID != owner || len(result.Payload) == 0 || len(result.Payload) > extensionv1.MaxPayloadBytes {
		return ctx, errors.New("provider: Cindy provider policy is unavailable")
	}
	var snapshot extensionv1.CindyPricingSnapshot
	decoder := json.NewDecoder(bytes.NewReader(result.Payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(new(any)) != io.EOF || snapshot.Results == nil ||
		snapshot.CatalogSnapshot == nil || snapshot.Config != snapshot.CatalogSnapshot.Config {
		return ctx, errors.New("invalid Cindy pricing snapshot")
	}
	catalog, err := newCindyCatalogSnapshot(*snapshot.CatalogSnapshot, result.PluginID, images)
	if err != nil {
		return ctx, err
	}
	value := &capturedCindyPricing{accountID: account.ID, owner: result.PluginID, snapshot: &snapshot, catalog: catalog}
	if c != nil && c.Request != nil {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), cindyPricingContextKey{}, value))
	}
	return context.WithValue(ctx, cindyPricingContextKey{}, value), nil
}

// A websocket turn is a new request. Prior turn snapshots remain immutable in
// their billing tasks while the new turn observes the current configuration.
func RefreshCindyPricingContext(ctx context.Context, account *Account) (context.Context, error) {
	return CaptureCindyPricingContext(context.WithValue(ctx, cindyPricingContextKey{}, (*capturedCindyPricing)(nil)), nil, account)
}

// ProviderPricingTurnContexts is connection-local, never Account state. A turn
// may be normalized before BeforeTurn runs; both phases must reuse its capture.
// Completed billing tasks keep their copied reference after Take removes it.
type ProviderPricingTurnContexts struct {
	mu       sync.Mutex
	account  *Account
	contexts map[int]context.Context
}

func NewProviderPricingTurnContexts(initial context.Context, account *Account) *ProviderPricingTurnContexts {
	turns := &ProviderPricingTurnContexts{account: account, contexts: make(map[int]context.Context)}
	if account == nil || !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		capturedCindyPolicyFromContext(initial, account) != nil {
		turns.contexts[1] = initial
	}
	return turns
}

func (t *ProviderPricingTurnContexts) Copy(turn int, base context.Context) (context.Context, error) {
	if t == nil || turn <= 0 || base == nil {
		return nil, errors.New("provider turn policy is unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	value, exists := t.contexts[turn]
	if !exists {
		var err error
		value, err = RefreshCindyPricingContext(base, t.account)
		if err != nil {
			return nil, err
		}
		t.contexts[turn] = value
	}
	return CopyProviderPricingContext(value, base), nil
}

func (t *ProviderPricingTurnContexts) Take(turn int, base context.Context) (context.Context, bool) {
	if t == nil {
		return base, false
	}
	t.mu.Lock()
	value, exists := t.contexts[turn]
	delete(t.contexts, turn)
	t.mu.Unlock()
	if !exists {
		return base, false
	}
	return CopyProviderPricingContext(value, base), true
}

func EnsureCindyProviderAvailable(ctx context.Context, account *Account) error {
	_, err := cindyProviderAdmissionOwner(ctx, account)
	return err
}

func cindyProviderAdmissionOwner(ctx context.Context, account *Account) (int64, error) {
	if account == nil || !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return 0, nil
	}
	if account.ID <= 0 {
		return 0, errors.New("provider: Cindy provider account identity is unavailable")
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeCindyProviderCached(call, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.features", AccountID: account.ID, Payload: json.RawMessage(`{}`)})
	if err != nil || result.Code != "" {
		return 0, errors.New("provider: Cindy provider policy is unavailable")
	}
	return result.PluginID, nil
}

func CopyProviderPricingContext(parent, base context.Context) context.Context {
	if parent != nil {
		if value, ok := parent.Value(cindyPricingContextKey{}).(*capturedCindyPricing); ok {
			return context.WithValue(base, cindyPricingContextKey{}, value)
		}
	}
	return base
}

func cindyPricingSnapshotFromContext(ctx context.Context, account *Account) *extensionv1.CindyPricingSnapshot {
	value := capturedCindyPolicyFromContext(ctx, account)
	if value == nil {
		return nil
	}
	return value.snapshot
}

func capturedCindyPolicyFromContext(ctx context.Context, account *Account) *capturedCindyPricing {
	if ctx == nil || account == nil {
		return nil
	}
	value, _ := ctx.Value(cindyPricingContextKey{}).(*capturedCindyPricing)
	if value == nil || value.accountID != account.ID {
		return nil
	}
	return value
}

func queryCindyPricingSnapshot(snapshot *extensionv1.CindyPricingSnapshot, method, model string, outputs []any) bool {
	if snapshot == nil {
		return queryCindyCatalog(method, []any{model}, outputs)
	}
	key, _ := json.Marshal([]string{method, strings.TrimSpace(model)})
	return decodeCindyCatalogResult(snapshot.Results[string(key)], outputs)
}

func cindyCatalogEnabledForBilling(ctx context.Context, account *Account) bool {
	if snapshot := cindyPricingSnapshotFromContext(ctx, account); snapshot != nil {
		return snapshot.Config.CatalogEnabled
	}
	return CindyCapabilityCatalogFeatureEnabled()
}

func cindyZeroPriceForBilling(ctx context.Context, account *Account, model string) bool {
	var value bool
	queryCindyPricingSnapshot(cindyPricingSnapshotFromContext(ctx, account), "CindyModelUsesExplicitZeroPrice", model, []any{&value})
	return value
}
