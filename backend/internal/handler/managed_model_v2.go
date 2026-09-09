package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ManagedModelV2 owns admission once, before composite middleware can collapse
// a public model to a single platform. Only the selected service Forward
// adapter is called again during failover; an HTTP handler is never re-entered.
// Legacy publications and unrelated/private requests keep their existing path.
func (h *GatewayHandler) ManagedModelV2(openAI *OpenAIGatewayHandler) gin.HandlerFunc {
	affinity := newManagedModelV2AffinityStore(h.gatewayService.ManagedModelAffinityCache())
	return func(c *gin.Context) {
		request, managed := service.ManagedModelRequestFromContext(c.Request.Context())
		if !managed || request.Version < 2 || len(request.Route.Branches) == 0 {
			c.Next()
			return
		}
		if service.IsManagedModelLegacyMetadataRequest(request) {
			if !prepareManagedModelLegacyMetadataHTTP(c, request) {
				return
			}
			c.Next()
			return
		}
		c.Abort()
		switch request.Endpoint {
		case service.CompositeRouteEndpointMessages, service.CompositeRouteEndpointResponses, service.CompositeRouteEndpointChatCompletions:
			h.serveManagedModelV2(c, openAI, affinity, request)
		default:
			// Generative evidence does not establish count_tokens or WS support.
			rejectManagedModelHTTP(c)
		}
	}
}

