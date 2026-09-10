package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type managedModelV2ForwardResult struct {
	Native *service.ForwardResult
	OpenAI *service.OpenAIForwardResult
}

// forwardManagedModelV2Attempt is the only transport dispatch point. All
// branches reuse the existing protocol converters and their usage observers.
// It deliberately has no authentication, user slot, billing check or retry.
func (h *GatewayHandler) forwardManagedModelV2Attempt(c *gin.Context, openAI *OpenAIGatewayHandler, account *service.Account, endpoint string, body []byte, ingress *service.ParsedRequest) (*managedModelV2ForwardResult, error) {
	ctx := c.Request.Context()
	result := &managedModelV2ForwardResult{}
	var err error
	setActualUpstreamEndpoint(c, "")
	if service.ManagedModelUsesOpenAIAdapter(account.Platform) {
		switch endpoint {
		case service.CompositeRouteEndpointMessages:
			result.OpenAI, err = openAI.gatewayService.ForwardAsAnthropic(ctx, c, account, body, "", "")
		case service.CompositeRouteEndpointChatCompletions:
			result.OpenAI, err = openAI.gatewayService.ForwardAsChatCompletions(ctx, c, account, body, "", "")
		case service.CompositeRouteEndpointResponses:
			result.OpenAI, err = openAI.gatewayService.Forward(ctx, c, account, body)
		default:
			return nil, service.ErrManagedModelRouteUnavailable
		}
		return result, err
	}
	parsed, parseErr := service.ParseGatewayRequest(service.NewRequestBodyRef(body), managedModelV2ParseProtocol(endpoint))
	if parseErr != nil {
		return nil, parseErr
	}
	parsed.GroupID, parsed.SessionContext = ingress.GroupID, ingress.SessionContext
	c.Set("parsed_request", parsed)
	switch endpoint {
	case service.CompositeRouteEndpointMessages:
		if err := parsed.ReplaceBody(h.gatewayService.ApplyBedrockCCCompat(c, body, parsed.Model, account, parsed.GroupID)); err != nil {
			return nil, err
		}
		body = parsed.Body.Bytes()
		switch {
		case shouldUseAntigravityCompat(account):
			if h.antigravityGatewayService == nil {
				return nil, fmt.Errorf("antigravity compatibility service is unavailable")
			}
			result.Native, err = h.antigravityGatewayService.Forward(ctx, c, account, body, false)
		case account.Platform == service.PlatformGemini:
			if h.geminiCompatService == nil {
				return nil, fmt.Errorf("gemini compatibility service is unavailable")
			}
			result.Native, err = h.geminiCompatService.Forward(ctx, c, account, body)
		default:
			setActualUpstreamEndpoint(c, "/v1/messages")
			result.Native, err = h.gatewayService.Forward(ctx, c, account, parsed)
		}
	case service.CompositeRouteEndpointResponses:
		if shouldUseAntigravityCompat(account) {
			if h.antigravityGatewayService == nil {
				return nil, fmt.Errorf("antigravity compatibility service is unavailable")
			}
			result.Native, err = h.antigravityGatewayService.ForwardAsResponses(ctx, c, account, body, parsed)
		} else {
			result.Native, err = h.gatewayService.ForwardAsResponses(ctx, c, account, body, parsed)
		}
	case service.CompositeRouteEndpointChatCompletions:
		if shouldUseAntigravityCompat(account) {
			if h.antigravityGatewayService == nil {
				return nil, fmt.Errorf("antigravity compatibility service is unavailable")
			}
			result.Native, err = h.antigravityGatewayService.ForwardAsChatCompletions(ctx, c, account, body, parsed)
		} else {
			result.Native, err = h.gatewayService.ForwardAsChatCompletions(ctx, c, account, body, parsed)
		}
	default:
		return nil, service.ErrManagedModelRouteUnavailable
	}
	return result, err
}

