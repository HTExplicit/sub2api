package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Forward forwards request to OpenAI API
func (s *OpenAIGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (_ *OpenAIForwardResult, forwardErr error) {
	rememberPromptRequestedModel(c, body)
	if account != nil && IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) && IsImageGenerationIntent(openAIResponsesEndpoint, gjson.GetBytes(body, "model").String(), body) {
		bound, release, err := bindProcessExtensionContext(ctx, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "image.responses.plan", AccountID: account.ID})
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Image bridge is unavailable"}})
			return nil, err
		}
		defer release()
		ctx = bound
		resolved, err := ResolveCindyResponsesImageToolsForAccount(ctx, account, body)
		if err != nil {
			status, code, message := http.StatusBadRequest, "invalid_request_error", "Invalid image bridge request"
			if errors.Is(err, ErrCindyResponsesImageToolModelNotFound) {
				status, code, message = http.StatusNotFound, "model_not_found", "Image tool model is not supported on the Responses endpoint"
			} else if errors.Is(err, ErrExtensionOperationDisabled) || errors.Is(err, ErrExtensionOperationUnavailable) {
				status, code, message = http.StatusServiceUnavailable, "service_unavailable", "Image bridge is unavailable"
			}
			c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
			return nil, err
		}
		body = resolved
	}
	pricingContext, pricingErr := CaptureCindyPricingContext(ctx, c, account)
	if pricingErr != nil {
		return nil, pricingErr
	}
	ctx = pricingContext
	diagnosticIncomingBody := body
	// Snapshot the client body for the request integrity check before any
	// rewrite; re-staged on every entry so a failover never reuses a stale copy.
	s.stageRequestIntegrityOriginal(c, account, "responses", body)
	beginUpstreamResponseModelObservation(c)
	ClearActualOpenAIUpstreamEndpoint(c)
	// Capture the continuation requirement before any compatibility transform.
	// Its absence after normalization cannot prove this was a stateless request.
	requestedPreviousResponseID := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	var integrityEffortPolicy func([]byte) ([]byte, error)
	if account != nil && account.IsOpenAI() {
		requestedModel := gjson.GetBytes(body, "model").String()
		mappedCandidate, mapErr := resolveOpenAIForwardModelContext(ctx, account, requestedModel, "")
		if mapErr != nil {
			return nil, mapErr
		}
		candidates := []string{mappedCandidate}
		if isOpenAIResponsesCompactPath(c) {
			if compactModel, matched := account.ResolveCompactMappedModel(requestedModel); matched {
				candidates = append([]string{compactModel}, candidates...)
			} else if compactModel := s.resolveOpenAICompactFallbackModel(account, requestedModel); compactModel != "" {
				candidates = append([]string{compactModel}, candidates...)
			}
		}
		withEffort, _, err := materializeOpenAIForwardReasoningEffort(ctx, body, candidates...)
		if err != nil {
			return nil, err
		}
		body = withEffort
		// The request integrity check replays this exact policy (same candidates)
		// on the client snapshot so governed effort values are not differences.
		integrityEffortPolicy = func(raw []byte) ([]byte, error) {
			governed, _, policyErr := materializeOpenAIForwardReasoningEffort(ctx, raw, candidates...)
			return governed, policyErr
		}
	}
	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions")
	}
	filteredBody, filterErr := filterOpenAIResponsesNoneReasoningEffortForAccount(account, body)
	if filterErr != nil {
		return nil, filterErr
	}
	body = filteredBody
	clearGrokResponsesClientToolMapping(c)
	clearOpenAIResponsesClientToolMapping(c)
	clearOpenAIResponsesNamespaceNames(c)
	setCodexToolNameReverse(c, nil)
	if _, err := s.prepareCodexAccountIdentitySource(ctx, c, account); err != nil {
		return nil, err
	}
	startTime := time.Now()
	// 固定渠道映射后的请求级 canonical body；账号 normalize/strip 不得改写跨 failover hint。
	canonicalImageIntentBody := body
	cindyRuntimeAccount := account != nil && IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials)

	restrictionResult := s.detectCodexClientRestriction(c, account, body)
	apiKeyID := getAPIKeyIDFromContext(c)
	// 执行作用域必须取自客户端原始身份：后面的账号 namespace 改写与指纹收敛会改掉
	// 请求体里的 client_metadata / prompt_cache_key，用改写后的值取键会让不同会话
	// 落到同一个键，也会与 WS 接入路径按原始报文算出的键对不上。
	wsExecutionScope, _ := resolveOpenAIWSExecutionScope(c, body, apiKeyID)
	logCodexCLIOnlyDetection(ctx, c, account, apiKeyID, restrictionResult, body)
	if restrictionResult.Enabled && !restrictionResult.Matched {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "forbidden_error",
				"message": CodexClientRestrictionMessage(restrictionResult),
			},
		})
		return nil, errors.New("codex_cli_only restriction: only codex official clients are allowed")
	}

	normalizedBody, normalized, err := normalizeOpenAICodexCompactReasoningEffortForAccount(c, account, body)
	if err != nil {
		return nil, err
	}
	if normalized {
		body = normalizedBody
	}
	legacyIngressBody, legacyIngressChanged, legacyIngressErr := normalizeOpenAIResponsesLegacyIngress(body)
	if legacyIngressErr != nil {
		return nil, legacyIngressErr
	}
	if legacyIngressChanged {
		body = legacyIngressBody
	}
	// 在分流到 passthrough / Codex transform / 原生 ChatCompletions 之前统一修正
	// 显式为 null 的工具 Schema type，否则 upstream 的 400 会被归一成可重试的 502，
	// 同一份坏定义在账号池里反复重放。
	sanitizedToolBody, toolSchemaSanitized, toolSchemaErr := sanitizeOpenAIResponsesToolSchemasForPlatform(body, account.Platform)
	if toolSchemaErr != nil {
		return nil, toolSchemaErr
	} else if toolSchemaSanitized {
		body = sanitizedToolBody
	}
	responsesLite := account.IsOpenAI() && isOpenAIResponsesLiteHeader(c.GetHeader(responsesLiteHeader))
	if responsesLite {
		liteBody, changed, liteErr := normalizeOpenAIResponsesLitePayloadForAccount(body, account)
		if liteErr != nil {
			param := "tools"
			var validationErr *openAIResponsesLiteValidationError
			if errors.As(liteErr, &validationErr) {
				param = validationErr.param
			}
			setOpsUpstreamError(c, http.StatusBadRequest, liteErr.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"type": "invalid_request_error", "message": liteErr.Error(), "param": param,
			}})
			return nil, liteErr
		}
		if changed {
			body = liteBody
		}
	}
	cindyHTTPFallbackBody := body
	wsDecision := s.getOpenAIWSProtocolResolver().Resolve(account)
	cindyHTTPToWSV2 := false
	if isOpenAICindyHTTPToWSV2Bypassed(c) {
		wsDecision = openAIWSHTTPDecision("cindy_handshake_http_fallback")
	} else if bridgeDecision, eligible := s.resolveCindyHTTPToWSV2Decision(c, account); eligible {
		wsDecision = bridgeDecision
		cindyHTTPToWSV2 = true
		markOpenAICindyHTTPToWSV2Required(c)
	} else if isOpenAICindyHTTPToWSV2Required(c) {
		// Once a request has entered the strict Cindy bridge, account failover may
		// only select another bridge-eligible Cindy account. Exclude incompatible
		// candidates without sending or attributing a health failure to them.
		return nil, newOpenAICindyHTTPToWSV2AccountRequiredError()
	} else {
		// 普通账号仍只允许 WS 入站走 WS 上游。Cindy 的 HTTP -> WSv2 是独立、严格受控的例外。
		wsDecision = resolveOpenAIWSDecisionByClientTransport(wsDecision, GetOpenAIClientTransport(c))
	}
	// Cindy HTTP -> WSv2 keeps the legacy session affinity key. The bridge is
	// intentionally single-turn and must share turn-state with the HTTP path;
	// execution-scope derivation is reserved for native WS ingress where
	// multi-agent thread isolation is required.
	if cindyHTTPToWSV2 {
		wsExecutionScope = ""
	}
	if requestedPreviousResponseID != "" && wsDecision.Transport != OpenAIUpstreamTransportResponsesWebsocketV2 &&
		account.UsesOpenAICodexProtocol() {
		// This endpoint-specific restriction is independent of the global WS
		// switch. Native Responses API-key HTTP supports its own stored IDs.
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"type": "invalid_request_error", "message": "this upstream requires Responses WebSocket v2 for previous_response_id",
		}})
		return nil, errors.New("selected upstream requires Responses WebSocket v2 for previous_response_id")
	}
	passthroughEnabled := account.IsOpenAIPassthroughEnabled() && !cindyHTTPToWSV2
	compactPath := isOpenAIResponsesCompactPath(c)
	// Records which service-owned rewrites this attempt applies so the request
	// integrity check can replay them on the client snapshot
	// (openai_request_integrity_semantics.go). Published to the gin context once
	// the Codex CLI predicates are known and again with the resolved upstream
	// model, for forwardOpenAIPassthrough and the WS forwarder.
	integrityOpts := requestIntegrityOptions{
		Compact:       compactPath,
		ResponsesLite: responsesLite,
		Platform:      account.Platform,
		EffortPolicy:  integrityEffortPolicy,
	}
	if shouldFlattenOpenAIResponsesNamespaces(account, wsDecision.Transport, passthroughEnabled, compactPath) {
		integrityOpts.FlattenNamespaces = true
		body, err = flattenOpenAIResponsesNamespaces(c, body)
		if err != nil {
			setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"type": "invalid_request_error", "message": err.Error(), "param": "tools",
			}})
			return nil, err
		}
	}
	if shouldStripOpenAIResponsesInputNamespaces(account, wsDecision.Transport, passthroughEnabled) {
		keepToolCallNamespaces := shouldKeepOpenAIResponsesToolCallNamespaces(
			account, wsDecision.Transport, passthroughEnabled, compactPath, body,
		)
		keepStandaloneOutputNamespaces := shouldKeepOpenAIResponsesStandaloneOutputNamespaces(account, compactPath)
		integrityOpts.StripInputNamespaces = true
		integrityOpts.KeepToolCallNamespaces = keepToolCallNamespaces
		integrityOpts.KeepStandaloneOutputNamespaces = keepStandaloneOutputNamespaces
		body, err = stripOpenAIResponsesInputNamespaces(body, keepToolCallNamespaces, keepStandaloneOutputNamespaces)
		if err != nil {
			setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"type": "invalid_request_error", "message": err.Error(), "param": "input",
			}})
			return nil, err
		}
	}
	nativeCNResponses := account.UsesNativeCNResponses()
	nativeDeepSeekResponses := account.Platform == PlatformDeepseek && nativeCNResponses
	if nativeDeepSeekResponses && account.Type == AccountTypeAPIKey && !compactPath &&
		needsOpenAIResponsesClientToolAdaptation(body) {
		adaptedBody, mapping, adaptErr := adaptOpenAIResponsesClientTools(body)
		if adaptErr != nil {
			return nil, fmt.Errorf("adapt DeepSeek Responses client tools: %w", adaptErr)
		}
		body = adaptedBody
		setOpenAIResponsesClientToolMapping(c, mapping)
	}

	originalBody := body
	rememberOpenCodeInboundBody(c, originalBody)
	requestView := newOpenAIRequestView(body)
	reqModel, reqStream := requestView.Model, requestView.Stream
	// Preserve the client session seed before OAuth compatibility transforms.
	// The messages bridge deliberately removes prompt_cache_key from the body,
	// but it still needs the original value to isolate the upstream Session_Id.
	promptCacheKey := strings.TrimSpace(requestView.PromptCacheKey)
	clientPromptCacheKey := promptCacheKey
	originalModel := reqModel
	if cindyRuntimeAccount {
		if !CindyFreePoolModelSupportsEndpoint(originalModel, CindyEndpointResponses) {
			err := fmt.Errorf("cindy model %q is not available to the free-key pool", originalModel)
			setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"type": "invalid_request_error", "message": err.Error(), "param": "model",
			}})
			return nil, err
		}
	}

	if account.Platform == PlatformGrok {
		return s.forwardGrokResponses(ctx, c, account, body, originalModel, reqStream, startTime)
	}

	if account.IsOpenCodeGo() {
		mapped := resolveOpenCodeGoMappedModel(account, body, "")
		switch openCodeGoNativeProtocol(account, mapped) {
		case APIProtocolAnthropic:
			return s.forwardResponsesViaNativeAnthropic(ctx, c, account, body, "")
		case APIProtocolResponses:
			break
		default:
			return s.forwardResponsesViaRawChatCompletions(ctx, c, account, body, compactPath)
		}
	}

	// CN 供应商 anthropic 协议账号：/v1/responses 入站是交叉协议组合
	// （Responses 客户端 × Anthropic 上游），转成 Anthropic 请求走原生端点。
	// 不能落到下面的 raw-CC 分支——其 URL 构造会把 anthropic base 当 CC base 用。
	if account.IsAnthropicProtocol() {
		return s.forwardResponsesViaNativeAnthropic(ctx, c, account, body, reqModel)
	}
	if account.IsOpenAIApiKey() && !cindyRuntimeAccount {
		if normalized, changed, normalizeErr := normalizeOpenAIParallelToolCallsWithoutTools(body, responsesLite); normalizeErr != nil {
			return nil, normalizeErr
		} else if changed {
			body = normalized
			originalBody = normalized
		}
		requestView = newOpenAIRequestView(body)
		reqModel, reqStream, promptCacheKey = requestView.Model, requestView.Stream, requestView.PromptCacheKey
		originalModel = reqModel
	}

	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		return s.forwardResponsesViaRawChatCompletions(ctx, c, account, body, compactPath)
	}
	if usesOfficialOpenAIResponsesInputContract(account) {
		SetActualOpenAIUpstreamEndpoint(c, openAIResponsesUpstreamEndpoint)
		normalizedReasoningBody, reasoningChanged, reasoningErr := normalizeOpenAIResponsesReasoningContentReplay(body)
		if reasoningErr != nil {
			return nil, fmt.Errorf("normalize OpenAI Responses reasoning content replay: %w", reasoningErr)
		}
		if reasoningChanged {
			body = normalizedReasoningBody
			originalBody = normalizedReasoningBody
			requestView = newOpenAIRequestView(normalizedReasoningBody)
			reqModel, reqStream, promptCacheKey = requestView.Model, requestView.Stream, requestView.PromptCacheKey
			originalModel = reqModel
		}
		sanitizedBody, changed, sanitizeErr := sanitizeOpenAIResponsesInputItemIDs(body)
		if sanitizeErr != nil {
			return nil, fmt.Errorf("sanitize OpenAI Responses input item IDs: %w", sanitizeErr)
		}
		if changed {
			body = sanitizedBody
			originalBody = sanitizedBody
			requestView = newOpenAIRequestView(sanitizedBody)
			reqModel, reqStream = requestView.Model, requestView.Stream
			originalModel = reqModel
		}
	}

	compatMessagesBridge := isOpenAICompatMessagesBridgeBody(body)
	setOpenAICompatMessagesBridgeContext(c, compatMessagesBridge)

	isCodexCLI := openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator")) || (s.cfg != nil && s.cfg.Gateway.ForceCodexCLI)
	codexImageGenerationExplicitToolPolicy := codexImageGenerationExplicitToolPolicyAllow
	if isCodexCLI {
		codexImageGenerationExplicitToolPolicy = account.CodexImageGenerationExplicitToolPolicy()
	}
	integrityOpts.ImageToolPolicyStrip = isCodexCLI && codexImageGenerationExplicitToolPolicy == codexImageGenerationExplicitToolPolicyStrip
	stageRequestIntegrityForwardOptions(c, integrityOpts)
	if c != nil {
		c.Set("openai_ws_transport_decision", string(wsDecision.Transport))
		c.Set("openai_ws_transport_reason", wsDecision.Reason)
	}
	if wsDecision.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		logOpenAIWSModeDebug(
			"selected account_id=%d account_type=%s transport=%s reason=%s model=%s stream=%v",
			account.ID,
			account.Type,
			normalizeOpenAIWSLogValue(string(wsDecision.Transport)),
			normalizeOpenAIWSLogValue(wsDecision.Reason),
			reqModel,
			reqStream,
		)
	}
	// 当前仅支持 WSv2；WSv1 命中时直接返回错误，避免出现“配置可开但行为不确定”。
	if wsDecision.Transport == OpenAIUpstreamTransportResponsesWebsocket {
		if c != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"message": "OpenAI WSv1 is temporarily unsupported. Please enable responses_websockets_v2.",
				},
			})
		}
		return nil, errors.New("openai ws v1 is temporarily unsupported; use ws v2")
	}
	if passthroughEnabled {
		attemptImageIntentInvalidated := false
		if isCodexCLI && codexImageGenerationExplicitToolPolicy == codexImageGenerationExplicitToolPolicyStrip {
			strippedBody, changed, stripErr := stripOpenAIImageGenerationToolsFromRawPayload(body)
			if stripErr != nil {
				return nil, stripErr
			}
			if changed {
				body = strippedBody
				originalBody = strippedBody
				attemptImageIntentInvalidated = true
				logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Stripped /responses image_generation tool for Codex client by account policy")
			}
		}
		// Effort is recorded from the final passthrough wire request below.
		return s.forwardOpenAIPassthrough(
			ctx,
			c,
			account,
			originalBody,
			canonicalImageIntentBody,
			reqModel,
			attemptImageIntentInvalidated,
			reqStream,
			startTime,
		)
	}

	bodyModified := false
	var reqBody map[string]any
	ensureReqBody := func() (map[string]any, error) {
		if requestView.HasPatches() {
			patchedBody, patchErr := requestView.ApplyPatches()
			if patchErr != nil {
				return nil, patchErr
			}
			body = patchedBody
			requestView = newOpenAIRequestView(body)
			reqBody = nil
			bodyModified = false
		}
		if reqBody != nil {
			return reqBody, nil
		}
		decoded, decodeErr := requestView.Decode(c)
		if decodeErr != nil {
			return nil, decodeErr
		}
		reqBody = decoded
		return reqBody, nil
	}
	markPatchSet := func(path string, value any) {
		bodyModified = true
		if requestView.patchesDisabled {
			if reqBody != nil {
				setOpenAIRequestMapPath(reqBody, path, value)
			}
			return
		}
		requestView.MarkPatchSet(path, value)
	}
	markPatchDelete := func(path string) {
		bodyModified = true
		if requestView.patchesDisabled {
			if reqBody != nil {
				deleteOpenAIRequestMapPath(reqBody, path)
			}
			return
		}
		requestView.MarkPatchDelete(path)
	}
	disablePatch := func() {
		requestView.DisablePatches()
	}
	markDecodedModified := func() {
		bodyModified = true
		disablePatch()
	}

	apiKey := getAPIKeyFromContext(c)
	imageGenerationAllowed := GroupAllowsImageGeneration(nil)
	if apiKey != nil {
		imageGenerationAllowed = GroupAllowsImageGeneration(apiKey.Group)
	}
	cindyResponsesImageBridge := IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) &&
		CindyModelSupportsResponsesImageBridge(originalModel)
	codexImageGenerationBridgeEnabled := isCodexCLI &&
		!isOpenAIResponsesLiteHeader(c.GetHeader(responsesLiteHeader)) &&
		imageGenerationAllowed &&
		codexImageGenerationExplicitToolPolicy != codexImageGenerationExplicitToolPolicyStrip &&
		s.isCodexImageGenerationBridgeEnabled(ctx, account, apiKey)
	integrityOpts.ImageBridgeEnabled = codexImageGenerationBridgeEnabled
	var imageIntent bool
	canonicalImageIntent := resolveOpenAIImageIntentHint(c, reqModel, canonicalImageIntentBody, IsImageGenerationIntent)
	if isCodexCLI && codexImageGenerationExplicitToolPolicy == codexImageGenerationExplicitToolPolicyStrip {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if stripOpenAIImageGenerationTools(decoded) {
			markDecodedModified()
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Stripped /responses image_generation tool for Codex client by account policy")
		}
		imageIntent = IsImageGenerationIntentMap(openAIResponsesEndpoint, reqModel, decoded)
	} else {
		imageIntent = canonicalImageIntent
	}
	if imageIntent && !imageGenerationAllowed {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": ImageGenerationPermissionMessage()}})
		return nil, errors.New("image generation disabled for group")
	}

	isCompactRequest := compactPath
	requestedModel := reqModel
	billingModel, upstreamModel, modelPolicyErr := resolveOpenAIForwardMappedModelsContext(ctx, account, requestedModel, isCompactRequest)
	if modelPolicyErr != nil {
		return nil, modelPolicyErr
	}
	if cindyRuntimeAccount {
		snapshot, err := LoadCindyCatalogSnapshot(ctx, account)
		if err != nil {
			return nil, err
		}
		if mappedModel, mapped := snapshot.CompatibilityMappings[requestedModel]; mapped {
			upstreamModel = mappedModel
		} else if mappedModel, mapped := snapshot.AvailableMappings[requestedModel]; mapped {
			upstreamModel = mappedModel
		}
	}
	if isCompactRequest {
		if compactModel := s.resolveOpenAICompactFallbackModel(account, requestedModel); compactModel != "" {
			upstreamModel = compactModel
		}
	}
	if account.IsOpenAIApiKey() {
		upstreamModel = normalizeOpenAIModelForUpstream(account, upstreamModel)
	}
	instructions := gjson.GetBytes(body, "instructions")
	instructionsEmpty := !instructions.Exists() || instructions.Type != gjson.String || strings.TrimSpace(instructions.String()) == ""
	if instructionsEmpty && account.UsesOpenAICodexProtocol() && !compatMessagesBridge && !nativeCNResponses {
		markPatchSet("instructions", defaultCodexSynthInstructions(upstreamModel))
	}
	if billingModel != requestedModel {
		logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Model mapping applied: %s -> %s (account: %s, isCodexCLI: %v)", requestedModel, billingModel, account.Name, isCodexCLI)
	}
	reqModel = billingModel
	if upstreamModel != requestedModel {
		markPatchSet("model", upstreamModel)
	}
	if upstreamModel != billingModel {
		if isCompactRequest {
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Compact model mapping applied: %s -> %s (account: %s, isCodexCLI: %v)", requestedModel, upstreamModel, account.Name, isCodexCLI)
		} else {
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Upstream model resolved: %s -> %s (account: %s, type: %s, isCodexCLI: %v)", billingModel, upstreamModel, account.Name, account.Type, isCodexCLI)
		}
	}

	imageIntent = imageIntent || cindyResponsesImageBridge || IsImageGenerationIntent(openAIResponsesEndpoint, reqModel, nil) || isOpenAIImageGenerationModel(upstreamModel)
	if imageIntent && !imageGenerationAllowed {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": ImageGenerationPermissionMessage()}})
		return nil, errors.New("image generation disabled for group")
	}

	// /responses/compact 是会话压缩请求：上游不接受 tool_choice（400 unknown_parameter），
	// 注入 image_generation 工具也没有意义，整块豁免。
	if imageGenerationAllowed && !isCompactRequest && (codexImageGenerationBridgeEnabled || cindyResponsesImageBridge || isOpenAIImageGenerationModel(requestView.Model) || openAIRequestBodyImageGenerationToolNeedsNormalization(body) || isOpenAIImageGenerationModel(upstreamModel)) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if codexImageGenerationBridgeEnabled && ensureOpenAIResponsesImageGenerationTool(decoded) {
			markDecodedModified()
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Injected /responses image_generation tool for Codex client")
		}
		if codexImageGenerationBridgeEnabled && ensureOpenAIResponsesImageGenerationToolChoiceAuto(decoded) {
			markDecodedModified()
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Set /responses image_generation tool_choice=auto for Codex client")
		}
		if normalizeOpenAIResponsesImageGenerationTools(decoded) {
			markDecodedModified()
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Normalized /responses image_generation tool payload")
		}
		imageOnlyModel := requestView.Model
		if cindyResponsesImageBridge {
			capability, _ := ResolveCindyCapability(originalModel)
			imageOnlyModel = capability.PublicID
		}
		if normalizeOpenAIResponsesImageOnlyModelWithModel(decoded, imageOnlyModel) {
			markDecodedModified()
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Normalized /responses image-only model request inbound_model=%s image_model=%s upstream_model=%s", requestView.Model, billingModel, upstreamModel)
		}
		mapped, mapErr := mapCindyOpenAIResponsesImageModels(ctx, decoded, account)
		if mapErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Image bridge is unavailable"}})
			return nil, mapErr
		}
		if mapped {
			markDecodedModified()
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Applied Cindy /responses image model mapping")
		}
		if model, ok := decoded["model"].(string); ok {
			upstreamModel = strings.TrimSpace(model)
		}
		if err := validateOpenAIResponsesImageModel(decoded, upstreamModel); err != nil {
			setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": err.Error(), "param": "model"}})
			return nil, err
		}
		if hasOpenAIImageGenerationTool(decoded) {
			imageIntent = true
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] /responses image_generation request inbound_model=%s mapped_model=%s account_type=%s", requestView.Model, upstreamModel, account.Type)
		}
		if codexImageGenerationBridgeEnabled && applyCodexImageGenerationBridgeInstructions(decoded) {
			markDecodedModified()
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Added Codex image_generation bridge instructions")
		}
	} else if imageGenerationAllowed && imageIntent && openAIRequestBodyHasImageGenerationDeclaration(body) {
		// 完整 image_generation tool 只做 raw 计费读取，校验/桥接/旧字段迁移命中时才展开大 input map。
		logger.LegacyPrintf("service.openai_gateway", "[OpenAI] /responses image_generation request inbound_model=%s mapped_model=%s account_type=%s", requestView.Model, upstreamModel, account.Type)
	}

	if isCodexSparkModel(upstreamModel) && openAIRequestBodyMayContainImageInput(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err := validateCodexSparkInput(decoded, upstreamModel); err != nil {
			setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": err.Error(), "param": "input"}})
			return nil, err
		}
	}

	// gpt-5.3-codex-spark also rejects the image_generation tool (HTTP 400,
	// param=tools). Strip it here so both APIKey and OAuth /responses paths are
	// covered regardless of the image-generation feature gate.
	if isCodexSparkModel(upstreamModel) && openAIRequestBodyHasImageGenerationDeclaration(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if stripCodexSparkImageGenerationTools(decoded) {
			markDecodedModified()
		}
	}

	if account.UsesOpenAICodexProtocol() {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		// Responses OAuth 与 Chat 兼容入口保持一致：纯文本 system 可以无损提升后删除，
		// JSON object 模式仍需在 input 中保留 JSON 指令供上游兼容校验。
		omitPromotedSystemMessages := !strings.EqualFold(
			strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String()),
			"json_object",
		)
		codexResult := codexTransformResult{}
		if compatMessagesBridge {
			codexResult = applyCodexOAuthTransformWithOptions(decoded, codexOAuthTransformOptions{
				IsCodexCLI:                          isCodexCLI,
				IsCompact:                           isCompactRequest,
				SkipDefaultInstructions:             true,
				PreserveToolCallIDs:                 true,
				OmitPromotedSystemMessagesFromInput: omitPromotedSystemMessages,
			})
			ensureCodexOAuthInstructionsField(decoded)
			markDecodedModified()
		} else {
			codexResult = applyCodexOAuthTransformWithOptions(decoded, codexOAuthTransformOptions{
				IsCodexCLI:                          isCodexCLI,
				IsCompact:                           isCompactRequest,
				OmitPromotedSystemMessagesFromInput: omitPromotedSystemMessages,
			})
		}
		if codexResult.Modified {
			markDecodedModified()
		}
		// 带真实 device_id 时补齐 client_metadata 安装标识，与真实 Codex 对齐（compact 形态不同，跳过）。
		if !isCompactRequest && applyCodexClientMetadata(decoded, account) {
			markDecodedModified()
		}
		if !isCompactRequest && applyCodexAccountIdentityClientMetadataMap(decoded, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c)) {
			markDecodedModified()
		}
		stageCodexFingerprintIDs(c, nil)
		// 指纹收敛：一次性解析收敛 ID，请求体和出站头共享同一份 IDs（保证 turn_id 等随机字段一致）。
		// fingerprintIDs 在此处解析，后续 buildUpstreamRequest 中使用同一份。
		if !isCompactRequest {
			var clientHeaders http.Header
			if c != nil && c.Request != nil {
				clientHeaders = c.Request.Header
			}
			fpIDs := s.resolveStagedCodexFingerprintIDs(c, account, clientHeaders)
			if fpIDs != nil {
				if applyCodexFingerprintClientMetadata(decoded, fpIDs) {
					markDecodedModified()
				}
			}
			// 将 fpIDs 存入 gin context，供 buildUpstreamRequest 中头改写使用。
			// 无条件覆写（含 nil）：failover 从收敛账号切到 off 账号时，上一
			// 账号的 IDs 不得残留（stageCodexFingerprintIDs 注释）。
			stageCodexFingerprintIDs(c, fpIDs)
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if currentPromptCacheKey, ok := decoded["prompt_cache_key"].(string); ok && currentPromptCacheKey != "" {
			promptCacheKey = currentPromptCacheKey
		} else if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		}
	}

	if !SupportsVerbosity(upstreamModel) && gjson.GetBytes(body, "text.verbosity").Exists() {
		markPatchDelete("text.verbosity")
	}

	if !isCodexCLI {
		maxOutputTokens := gjson.GetBytes(body, "max_output_tokens")
		if maxOutputTokens.Exists() {
			switch account.Platform {
			case PlatformOpenAI, PlatformDeepseek:
				// Preserve Responses-native output limits unless the selected upstream
				// explicitly rejects the field in the bounded HTTP retry loop below.
			case PlatformAnthropic:
				decoded, decodeErr := ensureReqBody()
				if decodeErr != nil {
					return nil, decodeErr
				}
				delete(decoded, "max_output_tokens")
				if _, hasMaxTokens := decoded["max_tokens"]; !hasMaxTokens {
					decoded["max_tokens"] = maxOutputTokens.Value()
				}
				markDecodedModified()
			case PlatformGemini:
				markPatchDelete("max_output_tokens")
			default:
				markPatchDelete("max_output_tokens")
			}
		}
		// /v1/responses 的规范输出上限字段是 max_output_tokens；部分客户端仍按
		// Chat Completions 习惯发送 max_tokens，兼容 Responses 上游会拒绝该字段（#4417）。
		// 仅对 OpenAI 平台归一化：Anthropic 合法使用 max_tokens，其 max_output_tokens
		// 反向转换已在上方 switch 中处理。
		if account.Platform == PlatformOpenAI {
			if maxTokens := gjson.GetBytes(body, "max_tokens"); maxTokens.Exists() {
				if !gjson.GetBytes(body, "max_output_tokens").Exists() {
					markPatchSet("max_output_tokens", maxTokens.Value())
				}
				markPatchDelete("max_tokens")
			}
		}
		if gjson.GetBytes(body, "max_completion_tokens").Exists() && (account.Type == AccountTypeAPIKey || account.Platform != PlatformOpenAI) {
			markPatchDelete("max_completion_tokens")
		}
		for _, unsupportedField := range []string{"prompt_cache_retention", "safety_identifier", "prompt_cache_options"} {
			if gjson.GetBytes(body, unsupportedField).Exists() {
				markPatchDelete(unsupportedField)
			}
		}
	}
	// Ollama Cloud（实际 Responses 上游为 ollama.com）输出上限 clamp：对 Codex 与
	// 非 Codex 客户端一律执行（真实 Codex 客户端同样会带超限 max_output_tokens 被
	// ollama.com 以 400 拒绝）。在 `!isCodexCLI` 归一化块之后独立调用：非 Codex 时
	// 位于平台字段归一化之后，不跳过原有平台 switch（patch 按追加顺序应用，set 在
	// 先前的 delete/set 之后生效）；Codex 请求不做归一化，直接按 body 现值判定。
	if clampedCap, ok := ollamaCloudResponsesMaxOutputTokensClamp(account, upstreamModel, body); ok {
		markPatchSet("max_output_tokens", clampedCap)
	}
	if openAIRequestBodyMayContainEmptyBase64InputImage(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if sanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(decoded) {
			markDecodedModified()
		}
	}

	rawTier := requestView.ServiceTier
	if openAIGroupForcesFast(ctx, account) {
		rawTier = OpenAIFastTierPriority
		if requestView.ServiceTier != OpenAIFastTierPriority {
			markPatchSet("service_tier", OpenAIFastTierPriority)
		}
	}
	if rawTier != "" {
		if normTier := normalizedOpenAIServiceTierValue(rawTier); normTier != "" {
			action, errMsg := s.evaluateOpenAIFastPolicy(ctx, account, upstreamModel, normTier)
			switch action {
			case BetaPolicyActionBlock:
				msg := errMsg
				if msg == "" {
					msg = fmt.Sprintf("openai service_tier=%s is not allowed for model %s", normTier, upstreamModel)
				}
				blocked := &OpenAIFastBlockedError{Message: msg}
				writeOpenAIFastPolicyBlockedResponse(c, blocked)
				return nil, blocked
			case BetaPolicyActionFilter:
				markPatchDelete("service_tier")
			case OpenAIFastPolicyActionForcePriority:
				if rawTier != OpenAIFastTierPriority {
					markPatchSet("service_tier", OpenAIFastTierPriority)
				}
			default:
				if normTier != rawTier {
					markPatchSet("service_tier", normTier)
				}
			}
		}
	} else if s.shouldForceOpenAIFastPriorityForMissingTier(ctx, account, upstreamModel) {
		markPatchSet("service_tier", OpenAIFastTierPriority)
	}
	if account.UsesOpenAICodexProtocol() {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if input, ok := decoded["input"].([]any); ok && sanitizeOpenAIResponsesOrphanToolOutputs(
			decoded,
			input,
			strings.TrimSpace(firstNonEmptyString(decoded["previous_response_id"])) != "",
		) {
			markDecodedModified()
		}
	}
	if reqBody != nil || openAIResponsesInputMayNeedTruncation(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if truncateOpenAIResponsesInputText(decoded) {
			markDecodedModified()
		}
	}

	if bodyModified {
		if requestView.HasPatches() {
			if patchedBody, patchErr := requestView.ApplyPatches(); patchErr == nil {
				body = patchedBody
				requestView = newOpenAIRequestView(body)
				reqBody = nil
				bodyModified = false
			}
		}
		if bodyModified {
			decoded, decodeErr := ensureReqBody()
			if decodeErr != nil {
				return nil, decodeErr
			}
			var marshalErr error
			body, marshalErr = marshalOpenAIUpstreamJSON(decoded)
			if marshalErr != nil {
				return nil, fmt.Errorf("serialize request body: %w", marshalErr)
			}
			requestView = newOpenAIRequestView(body)
		}
	}
	if account.IsOpenAIOAuthLike() {
		// Apply the official mode policy to the final account/compact target,
		// after serializing pending request edits. Public aliases do not identify
		// whether the actual upstream is Astra.
		reasoningBody, reasoningChanged, reasoningErr := normalizeOpenAIResponsesReasoningModeForModel(body, upstreamModel)
		if reasoningErr != nil {
			return nil, fmt.Errorf("normalize OpenAI Responses reasoning.mode: %w", reasoningErr)
		}
		if reasoningChanged {
			body = reasoningBody
			requestView = newOpenAIRequestView(body)
			reqBody = nil
		}
	}
	if normalizedBody, changed, normalizeErr := NormalizeCompactionTriggerInputOrder(body); normalizeErr != nil {
		return nil, fmt.Errorf("normalize compaction trigger order: %w", normalizeErr)
	} else if changed {
		body = normalizedBody
		requestView = newOpenAIRequestView(body)
		reqBody = nil
	}
	// A prior rejection is not evidence that an encrypted state item is
	// dispensable. Preserve the incoming history and let its source validate it.

	// Business System Prompt is deliberately the final service-owned prompt
	// layer. All legacy Codex/image/compat transforms above run first, so the
	// feature can be disabled without changing their request bytes.
	updatedBody, promptApplication, promptErr := s.applyBusinessSystemPromptForRequest(
		c, body, account, BusinessSystemPromptProtocolResponses, compactPath,
	)
	if promptErr != nil {
		if errors.Is(promptErr, ErrBusinessSystemPromptUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
				"type": "system_prompt_unavailable", "code": "system_prompt_unavailable",
				"message": "business system prompt is temporarily unavailable",
			}})
		}
		return nil, promptErr
	} else {
		body = updatedBody
		if promptApplication.Applied {
			body, promptErr = rewriteBusinessSystemPromptCacheKey(c, body, promptApplication)
			if promptErr != nil {
				return nil, promptErr
			}
			requestView = newOpenAIRequestView(body)
			reqBody = nil
		}
	}
	finalCacheBody, finalCacheChanged, finalCacheErr := normalizeCindyManagedPromptCacheKey(body, c, account)
	if finalCacheErr != nil {
		return nil, fmt.Errorf("normalize final Cindy prompt_cache_key: %w", finalCacheErr)
	}
	if finalCacheChanged {
		body = finalCacheBody
		observeCindyManagedPromptCacheNormalization(c, true)
		requestView = newOpenAIRequestView(body)
		reqBody = nil
	}
	// Prefer the final service-owned cache key when it remains on the wire (for
	// example after business-prompt rewriting), while retaining the pre-transform
	// seed for compatibility bridges that intentionally strip the body field.
	if effectivePromptCacheKey := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); effectivePromptCacheKey != "" {
		promptCacheKey = effectivePromptCacheKey
	}
	logBusinessSystemPromptObservation(ctx, c, promptApplication, wsDecision.Transport, wsDecision.Reason)
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfg OpenAIResponsesImageBillingConfig
		var imageCfgErr error
		if reqBody != nil {
			imageCfg, imageCfgErr = resolveOpenAIResponsesImageBillingConfigDetailed(reqBody, billingModel)
		} else {
			imageCfg, imageCfgErr = resolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, billingModel)
		}
		if imageCfgErr != nil {
			setOpsUpstreamError(c, http.StatusBadRequest, imageCfgErr.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": imageCfgErr.Error(), "param": "size"}})
			return nil, imageCfgErr
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	// Get access token
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)
	integrityOpts.UpstreamModel = upstreamModel
	stageRequestIntegrityForwardOptions(c, integrityOpts)

	// 命中 WS 时仅走 WebSocket Mode；不再自动回退 HTTP。
	if wsDecision.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		// WS 分支需要结构化 payload 与重连恢复，命中后再触发 full-map decode。
		wsReqBody, err := ensureReqBody()
		if err != nil {
			return nil, err
		}
		hasPreviousResponseID := strings.TrimSpace(openAIWSPayloadString(wsReqBody, "previous_response_id")) != ""
		strictCindyContinuation := cindyRuntimeAccount && hasPreviousResponseID
		logOpenAIWSModeDebug(
			"forward_start account_id=%d account_type=%s model=%s stream=%v has_previous_response_id=%v",
			account.ID,
			account.Type,
			upstreamModel,
			reqStream,
			hasPreviousResponseID,
		)
		maxAttempts := openAIWSReconnectRetryLimit + 1
		if cindyHTTPToWSV2 {
			maxAttempts = 1
		}
		wsAttempts := 0
		var wsResult *OpenAIForwardResult
		var wsErr error
		wsLastFailureReason := ""
		agentTaskRecoveryTried := false
		// This single-request forwarder has no trusted full-history accumulator.
		// Missing anchors and rejected encrypted state therefore terminate;
		// proven full-input reconstruction belongs to the WS ingress lifecycle.
		retryBudget := s.openAIWSRetryTotalBudget()
		retryStartedAt := time.Now()
	wsRetryLoop:
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			wsAttempts = attempt
			wsResult, wsErr = s.forwardOpenAIWSV2WithScope(
				ctx,
				c,
				account,
				wsReqBody,
				clientPromptCacheKey,
				wsExecutionScope,
				token,
				wsDecision,
				isCodexCLI,
				reqStream,
				originalModel,
				upstreamModel,
				startTime,
				attempt,
				wsLastFailureReason,
				&agentTaskRecoveryTried,
			)
			if wsErr == nil {
				break
			}
			if c != nil && c.Writer != nil && c.Writer.Written() {
				break
			}
			// Structured Cindy/Laxa capability failures must cross the WS retry
			// boundary intact. They are account/model scoped (and already cooled
			// by the WS forwarder), so converting them into the generic strict
			// continuation error would lose the required 400/model_not_supported
			// response at the handler.
			var modelNotSupportedErr *UpstreamFailoverError
			if errors.As(wsErr, &modelNotSupportedErr) && modelNotSupportedErr.IsOpenAIModelNotSupported() {
				return nil, modelNotSupportedErr
			}
			if cindyHTTPToWSV2 && !hasPreviousResponseID && isOpenAICindyHTTPToWSV2HandshakeForbidden(wsErr) {
				if fallbackBody, safe := prepareOpenAICindyStatelessHTTPFallback(cindyHTTPFallbackBody); safe {
					previousBypass, hadPreviousBypass := c.Get(openAICindyHTTPToWSV2BypassContextKey)
					c.Set(openAICindyHTTPToWSV2BypassContextKey, true)
					result, fallbackErr := s.Forward(ctx, c, account, fallbackBody)
					if hadPreviousBypass {
						c.Set(openAICindyHTTPToWSV2BypassContextKey, previousBypass)
					} else {
						c.Set(openAICindyHTTPToWSV2BypassContextKey, false)
					}
					logOpenAIWSModeInfo(
						"cindy_http_bridge_fallback account_id=%d attempt=%d reason=handshake_forbidden success=%v",
						account.ID,
						attempt,
						fallbackErr == nil,
					)
					return result, fallbackErr
				}
			}
			var taskRecoveredErr *agentIdentityTaskRecoveredError
			if errors.As(wsErr, &taskRecoveredErr) {
				continue
			}

			reason, retryable := classifyOpenAIWSReconnectReason(wsErr)
			if reason != "" {
				wsLastFailureReason = reason
			}
			if stateReason := strings.TrimPrefix(reason, "prewarm_"); stateReason == "previous_response_not_found" || stateReason == "invalid_encrypted_content" {
				break
			}
			if cindyHTTPToWSV2 && !hasPreviousResponseID {
				if failoverErr, ok := s.cindyHTTPToWSV2FirstTurnFailover(
					ctx, c, account, upstreamModel, wsErr,
				); ok {
					return nil, failoverErr
				}
			}
			if retryable && attempt < maxAttempts {
				backoff := s.openAIWSRetryBackoff(attempt)
				if retryBudget > 0 && time.Since(retryStartedAt)+backoff > retryBudget {
					s.recordOpenAIWSRetryExhausted()
					logOpenAIWSModeInfo(
						"reconnect_budget_exhausted account_id=%d attempts=%d max_retries=%d reason=%s elapsed_ms=%d budget_ms=%d",
						account.ID,
						attempt,
						openAIWSReconnectRetryLimit,
						normalizeOpenAIWSLogValue(reason),
						time.Since(retryStartedAt).Milliseconds(),
						retryBudget.Milliseconds(),
					)
					break
				}
				s.recordOpenAIWSRetryAttempt(backoff)
				logOpenAIWSModeInfo(
					"reconnect_retry account_id=%d retry=%d max_retries=%d reason=%s backoff_ms=%d",
					account.ID,
					attempt,
					openAIWSReconnectRetryLimit,
					normalizeOpenAIWSLogValue(reason),
					backoff.Milliseconds(),
				)
				if backoff > 0 {
					timer := time.NewTimer(backoff)
					select {
					case <-ctx.Done():
						if !timer.Stop() {
							<-timer.C
						}
						wsErr = wrapOpenAIWSFallback("retry_backoff_canceled", ctx.Err())
						break wsRetryLoop
					case <-timer.C:
					}
				}
				continue
			}
			if retryable {
				s.recordOpenAIWSRetryExhausted()
				logOpenAIWSModeInfo(
					"reconnect_exhausted account_id=%d attempts=%d max_retries=%d reason=%s",
					account.ID,
					attempt,
					openAIWSReconnectRetryLimit,
					normalizeOpenAIWSLogValue(reason),
				)
			} else if reason != "" {
				s.recordOpenAIWSNonRetryableFastFallback()
				logOpenAIWSModeInfo(
					"reconnect_stop account_id=%d attempt=%d reason=%s",
					account.ID,
					attempt,
					normalizeOpenAIWSLogValue(reason),
				)
			}
			break
		}
		if wsErr == nil {
			firstTokenMs := int64(0)
			hasFirstTokenMs := wsResult != nil && wsResult.FirstTokenMs != nil
			if hasFirstTokenMs {
				firstTokenMs = int64(*wsResult.FirstTokenMs)
			}
			requestID := ""
			if wsResult != nil {
				requestID = strings.TrimSpace(wsResult.RequestID)
			}
			logOpenAIWSModeDebug(
				"forward_succeeded account_id=%d request_id=%s stream=%v has_first_token_ms=%v first_token_ms=%d ws_attempts=%d",
				account.ID,
				requestID,
				reqStream,
				hasFirstTokenMs,
				firstTokenMs,
				wsAttempts,
			)
			wsResult.UpstreamModel = upstreamModel
			if wsResult.BillingModel == "" {
				wsResult.BillingModel = billingModel
			}
			if wsResult.ImageCount > 0 {
				wsResult.ImageSize = imageSizeTier
				wsResult.ImageInputSize = imageInputSize
				wsResult.BillingModel = imageBillingModel
			}
			if wsResult != nil && wsResult.wsReplayInputExists {
				s.bindCindyOpaqueContinuationAccount(
					ctx, c, account, cindyOpaqueBindingIDsFromRawItems(wsResult.wsReplayInput),
				)
			}
			if cindyHTTPToWSV2 {
				// This remains an HTTP response even though its upstream used WS.
				// Native WS keeps its existing ownership/session behavior.
				s.bindHTTPResponseAccount(ctx, c, account, wsResult.ResponseID)
			}
			return wsResult, nil
		}
		if strictCindyContinuation {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				UpstreamStatusCode: http.StatusServiceUnavailable,
				Kind:               "continuation_state",
				Message:            OpenAIContinuationStateUnavailableClientMessage,
			})
			return nil, NewOpenAIContinuationStateUnavailableError(http.StatusServiceUnavailable, nil, nil)
		}
		continuationReason, _ := classifyOpenAIWSReconnectReason(wsErr)
		switch strings.TrimPrefix(continuationReason, "prewarm_") {
		case "invalid_encrypted_content", "previous_response_not_found":
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: http.StatusBadRequest,
				Kind:               "continuation_state",
				Message:            OpenAIContinuationStateUnavailableClientMessage,
			})
			return nil, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
		}
		s.writeOpenAIWSFallbackErrorResponse(c, account, wsErr)
		return nil, wsErr
	}

	reasoningRecovery := s.newOpenAIReasoningRecoveryState(ctx, c, account, token)
	defer reasoningRecovery.Close()
	defer func() { forwardErr = reasoningRecovery.StopError(forwardErr) }()
	compactModelFallbackRetried := false
	agentTaskRecoveryTried := false
	rejectedFieldRetryState := newOpenAIResponsesRejectedFieldRetryState(body)
	for {
		// Read the final attempt payload. A compatibility retry may have changed
		// the request, so neither the original alias nor prior attempt is usage
		// evidence for what this upstream actually receives.
		reasoningEffort := extractOpenAIReasoningEffortFromBody(body, upstreamModel)
		reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, upstreamModel)
		reasoningEffortValue := ""
		if reasoningEffort != nil {
			reasoningEffortValue = *reasoningEffort
		}
		firstOutputTimeout := time.Duration(0)
		if reqStream && account.Platform == PlatformOpenAI {
			firstOutputTimeout = s.openAIFirstOutputTimeout(reasoningEffortValue)
		}
		// Build upstream request
		upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
		var headerGuard *openAIFirstOutputHeaderGuard
		if firstOutputTimeout > 0 {
			upstreamCtx, headerGuard = newOpenAIFirstOutputHeaderGuard(
				upstreamCtx, releaseUpstreamCtx, startTime.Add(firstOutputTimeout),
			)
		}
		upstreamReq, err := s.buildUpstreamRequest(upstreamCtx, c, account, body, token, reqStream, promptCacheKey, isCodexCLI)
		if headerGuard == nil {
			releaseUpstreamCtx()
		}
		if err != nil {
			if headerGuard != nil {
				headerGuard.close()
			}
			return nil, err
		}

		// Get proxy URL
		proxyURL := ""
		if account.ProxyID != nil && account.Proxy != nil {
			proxyURL = account.Proxy.URL()
		}
		upstreamReq, body, err = reasoningRecovery.PrepareRequest(upstreamReq, body, proxyURL)
		if err != nil {
			if headerGuard != nil {
				headerGuard.close()
			}
			return nil, err
		}
		// Final plaintext wire body: after every semantic rewrite (including the
		// recovery retry edits) and before zstd inside doOpenAICodexUpstream. The
		// check compares the slice held here and never reads req.Body.
		integrityOpts.UpstreamModel = upstreamModel
		if integrityErr := s.checkStagedRequestIntegrity(c, account, "http_forward", body, integrityOpts); integrityErr != nil {
			if headerGuard != nil {
				headerGuard.close()
			}
			return nil, integrityErr
		}
		reasoningRecovery.BindDiagnosticRequest(diagnosticIncomingBody, upstreamReq)

		// Send request
		upstreamStart := time.Now()
		reasoningRecovery.MarkAttemptDispatched()
		resp, err := s.doOpenAICodexUpstream(upstreamReq, account, proxyURL)
		reasoningRecovery.ObserveResponse(resp)
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		if headerGuard != nil && headerGuard.stopHeaderWait() {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			headerGuard.close()
			if reasoningRecovery.RecoveryAttempt() {
				return nil, errors.New("reasoning recovery upstream header timeout")
			}
			return nil, s.newOpenAIFirstOutputTimeoutError(
				ctx, c, account, opsUpstreamProxyID(account), opsUpstreamProxyName(account),
				startTime, originalModel, reasoningEffortValue,
				firstOutputTimeout, "response_headers", nil,
			)
		}
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if headerGuard != nil {
				headerGuard.close()
			}
			if reasoningRecovery.RecoveryAttempt() {
				return nil, err
			}
			// Transport-level failure (proxy/DNS/TCP/TLS — no HTTP response). Convert to
			// a failover so the handler switches to a healthy account, and temporarily
			// unschedule the account on durable faults (e.g. rejected proxy credentials).
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
		}
		if headerGuard != nil {
			resp.Body = &openAIRequestContextReadCloser{ReadCloser: resp.Body, cleanup: headerGuard.close}
		}

		// Handle error response
		if resp.StatusCode >= 400 {
			respBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			if retryBody, retry := reasoningRecovery.TryRecover(resp.StatusCode, resp.Header, respBody, false); retry {
				body = retryBody
				requestView = newOpenAIRequestView(body)
				reqBody = nil
				continue
			}
			if reasoningRecovery.RecoveryAttempt() {
				return nil, reasoningRecovery.FailureForResponse(resp.StatusCode, resp.Header, respBody)
			}
			if _, rejected := parseOpenAIReasoningRejection(respBody); rejected {
				return nil, NewOpenAIContinuationStateUnavailableError(resp.StatusCode, resp.Header, respBody)
			}
			if failoverErr, ok := s.handleCindyBalanceHTTPFailover(
				ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel,
			); ok {
				return nil, failoverErr
			}

			upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
			upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
			if !agentTaskRecoveryTried && s.isAgentIdentityAccount(ctx, account) && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
				agentTaskRecoveryTried = true
				expectedTaskID := account.GetCredential("task_id")
				if err := s.recoverAgentIdentityTask(ctx, account, expectedTaskID); err != nil {
					return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
				}
				continue
			}
			respBody = s.redactAgentIdentitySensitiveBody(ctx, account, respBody)
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			continuationStateError := classifyOpenAIContinuationStateError(upstreamMsg, respBody)
			if continuationStateError != openAIContinuationStateErrorNone {
				// The failure describes request history, not account health. Do not
				// let a compatibility proxy's 4xx/5xx wrapper fan this one request
				// out across the remaining scheduler pool.
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "continuation_state",
					Message:            OpenAIContinuationStateUnavailableClientMessage,
					ContinuationDiagnostic: buildOpenAIContinuationDiagnostic(
						c, diagnosticIncomingBody, upstreamReq, body, respBody, string(continuationStateError),
					),
				})
				return nil, NewOpenAIContinuationStateUnavailableError(resp.StatusCode, resp.Header, respBody)
			}
			if retryBody, reason, changed, retryErr := normalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, respBody); retryErr != nil {
				return nil, fmt.Errorf("normalize rejected Responses field retry body: %w", retryErr)
			} else if changed && rejectedFieldRetryState.Allow(retryBody) {
				body = retryBody
				requestView = newOpenAIRequestView(body)
				reqBody = nil
				logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Retrying non-WSv2 request after %s (account: %s)", reason, account.Name)
				continue
			}
			if retryBody, fallbackModel, retry := s.prepareOpenAICompactFallbackRetry(
				c, account, originalModel, body, resp.StatusCode, upstreamMsg, respBody, compactModelFallbackRetried,
			); retry {
				s.appendOpenAICompactFallbackRetryOps(c, account, resp, respBody, upstreamMsg, false)
				body = retryBody
				requestView = newOpenAIRequestView(body)
				reqBody = nil
				upstreamModel = fallbackModel
				compactModelFallbackRetried = true
				SetOpsUpstreamModel(c, fallbackModel)
				continue
			}
			if classification := classifyOpenAIRequestRejection(resp.StatusCode, upstreamMsg, respBody); classification != "" {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "request_rejected",
					Message:            OpenAIRequestRejectedClientMessage,
					ContinuationDiagnostic: buildOpenAIContinuationDiagnostic(
						c, diagnosticIncomingBody, upstreamReq, body, respBody, classification,
					),
				})
				return nil, NewOpenAIRequestRejectedError(resp.StatusCode, resp.Header)
			}
			shouldFailover := s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMsg, respBody)
			if shouldFailover {
				upstreamDetail := ""
				if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
					maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
					if maxBytes <= 0 {
						maxBytes = 2048
					}
					upstreamDetail = truncateString(string(respBody), maxBytes)
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					ProxyID:            opsUpstreamProxyID(account),
					ProxyName:          opsUpstreamProxyName(account),
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "failover",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				shouldDisable := s.handleFailoverSideEffects(ctx, resp, account, respBody, upstreamModel)
				failoverErr := s.newOpenAIAccountFailoverError(
					account,
					resp.StatusCode,
					resp.Header,
					respBody,
					upstreamMsg,
					shouldDisable,
					openAIHTTPPoolRetryable(ctx, account, resp.StatusCode, upstreamMsg, respBody, shouldDisable),
				)
				if resp.StatusCode == http.StatusForbidden &&
					IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
					failoverErr = sanitizeOpenAICindyFailoverError(failoverErr)
				}
				return nil, failoverErr
			}
			return s.handleErrorResponse(ctx, resp, c, account, body, billingModel)
		}
		defer func() { _ = resp.Body.Close() }()
		if mapping, ok := openAIResponsesClientToolMapping(c); ok && isEventStreamResponse(resp.Header) {
			maxLineSize := defaultMaxLineSize
			if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
				maxLineSize = s.cfg.Gateway.MaxLineSize
			}
			resp.Body = newResponsesClientToolStreamBody(resp.Body, mapping, maxLineSize)
		}

		serviceTier := extractOpenAIServiceTierFromBody(body)
		// 上游接受后只保留计费需要的标量，避免响应处理期间继续保活完整 input/tools map。
		reqBody = nil

		// Handle normal response
		var usage *OpenAIUsage
		var firstTokenMs *int
		responseID := ""
		imageCount := 0
		searchCount := 0
		var imageOutputSizes []string
		var opaqueBindingIDs []string
		if reqStream {
			setOpenAIRefusalEarlyStreamEligibility(c, account, body)
			streamResult, err := s.handleStreamingResponseWithReasoning(ctx, resp, c, account, startTime, originalModel, upstreamModel, reasoningEffortValue)
			if err != nil {
				if retryBody, retry := reasoningRecovery.TryRecoverError(err); retry {
					_ = resp.Body.Close()
					body = retryBody
					requestView = newOpenAIRequestView(body)
					reqBody = nil
					continue
				}
				if reasoningRecovery.RecoveryAttempt() {
					return nil, err
				}
				if signal, ok := asOpenAICompactFallbackSignal(err); ok {
					if retryBody, fallbackModel, retry := s.prepareOpenAICompactFallbackRetry(
						c, account, originalModel, body, http.StatusBadRequest, signal.message, signal.payload, compactModelFallbackRetried,
					); retry {
						s.appendOpenAICompactFallbackRetryOps(c, account, resp, signal.payload, signal.message, false)
						_ = resp.Body.Close()
						body = retryBody
						requestView = newOpenAIRequestView(body)
						reqBody = nil
						upstreamModel = fallbackModel
						compactModelFallbackRetried = true
						SetOpsUpstreamModel(c, fallbackModel)
						continue
					}
					if resp.Body != nil {
						_ = resp.Body.Close()
					}
					compactResp, compactBody := openAICompactFallbackErrorResponse(resp, signal)
					if s.shouldFailoverOpenAIUpstreamResponse(account, compactResp.StatusCode, signal.message, compactBody) {
						appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
							ProxyID:            opsUpstreamProxyID(account),
							ProxyName:          opsUpstreamProxyName(account),
							Platform:           account.Platform,
							AccountID:          account.ID,
							AccountName:        account.Name,
							UpstreamStatusCode: compactResp.StatusCode,
							UpstreamRequestID:  compactResp.Header.Get("x-request-id"),
							Kind:               "failover",
							Message:            signal.message,
						})
						shouldDisable := s.handleFailoverSideEffects(ctx, compactResp, account, compactBody, upstreamModel)
						return nil, s.newOpenAIAccountFailoverError(
							account, compactResp.StatusCode, compactResp.Header, compactBody, signal.message, shouldDisable,
							!shouldDisable && account.IsPoolMode() && (account.IsPoolModeRetryableStatus(compactResp.StatusCode) || isOpenAITransientProcessingError(compactResp.StatusCode, signal.message, compactBody)),
						)
					}
					return s.handleErrorResponse(ctx, compactResp, c, account, body, resolveOpenAIErrorSchedulingModel(billingModel, upstreamModel))
				}
				return nil, err
			}
			usage = streamResult.usage
			firstTokenMs = streamResult.firstTokenMs
			responseID = strings.TrimSpace(streamResult.responseID)
			imageCount = streamResult.imageCount
			imageOutputSizes = streamResult.imageOutputSizes
			searchCount = streamResult.searchCount
			opaqueBindingIDs = streamResult.opaqueBindingIDs
		} else {
			nonStreamResult, err := s.handleNonStreamingResponse(ctx, resp, c, account, originalModel, upstreamModel)
			if err != nil {
				if retryBody, retry := reasoningRecovery.TryRecoverError(err); retry {
					_ = resp.Body.Close()
					body = retryBody
					requestView = newOpenAIRequestView(body)
					reqBody = nil
					continue
				}
				if reasoningRecovery.RecoveryAttempt() {
					return nil, err
				}
				if signal, ok := asOpenAICompactFallbackSignal(err); ok {
					if retryBody, fallbackModel, retry := s.prepareOpenAICompactFallbackRetry(
						c, account, originalModel, body, http.StatusBadRequest, signal.message, signal.payload, compactModelFallbackRetried,
					); retry {
						s.appendOpenAICompactFallbackRetryOps(c, account, resp, signal.payload, signal.message, false)
						_ = resp.Body.Close()
						body = retryBody
						requestView = newOpenAIRequestView(body)
						reqBody = nil
						upstreamModel = fallbackModel
						compactModelFallbackRetried = true
						SetOpsUpstreamModel(c, fallbackModel)
						continue
					}
					_ = resp.Body.Close()
					compactResp, _ := openAICompactFallbackErrorResponse(resp, signal)
					return s.handleErrorResponse(ctx, compactResp, c, account, body, resolveOpenAIErrorSchedulingModel(billingModel, upstreamModel))
				}
				return nil, err
			}
			usage = nonStreamResult.usage
			responseID = strings.TrimSpace(nonStreamResult.responseID)
			imageCount = nonStreamResult.imageCount
			imageOutputSizes = nonStreamResult.imageOutputSizes
			searchCount = nonStreamResult.searchCount
			opaqueBindingIDs = nonStreamResult.opaqueBindingIDs
		}
		s.bindHTTPResponseAccount(ctx, c, account, responseID)
		s.bindCindyOpaqueContinuationAccount(ctx, c, account, opaqueBindingIDs)

		// Extract and save Codex usage snapshot from response headers (for OAuth accounts).
		// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
		if account.UsesOpenAICodexProtocol() && !account.IsShadow() {
			if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
				s.updateCodexUsageSnapshot(ctx, account.ID, snapshot)
			}
		}

		if usage == nil {
			usage = &OpenAIUsage{}
		}

		forwardResult := &OpenAIForwardResult{
			RequestID:                     resp.Header.Get("x-request-id"),
			UpstreamHeaders:               resp.Header,
			ResponseID:                    responseID,
			Usage:                         *usage,
			Model:                         originalModel,
			BillingModel:                  billingModel,
			UpstreamModel:                 upstreamModel,
			UpstreamResponseModel:         observedUpstreamResponseModel(c),
			UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
			UpstreamResponseServiceTier:   observedUpstreamResponseServiceTier(c),
			ServiceTier:                   resolvedOpenAIUpstreamServiceTier(c, serviceTier),
			ReasoningEffort:               reasoningEffort,
			Stream:                        reqStream,
			OpenAIWSMode:                  false,
			Duration:                      time.Since(startTime),
			FirstTokenMs:                  firstTokenMs,
		}
		if imageCount > 0 {
			forwardResult.ImageCount = imageCount
			forwardResult.ImageSize = imageSizeTier
			forwardResult.ImageInputSize = imageInputSize
			forwardResult.ImageOutputSizes = imageOutputSizes
			forwardResult.BillingModel = imageBillingModel
		}
		// Grok-native web_search / x_search / tool_search tool invocations (per-1k pricing).
		// Token cost still applies separately when usage is present; search is additive only
		// when search_price_per_1k is configured (nil price → $0 from CalculateSearchCost).
		if searchCount > 0 && account != nil && account.IsGrok() {
			forwardResult.SearchCount = searchCount
		}
		stampOpenAIResponsesUpstreamEndpoint(c, forwardResult)
		return forwardResult, nil
	}
}