func (h *GatewayHandler) serveManagedModelV2(c *gin.Context, openAI *OpenAIGatewayHandler, affinity *managedModelV2AffinityStore, request *service.ManagedModelRequest) {
	startedAt := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || openAI == nil || h.gatewayService == nil || openAI.gatewayService == nil || h.concurrencyHelper == nil || h.billingCacheService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Gateway is not available")
		return
	}
	if c.Request.Method != http.MethodPost || (request.Endpoint == service.CompositeRouteEndpointResponses && (!service.IsForwardableOpenAIResponsesRequestPath(c) || service.IsOpenAIResponsesInputTokensRequestPath(c))) {
		rejectManagedModelHTTP(c)
		return
	}
	setOpenAIClientTransportHTTP(c)
	model := request.Route.PublicModel
	reqLog := requestLogger(c, "handler.gateway.managed_v2", zap.Int64("api_key_id", apiKey.ID), zap.Int64("group_id", request.GroupID), zap.String("model", model))
	defer func() {
		if c.GetBool(managedModelV2AffinityPersistenceFailureKey) {
			// Forwarding may already have delivered a successful response. Keep
			// it intact while making unavailable continuation state observable;
			// opaque references and cache errors must never enter the log.
			reqLog.Warn("managed_model_v2.affinity_persistence_failed")
		}
	}()
	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil || len(body) == 0 || !managedModelV2RoutingFieldsValid(body, model) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	if request.Endpoint == service.CompositeRouteEndpointResponses {
		var normalized bool
		body, normalized = openAI.normalizeOpenAIResponsesCompactRequest(c, reqLog, body)
		if !normalized {
			return
		}
		if isBareOpenAIResponsesPath(c) && isOpenAIRemoteCompactionV2Request(body) {
			service.MarkOpenAINativeCompactionV2(c)
		}
		// Preserve the existing ingress normalization and keepalive once for
		// the whole request, not once each time a verified branch is selected.
		stopCompactKeepalive := service.StartOpenAICompactSSEKeepalive(c, openAI.openAICompactKeepaliveInterval())
		defer stopCompactKeepalive()
		defer openAI.logOpenAIRemoteCompactOutcome(c, startedAt)
	}
	stream, validStream := parseOpenAICompatibleStream(body)
	if !validStream {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage)
		return
	}
	bindRequestedReasoningEffort(c, body, model)
	// Apply the group policy to the canonical ingress once. Mapping a branch or
	// retrying must not apply a non-idempotent effort mapping a second time.
	if request.Endpoint == service.CompositeRouteEndpointMessages {
		maxEffort, mappings := anthropicCompatibleReasoningEffortPolicy(apiKey.Group.MaxReasoningEffort, apiKey.Group.ReasoningEffortMappings)
		body, _, err = service.ApplyReasoningEffortPolicy(body, maxEffort, mappings, apiKey.Group.MaxReasoningEffortOverLimit)
	} else {
		body, _, err = service.ApplyOpenAIReasoningEffortPolicy(body, apiKey.Group.MaxReasoningEffort, apiKey.Group.ReasoningEffortMappings, apiKey.Group.MaxReasoningEffortOverLimit)
	}
	if err != nil {
		respondOpenAIReasoningEffortPolicyError(c, err, h.errorResponse)
		return
	}
	parsed, err := service.ParseGatewayRequest(service.NewRequestBodyRef(body), managedModelV2ParseProtocol(request.Endpoint))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	parsed.GroupID = apiKey.GroupID
	parsed.SessionContext = &service.SessionContext{ClientIP: ip.GetClientIP(c), UserAgent: c.GetHeader("User-Agent"), APIKeyID: apiKey.ID}
	if request.Endpoint == service.CompositeRouteEndpointMessages {
		SetClaudeCodeClientContext(c, body, parsed)
		if !h.checkClaudeCodeVersion(c) {
			return
		}
		c.Request = c.Request.WithContext(service.WithThinkingEnabled(c.Request.Context(), parsed.ThinkingEnabled, h.metadataBridgeEnabled()))
	}
	if apiKey.Group.ClaudeCodeOnly && (request.Endpoint != service.CompositeRouteEndpointMessages || !service.IsClaudeCodeClient(c.Request.Context())) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "This group is restricted to Claude Code clients")
		return
	}
	setOpsRequestContext(c, model, stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(stream, false)))
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, managedModelV2AuditProtocol(request.Endpoint), model, body); decision != nil && !decision.AllowNextStage {
		if request.Endpoint == service.CompositeRouteEndpointMessages {
			h.anthropicSecurityAuditError(c, decision)
		} else if request.Endpoint == service.CompositeRouteEndpointResponses {
			h.responsesSecurityAuditError(c, decision)
		} else {
			h.openAISecurityAuditError(c, decision)
		}
		return
	}
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}
	pin, err := affinity.ResolveRequest(c.Request.Context(), apiKey.ID, request.GroupID, model, body, request, openAI.gatewayService.LookupManagedModelLegacyResponse)
	if err != nil {
		h.handleStreamingAwareErrorWithCode(c, http.StatusBadRequest, "invalid_request_error", "continuation_state_unavailable", service.OpenAIContinuationStateUnavailableClientMessage, false)
		return
	}
	pricingCtx, pricingAt := service.WithGatewayTokenRequestPricing(c.Request.Context())
	c.Request = c.Request.WithContext(pricingCtx)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	streamStarted := false
	userRelease, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, stream, &streamStarted)
	if err != nil {
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}
	if userRelease = wrapReleaseOnDone(c.Request.Context(), userRelease); userRelease != nil {
		defer userRelease()
	}
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(startedAt).Milliseconds())
	sessionHash := h.gatewayService.GenerateSessionHash(parsed)
	canonicalContext := c.Request.Context()
	canonicalHeaders := c.Request.Header.Clone()
	sessionAccounts := make(map[int64]*service.Account)
	var servedSessionAccountID int64
	defer func() {
		if len(sessionAccounts) == 0 {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(canonicalContext), 5*time.Second)
		defer cancel()
		for id, account := range sessionAccounts {
			// A successfully served session retains normal idle expiry. Failed
			// branches must not occupy max_sessions for an entire idle window.
			if id != servedSessionAccountID {
				h.gatewayService.ReleaseAccountSession(cleanupCtx, account, sessionHash)
			}
		}
	}()
	excluded := make(map[string]struct{})
	retry := newManagedModelV2RetryState(h.maxAccountSwitches)
	options := service.ManagedModelSelectionOptions{Excluded: excluded, SessionHash: sessionHash, MetadataUserID: parsed.MetadataUserID, UserID: subject.UserID, RequireCompact: strings.HasSuffix(c.Request.URL.Path, "/responses/compact"), RequireResponsesWire: service.IsOpenAINativeCompactionV2(c)}
	if pin != nil {
		options.PreferredAccountID, options.PreferredBranch = pin.AccountID, pin.BranchSelector
	}
	var lastFailover *service.UpstreamFailoverError
	for {
		if canonicalContext.Err() != nil {
			return
		}
		selected, selectErr := h.gatewayService.SelectManagedModelCandidate(canonicalContext, openAI.gatewayService, request, options)
		if selectErr != nil || selected == nil || selected.Selection == nil || selected.Selection.Account == nil {
			if lastFailover != nil {
				h.managedModelV2FailoverError(c, openAI, request.Endpoint, lastFailover, streamStarted)
			} else {
				markOpsRoutingCapacityLimited(c)
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available verified accounts for this public model", streamStarted)
			}
			return
		}
		c.Request = c.Request.WithContext(selected.Context)
		c.Request.Header = canonicalHeaders.Clone()
		if !service.ManagedModelUsesOpenAIAdapter(selected.Candidate.Branch.TargetPlatform) {
			sessionAccounts[selected.Selection.Account.ID] = selected.Selection.Account
		}
		accountRelease, slotResult := h.acquireManagedModelV2Slot(c, openAI, selected, sessionHash, stream, &streamStarted, reqLog)
		if slotResult == openAISlotAcquireProfitVetoed {
			excluded[selected.Candidate.Key()] = struct{}{}
			if pin != nil || !retry.profitVetoAllowed() {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", profitVetoExhaustedMessage, streamStarted)
				return
			}
			continue
		}
		if slotResult != openAISlotAcquireOK {
			return
		}
		// A final profit refresh may return scheduler metadata rather than the
		// original full account. Fetch the complete, current account before the
		// credential boundary and check its current rate under the frozen gate.
		originalAccount, hydrateErr := h.gatewayService.HydrateManagedModelAccount(c.Request.Context(), selected.Selection.Account.ID, selected.Candidate.Branch.Selector)
		if hydrateErr != nil {
			if accountRelease != nil {
				accountRelease()
			}
			openAI.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selected.Selection)
			h.handleStreamingAwareError(c, http.StatusNotFound, "not_found_error", service.ErrManagedModelRouteUnavailable.Error(), streamStarted)
			return
		}
		if vetoed, _ := service.OpenAIProfitControlVeto(c.Request.Context(), originalAccount); vetoed {
			if accountRelease != nil {
				accountRelease()
			}
			openAI.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selected.Selection)
			excluded[selected.Candidate.Key()] = struct{}{}
			if pin != nil || !retry.profitVetoAllowed() {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", profitVetoExhaustedMessage, streamStarted)
				return
			}
			continue
		}
		selected.Selection.Account = originalAccount
		selected.Candidate.Account = originalAccount
		if !service.ManagedModelUsesOpenAIAdapter(selected.Candidate.Branch.TargetPlatform) {
			sessionAccounts[originalAccount.ID] = originalAccount
		}
		attemptCtx, account, overlayErr := service.WithManagedModelAccountProtocol(c.Request.Context(), originalAccount, selected.Candidate.Branch)
		if overlayErr != nil {
			if accountRelease != nil {
				accountRelease()
			}
			openAI.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selected.Selection)
			h.handleStreamingAwareError(c, http.StatusNotFound, "not_found_error", service.ErrManagedModelRouteUnavailable.Error(), streamStarted)
			return
		}
		attemptCtx = service.WithAccountSwitchCount(attemptCtx, retry.switches, h.metadataBridgeEnabled())
		c.Request = c.Request.WithContext(attemptCtx)
		setOpsSelectedAccount(c, account.ID, account.Platform)
		branch := selected.Candidate.Branch
		attemptBody := h.gatewayService.ReplaceModelInBody(body, branch.Selector)
		mapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(attemptCtx, apiKey.GroupID, branch.Selector)
		// The publication's exact target wins over ordinary/private mappings.
		// Resolve above is used for the existing price/channel identity only.
		before := c.Writer.Size()
		responseHeaders := c.Writer.Header().Clone()
		result, forwardErr := func() (*managedModelV2ForwardResult, error) {
			if accountRelease != nil {
				defer accountRelease()
			}
			restore := affinity.Wrap(c, apiKey.ID, request.GroupID, model, managedModelV2Pin{AccountID: account.ID, BranchSelector: branch.Selector})
			defer restore()
			return h.forwardManagedModelV2Attempt(c, openAI, account, request.Endpoint, attemptBody, parsed)
		}()
		if forwardErr == nil && result != nil && (result.Native != nil || result.OpenAI != nil) {
			if result.Native != nil {
				servedSessionAccountID = originalAccount.ID
			}
			if result.OpenAI != nil {
				openAI.gatewayService.ReportOpenAIAccountScheduleResultForSelectionWithContext(selected.Selection, account.ID, account.GetMappedModel(branch.Selector), openAIForwardSucceededForScheduling(result.OpenAI), result.OpenAI.FirstTokenMs, attemptCtx)
			}
			h.recordManagedModelV2Usage(c, openAI, result, apiKey, originalAccount, subscription, mapping, model, body, pricingAt, reqLog)
			return
		}
		var failoverErr *service.UpstreamFailoverError
		if !errors.As(forwardErr, &failoverErr) || !managedModelV2MayReplay(c, before, pin, failoverErr) {
			openAI.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selected.Selection)
			// Native Messages explicitly reports measured partial usage on a
			// delivered but interrupted stream; retain that existing bill once.
			if result != nil && result.Native != nil && c.Writer.Size() > before {
				h.recordManagedModelV2Usage(c, openAI, result, apiKey, originalAccount, subscription, mapping, model, body, pricingAt, reqLog)
			}
			if failoverErr != nil {
				h.managedModelV2FailoverError(c, openAI, request.Endpoint, failoverErr, streamStarted || c.Writer.Size() > before)
			} else if !gatewayForwardErrorAlreadyCommunicated(c, before, forwardErr) {
				h.ensureForwardErrorResponse(c, streamStarted)
			}
			return
		}
		if c.Writer.Written() {
			streamStarted = true
		}
		lastFailover = failoverErr
		action := retry.next(c.Request.Context(), selected.Candidate, failoverErr)
		if service.ManagedModelUsesOpenAIAdapter(branch.TargetPlatform) {
			finalizeOpenAIFailoverSelection(openAI.gatewayService, selected.Selection, originalAccount, account.GetMappedModel(branch.Selector), failoverErr, action)
		} else {
			openAI.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selected.Selection)
		}
		switch action {
		case openAIFailoverRetrySameAccount:
			options.PreferredAccountID, options.PreferredBranch = account.ID, branch.Selector
		case openAIFailoverRetrySwitchAccount:
			excluded[selected.Candidate.Key()] = struct{}{}
			options.PreferredAccountID, options.PreferredBranch = 0, ""
			if service.ManagedModelUsesOpenAIAdapter(branch.TargetPlatform) {
				openAI.gatewayService.CooldownOpenAIRetryExhausted(attemptCtx, originalAccount, account.GetMappedModel(branch.Selector), failoverErr)
				openAI.gatewayService.RecordOpenAIAccountSwitch()
			} else if failoverErr.RetryableOnSameAccount {
				h.gatewayService.TempUnscheduleRetryableError(attemptCtx, account.ID, failoverErr)
			}
		case openAIFailoverRetryCanceled:
			return
		default:
			h.managedModelV2FailoverError(c, openAI, request.Endpoint, failoverErr, streamStarted)
			return
		}
		if !c.Writer.Written() {
			restoreManagedModelV2Headers(c.Writer.Header(), responseHeaders)
		}
	}
}