func (h *GatewayHandler) acquireManagedModelV2Slot(c *gin.Context, openAI *OpenAIGatewayHandler, selected *service.ManagedModelSelection, sessionHash string, stream bool, streamStarted *bool, log *zap.Logger) (func(), openAISlotAcquireResult) {
	selection := selected.Selection
	request, _ := service.ManagedModelRequestFromContext(c.Request.Context())
	if service.ManagedModelUsesOpenAIAdapter(selected.Candidate.Branch.TargetPlatform) {
		return openAI.acquireResponsesAccountSlot(c, &request.GroupID, sessionHash, selection, stream, streamStarted, log)
	}
	ctx := service.ContextWithSelectionProfitGate(c.Request.Context(), selection)
	account := selection.Account
	release := selection.ReleaseFunc
	if !selection.Acquired {
		wait := selection.WaitPlan
		if wait == nil {
			h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", *streamStarted)
			return nil, openAISlotAcquireFailed
		}
		canWait, countErr := h.concurrencyHelper.IncrementAccountWaitCount(ctx, account.ID, wait.MaxWaiting)
		if countErr == nil && !canWait {
			h.handleStreamingAwareErrorWithCode(c, http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode, "Too many pending requests, please retry later", *streamStarted)
			return nil, openAISlotAcquireFailed
		}
		var acquireErr error
		release, acquireErr = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(c, account.ID, wait.MaxConcurrency, wait.Timeout, stream, streamStarted)
		if countErr == nil && canWait {
			h.concurrencyHelper.DecrementAccountWaitCount(ctx, account.ID)
		}
		if acquireErr != nil {
			h.handleConcurrencyError(c, acquireErr, "account", *streamStarted)
			return nil, openAISlotAcquireFailed
		}
	}
	latest, vetoed, _ := h.gatewayService.GatewayProfitControlVetoLatest(ctx, account)
	if vetoed {
		if release != nil {
			release()
		}
		return nil, openAISlotAcquireProfitVetoed
	}
	selection.Account = latest
	if selection.ProfitGateActive() {
		if err := h.gatewayService.BindStickySessionAfterProfitAdmission(ctx, &request.GroupID, sessionHash, latest.ID); err != nil {
			log.Warn("managed_v2.sticky_binding_failed", zap.Error(err))
		}
	}
	return wrapReleaseOnDone(ctx, release), openAISlotAcquireOK
}

func (h *GatewayHandler) recordManagedModelV2Usage(c *gin.Context, openAI *OpenAIGatewayHandler, result *managedModelV2ForwardResult, apiKey *service.APIKey, account *service.Account, subscription *service.UserSubscription, mapping service.ChannelMappingResult, model string, body []byte, pricingAt time.Time, log *zap.Logger) {
	if result == nil {
		return
	}
	if result.OpenAI != nil {
		// Forward adapters see an internal selector. Publication pricing and
		// usage rows are owned by the public model, including direct Responses
		// forwards that do not pass through a response-conversion layer.
		billable := *result.OpenAI
		billable.Model, billable.BillingModel = model, model
		result.OpenAI = &billable
		stampOpenAIRequestedReasoningEffort(result.OpenAI, c)
		snapshot := snapshotOpenAIUsageMetadata(c, apiKey, account, subscription, mapping, model, result.OpenAI, body)
		input := snapshot.Input(result.OpenAI, h.apiKeyService, pricingAt)
		openAI.submitOpenAIUsageRecordTask(c.Request.Context(), result.OpenAI, func(ctx context.Context) {
			if err := openAI.gatewayService.RecordUsage(ctx, input); err != nil {
				log.Error("managed_v2.record_usage_failed", zap.Error(err))
			}
		})
		return
	}
	if result.Native == nil {
		return
	}
	billable := *result.Native
	billable.Model = model
	result.Native = &billable
	stampForwardRequestedReasoningEffort(result.Native, service.RequestedReasoningEffortFromContext(c.Request.Context()))
	// Reuse the immutable metadata snapshot so the usage worker never retains
	// a recycled Gin context or the attempt-only protocol overlay.
	snapshot := snapshotOpenAIUsageMetadata(c, apiKey, account, subscription, mapping, model, openAIForwardResultFromNativeAnthropic(result.Native), body)
	input := &service.RecordUsageInput{
		Result: result.Native, APIKey: &snapshot.apiKey, User: snapshot.apiKey.User,
		Account: &snapshot.account, Subscription: snapshot.subscription, PricingAt: pricingAt,
		QuotaPlatform: snapshot.quotaPlatform, InboundEndpoint: snapshot.inboundEndpoint,
		UpstreamEndpoint: snapshot.upstreamEndpoint, UserAgent: snapshot.userAgent,
		IPAddress: snapshot.ipAddress, SessionID: snapshot.sessionID,
		RequestPayloadHash: snapshot.requestPayloadHash, APIKeyService: h.apiKeyService,
		ChannelUsageFields: snapshot.channelFields,
	}
	h.submitUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, input); err != nil {
			log.Error("managed_v2.record_usage_failed", zap.Error(err))
		}
	})
}