func shouldForwardOpenAIResponsesViaRawChatCompletions(account *Account) bool {
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	if account.IsOpenCodeGo() {
		// Model protocol_rules are the authority. Probe Extra must not collapse
		// Grok/GPT/Muse into Chat Completions.
		return false
	}
	if account.IsCNProvider() {
		// CN 的显式协议配置优先于异步探针 Extra；adaptive 仅 DeepSeek / Kimi
		// 有原生 Responses，GLM 回退 Chat Completions。
		switch account.GetAPIProtocol() {
		case APIProtocolChatCompletions:
			return true
		case APIProtocolAdaptive:
			return !account.SupportsNativeCNResponses()
		default:
			return false
		}
	}
	return !openai_compat.ShouldUseResponsesAPI(account.Extra)
}

func (s *OpenAIGatewayService) buildUpstreamRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token string, isStream bool, promptCacheKey string, isCodexCLI bool) (*http.Request, error) {
	// Determine target URL based on account type
	var targetURL string
	switch account.Type {
	case AccountTypeOAuth, AccountTypeSetupToken:
		// OAuth accounts use ChatGPT internal API
		targetURL = chatgptCodexURL
	case AccountTypeAPIKey:
		// API Key accounts use Platform API or custom base URL
		baseURL := account.GetOpenAIBaseURL()
		if account.UsesNativeCNResponses() && account.IsAdaptiveAPIProtocol() {
			baseURL = account.GetCNProtocolBaseURL(APIProtocolResponses)
		}
		if baseURL == "" {
			targetURL = openaiPlatformAPIURL
		} else {
			validatedURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, err
			}
			targetURL = buildOpenAIResponsesURLForPlatform(account.Platform, validatedURL)
		}
	default:
		targetURL = openaiPlatformAPIURL
	}
	targetURL = appendOpenAIResponsesRequestPathSuffix(targetURL, openAIResponsesRequestPathSuffix(c))

	// DeepSeek / Kimi 原生 Responses 端点为无状态实现：强制 store=false、清除
	// previous_response_id，避免携带状态字段被上游拒绝。
	body = normalizeDeepSeekResponsesRequestBody(account, body)

	body, err := normalizeOpenAICompatibleResponsesReasoningSummary(account, targetURL, body)
	if err != nil {
		return nil, fmt.Errorf("normalize compatible Responses reasoning summary: %w", err)
	}

	body, err = s.finalizeBusinessPromptForSend(c, account, body, BusinessSystemPromptProtocolResponses, isOpenAIResponsesCompactPath(c))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))

	// Build authentication for this request. Agent Identity signs a fresh
	// assertion here; OAuth/PAT/API-key keep their existing Bearer behavior.
	authHeaders, err := s.buildOpenAIAuthenticationHeaders(ctx, account, token)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Set headers specific to OAuth accounts (ChatGPT internal API)
	if account.UsesOpenAICodexProtocol() {
		// Required: set Host for ChatGPT API (must use req.Host, not Header.Set)
		req.Host = "chatgpt.com"
		if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
			return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
		}
	}

	// Whitelist passthrough headers
	for key, values := range c.Request.Header {
		lowerKey := strings.ToLower(key)
		if openaiAllowedHeaders[lowerKey] {
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
	}
	// 客户端回带的 x-codex-turn-state 若已知由其他账号铸造（failover 换号），
	// 剥离后再出站——异账号 blob 与本账号的（指纹收敛后）出站身份自相矛盾。
	s.guardOpenAICodexTurnStateEcho(c, account, req.Header)
	req = withCodexRoutingModel(req, extractOpenAICodexTicketModel(body))
	stageCodexRoutingTurn(c, body)
	if err := s.applyOpenAICodexTicket(ctx, account, extractOpenAICodexTicketModel(body), req.Header); err != nil {
		return nil, err
	}
	if account.UsesOpenAICodexProtocol() {
		compatMessagesBridge := isOpenAICompatMessagesBridgeContext(c) || isOpenAICompatMessagesBridgeBody(body)
		// 真实 Codex 只发送连字符形式的 session-id / thread-id；下划线形式的
		// session_id / conversation_id 只在 legacy compact 桥接上保留，其余路径一律清除。
		req.Header.Del("conversation_id")
		req.Header.Del("session_id")

		if compatMessagesBridge {
			req.Header.Del("OpenAI-Beta")
			req.Header.Del("originator")
		} else {
			req.Header.Set("originator", resolveOpenAIUpstreamOriginator(c, isCodexCLI))
		}
		apiKeyID := getAPIKeyIDFromContext(c)
		if isOpenAIResponsesCompactPath(c) {
			req.Header.Set("accept", "application/json")
			if req.Header.Get("version") == "" {
				req.Header.Set("version", CodexCanonicalClientVersion())
			}
			compactSession := resolveOpenAICompactSessionID(c)
			req.Header.Set("session_id", isolateOpenAIUpstreamSessionID(apiKeyID, codexAccountIdentitySource(c, account), compactSession))
		} else {
			req.Header.Set("accept", "text/event-stream")
		}
	} else if isOpenAIResponsesCompactPath(c) {
		// compact 上游是 unary JSON 协议：API-key 账号也显式声明 Accept，
		// 避免 OpenAI 兼容网关按 SSE 返回（#3777 期望行为 4）。
		req.Header.Set("accept", "application/json")
	}

	// Apply custom User-Agent if configured
	customUA := account.GetOpenAIUserAgent()
	if customUA != "" {
		req.Header.Set("user-agent", customUA)
	}

	// 若开启 ForceCodexCLI，则强制将上游 User-Agent 伪装为规范 Codex 身份。
	// 用于网关未透传/改写 User-Agent 时，仍能命中 Codex 侧识别逻辑。
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		req.Header.Set("user-agent", CodexCanonicalUserAgent())
	}

	applyCodexAccountIdentityHeaders(req.Header, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c))
	// 指纹收敛：使用 Forward() 中预计算的收敛 ID 改写出站头，与请求体使用同一份 IDs。
	applyStagedCodexFingerprintHeaders(c, account, req.Header)
	// 真实 Codex 必带的连字符会话头：入站缺失时以最终请求体的 prompt_cache_key
	// （已作用域改写，与 client_metadata.session_id 同源）补齐，保持 root 线程不变式。
	if account.UsesOpenAICodexProtocol() && !isOpenAIResponsesCompactPath(c) {
		ensureCodexSessionIdentityHeaders(req.Header, gjson.GetBytes(body, "prompt_cache_key").String())
	}

	// 终态收口：强制统一 OAuth 出站身份（User-Agent / originator / version 同源自洽），
	// 身份取自该 OAuth 凭据的账号级 Codex TUI 身份；客户端自报身份不参与构造。
	if account.UsesOpenAICodexProtocol() {
		if err := enforceCodexIdentityHeadersForAccountContext(req.Context(), req.Header, codexAccountIdentitySource(c, account), s.codexIdentityOverrideUA(account)); err != nil {
			return nil, err
		}
	}

	// Ensure required headers exist
	if req.Header.Get("content-type") == "" {
		req.Header.Set("content-type", "application/json")
	}

	// 账号级请求头覆写（仅 openai api_key 账号启用时生效；OAuth 路径 no-op）
	account.ApplyHeaderOverrides(req.Header)
	applyOpenCodeSessionHeader(c, account, targetURL, req.Header, body, openCodeSessionHintBody(promptCacheKey))
	// x-codex-beta-features：按真实 Codex 的会话级行为补注（在账号级覆写之后，
	// 保证不被覆盖丢失）。
	applyOpenAICodexBetaFeatures(c, account, req.Header)
	setOpenAICodexRoutingHintFromBody(req.Header, account, body)
	logOpenAIRoutingDiagnosticsFromBody(ctx, account, "http", req.Header, body, "not_applicable")

	return req, nil
}