func managedModelV2ParseProtocol(endpoint string) string {
	if endpoint == service.CompositeRouteEndpointMessages {
		return service.PlatformAnthropic
	}
	return endpoint
}

func managedModelV2RoutingFieldsValid(body []byte, model string) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return false
	}
	models, streams, valid := 0, 0, true
	root.ForEach(func(key, value gjson.Result) bool {
		switch strings.ToLower(key.String()) {
		case "model":
			models++
			valid = valid && key.String() == "model" && value.Type == gjson.String && value.String() == model
		case "stream":
			streams++
			valid = valid && key.String() == "stream"
		}
		return valid
	})
	return valid && models == 1 && streams <= 1
}

func managedModelV2AuditProtocol(endpoint string) string {
	switch endpoint {
	case service.CompositeRouteEndpointMessages:
		return service.ContentModerationProtocolAnthropicMessages
	case service.CompositeRouteEndpointResponses:
		return service.ContentModerationProtocolOpenAIResponses
	default:
		return service.ContentModerationProtocolOpenAIChat
	}
}

func restoreManagedModelV2Headers(target, original http.Header) {
	for key := range target {
		delete(target, key)
	}
	for key, values := range original {
		target[key] = append([]string(nil), values...)
	}
}

func (h *GatewayHandler) managedModelV2FailoverError(c *gin.Context, openAI *OpenAIGatewayHandler, endpoint string, err *service.UpstreamFailoverError, streamStarted bool) {
	if endpoint == service.CompositeRouteEndpointResponses {
		h.handleResponsesFailoverExhausted(c, err, streamStarted)
	} else if endpoint == service.CompositeRouteEndpointMessages {
		h.handleFailoverExhausted(c, err, service.PlatformAnthropic, streamStarted)
	} else {
		openAI.handleFailoverExhausted(c, err, streamStarted)
	}
}
