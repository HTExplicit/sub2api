package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
)

type cindyPricingContextKey struct{}
type capturedCindyPricing struct {
	accountID int64
	snapshot  *extensionv1.CindyPricingSnapshot
}

// Capture before provider IO. Billing retains this immutable reference when a
// plugin is stopped, replaced, or reconfigured while the response is streaming.
func CaptureCindyPricingContext(ctx context.Context, c *gin.Context, account *Account) (context.Context, error) {
	if account == nil || !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return ctx, nil
	}
	if err := EnsureCindyProviderAvailable(ctx, account); err != nil {
		return ctx, err
	}
	if existing, ok := ctx.Value(cindyPricingContextKey{}).(*capturedCindyPricing); ok && existing != nil && existing.accountID == account.ID {
		return ctx, nil
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.pricing", AccountID: account.ID, Payload: json.RawMessage(`{}`)})
	if err != nil || result.Code != "" {
		return ctx, errors.New("Cindy provider policy is unavailable")
	}
	var snapshot extensionv1.CindyPricingSnapshot
	if json.Unmarshal(result.Payload, &snapshot) != nil || snapshot.Results == nil {
		return ctx, errors.New("invalid Cindy pricing snapshot")
	}
	value := &capturedCindyPricing{accountID: account.ID, snapshot: &snapshot}
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

func EnsureCindyProviderAvailable(ctx context.Context, account *Account) error {
	if account == nil || !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return nil
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.features", AccountID: account.ID, Payload: json.RawMessage(`{}`)})
	if err != nil || result.Code != "" {
		return errors.New("Cindy provider policy is unavailable")
	}
	return nil
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
	if ctx == nil || account == nil {
		return nil
	}
	value, _ := ctx.Value(cindyPricingContextKey{}).(*capturedCindyPricing)
	if value == nil || value.accountID != account.ID {
		return nil
	}
	return value.snapshot
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