// codexIdentityOverrideUA 返回账号级显式配置的出站 User-Agent，供强制统一身份时作为覆写来源。
// ForceCodexCLI 语义是「强制使用 Codex CLI 身份」，等价于使用网关规范身份，故返回空串；
// 该优先级与历史行为一致（ForceCodexCLI 在账号自定义 UA 之后生效）。
func (s *OpenAIGatewayService) codexIdentityOverrideUA(account *Account) string {
	if s != nil && s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		return ""
	}
	return codexAccountIdentityOverrideUA(account)
}

// resolveStagedCodexFingerprintIDs 解析本次请求的收敛 ID，并让 turn metadata 的 sandbox 跟随
// 最终出站 UA：UA 来源与构造器/收口点一致——影子账号取 staged 的父账号身份，账号显式 UA
// 按 ForceCodexCLI 策略生效——否则父/子系统不同或显式 UA 覆盖时 sandbox 会与 UA 分裂。
func (s *OpenAIGatewayService) resolveStagedCodexFingerprintIDs(c *gin.Context, account *Account, clientHeaders http.Header) *codexFingerprintIDs {
	ids := resolveCodexFingerprintIDsFromRequest(account, clientHeaders)
	if ids == nil {
		return nil
	}
	ids.alignSandboxWithUserAgent(s.effectiveCodexOutboundUserAgent(c, account, clientHeaders))
	return ids
}

