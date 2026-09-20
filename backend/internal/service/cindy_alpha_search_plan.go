package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const maxCindyAlphaSearchUses = 1

// CindyAlphaSearchPlan is the provider-owned admission decision for the
// first-class Cindy search bridge. It deliberately contains no request,
// response, credential, proxy, or endpoint data; the host retains those
// boundaries and performs all upstream I/O.
type CindyAlphaSearchPlan = extensionv1.CindyAlphaSearchPlan

// ResolveCindyAlphaSearchPlan asks the Cindy provider for a bounded routing
// decision. Only the requested public model crosses the provider boundary.
func ResolveCindyAlphaSearchPlan(ctx context.Context, requestedModel string) (CindyAlphaSearchPlan, error) {
	return resolveCindyAlphaSearchPlanForAccount(ctx, requestedModel, 0)
}

func resolveCindyAlphaSearchPlanForAccount(ctx context.Context, requestedModel string, accountID int64) (CindyAlphaSearchPlan, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return CindyAlphaSearchPlan{}, fmt.Errorf("Cindy search model is required")
	}
	payload, err := json.Marshal(extensionv1.CindyAlphaSearchPlanRequest{Model: requestedModel})
	if err != nil {
		return CindyAlphaSearchPlan{}, fmt.Errorf("marshal Cindy search plan request: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(callCtx, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{
		Capability: extensionv1.CapabilityProvider,
		Operation:  "cindy.search.plan",
		Payload:    payload,
		AccountID:  accountID,
	})
	if err != nil {
		return CindyAlphaSearchPlan{}, fmt.Errorf("resolve Cindy search plan: %w", err)
	}
	if result.Code != "" {
		return CindyAlphaSearchPlan{}, fmt.Errorf("resolve Cindy search plan returned code %q", result.Code)
	}
	var plan CindyAlphaSearchPlan
	decoder := json.NewDecoder(bytes.NewReader(result.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return CindyAlphaSearchPlan{}, fmt.Errorf("decode Cindy search plan: %w", err)
	}
	if err := validateCindyAlphaSearchPlan(requestedModel, plan); err != nil {
		return CindyAlphaSearchPlan{}, err
	}
	return plan, nil
}

func validateCindyAlphaSearchPlan(requestedModel string, plan CindyAlphaSearchPlan) error {
	if plan.RequestedModel != requestedModel {
		return fmt.Errorf("invalid Cindy search plan: requested model mismatch")
	}
	if strings.Contains(plan.RequestedModel, "://") || strings.Contains(plan.UpstreamModel, "://") || strings.Contains(plan.NativeMessagesModel, "://") {
		return fmt.Errorf("invalid Cindy search plan: URL-like model value")
	}
	if !plan.Allowed {
		switch plan.Reason {
		case "", "search_disabled", "model_required", "model_unavailable", "responses_search_unavailable":
		default:
			return fmt.Errorf("invalid Cindy search plan: unknown denial reason")
		}
		if plan.UpstreamModel != "" || plan.PrimaryProtocol != "" || plan.FallbackProtocol != "" ||
			plan.FallbackOnCapabilityMiss || plan.FallbackOnMissingSearchEvidence || plan.ResponsesToolType != "" || plan.NativeMessagesModel != "" || plan.MaxSearchUses != 0 {
			return fmt.Errorf("invalid Cindy search plan: denied plan contains routing fields")
		}
		return nil
	}
	if plan.Reason != "" {
		return fmt.Errorf("invalid Cindy search plan: allowed plan contains a reason")
	}
	if strings.TrimSpace(plan.UpstreamModel) == "" {
		return fmt.Errorf("invalid Cindy search plan: upstream model is required")
	}
	if plan.PrimaryProtocol != "responses" {
		return fmt.Errorf("invalid Cindy search plan: primary protocol must be responses")
	}
	if plan.ResponsesToolType != "web_search" {
		return fmt.Errorf("invalid Cindy search plan: unsupported Responses tool type")
	}
	if plan.MaxSearchUses < 1 || plan.MaxSearchUses > maxCindyAlphaSearchUses {
		return fmt.Errorf("invalid Cindy search plan: max search uses is out of bounds")
	}
	if plan.FallbackOnCapabilityMiss || plan.FallbackOnMissingSearchEvidence {
		if plan.FallbackProtocol != "messages" || plan.NativeMessagesModel != CindyWebSearchModel {
			return fmt.Errorf("invalid Cindy search plan: Messages fallback is incomplete")
		}
	} else if plan.FallbackProtocol != "" || strings.TrimSpace(plan.NativeMessagesModel) != "" {
		return fmt.Errorf("invalid Cindy search plan: unexpected fallback fields")
	}
	return nil
}