// managedModelV2MayReplay is deliberately stricter than transport status:
// account-bound state and delivered semantic content can never cross branches.
func managedModelV2MayReplay(c *gin.Context, before int, pin *managedModelV2Pin, err *service.UpstreamFailoverError) bool {
	if pin != nil || c == nil || c.Request == nil || c.Request.Context().Err() != nil || err == nil || !err.ShouldRetryNextAccount() {
		return false
	}
	if service.IsResponseCommitted(c) {
		return false
	}
	if c.Writer.Size() == before {
		return true
	}
	return err.SafeToFailoverAfterWrite && gatewayStreamHasOnlyHeartbeats(c)
}

type managedModelV2RetryState struct {
	maxSwitches         int
	switches            int
	firstOutputSwitches int
	profitVetoes        int
	same                map[int64]int
}

func newManagedModelV2RetryState(maxSwitches int) *managedModelV2RetryState {
	return &managedModelV2RetryState{maxSwitches: maxSwitches, same: make(map[int64]int)}
}

func (state *managedModelV2RetryState) profitVetoAllowed() bool {
	state.profitVetoes++
	return state.profitVetoes < maxProfitVetoAttempts
}

func (state *managedModelV2RetryState) next(ctx context.Context, candidate service.ManagedModelCandidate, failover *service.UpstreamFailoverError) openAIFailoverRetryAction {
	if ctx.Err() != nil {
		return openAIFailoverRetryCanceled
	}
	if failover == nil || !failover.ShouldRetryNextAccount() || candidate.Account == nil {
		return openAIFailoverRetryStop
	}
	limit := candidate.Account.GetPoolModeRetryCount()
	if service.ManagedModelUsesOpenAIAdapter(candidate.Branch.TargetPlatform) {
		limit = openAISameAccountRetryLimit(candidate.Account, failover, true)
	}
	// Alternate targets have independent exclusions, not a new allowance for
	// the same account's configured retry budget.
	key := candidate.Account.ID
	if sameAccountRetryAllowed(failover, state.same[key], limit) {
		state.same[key]++
		if !sleepWithContext(ctx, sameAccountRetryDelayFor(failover, state.same[key])) {
			return openAIFailoverRetryCanceled
		}
		return openAIFailoverRetrySameAccount
	}
	if state.switches >= state.maxSwitches {
		return openAIFailoverRetryStop
	}
	if service.ManagedModelUsesOpenAIAdapter(candidate.Branch.TargetPlatform) && openAIFirstOutputFailoverExhausted(failover, &state.firstOutputSwitches) {
		return openAIFailoverRetryStop
	}
	state.switches++
	return openAIFailoverRetrySwitchAccount
}
