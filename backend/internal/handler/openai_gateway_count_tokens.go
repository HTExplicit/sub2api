package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GrokCountTokens handles Anthropic-compatible count_tokens requests locally.
// The route middleware already authenticates the API key and resolves the
// group; this handler intentionally does not select an account or check billing.
func (h *OpenAIGatewayHandler) GrokCountTokens(c *gin.Context) {
	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.anthropicErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	bodyRef := service.NewRequestBodyRef(body)
	parsedReq, err := service.ParseGatewayRequest(bodyRef, domain.PlatformAnthropic)
	if err != nil {
		logRequestBodyParseFailure(requestLogger(c, "handler.openai_gateway.grok_count_tokens"), body, err)
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	if parsedReq.Model == "" {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	estimated, err := service.EstimateGrokCountTokens(parsedReq.Body.Bytes())
	if err != nil {
		requestLogger(c, "handler.openai_gateway.grok_count_tokens").Warn("grok_count_tokens.local_estimate_failed", zap.Error(err))
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	setOpsRequestContext(c, parsedReq.Model, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(false, false)))
	c.JSON(http.StatusOK, gin.H{"input_tokens": estimated})
}

// CountTokens handles Anthropic-compatible POST /v1/messages/count_tokens for OpenAI groups.
// It validates billing and routes to an OpenAI token-count bridge without taking concurrency slots
// or recording usage.
func (h *OpenAIGatewayHandler) CountTokens(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.count_tokens",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	if apiKey.Group != nil && !apiKey.Group.AllowMessagesDispatch {
		h.anthropicErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group does not allow /v1/messages dispatch")
		return
	}

	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.anthropicErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	bodyRef := service.NewRequestBodyRef(body)
	parsedReq, err := service.ParseGatewayRequest(bodyRef, domain.PlatformAnthropic)
	if err != nil {
		logRequestBodyParseFailure(reqLog, body, err)
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	body = parsedReq.Body.Bytes()
	if parsedReq.Model == "" {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	reqModel := parsedReq.Model
	ensureCompositeTargetPlatform(c, apiKey, reqModel)
	if !compositeTargetPlatformAllowed(c, apiKey, reqModel, service.PlatformOpenAI) {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Model is not supported by this OpenAI-compatible endpoint for composite groups")
		return
	}
	routingModel := service.NormalizeOpenAICompatRequestedModel(reqModel)
	preferredMappedModel := resolveOpenAIMessagesDispatchMappedModel(apiKey, reqModel)
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", parsedReq.Stream))

	setOpsRequestContext(c, reqModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(false, false)))

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	mappedBodyForMessages := newOpenAIModelMappedBodyCache(body, h.gatewayService.ReplaceModelInBody)

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai_count_tokens.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.anthropicErrorResponse(c, status, code, message)
		return
	}

	requestStart := time.Now()
	// count_tokens 不计费：显式豁免利润门，避免高倍率账号池被门排除后连
	// token 计数都返回 no available accounts。
	c.Request = c.Request.WithContext(service.WithOpenAIProfitControlSuppressed(c.Request.Context()))
	sessionHash := h.gatewayService.GenerateSessionHash(c, body)
	currentRoutingModel := routingModel
	if preferredMappedModel != "" {
		currentRoutingModel = preferredMappedModel
	}
	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	retryState := newOpenAIFailoverRetryState()
	var lastUpstreamErr error
	var lastFailoverErr *service.UpstreamFailoverError
	var sameAccountRetrySelection *service.AccountSelectionResult
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	forwardBody := mappedBodyForMessages(channelMapping.Mapped, channelMapping.MappedModel)
	defaultMappedModel := preferredMappedModel

	for {
		retryingSameAccount := sameAccountRetrySelection != nil
		var selection *service.AccountSelectionResult
		var err error
		if retryingSameAccount {
			selection, err = h.gatewayService.ReacquireOpenAISameAccountSelection(c.Request.Context(), sameAccountRetrySelection)
			sameAccountRetrySelection = nil
		} else {
			selection, _, err = h.gatewayService.SelectAccountWithSchedulerForCapability(
				c.Request.Context(), apiKey.GroupID, "", sessionHash, currentRoutingModel,
				failedAccountIDs, service.OpenAIUpstreamTransportAny,
				service.OpenAIEndpointCapabilityChatCompletions, false, false, false,
				openAICompatibleRequestPlatform(c.Request.Context(), apiKey),
			)
		}
		service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
		if err != nil || selection == nil || selection.Account == nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if retryingSameAccount && lastUpstreamErr != nil {
				writeCountTokensFailoverError(c, lastFailoverErr, lastUpstreamErr)
				return
			}
			if len(failedAccountIDs) > 0 && lastUpstreamErr != nil {
				writeCountTokensFailoverError(c, lastFailoverErr, lastUpstreamErr)
				return
			}
			requestPlatform := openAICompatibleRequestPlatform(c.Request.Context(), apiKey)
			if err == nil {
				err = service.ErrNoAvailableAccounts
			}
			reqLog.Warn("openai_count_tokens.account_select_failed", zap.Error(openAICompatibleSelectionErrorForLog(err, requestPlatform)))
			cls := classifyOpenAICompatibleNoAccountErrorFromGin(c, h.gatewayService, apiKey, currentRoutingModel, reqModel)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
			}
			h.anthropicErrorResponse(c, cls.Status, cls.ErrType, cls.Message)
			return
		}

		account := selection.Account
		defer h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
		setOpsSelectedAccount(c, account.ID, account.Platform)
		// CountTokens has an Anthropic response contract; acquire silently and let
		// this handler render any slot error in the correct envelope.
		accountRelease, slotResult := h.acquireResponsesAccountSlotForSameAccountRetry(
			c, apiKey.GroupID, sessionHash, selection, false, new(bool), reqLog,
		)
		if slotResult != openAISlotAcquireOK {
			if retryingSameAccount && lastFailoverErr != nil {
				if h.failoverAfterSameAccountSlotFailure(
					c, account, account.GetMappedModel(currentRoutingModel), lastFailoverErr,
					failedAccountIDs, &switchCount, maxAccountSwitches, &oauth429FailoverState,
					false, "count_tokens", reqLog, true,
					func() { writeCountTokensFailoverError(c, lastFailoverErr, lastUpstreamErr) },
				) {
					continue
				}
				return
			}
			h.anthropicErrorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Account concurrency limit exceeded")
			return
		}

		attemptErr := func() error {
			if accountRelease != nil {
				defer accountRelease()
			}
			return h.gatewayService.ForwardCountTokensAsAnthropic(c.Request.Context(), c, account, forwardBody, defaultMappedModel)
		}()
		if attemptErr == nil {
			h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, account.GetMappedModel(currentRoutingModel), true, nil)
			return
		}
		var failoverErr *service.UpstreamFailoverError
		if !errors.As(attemptErr, &failoverErr) || c.Writer.Written() {
			h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, account.GetMappedModel(currentRoutingModel), false, nil)
			return
		}
		h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, account.GetMappedModel(currentRoutingModel), false, nil)
		if !failoverErr.ShouldRetryNextAccount() {
			writeCountTokensFailoverError(c, failoverErr, attemptErr)
			return
		}
		lastUpstreamErr = attemptErr
		lastFailoverErr = failoverErr
		switch retryState.Handle(c.Request.Context(), h.gatewayService, account, account.GetMappedModel(currentRoutingModel), failoverErr, true, sameAccountRetryDelay, "count_tokens") {
		case openAIFailoverRetrySameAccount:
			sameAccountRetrySelection = selection
			continue
		case openAIFailoverRetryCanceled:
			return
		case openAIFailoverRetryStop:
			writeCountTokensFailoverError(c, failoverErr, attemptErr)
			return
		}
		failedAccountIDs[account.ID] = struct{}{}
		if switchCount >= maxAccountSwitches {
			writeCountTokensFailoverError(c, failoverErr, attemptErr)
			return
		}
		switchCount++
		if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
			writeCountTokensFailoverError(c, failoverErr, attemptErr)
			return
		}
		h.gatewayService.RecordOpenAIAccountSwitch()
	}
}

func writeCountTokensFailoverError(c *gin.Context, failoverErr *service.UpstreamFailoverError, _ error) {
	status := http.StatusBadGateway
	if failoverErr != nil && failoverErr.StatusCode >= 500 && failoverErr.StatusCode < 600 {
		status = failoverErr.StatusCode
	}
	message := "Upstream request failed"
	if failoverErr != nil && failoverErr.ClientMessage != "" {
		message = failoverErr.ClientMessage
	}
	c.JSON(status, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
}