// effectiveCodexOutboundUserAgent 预测构造器终态收口后实际发出的 User-Agent，与
// enforceCodexIdentityHeadersForAccount 使用同一套来源与策略：
//   - 强制统一开启：账号显式 UA（按 ForceCodexCLI 策略）> staged 父账号 / 自身派生身份 > 规范身份；
//   - 强制统一关闭（gateway.disable_codex_identity_enforcement）：终态只做 UA↔originator 配对，
//     保留构造器写入的 UA——ForceCodexCLI 时为规范 UA，否则账号显式 UA，否则客户端 UA；
//     不合法的 UA 回落规范身份。该全局开关与 fingerprint mode 是不同门控。
func (s *OpenAIGatewayService) effectiveCodexOutboundUserAgent(c *gin.Context, account *Account, clientHeaders http.Header) string {
	if isOpenAICompatMessagesBridgeContext(c) {
		// 兼容 Messages 桥接：构造器删除 originator，终态收口早退，不做 ForAccount 统一；
		// 线上 UA 就是构造器写入的值（ForceCodexCLI 规范 UA > 账号显式 UA > 客户端 UA），
		// 保持"不补回桥接 originator"的既有契约，只让 metadata 跟随该实际 UA。
		if s != nil && s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
			return CodexCanonicalUserAgent()
		}
		if custom := account.GetOpenAIUserAgent(); custom != "" {
			return custom
		}
		if clientHeaders != nil {
			return clientHeaders.Get("user-agent")
		}
		return ""
	}
	overrideUA := s.codexIdentityOverrideUA(account)
	if codexIdentityEnforcement.Load() {
		return resolveCodexOutboundIdentityForAccount(codexAccountIdentitySource(c, account), overrideUA).userAgent
	}
	candidate := overrideUA
	if candidate == "" {
		if s != nil && s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
			candidate = CodexCanonicalUserAgent()
		} else if clientHeaders != nil {
			candidate = clientHeaders.Get("user-agent")
		}
	}
	if _, paired, ok := openai.PairCodexClientIdentity(candidate); ok {
		return paired
	}
	return resolveCodexOutboundIdentity("").userAgent
}

// stageCodexFingerprintForWSFrame 为原生 WS 入口的每一帧解析、暂存并应用收敛 ID：原生
// Proxy 不经过 HTTP Forward，握手头（buildOpenAIWSHeaders → applyStagedCodexFingerprintHeaders）
// 与帧体 client_metadata 由此共用同一份 IDs；每帧重算使 session/full 模式的 turn_id 逐轮更新，
// device 模式只收敛 installation 与 sandbox。返回改写后的帧（未变化时原样返回）。
func (s *OpenAIGatewayService) stageCodexFingerprintForWSFrame(c *gin.Context, account *Account, frame []byte) ([]byte, error) {
	var clientHeaders http.Header
	if c != nil && c.Request != nil {
		clientHeaders = c.Request.Header
	}
	ids := s.resolveStagedCodexFingerprintIDs(c, account, clientHeaders)
	stageCodexFingerprintIDs(c, ids)
	if ids == nil {
		return frame, nil
	}
	next, changed, err := applyCodexFingerprintClientMetadataRaw(frame, ids)
	if err != nil {
		return frame, err
	}
	if changed {
		return next, nil
	}
	return frame, nil
}
