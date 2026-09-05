package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type openAIWSRejectedFieldRetryError struct {
	body   []byte
	reason string
}

func (e *openAIWSRejectedFieldRetryError) Error() string {
	if e == nil || strings.TrimSpace(e.reason) == "" {
		return "retry websocket turn after rejected field normalization"
	}
	return "retry websocket turn after rejected field normalization: " + e.reason
}

func openAIWSRejectedFieldRetryHTTPStatus(message []byte) int {
	for _, value := range gjson.GetManyBytes(message, "status", "status_code", "error.status", "error.status_code") {
		status := int(value.Int())
		if status >= 100 && status <= 599 {
			return status
		}
	}
	return openAIWSErrorHTTPStatus(message)
}

func (s *OpenAIGatewayService) openAIWSIngressInterTurnIdleTimeout() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds) * time.Second
}

// newOpenAIWSDownstreamWriteContext binds writes directly to the client
// lifecycle while excluding the separate ingress-lease cancellation signal.
// This lets a lease-loss path finish its current client write before
// ReadOpenAIWSClientMessage sends the retryable close frame.
func newOpenAIWSDownstreamWriteContext(controlCtx context.Context, hooks *OpenAIWSIngressHooks, timeout time.Duration) (context.Context, context.CancelFunc) {
	writeParent := controlCtx
	if hooks != nil && hooks.ClientLifecycleContext != nil {
		writeParent = hooks.ClientLifecycleContext
	}
	if writeParent == nil {
		writeParent = context.Background()
	}
	return context.WithTimeout(writeParent, timeout)
}

func (s *OpenAIGatewayService) ProxyResponsesWebSocketFromClient(
	ctx context.Context,
	c *gin.Context,
	clientConn *coderws.Conn,
	account *Account,
	token string,
	firstClientMessage []byte,
	hooks *OpenAIWSIngressHooks,
) (returnErr error) {
	if s == nil {
		return errors.New("service is nil")
	}
	if c == nil {
		return errors.New("gin context is nil")
	}
	if clientConn == nil {
		return errors.New("client websocket is nil")
	}
	if account == nil {
		return errors.New("account is nil")
	}
	setCodexToolNameReverse(c, nil)
	if _, err := s.prepareCodexAccountIdentitySource(ctx, c, account); err != nil {
		return err
	}
	if err := validateOpenAIWSBearerToken(account, token); err != nil {
		return err
	}
	trimmedFirstMessage := bytes.TrimSpace(firstClientMessage)
	if len(trimmedFirstMessage) == 0 {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "empty websocket request payload", nil)
	}
	if !gjson.ValidBytes(trimmedFirstMessage) {
		return NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"invalid websocket request payload",
			errors.New("invalid json"),
		)
	}

	// 预取一次 OpenAI Fast Policy settings，绑定到 ctx，让该 WS session
	// 内所有帧的 evaluateOpenAIFastPolicy 调用复用同一份快照，避免每帧
	// 进入 DB / settingRepo。Trade-off 见 withOpenAIFastPolicyContext 注释。
	if s.settingService != nil {
		if settings, err := s.settingService.GetOpenAIFastPolicySettings(ctx); err == nil && settings != nil {
			ctx = withOpenAIFastPolicyContext(ctx, settings)
		}
	}
	if preemptCtx, cleanupPreempt, armed := s.BeginOpenAIWSIngressSessionPreemption(ctx, c, account, firstClientMessage); armed {
		ctx = preemptCtx
		defer cleanupPreempt()
		defer func() {
			if isOpenAIWSSessionPreempted(ctx) {
				returnErr = errOpenAIWSSessionPreempted
			}
		}()
	}
	refusalRuntime := s.openAIRefusalRecoveryRuntime(ctx)

	wsDecision := s.getOpenAIWSProtocolResolver().Resolve(account)
	legacyLaxaAccount := IsLegacyCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
	// Fresh legacy Laxa turns remain on the explicitly selected passthrough
	// transport. Once a frame carries an anchor, an opaque carrier, or an
	// external reference, however, it has the same connection-affinity contract
	// as first-class Cindy continuation state and must use the stateful pool.
	// Classify the first frame before the mode-router branch so a busy bound
	// connection cannot be bypassed by an early passthrough return.
	legacyLaxaContinuation := false
	if legacyLaxaAccount {
		if classification, classifyErr := ClassifyCindyContinuation(firstClientMessage, CindyContinuationProof{}); classifyErr == nil {
			legacyLaxaContinuation = classification.HasAnchor || classification.Mode != CindyContinuationFullReplay
		}
	}
	strictCindyContinuation := IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) || legacyLaxaContinuation
	forceHTTPBridge := account.Platform == PlatformGrok
	modeRouterV2Enabled := s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled
	ingressMode := OpenAIWSIngressModeCtxPool
	if modeRouterV2Enabled && !forceHTTPBridge {
		ingressMode = account.ResolveOpenAIResponsesWebSocketV2Mode(s.cfg.Gateway.OpenAIWS.IngressModeDefault)
		if ingressMode == OpenAIWSIngressModeOff {
			return NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				"websocket mode is disabled for this account",
				nil,
			)
		}
		switch ingressMode {
		case OpenAIWSIngressModePassthrough:
			if strictCindyContinuation {
				// Strict Cindy continuation needs parsed anchors, exact live-connection
				// affinity, accumulator recovery, and opaque output binding.
				ingressMode = OpenAIWSIngressModeCtxPool
				break
			}
			if wsDecision.Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
				return fmt.Errorf("websocket ingress requires ws_v2 transport, got=%s", wsDecision.Transport)
			}
			if s.shouldBridgeOpenAIWSPassthroughFirstMessage(account, firstClientMessage) {
				forceHTTPBridge = true
				break
			}
			// 透传 relay 通过 TurnStarted 记录每个 turn 的开始时刻，但不触发
			// BeforeTurn；因此仍只有建连时的利润准入门，没有 turn 级复核。
			// handler 计费在 turn 定价未冻结时回退到对应的 turn 开始时刻。
			return s.proxyResponsesWebSocketV2Passthrough(
				ctx,
				c,
				clientConn,
				account,
				token,
				firstClientMessage,
				hooks,
				wsDecision,
			)
		case OpenAIWSIngressModeHTTPBridge:
			forceHTTPBridge = true
		case OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeShared, OpenAIWSIngressModeDedicated:
			// continue
		default:
			return NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				"websocket mode only supports ctx_pool/passthrough/http_bridge",
				nil,
			)
		}
	}
	if !forceHTTPBridge && wsDecision.Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
		return fmt.Errorf("websocket ingress requires ws_v2 transport, got=%s", wsDecision.Transport)
	}
	dedicatedMode := modeRouterV2Enabled && ingressMode == OpenAIWSIngressModeDedicated

	wsURL := ""
	wsHost := "-"
	wsPath := "-"
	if forceHTTPBridge {
		wsHost = "xai-http-bridge"
		wsPath = "/v1/responses"
	} else {
		var err error
		wsURL, err = s.buildOpenAIResponsesWSURL(account)
		if err != nil {
			return fmt.Errorf("build ws url: %w", err)
		}
		if parsedURL, parseErr := url.Parse(wsURL); parseErr == nil && parsedURL != nil {
			wsHost = normalizeOpenAIWSLogValue(parsedURL.Host)
			wsPath = normalizeOpenAIWSLogValue(parsedURL.Path)
		}
	}
	debugEnabled := isOpenAIWSModeDebugEnabled()
	isCodexCLI := openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator")) || (s.cfg != nil && s.cfg.Gateway.ForceCodexCLI)

	type openAIWSClientPayload struct {
		payloadRaw               []byte
		accountIdentitySourceRaw []byte
		rawForHash               []byte
		promptCacheKey           string
		previousResponseID       string
		originalModel            string
		imageBillingModel        string
		imageSizeTier            string
		imageInputSize           string
		payloadBytes             int
		requestedReasoningEffort *string
	}
	ingressSessionOriginalModel := ""

	applyPayloadMutation := func(current []byte, path string, value any) ([]byte, error) {
		next, err := sjson.SetBytes(current, path, value)
		if err == nil {
			return next, nil
		}

		// 仅在确实需要修改 payload 且 sjson 失败时，退回 map 路径确保兼容性。
		payload := make(map[string]any)
		if unmarshalErr := json.Unmarshal(current, &payload); unmarshalErr != nil {
			return nil, err
		}
		switch path {
		case "type", "model":
			payload[path] = value
		case "client_metadata." + openAIWSTurnMetadataHeader:
			setOpenAIWSTurnMetadata(payload, fmt.Sprintf("%v", value))
		default:
			return nil, err
		}
		rebuilt, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, marshalErr
		}
		return rebuilt, nil
	}

	parseClientPayload := func(turn int, raw []byte) (openAIWSClientPayload, error) {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "empty websocket request payload", nil)
		}
		if !gjson.ValidBytes(trimmed) {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", errors.New("invalid json"))
		}
		previousResponseID, anchorErr := ParseOpenAIContinuationAnchor(trimmed)
		if anchorErr != nil {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				OpenAIContinuationAnchorValidationMessage,
				anchorErr,
			)
		}

		values := gjson.GetManyBytes(trimmed, "type", "model", "prompt_cache_key", "previous_response_id")
		eventType := strings.TrimSpace(values[0].String())
		normalized := trimmed
		switch eventType {
		case "":
			eventType = "response.create"
			next, setErr := applyPayloadMutation(normalized, "type", eventType)
			if setErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", setErr)
			}
			normalized = next
		case "response.create":
		case "response.append":
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				"response.append is not supported in ws v2; use response.create with previous_response_id",
				nil,
			)
		default:
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				fmt.Sprintf("unsupported websocket request type: %s", eventType),
				nil,
			)
		}
		requestedReasoningEffort := CanonicalRequestedReasoningEffort(normalized, strings.TrimSpace(values[1].String()))
		if next, policyErr := applyOpenAIWSReasoningEffortPolicy(normalized, hooks); policyErr != nil {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, policyErr.Error(), policyErr)
		} else {
			normalized = next
		}
		responsesLite := isOpenAIResponsesLiteWebSocketPayload(normalized)
		if !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			if compatibilityBody, compatibilityChanged, compatibilityErr := normalizeOpenAIResponsesWebSocketCompatibilityBody(normalized, account, responsesLite); compatibilityErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", compatibilityErr)
			} else if compatibilityChanged {
				normalized = compatibilityBody
			}
		}
		if account.IsOpenAIOAuthLike() {
			aliasedBody, reverse, aliased, aliasErr := aliasOpenAIOAuthReservedToolNamesBody(normalized)
			if aliasErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, aliasErr.Error(), aliasErr)
			}
			updateCodexToolNameReverseForWSFrame(c, normalized, reverse)
			if aliased {
				normalized = aliasedBody
			}
		}

		originalModel := strings.TrimSpace(values[1].String())
		modelMissing := originalModel == ""
		if originalModel == "" {
			// 入站 WS 长会话里，部分客户端只在第一轮 response.create 上声明
			// model，后续 turn 复用同一 session-level model。为避免因省略
			// model 直接断开用户连接，这里回落到上一轮已通过校验的客户端模型，
			// 并在下方写回上游 payload，保证账号模型映射/fast policy/图片权限
			// 仍按同一模型执行。
			originalModel = ingressSessionOriginalModel
			if originalModel == "" {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
					coderws.StatusPolicyViolation,
					"model is required in response.create payload",
					nil,
				)
			}
		}
		promptCacheKey := strings.TrimSpace(values[2].String())
		if turnMetadata := strings.TrimSpace(c.GetHeader(openAIWSTurnMetadataHeader)); turnMetadata != "" {
			next, setErr := applyPayloadMutation(normalized, "client_metadata."+openAIWSTurnMetadataHeader, turnMetadata)
			if setErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", setErr)
			}
			normalized = next
		}
		accountIdentitySourceRaw := append([]byte(nil), normalized...)
		accountScopedPayload, accountScoped, scopeErr := applyCodexAccountIdentityClientMetadataRaw(normalized, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c))
		if scopeErr != nil {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket identity metadata", scopeErr)
		}
		if accountScoped {
			normalized = accountScopedPayload
		}
		if responsesLite {
			litePayload, _, liteErr := normalizeOpenAIResponsesLitePayloadForAccount(normalized, account)
			if liteErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
					coderws.StatusPolicyViolation,
					liteErr.Error(),
					liteErr,
				)
			}
			normalized = litePayload
		}
		apiKey := getAPIKeyFromContext(c)
		imageGenerationAllowed := GroupAllowsImageGeneration(apiKeyGroup(apiKey))
		codexImageGenerationExplicitToolPolicy := codexImageGenerationExplicitToolPolicyAllow
		if isCodexCLI {
			codexImageGenerationExplicitToolPolicy = account.CodexImageGenerationExplicitToolPolicy()
		}
		codexBridgeEnabled := isCodexCLI &&
			!isOpenAIResponsesLiteWebSocketPayload(normalized) &&
			imageGenerationAllowed &&
			codexImageGenerationExplicitToolPolicy != codexImageGenerationExplicitToolPolicyStrip &&
			s.isCodexImageGenerationBridgeEnabled(ctx, account, apiKey)
		if codexBridgeEnabled {
			payloadMap := make(map[string]any)
			if err := json.Unmarshal(normalized, &payloadMap); err != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", err)
			}
			bridgeModified := false
			if ensureOpenAIResponsesImageGenerationTool(payloadMap) {
				bridgeModified = true
				logOpenAIWSModeInfo("ingress_ws_codex_image_tool_injected account_id=%d", account.ID)
			}
			if ensureOpenAIResponsesImageGenerationToolChoiceAuto(payloadMap) {
				bridgeModified = true
				logOpenAIWSModeInfo("ingress_ws_codex_image_tool_choice_auto account_id=%d", account.ID)
			}
			if normalizeOpenAIResponsesImageGenerationTools(payloadMap) {
				bridgeModified = true
			}
			if applyCodexImageGenerationBridgeInstructions(payloadMap) {
				bridgeModified = true
				logOpenAIWSModeInfo("ingress_ws_codex_image_bridge_instructions_added account_id=%d", account.ID)
			}
			if bridgeModified {
				rebuilt, marshalErr := json.Marshal(payloadMap)
				if marshalErr != nil {
					return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", marshalErr)
				}
				normalized = rebuilt
			}
		}
		requestModel := originalModel
		if hooks != nil && hooks.MapRequestModel != nil {
			mappedModel, mapErr := hooks.MapRequestModel(turn, originalModel)
			if mapErr != nil {
				return openAIWSClientPayload{}, mapErr
			}
			if mappedModel = strings.TrimSpace(mappedModel); mappedModel != "" {
				requestModel = mappedModel
			}
		}
		legacyModel, legacyModelKnown := requestModel, false
		if legacyLaxaAccount {
			legacyModel, legacyModelKnown = cindyLegacyLaxaLiveUpstreamModel(requestModel)
		}
		if !legacyModelKnown {
			legacyModel = requestModel
		}
		if legacyModelKnown {
			// A stale account model_mapping must not override the verified live
			// wire ID for a direct legacy Laxa public model or compatibility alias.
			requestModel = legacyModel
		}
		mappedRequestModel := requestModel
		if !legacyModelKnown {
			mappedRequestModel = account.GetMappedModel(requestModel)
		}
		upstreamModel := normalizeOpenAIModelForUpstream(account, mappedRequestModel)
		// Legacy Laxa API-key rows are still represented as PlatformOpenAI during
		// the projection window. Their account mapping therefore does not enter
		// the first-class Cindy resolver; apply the same provider-qualified live
		// ID used by HTTP passthrough before writing every WS request frame.
		upstreamModel = resolveLegacyCindyOpenAIModel(account, upstreamModel)
		if modelMissing || upstreamModel != originalModel {
			next, setErr := applyPayloadMutation(normalized, "model", upstreamModel)
			if setErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", setErr)
			}
			normalized = next
		}
		if isCodexCLI && codexImageGenerationExplicitToolPolicy == codexImageGenerationExplicitToolPolicyStrip {
			if stripped, changed, stripErr := stripOpenAIImageGenerationToolsFromRawPayload(normalized); stripErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", stripErr)
			} else if changed {
				normalized = stripped
				logOpenAIWSModeInfo("ingress_ws_codex_image_tool_stripped_by_policy account_id=%d", account.ID)
			}
		}
		if stripped, changed, stripErr := stripCodexSparkImageGenerationToolFromRawPayload(normalized, upstreamModel); stripErr != nil {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", stripErr)
		} else if changed {
			normalized = stripped
			logOpenAIWSModeInfo("ingress_ws_codex_spark_image_tool_stripped account_id=%d", account.ID)
		}
		imageIntent := IsImageGenerationIntentForPlatform(openAIResponsesEndpoint, originalModel, normalized, account.Platform)
		if imageIntent && !imageGenerationAllowed {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, ImageGenerationPermissionMessage(), nil)
		}
		imageBillingModel := ""
		imageSizeTier := ""
		imageInputSize := ""
		if imageIntent {
			var imageCfgErr error
			imageCfg, imageCfgErr := resolveOpenAIResponsesImageBillingConfigDetailedFromBody(normalized, originalModel)
			if imageCfgErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, imageCfgErr.Error(), imageCfgErr)
			}
			imageBillingModel = imageCfg.Model
			imageSizeTier = imageCfg.SizeTier
			imageInputSize = imageCfg.InputSize
		}

		// Apply OpenAI Fast Policy on the response.create frame using the same
		// evaluator/normalize/scope rules as the HTTP entrypoints. This is the
		// single integration point for all WS ingress turns (first + follow-up
		// frames flow through here).
		//
		// Model fallback: first turn still requires model at the handler layer；
		// follow-up response.create frames may omit it and then reuse
		// ingressSessionOriginalModel. We always write a concrete upstream model
		// before evaluating policy, so whitelist / filter behavior remains stable.
		policyApplied, blocked, policyErr := s.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, upstreamModel, normalized)
		if policyErr != nil {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", policyErr)
		}
		if blocked != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			// Send a Realtime-style error event to the client first, then
			// signal the handler to close the connection with PolicyViolation.
			// We intentionally do NOT forward this frame upstream.
			//
			// coder/websocket@v1.8.14 Conn.Write is synchronous and flushes
			// the underlying bufio writer before returning (write.go:42 →
			// 307-311), and the subsequent close handshake re-acquires the
			// same writeFrameMu, so the error event is guaranteed to reach
			// the kernel send buffer before any close frame is queued.
			eventBytes := buildOpenAIFastPolicyBlockedWSEvent(blocked)
			if eventBytes != nil {
				writeCtx, cancel := newOpenAIWSDownstreamWriteContext(ctx, hooks, s.openAIWSWriteTimeout())
				_ = clientConn.Write(writeCtx, coderws.MessageText, eventBytes)
				cancel()
			}
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				blocked.Message,
				blocked,
			)
		}
		normalized = policyApplied
		beginBusinessSystemPromptRequestTurn(c)
		if updatedPromptPayload, application, promptErr := s.applyBusinessSystemPromptForRequest(
			c, normalized, account, BusinessSystemPromptProtocolResponses, isOpenAIResponsesCompactPath(c),
		); promptErr != nil {
			if errors.Is(promptErr, ErrBusinessSystemPromptUnavailable) {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
					coderws.StatusTryAgainLater,
					"system_prompt_unavailable",
					promptErr,
				)
			}
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				"invalid websocket request payload",
				promptErr,
			)
		} else {
			normalized = updatedPromptPayload
			normalized, promptErr = rewriteBusinessSystemPromptCacheKey(normalized, application)
			if promptErr != nil {
				return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
					coderws.StatusPolicyViolation,
					"invalid websocket request payload",
					promptErr,
				)
			}
		}
		if normalizedPayload, changed, normalizeErr := normalizeCindyManagedPromptCacheKey(normalized, c, account); normalizeErr != nil {
			return openAIWSClientPayload{}, NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				"invalid websocket request payload",
				normalizeErr,
			)
		} else if changed {
			normalized = normalizedPayload
			promptCacheKey = strings.TrimSpace(gjson.GetBytes(normalized, "prompt_cache_key").String())
			observeCindyManagedPromptCacheNormalization(c, true)
		}
		ingressSessionOriginalModel = originalModel

		return openAIWSClientPayload{
			payloadRaw:               normalized,
			accountIdentitySourceRaw: accountIdentitySourceRaw,
			rawForHash:               trimmed,
			promptCacheKey:           promptCacheKey,
			previousResponseID:       previousResponseID,
			originalModel:            originalModel,
			imageBillingModel:        imageBillingModel,
			imageSizeTier:            imageSizeTier,
			imageInputSize:           imageInputSize,
			payloadBytes:             len(normalized),
			requestedReasoningEffort: requestedReasoningEffort,
		}, nil
	}

	var commitIngressTurnState func()
	rawWriteClientMessage := func(message []byte) error {
		writeCtx, cancel := newOpenAIWSDownstreamWriteContext(ctx, hooks, s.openAIWSWriteTimeout())
		defer cancel()
		if writeErr := clientConn.Write(writeCtx, coderws.MessageText, message); writeErr != nil {
			return writeErr
		}
		if commitIngressTurnState != nil {
			commitIngressTurnState()
		}
		return nil
	}
	writeClientMessage := rawWriteClientMessage
	var refusalOutput *openAIRefusalRecoveryWSOutput
	if refusalRuntime.CyberFailoverEnabled() || refusalRuntime.RewriteEnabled() {
		matcher := (*OpenAIRefusalMatcher)(nil)
		if refusalRuntime.RewriteEnabled() {
			matcher = refusalRuntime.Matcher
		}
		refusalOutput = newOpenAIRefusalRecoveryWSOutput(
			matcher,
			refusalRuntime.CyberFailoverEnabled(),
			func(writeCtx context.Context, messageType coderws.MessageType, payload []byte) error {
				if messageType != coderws.MessageText {
					return errors.New("unsupported websocket response message type")
				}
				return rawWriteClientMessage(payload)
			},
			func() {
				logOpenAIWSModeInfo("refusal_recovery_buffer_limit account_id=%d transport=websocket", account.ID)
			},
		)
		writeClientMessage = func(message []byte) error {
			return refusalOutput.Write(ctx, coderws.MessageText, message)
		}
	}

	readClientMessage := func() ([]byte, error) {
		idleTimeout := s.openAIWSIngressInterTurnIdleTimeout()
		msgType, payload, readErr := ReadOpenAIWSClientMessage(
			ctx,
			clientConn,
			idleTimeout,
			coderws.StatusNormalClosure,
			"websocket idle timeout",
		)
		if readErr != nil {
			var closeErr *OpenAIWSClientCloseError
			if errors.As(readErr, &closeErr) && closeErr.StatusCode() == coderws.StatusNormalClosure {
				logOpenAIWSModeInfo("ingress_ws_inter_turn_idle_timeout account_id=%d timeout_seconds=%d", account.ID, int(idleTimeout.Seconds()))
			}
			return nil, readErr
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			return nil, NewOpenAIWSClientCloseError(
				coderws.StatusPolicyViolation,
				fmt.Sprintf("unsupported websocket client message type: %s", msgType.String()),
				nil,
			)
		}
		return payload, nil
	}

	firstPayload, err := parseClientPayload(1, firstClientMessage)
	if err != nil {
		return err
	}

	turnState := strings.TrimSpace(c.GetHeader(openAIWSTurnStateHeader))
	stateStore := s.getOpenAIWSStateStore()
	groupID := getOpenAIGroupIDFromContext(c)
	resolveStrictCindyAnchorConn := func(previousResponseID string, liveConnID string) (string, error) {
		previousResponseID = strings.TrimSpace(previousResponseID)
		if !strictCindyContinuation || previousResponseID == "" {
			return "", nil
		}
		if stateStore == nil {
			return "", NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
		}
		boundConnID, ok := stateStore.GetResponseConn(previousResponseID)
		boundConnID = strings.TrimSpace(boundConnID)
		if !ok || boundConnID == "" {
			return "", NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
		}
		liveConnID = strings.TrimSpace(liveConnID)
		if liveConnID != "" && liveConnID != boundConnID {
			return "", NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
		}
		return boundConnID, nil
	}
	sessionHash := ""
	preferredConnID := ""
	storeDisabled := false
	refreshIngressRouteState := func(payload openAIWSClientPayload) {
		sessionHash = s.GenerateSessionHash(c, payload.rawForHash)
		if turnState == "" && stateStore != nil && sessionHash != "" {
			if savedTurnState, ok := stateStore.GetSessionTurnState(groupID, sessionHash, account.ID); ok {
				turnState = savedTurnState
			}
		}

		preferredConnID = ""
		if stateStore != nil && payload.previousResponseID != "" {
			if connID, ok := stateStore.GetResponseConn(payload.previousResponseID); ok {
				preferredConnID = connID
			}
		}

		storeDisabled = s.isOpenAIWSStoreDisabledInRequestRaw(payload.payloadRaw, account)
		if stateStore != nil && storeDisabled && payload.previousResponseID == "" && sessionHash != "" {
			if connID, ok := stateStore.GetSessionConn(groupID, sessionHash); ok {
				preferredConnID = connID
			}
		}
	}
	refreshIngressRouteState(firstPayload)

	if forceHTTPBridge || s.shouldBridgeOpenAIWSHTTP(account, firstPayload.payloadBytes, firstPayload.previousResponseID) {
		logOpenAIWSModeInfo(
			"ingress_ws_http_bridge_start account_id=%d account_type=%s payload_bytes=%d threshold_bytes=%d has_session_hash=%v store_disabled=%v",
			account.ID,
			account.Type,
			firstPayload.payloadBytes,
			s.openAIWSHTTPBridgeThresholdBytes(),
			sessionHash != "",
			storeDisabled,
		)
		currentBridgePayload := firstPayload
		// Keep the first turn as the stable conversation seed. The mapped model
		// is resolved again for each turn below so an in-connection model switch
		// cannot reuse another model's upstream cache identity.
		grokCacheSeedPayload := firstPayload.payloadRaw
		var bridgeReplayInput []json.RawMessage
		bridgeReplayInputExists := false
		var bridgeAccountFailoverInput []json.RawMessage
		bridgeAccountFailoverInputExists := false
		for turn := 1; ; turn++ {
			if turn > 1 && hooks != nil && hooks.BeforeRequest != nil {
				if err := hooks.BeforeRequest(turn, currentBridgePayload.payloadRaw, currentBridgePayload.originalModel); err != nil {
					return err
				}
			}
			if hooks != nil && hooks.BeforeTurn != nil {
				if err := hooks.BeforeTurn(turn); err != nil {
					return err
				}
			}
			if c != nil && c.Request != nil {
				if turnState == "" {
					c.Request.Header.Del(openAIWSTurnStateHeader)
				} else {
					c.Request.Header.Set(openAIWSTurnStateHeader, turnState)
				}
			}
			if c != nil && sessionHash != "" {
				c.Set(openAIWSIngressSessionHashContextKey, sessionHash)
			}
			// 剥离本会话已知失效的加密项，阻断同一失效密文随历史反复触发上游拒绝。
			// 历史序列须同步剥离，否则与已剥离的当前 input 项错位，prefix 复用失配。
			if invalidDigests := s.sessionInvalidEncryptedContentDigests(groupID, sessionHash); len(invalidDigests) > 0 {
				strippedPayload, strippedCount := s.stripSessionInvalidEncryptedContentLogged(
					currentBridgePayload.payloadRaw, invalidDigests, "ingress_ws_http_bridge_invalid_encrypted_lineage_strip", account.ID, turn,
				)
				if strippedCount > 0 {
					currentBridgePayload.payloadRaw = strippedPayload
					currentBridgePayload.payloadBytes = len(strippedPayload)
				}
				if bridgeReplayInputExists {
					bridgeReplayInput, _ = stripOpenAIInvalidEncryptedContentFromReplayItems(bridgeReplayInput, invalidDigests)
				}
				if bridgeAccountFailoverInputExists {
					bridgeAccountFailoverInput, _ = stripOpenAIInvalidEncryptedContentFromReplayItems(bridgeAccountFailoverInput, invalidDigests)
				}
			}
			bridgePayloadRaw := currentBridgePayload.payloadRaw
			bridgePayloadBytes := currentBridgePayload.payloadBytes
			toolOutputCoverage := AnalyzeToolCallOutputContextCoverageBytes(currentBridgePayload.payloadRaw)
			needsBridgeReplay := currentBridgePayload.previousResponseID != "" ||
				(toolOutputCoverage.HasFunctionCallOutput && !toolOutputCoverage.ContextCoversAllCallIDs)
			// 一次解析当前 input，正常 replay 与 account-failover 两份序列共享同一批正文。
			bridgeCurrentItems, bridgeCurrentItemsExist, extractErr := openAIWSExtractNormalizedInputSequence(
				currentBridgePayload.payloadRaw,
			)
			if extractErr != nil {
				return fmt.Errorf("build websocket http bridge replay input: %w", extractErr)
			}
			turnReplayInput, turnReplayInputExists := buildOpenAIWSReplayInputSequenceFromItems(
				bridgeReplayInput,
				bridgeReplayInputExists,
				bridgeCurrentItems,
				bridgeCurrentItemsExist,
				needsBridgeReplay,
			)
			turnAccountFailoverInput, turnAccountFailoverInputExists := buildOpenAIWSReplayInputSequenceFromItems(
				bridgeAccountFailoverInput,
				bridgeAccountFailoverInputExists,
				bridgeCurrentItems,
				bridgeCurrentItemsExist,
				needsBridgeReplay,
			)
			if needsBridgeReplay && turnReplayInputExists {
				updatedPayload, setInputErr := setOpenAIWSPayloadInputSequence(
					currentBridgePayload.payloadRaw,
					turnReplayInput,
					true,
				)
				if setInputErr != nil {
					return fmt.Errorf("set websocket http bridge replay input: %w", setInputErr)
				}
				bridgePayloadRaw = updatedPayload
				bridgePayloadBytes = len(updatedPayload)
				logOpenAIWSModeInfo(
					"ingress_ws_http_bridge_replay_input account_id=%d turn=%d input_items=%d previous_response_id_present=%v has_tool_output=%v",
					account.ID,
					turn,
					len(turnReplayInput),
					currentBridgePayload.previousResponseID != "",
					openAIWSRawPayloadHasToolCallOutput(currentBridgePayload.payloadRaw),
				)
			}
			grokCacheIdentity := ""
			if account.Platform == PlatformGrok {
				grokCacheIdentity, err = resolveGrokWSCacheIdentity(
					c,
					account,
					grokCacheSeedPayload,
					currentBridgePayload.payloadRaw,
					currentBridgePayload.originalModel,
				)
				if err != nil {
					return fmt.Errorf("resolve Grok websocket cache identity: %w", err)
				}
			}
			result, bridgeErr := s.proxyOpenAIWSHTTPBridgeTurn(
				ctx,
				c,
				account,
				token,
				bridgePayloadRaw,
				bridgePayloadBytes,
				currentBridgePayload.originalModel,
				currentBridgePayload.imageBillingModel,
				currentBridgePayload.imageSizeTier,
				currentBridgePayload.imageInputSize,
				grokCacheIdentity,
				turn,
				writeClientMessage,
			)
			if bridgeErr != nil && IsOpenAIRefusalRecoveryFailover(bridgeErr) && turn > 1 {
				if refusalOutput != nil {
					_ = refusalOutput.WriteRetryableFailure(ctx)
				}
				bridgeErr = newOpenAIWSCyberRecoveryError(nil, nil, false)
			}
			if hooks != nil && hooks.AfterTurn != nil {
				hooks.AfterTurn(turn, result, bridgeErr)
			}
			if bridgeErr != nil {
				var failoverErr *UpstreamFailoverError
				if turn > 1 && errors.As(bridgeErr, &failoverErr) && failoverErr != nil {
					retryPayload, retrySafe, retryPayloadErr := buildOpenAIWSCurrentTurnRetryPayload(
						currentBridgePayload.accountIdentitySourceRaw,
						turnAccountFailoverInput,
						turnAccountFailoverInputExists,
						currentBridgePayload.originalModel,
					)
					if retryPayloadErr != nil {
						return fmt.Errorf("build websocket current-turn failover payload: %w", retryPayloadErr)
					}
					if !retrySafe {
						retryPayload = nil
					}
					return newOpenAIWSCurrentTurnFailoverError(bridgeErr, retryPayload)
				}
				return bridgeErr
			}
			if result == nil {
				return errors.New("websocket http bridge turn result is nil")
			}
			// turnReplayInput/turnAccountFailoverInput 可能共享同一头数组（转移自
			// bridgeCurrentItems），保存历史必须经 combine 新建头，禁止就地 append。
			bridgeReplayInput = turnReplayInput
			bridgeReplayInputExists = turnReplayInputExists
			if result.wsReplayInputExists {
				bridgeReplayInput = combineOpenAIWSReplayItems(bridgeReplayInput, result.wsReplayInput)
				bridgeReplayInputExists = true
				s.bindCindyOpaqueContinuationAccount(
					ctx, c, account, cindyOpaqueBindingIDsFromRawItems(result.wsReplayInput),
				)
			}
			bridgeAccountFailoverInput = turnAccountFailoverInput
			bridgeAccountFailoverInputExists = turnAccountFailoverInputExists
			if len(result.wsAccountFailoverReplayInput) > 0 {
				bridgeAccountFailoverInput = combineOpenAIWSReplayItems(
					bridgeAccountFailoverInput,
					result.wsAccountFailoverReplayInput,
				)
				bridgeAccountFailoverInputExists = true
			}
			bridgeTurnState := strings.TrimSpace(result.ResponseHeaders.Get(openAIWSTurnStateHeader))
			turnState = bridgeTurnState
			s.commitOpenAIWSSessionTurnState(c, account, stateStore, groupID, sessionHash, bridgeTurnState)
			responseID := strings.TrimSpace(result.RequestID)
			if responseID != "" && stateStore != nil {
				ttl := s.openAIWSResponseStickyTTL()
				logOpenAIWSBindResponseAccountWarn(groupID, account.ID, responseID, stateStore.BindResponseAccount(ctx, groupID, responseID, account.ID, ttl))
			}
			nextClientMessage, readErr := readClientMessage()
			if readErr != nil {
				if isOpenAIWSClientDisconnectError(readErr) {
					closeStatus, closeReason := summarizeOpenAIWSReadCloseError(readErr)
					logOpenAIWSModeInfo(
						"ingress_ws_http_bridge_client_closed account_id=%d close_status=%s close_reason=%s",
						account.ID,
						closeStatus,
						truncateOpenAIWSLogValue(closeReason, openAIWSHeaderValueMaxLen),
					)
					return nil
				}
				return fmt.Errorf("read client websocket request: %w", readErr)
			}
			nextPayload, parseErr := parseClientPayload(turn+1, nextClientMessage)
			if parseErr != nil {
				return parseErr
			}
			currentBridgePayload = nextPayload
		}
	}

	// Native ingress can retry the first turn once only while no downstream
	// output has been emitted. Always hold metadata-only preamble frames so a
	// failed attempt cannot leak response IDs before that retry decision.
	nativeMatcher := (*OpenAIRefusalMatcher)(nil)
	if refusalRuntime.RewriteEnabled() {
		nativeMatcher = refusalRuntime.Matcher
	}
	refusalOutput = newOpenAIRefusalRecoveryWSOutput(
		nativeMatcher,
		true,
		func(writeCtx context.Context, messageType coderws.MessageType, payload []byte) error {
			if messageType != coderws.MessageText {
				return errors.New("unsupported websocket response message type")
			}
			return rawWriteClientMessage(payload)
		},
		func() {
			logOpenAIWSModeInfo("ingress_replay_buffer_limit account_id=%d transport=websocket", account.ID)
		},
	)
	writeClientMessage = func(message []byte) error {
		return refusalOutput.Write(ctx, coderws.MessageText, message)
	}

	firstRoutingFields := gjson.GetManyBytes(firstPayload.payloadRaw, "model", "service_tier")
	wsHeaders, _, buildHdrErr := s.buildOpenAIWSHeaders(
		ctx,
		c,
		account,
		token,
		wsDecision,
		isCodexCLI,
		turnState,
		strings.TrimSpace(c.GetHeader(openAIWSTurnMetadataHeader)),
		firstPayload.promptCacheKey,
		firstRoutingFields[0].String(),
		firstRoutingFields[1].String(),
	)
	if buildHdrErr != nil {
		return fmt.Errorf("build ws headers: %w", buildHdrErr)
	}
	turnState = strings.TrimSpace(wsHeaders.Get(openAIWSTurnStateHeader))
	baseAcquireReq := openAIWSAcquireRequest{
		Account: account,
		WSURL:   wsURL,
		Headers: wsHeaders,
		HeadersFactory: func(factoryCtx context.Context, headers http.Header) (http.Header, error) {
			return s.refreshOpenAIAgentIdentityHeaders(factoryCtx, account, headers)
		},
		ProxyURL: func() string {
			if account.ProxyID != nil && account.Proxy != nil {
				return account.Proxy.URL()
			}
			return ""
		}(),
		ForceNewConn: false,
	}
	pool := s.getOpenAIWSConnPool()
	if pool == nil {
		return errors.New("openai ws conn pool is nil")
	}

	logOpenAIWSModeInfo(
		"ingress_ws_protocol_confirm account_id=%d account_type=%s transport=%s ws_host=%s ws_path=%s ws_mode=%s store_disabled=%v has_session_hash=%v has_previous_response_id=%v",
		account.ID,
		account.Type,
		normalizeOpenAIWSLogValue(string(wsDecision.Transport)),
		wsHost,
		wsPath,
		normalizeOpenAIWSLogValue(ingressMode),
		storeDisabled,
		sessionHash != "",
		firstPayload.previousResponseID != "",
	)

	if debugEnabled {
		logOpenAIWSModeDebug(
			"ingress_ws_start account_id=%d account_type=%s transport=%s ws_host=%s preferred_conn_id=%s has_session_hash=%v has_previous_response_id=%v store_disabled=%v",
			account.ID,
			account.Type,
			normalizeOpenAIWSLogValue(string(wsDecision.Transport)),
			wsHost,
			truncateOpenAIWSLogValue(preferredConnID, openAIWSIDValueMaxLen),
			sessionHash != "",
			firstPayload.previousResponseID != "",
			storeDisabled,
		)
	}
	if firstPayload.previousResponseID != "" {
		firstPreviousResponseIDKind := ClassifyOpenAIPreviousResponseIDKind(firstPayload.previousResponseID)
		logOpenAIWSModeInfo(
			"ingress_ws_continuation_probe account_id=%d turn=%d previous_response_id=%s previous_response_id_kind=%s preferred_conn_id=%s session_hash=%s header_session_id=%s header_conversation_id=%s has_turn_state=%v turn_state_len=%d has_prompt_cache_key=%v store_disabled=%v",
			account.ID,
			1,
			truncateOpenAIWSLogValue(firstPayload.previousResponseID, openAIWSIDValueMaxLen),
			normalizeOpenAIWSLogValue(firstPreviousResponseIDKind),
			truncateOpenAIWSLogValue(preferredConnID, openAIWSIDValueMaxLen),
			truncateOpenAIWSLogValue(sessionHash, 12),
			openAIWSHeaderValueForLog(baseAcquireReq.Headers, "session_id"),
			openAIWSHeaderValueForLog(baseAcquireReq.Headers, "conversation_id"),
			turnState != "",
			len(turnState),
			firstPayload.promptCacheKey != "",
			storeDisabled,
		)
	}

	acquireTimeout := s.openAIWSAcquireTimeout()
	if acquireTimeout <= 0 {
		acquireTimeout = 30 * time.Second
	}

	agentTaskRecoveryTried := false
	initialTransportRetryUsed := false
	var acquireTurnLease func(int, string, bool) (*openAIWSConnLease, error)
	acquireTurnLease = func(turn int, preferred string, forcePreferredConn bool) (*openAIWSConnLease, error) {
		req := cloneOpenAIWSAcquireRequest(baseAcquireReq)
		req.PreferredConnID = strings.TrimSpace(preferred)
		req.ForcePreferredConn = forcePreferredConn
		// dedicated 模式下每次获取均新建连接，避免跨会话复用残留上下文。
		req.ForceNewConn = dedicatedMode || (turn == 1 && initialTransportRetryUsed)
		acquireCtx, acquireCancel := context.WithTimeout(ctx, acquireTimeout)
		lease, acquireErr := pool.Acquire(acquireCtx, req)
		acquireCancel()
		var dialErr *openAIWSDialError
		if acquireErr != nil && s.isAgentIdentityAccount(ctx, account) && errors.As(acquireErr, &dialErr) && isAgentIdentityTaskInvalidWSDialError(dialErr) && !agentTaskRecoveryTried {
			agentTaskRecoveryTried = true
			if recoveryErr := s.recoverAgentIdentityTask(ctx, account, account.GetCredential("task_id")); recoveryErr != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
			}
			return acquireTurnLease(turn, preferred, forcePreferredConn)
		}
		if acquireErr != nil {
			canonicalModel := canonicalOpenAIAccountSchedulingModel(account, ingressSessionOriginalModel)
			dialStatus, dialClass, dialCloseStatus, dialCloseReason, dialRespServer, dialRespVia, dialRespCFRay, dialRespReqID := summarizeOpenAIWSDialError(acquireErr)
			logOpenAIWSModeInfo(
				"ingress_ws_upstream_acquire_fail account_id=%d turn=%d reason=%s dial_status=%d dial_class=%s dial_close_status=%s dial_close_reason=%s dial_resp_server=%s dial_resp_via=%s dial_resp_cf_ray=%s dial_resp_x_request_id=%s cause=%s preferred_conn_id=%s force_preferred_conn=%v ws_host=%s ws_path=%s proxy_enabled=%v",
				account.ID,
				turn,
				normalizeOpenAIWSLogValue(classifyOpenAIWSAcquireError(acquireErr)),
				dialStatus,
				dialClass,
				dialCloseStatus,
				truncateOpenAIWSLogValue(dialCloseReason, openAIWSHeaderValueMaxLen),
				dialRespServer,
				dialRespVia,
				dialRespCFRay,
				dialRespReqID,
				truncateOpenAIWSLogValue(acquireErr.Error(), openAIWSLogValueMaxLen),
				truncateOpenAIWSLogValue(preferred, openAIWSIDValueMaxLen),
				forcePreferredConn,
				wsHost,
				wsPath,
				account.ProxyID != nil && account.Proxy != nil,
			)
			var dialErr *openAIWSDialError
			if errors.As(acquireErr, &dialErr) && dialErr != nil && dialErr.StatusCode == http.StatusTooManyRequests {
				s.persistOpenAIWSRateLimitSignal(ctx, account, dialErr.ResponseHeaders, nil, "rate_limit_exceeded", "rate_limit_error", strings.TrimSpace(acquireErr.Error()), canonicalModel)
				return nil, s.newOpenAIWSRateLimitFailoverError(account, dialErr.ResponseHeaders, nil, acquireErr.Error())
			}
			if errors.Is(acquireErr, errOpenAIWSPreferredConnUnavailable) {
				return nil, NewOpenAIWSClientCloseError(
					coderws.StatusPolicyViolation,
					"upstream continuation connection is unavailable; please restart the conversation",
					acquireErr,
				)
			}
			retrySameAccount, failoverErr := openAIWSInitialDialFailover(account, acquireErr)
			if failoverErr != nil && failoverErr.IsOpenAIModelNotSupported() {
				_ = s.handleOpenAIAccountUpstreamError(
					ctx,
					account,
					http.StatusBadRequest,
					failoverErr.ResponseHeaders,
					failoverErr.ResponseBody,
					canonicalModel,
				)
			}
			if failoverErr != nil {
				if turn == 1 && retrySameAccount && !initialTransportRetryUsed {
					initialTransportRetryUsed = true
					logOpenAIWSModeInfo(
						"ingress_ws_upstream_acquire_retry account_id=%d turn=%d status=%d retry=1",
						account.ID,
						turn,
						failoverErr.StatusCode,
					)
					return acquireTurnLease(turn, preferred, forcePreferredConn)
				}
				if turn > 1 {
					s.CooldownOpenAIRetryExhausted(ctx, account, canonicalModel, failoverErr)
				}
				return nil, failoverErr
			}
			if errors.Is(acquireErr, context.DeadlineExceeded) || errors.Is(acquireErr, errOpenAIWSConnQueueFull) {
				return nil, NewOpenAIWSClientCloseError(
					coderws.StatusTryAgainLater,
					"upstream websocket is busy, please retry later",
					acquireErr,
				)
			}
			return nil, acquireErr
		}
		connID := strings.TrimSpace(lease.ConnID())
		logOpenAIWSModeInfo(
			"ingress_ws_upstream_connected account_id=%d turn=%d conn_id=%s conn_reused=%v conn_pick_ms=%d queue_wait_ms=%d preferred_conn_id=%s",
			account.ID,
			turn,
			truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
			lease.Reused(),
			lease.ConnPickDuration().Milliseconds(),
			lease.QueueWaitDuration().Milliseconds(),
			truncateOpenAIWSLogValue(preferred, openAIWSIDValueMaxLen),
		)
		return lease, nil
	}

	var rejectedFieldRetryState *openAIResponsesRejectedFieldRetryState
	sendAndRelay := func(turn int, lease *openAIWSConnLease, payload []byte, payloadBytes int, originalModel string, imageBillingModel string, imageSizeTier string, imageInputSize string, requestedReasoningEffort *string) (*OpenAIForwardResult, error) {
		responseModelObserver := &upstreamResponseModelObserver{}
		if lease == nil {
			return nil, errors.New("upstream websocket lease is nil")
		}
		// A handshake state is local to this attempt until a downstream frame is
		// actually written. Draining an upstream terminal after client disconnect
		// must not claim that the client received this attempt's state.
		handshakeTurnState := strings.TrimSpace(lease.HandshakeHeader(openAIWSTurnStateHeader))
		turnStateCommitted := false
		commitTurnState := func() {
			if turnStateCommitted {
				return
			}
			turnStateCommitted = true
			turnState = handshakeTurnState
			updatedHeaders := cloneHeader(baseAcquireReq.Headers)
			if updatedHeaders == nil {
				updatedHeaders = make(http.Header)
			}
			if handshakeTurnState == "" {
				updatedHeaders.Del(openAIWSTurnStateHeader)
			} else {
				updatedHeaders.Set(openAIWSTurnStateHeader, handshakeTurnState)
			}
			baseAcquireReq.Headers = updatedHeaders
			s.commitOpenAIWSSessionTurnState(c, account, stateStore, groupID, sessionHash, handshakeTurnState)
		}
		previousCommit := commitIngressTurnState
		commitIngressTurnState = commitTurnState
		defer func() {
			commitIngressTurnState = previousCommit
		}()
		turnStart := time.Now()
		if refusalOutput != nil {
			refusalOutput.DropTurn()
		}
		wroteDownstream := false
		downstreamOutputStarted := func() bool {
			if refusalOutput != nil {
				return refusalOutput.DownstreamOutputStarted()
			}
			return wroteDownstream
		}
		payload = s.prepareCodexQuotaOverdraftBody(ctx, account, false, payload)
		if err := lease.WriteJSONWithContextTimeout(ctx, json.RawMessage(payload), s.openAIWSWriteTimeout()); err != nil {
			return nil, wrapOpenAIWSIngressTurnError(
				"write_upstream",
				fmt.Errorf("write upstream websocket request: %w", err),
				false,
			)
		}
		if debugEnabled {
			logOpenAIWSModeDebug(
				"ingress_ws_turn_request_sent account_id=%d turn=%d conn_id=%s payload_bytes=%d",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(lease.ConnID(), openAIWSIDValueMaxLen),
				payloadBytes,
			)
		}

		responseID := ""
		usage := OpenAIUsage{}
		imageCounter := newOpenAIImageOutputCounter()
		var firstTokenMs *int
		reqStream := openAIWSPayloadBoolFromRaw(payload, "stream", true)
		turnPreviousResponseID := openAIWSPayloadStringFromRaw(payload, "previous_response_id")
		turnPreviousResponseIDKind := ClassifyOpenAIPreviousResponseIDKind(turnPreviousResponseID)
		turnPromptCacheKey := openAIWSPayloadStringFromRaw(payload, "prompt_cache_key")
		turnStoreDisabled := s.isOpenAIWSStoreDisabledInRequestRaw(payload, account)
		turnHasFunctionCallOutput := openAIWSRawPayloadHasToolCallOutput(payload)
		eventCount := 0
		tokenEventCount := 0
		terminalEventCount := 0
		replayCollector := &openAIWSToolCallReplayCollector{}
		firstEventType := ""
		lastEventType := ""
		needModelReplace := false
		clientDisconnected := false
		mappedModel := ""
		var mappedModelBytes []byte
		if originalModel != "" {
			mappedModel = strings.TrimSpace(gjson.GetBytes(payload, "model").String())
			if mappedModel == "" {
				mappedModel = normalizeOpenAIModelForUpstream(account, account.GetMappedModel(originalModel))
			}
			needModelReplace = mappedModel != "" && mappedModel != originalModel
			if needModelReplace {
				mappedModelBytes = []byte(mappedModel)
			}
		}
		for {
			upstreamMessage, readErr := lease.ReadMessageWithContextTimeout(ctx, s.openAIWSReadTimeout())
			if readErr != nil {
				lease.MarkBroken()
				return nil, wrapOpenAIWSIngressTurnError(
					"read_upstream",
					fmt.Errorf("read upstream websocket event: %w", readErr),
					downstreamOutputStarted(),
				)
			}
			rawUpstreamMessage := append([]byte(nil), upstreamMessage...)
			rawEventType, _, _ := parseOpenAIWSEventEnvelope(rawUpstreamMessage)
			if rawEventType == "error" || rawEventType == "response.failed" {
				canonicalModel := canonicalOpenAIAccountSchedulingModel(account, originalModel)
				if failoverErr, ok := s.cindyBalanceTerminalFailover(
					ctx, account, lease.HandshakeHeaders(), rawUpstreamMessage, canonicalModel,
				); ok {
					lease.MarkBroken()
					replaySafe := turn == 1 && !downstreamOutputStarted()
					if refusalOutput != nil {
						refusalOutput.DropTurn()
					}
					if replaySafe {
						return nil, failoverErr
					}
					if !clientDisconnected {
						if refusalOutput != nil {
							_ = refusalOutput.WriteRetryableFailure(ctx)
						} else {
							_ = rawWriteClientMessage(OpenAIWSRetryableFailureEvent())
						}
					}
					return nil, NewOpenAIWSClientCloseError(
						coderws.StatusTryAgainLater,
						"Temporary upstream failure; please retry",
						errors.New("cindy balance exhausted after downstream output"),
					)
				}
			}
			if normalized, changed := normalizeCompletedImageGenerationStatus(upstreamMessage); changed {
				upstreamMessage = normalized
			}
			// Rewrite structured instructions before event parsing, diagnostics,
			// refusal handling, and downstream forwarding so the server-owned
			// segment cannot leak through native ingress WS frames or logs.
			upstreamMessage = s.rewriteBusinessSystemPromptJSONForRequest(c, upstreamMessage, BusinessSystemPromptProtocolResponses)

			eventType, eventResponseID, _ := parseOpenAIWSEventEnvelope(upstreamMessage)
			responseModelObserver.ObserveOpenAI(upstreamMessage, eventType)
			if responseID == "" && eventResponseID != "" {
				responseID = eventResponseID
			}
			if eventType != "" {
				eventCount++
				if firstEventType == "" {
					firstEventType = eventType
				}
				lastEventType = eventType
			}
			if openAIWSMessageShouldParseUsage(eventType, upstreamMessage) {
				parseOpenAIWSResponseUsageFromCompletedEvent(upstreamMessage, &usage)
			}
			if eventType == "error" || eventType == "response.failed" {
				markOpenAICyberPolicyEvent(c, upstreamMessage, http.StatusOK, &usage)
			}
			if eventType == "error" {
				errCodeRaw, errTypeRaw, errMsgRaw := parseOpenAIWSErrorEventFields(upstreamMessage)
				statusCode := openAIWSRejectedFieldRetryHTTPStatus(upstreamMessage)
				if !wroteDownstream && statusCode == http.StatusBadRequest && rejectedFieldRetryState != nil {
					retryBody, retryReason, changed, retryErr := normalizeOpenAIResponsesRejectedFieldRetryBody(
						statusCode,
						payload,
						upstreamMessage,
					)
					if retryErr != nil {
						return nil, fmt.Errorf("normalize websocket rejected field retry: %w", retryErr)
					}
					if changed && rejectedFieldRetryState.Allow(retryBody) {
						logOpenAIWSModeInfo(
							"ingress_ws_rejected_field_retry account_id=%d turn=%d conn_id=%s reason=%s",
							account.ID,
							turn,
							truncateOpenAIWSLogValue(lease.ConnID(), openAIWSIDValueMaxLen),
							truncateOpenAIWSLogValue(retryReason, openAIWSLogValueMaxLen),
						)
						return nil, &openAIWSRejectedFieldRetryError{
							body:   append([]byte(nil), retryBody...),
							reason: retryReason,
						}
					}
				}
				s.persistOpenAIWSRateLimitSignal(ctx, account, lease.HandshakeHeaders(), upstreamMessage, errCodeRaw, errTypeRaw, errMsgRaw, mappedModel)
				fallbackReason, _ := classifyOpenAIWSErrorEventFromRaw(errCodeRaw, errTypeRaw, errMsgRaw)
				if fallbackReason == openAIWSFallbackReasonInvalidEncryptedContent {
					// 记录被上游拒绝的密文摘要；错误照旧透传，下一轮进场时按摘要预剥离。
					if digests := collectOpenAIEncryptedContentDigestsRaw(payload); len(digests) > 0 {
						s.markOpenAIWSInvalidEncryptedContentLineage(groupID, sessionHash, digests)
						logOpenAIWSModeInfo(
							"ingress_ws_invalid_encrypted_lineage_mark account_id=%d turn=%d digests=%d",
							account.ID,
							turn,
							len(digests),
						)
					}
				}
				errCode, errType, errMessage := summarizeOpenAIWSErrorEventFieldsFromRaw(errCodeRaw, errTypeRaw, errMsgRaw)
				continuationStateError := fallbackReason == openAIWSIngressStagePreviousResponseNotFound ||
					fallbackReason == string(openAIContinuationStateErrorInvalidEncryptedContent)
				recoverablePrevNotFound := fallbackReason == openAIWSIngressStagePreviousResponseNotFound &&
					turnPreviousResponseID != "" &&
					(!turnHasFunctionCallOutput || strictCindyContinuation) &&
					s.openAIWSIngressPreviousResponseRecoveryEnabled() &&
					!downstreamOutputStarted()
				recoverableInvalidEncrypted := fallbackReason == openAIWSIngressStageInvalidEncryptedContent &&
					!turnHasFunctionCallOutput &&
					!downstreamOutputStarted()
				if recoverablePrevNotFound {
					// 可恢复场景使用非 error 关键字日志，避免被 LegacyPrintf 误判为 ERROR 级别。
					logOpenAIWSModeInfo(
						"ingress_ws_prev_response_recoverable account_id=%d turn=%d conn_id=%s idx=%d reason=%s code=%s type=%s message=%s previous_response_id=%s previous_response_id_kind=%s response_id=%s store_disabled=%v has_prompt_cache_key=%v",
						account.ID,
						turn,
						truncateOpenAIWSLogValue(lease.ConnID(), openAIWSIDValueMaxLen),
						eventCount,
						truncateOpenAIWSLogValue(fallbackReason, openAIWSLogValueMaxLen),
						errCode,
						errType,
						errMessage,
						truncateOpenAIWSLogValue(turnPreviousResponseID, openAIWSIDValueMaxLen),
						normalizeOpenAIWSLogValue(turnPreviousResponseIDKind),
						truncateOpenAIWSLogValue(responseID, openAIWSIDValueMaxLen),
						turnStoreDisabled,
						turnPromptCacheKey != "",
					)
				} else {
					logOpenAIWSModeInfo(
						"ingress_ws_error_event account_id=%d turn=%d conn_id=%s idx=%d fallback_reason=%s err_code=%s err_type=%s err_message=%s previous_response_id=%s previous_response_id_kind=%s response_id=%s store_disabled=%v has_prompt_cache_key=%v",
						account.ID,
						turn,
						truncateOpenAIWSLogValue(lease.ConnID(), openAIWSIDValueMaxLen),
						eventCount,
						truncateOpenAIWSLogValue(fallbackReason, openAIWSLogValueMaxLen),
						errCode,
						errType,
						errMessage,
						truncateOpenAIWSLogValue(turnPreviousResponseID, openAIWSIDValueMaxLen),
						normalizeOpenAIWSLogValue(turnPreviousResponseIDKind),
						truncateOpenAIWSLogValue(responseID, openAIWSIDValueMaxLen),
						turnStoreDisabled,
						turnPromptCacheKey != "",
					)
				}
				// previous_response_not_found 在 ingress 模式支持单次恢复重试：
				// 不把该 error 直接下发客户端，而是由上层去掉 previous_response_id 后重放当前 turn。
				if recoverablePrevNotFound {
					lease.MarkBroken()
					return nil, wrapOpenAIWSIngressTurnError(
						openAIWSIngressStagePreviousResponseNotFound,
						NewOpenAIContinuationStateUnavailableError(
							openAIWSErrorHTTPStatusFromRaw(errCodeRaw, errTypeRaw),
							lease.HandshakeHeaders(),
							append([]byte(nil), upstreamMessage...),
						),
						false,
					)
				}
				if recoverableInvalidEncrypted {
					lease.MarkBroken()
					return nil, wrapOpenAIWSIngressTurnError(
						openAIWSIngressStageInvalidEncryptedContent,
						NewOpenAIContinuationStateUnavailableError(
							openAIWSErrorHTTPStatusFromRaw(errCodeRaw, errTypeRaw),
							lease.HandshakeHeaders(),
							append([]byte(nil), upstreamMessage...),
						),
						false,
					)
				}
				if continuationStateError {
					lease.MarkBroken()
					return nil, NewOpenAIContinuationStateUnavailableError(
						openAIWSErrorHTTPStatusFromRaw(errCodeRaw, errTypeRaw),
						lease.HandshakeHeaders(),
						append([]byte(nil), upstreamMessage...),
					)
				}
				canonicalModel := canonicalOpenAIAccountSchedulingModel(account, originalModel)
				if IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) &&
					isOpenAIModelNotSupportedPayload(upstreamMessage) {
					// The upstream transport is already HTTP 200, but this exact
					// structured event means the selected Cindy key cannot serve the
					// requested model. Persist an account/model cooldown and expose a
					// replayable failover only before client-visible output. The outer
					// ingress handler still enforces continuation affinity.
					_ = s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusBadRequest, nil, upstreamMessage, canonicalModel)
					if !downstreamOutputStarted() {
						lease.MarkBroken()
						return nil, newOpenAIModelNotSupportedFailoverError(lease.HandshakeHeaders(), upstreamMessage)
					}
				}
				s.handleOpenAIWSErrorEventTransientFailure(ctx, account, canonicalModel, lease.HandshakeHeaders(), upstreamMessage)
				s.persistOpenAIWSRateLimitSignal(ctx, account, lease.HandshakeHeaders(), upstreamMessage, errCodeRaw, errTypeRaw, errMsgRaw)
				if !downstreamOutputStarted() && isOpenAIWSRateLimitError(errCodeRaw, errTypeRaw, errMsgRaw) {
					lease.MarkBroken()
					return nil, &UpstreamFailoverError{
						StatusCode:      http.StatusTooManyRequests,
						ResponseBody:    append([]byte(nil), upstreamMessage...),
						ResponseHeaders: cloneHeader(lease.HandshakeHeaders()),
					}
				}
			}
			isTokenEvent := isOpenAIWSTokenEvent(eventType)
			if isTokenEvent {
				tokenEventCount++
			}
			isTerminalEvent := isOpenAIWSTerminalEvent(eventType)
			if isTerminalEvent {
				terminalEventCount++
			}
			if firstTokenMs == nil && isTokenEvent {
				ms := int(time.Since(turnStart).Milliseconds())
				firstTokenMs = &ms
			}
			imageCounter.AddSSEData(upstreamMessage)

			if eventType == "response.failed" {
				continuationErr := openAIContinuationStateErrorFromFailedEvent(http.StatusOK, lease.HandshakeHeaders(), upstreamMessage)
				if continuationErr != nil {
					lease.MarkBroken()
					kind := classifyOpenAIContinuationStateError(extractOpenAISSEErrorMessage(upstreamMessage), upstreamMessage)
					if !downstreamOutputStarted() {
						switch kind {
						case openAIContinuationStateErrorPreviousResponseNotFound:
							if turnPreviousResponseID != "" && s.openAIWSIngressPreviousResponseRecoveryEnabled() {
								return nil, wrapOpenAIWSIngressTurnError(openAIWSIngressStagePreviousResponseNotFound, continuationErr, false)
							}
						case openAIContinuationStateErrorInvalidEncryptedContent:
							return nil, wrapOpenAIWSIngressTurnError(openAIWSIngressStageInvalidEncryptedContent, continuationErr, false)
						}
					}
					return nil, continuationErr
				}
				if IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) &&
					isOpenAIModelNotSupportedPayload(upstreamMessage) {
					canonicalModel := canonicalOpenAIAccountSchedulingModel(account, originalModel)
					_ = s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusBadRequest, nil, upstreamMessage, canonicalModel)
					if !downstreamOutputStarted() {
						lease.MarkBroken()
						return nil, newOpenAIModelNotSupportedFailoverError(lease.HandshakeHeaders(), upstreamMessage)
					}
				}
				if hit, code, msg := detectOpenAICyberPolicy(upstreamMessage); hit {
					MarkOpsCyberPolicy(c, CyberPolicyMark{
						Code:           code,
						Message:        msg,
						Body:           truncateString(string(upstreamMessage), 4096),
						UpstreamStatus: http.StatusOK,
						UpstreamInTok:  usage.InputTokens,
						UpstreamOutTok: usage.OutputTokens,
					})
					if refusalRuntime.CyberFailoverEnabled() {
						replaySafe := turn == 1 && (refusalOutput == nil || !refusalOutput.SemanticOutputStarted())
						if refusalOutput != nil {
							refusalOutput.DropTurn()
							if !replaySafe {
								_ = refusalOutput.WriteRetryableFailure(ctx, upstreamMessage)
							}
						}
						return nil, newOpenAIWSCyberRecoveryError(upstreamMessage, lease.HandshakeHeaders(), replaySafe)
					}
				}
			}

			if !clientDisconnected {
				if needModelReplace && len(mappedModelBytes) > 0 && openAIWSEventMayContainModel(eventType) && bytes.Contains(upstreamMessage, mappedModelBytes) {
					upstreamMessage = replaceOpenAIWSMessageModel(upstreamMessage, mappedModel, originalModel)
				}
				if openAIWSEventMayContainToolCalls(eventType) && openAIWSMessageLikelyContainsToolCalls(upstreamMessage) {
					if corrected, changed := s.toolCorrector.CorrectToolCallsInSSEBytes(upstreamMessage); changed {
						upstreamMessage = corrected
					}
				}
				replayCollector.AddEvent(eventType, upstreamMessage)
				// 客户端写出副本改写容量降载码：Codex 对 error/response.failed 中的
				// server_is_overloaded / slow_down 判致命并终止会话，改写后走客户端
				// 内置退避重试。HTTP/SSE（openai_gateway_response_handling.go）与
				// http_bridge（openai_ws_http_bridge.go）两条路径早已这么做，
				// ctx_pool 的 ingress 直写路径是唯一漏掉的一条 —— 同一个上游降载
				// 事件在这里会让会话就地终止，切到 http_bridge 却能正常退避重试。
				//
				// 必须写进独立变量而不是原地改 upstreamMessage：下面的
				// markOpenAIWSClientVisibleFailure 与 handleOpenAIWSTerminalTransientFailure
				// 仍要按未改写的原始 payload 判定账号状态，这正是
				// sanitizeOpenAICapacityShedErrorCodeForClient 注释里写明的前提。
				clientMessage := upstreamMessage
				if eventType == "error" || eventType == "response.failed" {
					if rewritten, changed := sanitizeOpenAICapacityShedErrorCodeForClient(clientMessage); changed {
						clientMessage = rewritten
					}
				}
				if err := writeClientMessage(clientMessage); err != nil {
					if isOpenAIWSClientDisconnectError(err) {
						clientDisconnected = true
						closeStatus, closeReason := summarizeOpenAIWSReadCloseError(err)
						logOpenAIWSModeInfo(
							"ingress_ws_client_disconnected_drain account_id=%d turn=%d conn_id=%s close_status=%s close_reason=%s",
							account.ID,
							turn,
							truncateOpenAIWSLogValue(lease.ConnID(), openAIWSIDValueMaxLen),
							closeStatus,
							truncateOpenAIWSLogValue(closeReason, openAIWSHeaderValueMaxLen),
						)
					} else {
						return nil, wrapOpenAIWSIngressTurnError(
							"write_client",
							fmt.Errorf("write client websocket event: %w", err),
							downstreamOutputStarted(),
						)
					}
				} else {
					wroteDownstream = downstreamOutputStarted()
				}
			}
			if isTerminalEvent {
				canonicalModel := canonicalOpenAIAccountSchedulingModel(account, originalModel)
				terminalEvent := s.handleOpenAIWSTerminalTransientFailure(ctx, account, canonicalModel, lease.HandshakeHeaders(), upstreamMessage)
				// 客户端已断连时，上游连接的 session 状态不可信，标记 broken 避免回池复用。
				if clientDisconnected {
					lease.MarkBroken()
				}
				firstTokenMsValue := -1
				if firstTokenMs != nil {
					firstTokenMsValue = *firstTokenMs
				}
				if debugEnabled {
					logOpenAIWSModeDebug(
						"ingress_ws_turn_completed account_id=%d turn=%d conn_id=%s response_id=%s duration_ms=%d events=%d token_events=%d terminal_events=%d first_event=%s last_event=%s first_token_ms=%d client_disconnected=%v",
						account.ID,
						turn,
						truncateOpenAIWSLogValue(lease.ConnID(), openAIWSIDValueMaxLen),
						truncateOpenAIWSLogValue(responseID, openAIWSIDValueMaxLen),
						time.Since(turnStart).Milliseconds(),
						eventCount,
						tokenEventCount,
						terminalEventCount,
						truncateOpenAIWSLogValue(firstEventType, openAIWSLogValueMaxLen),
						truncateOpenAIWSLogValue(lastEventType, openAIWSLogValueMaxLen),
						firstTokenMsValue,
						clientDisconnected,
					)
				}
				imageCount := imageCounter.Count()
				result := &OpenAIForwardResult{
					RequestID:                     responseID,
					Usage:                         usage,
					Model:                         originalModel,
					UpstreamModel:                 mappedModel,
					UpstreamResponseModel:         responseModelObserver.Model(),
					UpstreamResponseModelConflict: responseModelObserver.Conflict(),
					ServiceTier:                   extractOpenAIServiceTierFromBody(payload),
					ReasoningEffort:               ApplyThinkingEnabledFallback(extractOpenAIReasoningEffortFromBody(payload, mappedModel, originalModel), payload, mappedModel),
					RequestedReasoningEffort:      requestedReasoningEffort,
					Stream:                        reqStream,
					OpenAIWSMode:                  true,
					UpstreamTerminalEvent:         terminalEvent,
					ResponseHeaders:               lease.HandshakeHeaders(),
					Duration:                      time.Since(turnStart),
					FirstTokenMs:                  firstTokenMs,
				}
				if replayInput := replayCollector.Items(); len(replayInput) > 0 {
					result.wsReplayInput = replayInput
					result.wsReplayInputExists = true
				}
				if imageCount > 0 {
					result.ImageCount = imageCount
					result.ImageSize = imageSizeTier
					result.ImageInputSize = imageInputSize
					result.ImageOutputSizes = imageCounter.Sizes()
					result.BillingModel = imageBillingModel
				}
				return result, nil
			}
		}
	}

	currentPayload := firstPayload.payloadRaw
	currentOriginalModel := firstPayload.originalModel
	currentImageBillingModel := firstPayload.imageBillingModel
	currentImageSizeTier := firstPayload.imageSizeTier
	currentImageInputSize := firstPayload.imageInputSize
	currentPayloadBytes := firstPayload.payloadBytes
	currentRequestedReasoningEffort := firstPayload.requestedReasoningEffort
	isStrictAffinityTurn := func(payload []byte) bool {
		hasAnchor := strings.TrimSpace(openAIWSPayloadStringFromRaw(payload, "previous_response_id")) != ""
		return hasAnchor && (strictCindyContinuation || storeDisabled)
	}
	var sessionLease *openAIWSConnLease
	sessionConnID := ""
	pinnedSessionConnID := ""
	unpinSessionConn := func(connID string) {
		connID = strings.TrimSpace(connID)
		if connID == "" || pinnedSessionConnID != connID {
			return
		}
		pool.UnpinConn(account.ID, connID)
		pinnedSessionConnID = ""
	}
	pinSessionConn := func(connID string) {
		if !storeDisabled {
			return
		}
		connID = strings.TrimSpace(connID)
		if connID == "" || pinnedSessionConnID == connID {
			return
		}
		if pinnedSessionConnID != "" {
			pool.UnpinConn(account.ID, pinnedSessionConnID)
			pinnedSessionConnID = ""
		}
		if pool.PinConn(account.ID, connID) {
			pinnedSessionConnID = connID
		}
	}
	// lastTurnClean 标记最后一轮 sendAndRelay 是否正常完成（收到终端事件且客户端未断连）。
	// 所有异常路径（读写错误、error 事件、客户端断连）已在各自分支或上层（L3403）中 MarkBroken，
	// 因此 releaseSessionLease 中只需在非正常结束时 MarkBroken。
	lastTurnClean := false
	releaseSessionLease := func() {
		if sessionLease == nil {
			return
		}
		if !lastTurnClean {
			sessionLease.MarkBroken()
		}
		unpinSessionConn(sessionConnID)
		sessionLease.Release()
		if debugEnabled {
			logOpenAIWSModeDebug(
				"ingress_ws_upstream_released account_id=%d conn_id=%s",
				account.ID,
				truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
			)
		}
	}
	defer releaseSessionLease()

	turn := 1
	rejectedFieldRetryState = newOpenAIResponsesRejectedFieldRetryState(currentPayload)
	turnRetry := 0
	turnPrevRecoveryTried := false
	turnInvalidEncryptedRecoveryTried := false
	lastTurnFinishedAt := time.Time{}
	lastTurnResponseID := ""
	lastTurnPayload := []byte(nil)
	var lastTurnStrictState *openAIWSIngressPreviousTurnStrictState
	lastTurnReplayInput := []json.RawMessage(nil)
	lastTurnReplayInputExists := false
	currentTurnReplayInput := []json.RawMessage(nil)
	currentTurnReplayInputExists := false
	currentTurnReplayVerified := false
	skipBeforeTurn := false
	hasCurrentOrReplayFunctionCallOutput := func(payload []byte) bool {
		if openAIWSRawPayloadHasToolCallOutput(payload) {
			return true
		}
		return currentTurnReplayInputExists && openAIWSRawItemsHasFunctionCallOutput(currentTurnReplayInput)
	}
	resetSessionLease := func(markBroken bool) {
		if sessionLease == nil {
			return
		}
		if markBroken {
			sessionLease.MarkBroken()
		}
		releaseSessionLease()
		sessionLease = nil
		sessionConnID = ""
		preferredConnID = ""
	}
	prepareCindyFullReplay := func() (CindyContinuationClassification, bool) {
		candidate, classification, replayable := prepareCindyContinuationReplayPayload(
			currentPayload,
			currentTurnReplayInput,
			currentTurnReplayInputExists,
			currentTurnReplayVerified,
		)
		if !replayable {
			return classification, false
		}
		currentPayload = candidate
		currentPayloadBytes = len(candidate)
		return classification, true
	}
	recoverIngressPrevResponseNotFound := func(relayErr error, turn int, connID string) bool {
		if !isOpenAIWSIngressPreviousResponseNotFound(relayErr) {
			return false
		}
		if turnRetry >= 1 || turnPrevRecoveryTried || !s.openAIWSIngressPreviousResponseRecoveryEnabled() {
			return false
		}
		if strictCindyContinuation {
			turnPrevRecoveryTried = true
			classification, recovered := prepareCindyFullReplay()
			if !recovered || classification.Mode != CindyContinuationAnchorPlusFull {
				return false
			}
			turnRetry++
			resetSessionLease(true)
			skipBeforeTurn = true
			logOpenAIWSModeInfo(
				"ingress_ws_prev_response_recovery account_id=%d turn=%d conn_id=%s action=full_replay retry=1 continuation_mode=%s",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
				normalizeOpenAIWSLogValue(string(classification.Mode)),
			)
			return true
		}
		if turn != 1 {
			return false
		}
		if isStrictAffinityTurn(currentPayload) {
			return false
		}
		// 携带 function_call_output 的请求不能丢弃 previous_response_id：
		// 上游 API 需要 response chain 来匹配 tool_result 与之前的 tool_use，
		// 丢弃后会导致 "No tool call found for function call output" 400 错误。
		if hasCurrentOrReplayFunctionCallOutput(currentPayload) {
			return false
		}
		turnPrevRecoveryTried = true
		updatedPayload, removed, dropErr := dropPreviousResponseIDFromRawPayload(currentPayload)
		if dropErr != nil || !removed {
			reason := "not_removed"
			if dropErr != nil {
				reason = "drop_error"
			}
			logOpenAIWSModeInfo(
				"ingress_ws_prev_response_recovery_skip account_id=%d turn=%d conn_id=%s reason=%s",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
				normalizeOpenAIWSLogValue(reason),
			)
			return false
		}
		updatedWithInput, setInputErr := setOpenAIWSPayloadInputSequence(
			updatedPayload,
			currentTurnReplayInput,
			currentTurnReplayInputExists,
		)
		if setInputErr != nil {
			logOpenAIWSModeInfo(
				"ingress_ws_prev_response_recovery_skip account_id=%d turn=%d conn_id=%s reason=set_full_input_error cause=%s",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
				truncateOpenAIWSLogValue(setInputErr.Error(), openAIWSLogValueMaxLen),
			)
			return false
		}
		logOpenAIWSModeInfo(
			"ingress_ws_prev_response_recovery account_id=%d turn=%d conn_id=%s action=drop_previous_response_id retry=1",
			account.ID,
			turn,
			truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
		)
		currentPayload = updatedWithInput
		currentPayloadBytes = len(updatedWithInput)
		turnRetry++
		resetSessionLease(true)
		skipBeforeTurn = true
		return true
	}
	recoverIngressInvalidEncryptedContent := func(relayErr error, turn int, connID string) bool {
		if strictCindyContinuation {
			return false
		}
		if turn != 1 || turnRetry >= 1 || !isOpenAIWSIngressInvalidEncryptedContent(relayErr) || turnInvalidEncryptedRecoveryTried {
			return false
		}
		if isStrictAffinityTurn(currentPayload) {
			return false
		}
		if hasCurrentOrReplayFunctionCallOutput(currentPayload) {
			return false
		}
		var decoded map[string]any
		decoder := json.NewDecoder(bytes.NewReader(currentPayload))
		decoder.UseNumber()
		if decodeErr := decoder.Decode(&decoded); decodeErr != nil || !trimOpenAIEncryptedReasoningItems(decoded) {
			logOpenAIWSModeInfo(
				"ingress_ws_invalid_encrypted_recovery_skip account_id=%d turn=%d conn_id=%s reason=missing_or_invalid_encrypted_reasoning",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
			)
			return false
		}
		delete(decoded, "previous_response_id")
		updatedPayload, marshalErr := marshalOpenAIUpstreamJSON(decoded)
		if marshalErr != nil {
			logOpenAIWSModeInfo(
				"ingress_ws_invalid_encrypted_recovery_skip account_id=%d turn=%d conn_id=%s reason=serialize_failed cause=%s",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
				truncateOpenAIWSLogValue(marshalErr.Error(), openAIWSLogValueMaxLen),
			)
			return false
		}
		turnInvalidEncryptedRecoveryTried = true
		currentPayload = updatedPayload
		currentPayloadBytes = len(updatedPayload)
		turnRetry++
		resetSessionLease(true)
		skipBeforeTurn = true
		logOpenAIWSModeInfo(
			"ingress_ws_invalid_encrypted_recovery account_id=%d turn=%d conn_id=%s action=drop_encrypted_reasoning_and_previous_response_id retry=1",
			account.ID,
			turn,
			truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
		)
		return true
	}
	retryIngressTurn := func(relayErr error, turn int, connID string) bool {
		_, transportFailure := openAIWSIngressTurnTransportCause(relayErr)
		if transportFailure && initialTransportRetryUsed {
			return false
		}
		if strictCindyContinuation {
			if !transportFailure || turnRetry >= 1 {
				return false
			}
			classification, replayable := prepareCindyFullReplay()
			if !replayable {
				logOpenAIWSModeInfo(
					"ingress_ws_turn_retry_skip account_id=%d turn=%d conn_id=%s reason=continuation_not_replayable continuation_mode=%s",
					account.ID,
					turn,
					truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
					normalizeOpenAIWSLogValue(string(classification.Mode)),
				)
				return false
			}
		} else if !shouldRetryOpenAIWSIngressTurn(turn, turnRetry, relayErr) {
			return false
		}
		if !strictCindyContinuation && isStrictAffinityTurn(currentPayload) {
			logOpenAIWSModeInfo(
				"ingress_ws_turn_retry_skip account_id=%d turn=%d conn_id=%s reason=strict_affinity",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
			)
			return false
		}
		turnRetry++
		if transportFailure {
			initialTransportRetryUsed = true
		}
		logOpenAIWSModeInfo(
			"ingress_ws_turn_retry account_id=%d turn=%d retry=%d reason=%s conn_id=%s",
			account.ID,
			turn,
			turnRetry,
			truncateOpenAIWSLogValue(openAIWSIngressTurnRetryReason(relayErr), openAIWSLogValueMaxLen),
			truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
		)
		resetSessionLease(true)
		skipBeforeTurn = true
		return true
	}
	for {
		if turn > 1 && !skipBeforeTurn && hooks != nil && hooks.BeforeRequest != nil {
			if err := hooks.BeforeRequest(turn, currentPayload, currentOriginalModel); err != nil {
				return err
			}
		}
		if !skipBeforeTurn && hooks != nil && hooks.BeforeTurn != nil {
			if err := hooks.BeforeTurn(turn); err != nil {
				return err
			}
		}
		skipBeforeTurn = false
		// 剥离本会话已知失效的加密项，阻断同一失效密文随历史反复触发上游拒绝。
		// 历史序列须同步剥离，否则与已剥离的当前 input 项错位，prefix 复用失配。
		if invalidDigests := s.sessionInvalidEncryptedContentDigests(groupID, sessionHash); len(invalidDigests) > 0 {
			strippedPayload, strippedCount := s.stripSessionInvalidEncryptedContentLogged(
				currentPayload, invalidDigests, "ingress_ws_invalid_encrypted_lineage_strip", account.ID, turn,
			)
			if strippedCount > 0 {
				currentPayload = strippedPayload
				currentPayloadBytes = len(strippedPayload)
			}
			if lastTurnReplayInputExists {
				lastTurnReplayInput, _ = stripOpenAIInvalidEncryptedContentFromReplayItems(lastTurnReplayInput, invalidDigests)
			}
		}
		currentPreviousResponseID := openAIWSPayloadStringFromRaw(currentPayload, "previous_response_id")
		if strictCindyContinuation && currentPreviousResponseID != "" && turn > 1 {
			boundConnID, resolveErr := resolveStrictCindyAnchorConn(currentPreviousResponseID, sessionConnID)
			if resolveErr != nil {
				return resolveErr
			}
			preferredConnID = boundConnID
		}
		expectedPrev := strings.TrimSpace(lastTurnResponseID)
		toolSignals := ToolContinuationSignals{
			HasFunctionCallOutput: openAIWSRawPayloadHasToolCallOutput(currentPayload),
		}
		if toolSignals.HasFunctionCallOutput {
			var currentReqBody map[string]any
			if err := json.Unmarshal(currentPayload, &currentReqBody); err == nil {
				toolSignals = AnalyzeToolContinuationSignals(currentReqBody)
			}
		}
		hasFunctionCallOutput := toolSignals.HasFunctionCallOutput
		// store=false + function_call_output 场景必须有续链锚点。
		// 若客户端未传 previous_response_id，优先回填上一轮响应 ID，避免上游报 call_id 无法关联。
		if shouldInferIngressFunctionCallOutputPreviousResponseID(
			storeDisabled,
			turn,
			toolSignals,
			currentPreviousResponseID,
			expectedPrev,
		) {
			updatedPayload, setPrevErr := setPreviousResponseIDToRawPayload(currentPayload, expectedPrev)
			if setPrevErr != nil {
				logOpenAIWSModeInfo(
					"ingress_ws_function_call_output_prev_infer_skip account_id=%d turn=%d conn_id=%s reason=set_previous_response_id_error cause=%s expected_previous_response_id=%s",
					account.ID,
					turn,
					truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
					truncateOpenAIWSLogValue(setPrevErr.Error(), openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
				)
			} else {
				currentPayload = updatedPayload
				currentPayloadBytes = len(updatedPayload)
				currentPreviousResponseID = expectedPrev
				logOpenAIWSModeInfo(
					"ingress_ws_function_call_output_prev_infer account_id=%d turn=%d conn_id=%s action=set_previous_response_id previous_response_id=%s",
					account.ID,
					turn,
					truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
					truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
				)
			}
		}
		nextReplayInput, nextReplayInputExists, replayInputErr := buildOpenAIWSReplayInputSequence(
			lastTurnReplayInput,
			lastTurnReplayInputExists,
			currentPayload,
			currentPreviousResponseID != "",
		)
		if replayInputErr != nil {
			logOpenAIWSModeInfo(
				"ingress_ws_replay_input_skip account_id=%d turn=%d conn_id=%s reason=build_error cause=%s",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
				truncateOpenAIWSLogValue(replayInputErr.Error(), openAIWSLogValueMaxLen),
			)
			currentTurnReplayInput = nil
			currentTurnReplayInputExists = false
			currentTurnReplayVerified = false
		} else {
			currentTurnReplayInput = nextReplayInput
			currentTurnReplayInputExists = nextReplayInputExists
			// Anchored history is verified only when this connection accumulated
			// the exact prior full-input baseline; item count is not proof.
			currentTurnReplayVerified = verifiedCindyContinuationHistory(
				currentPreviousResponseID,
				lastTurnResponseID,
				lastTurnReplayInputExists,
			)
		}
		replayHasFunctionCallOutput := currentTurnReplayInputExists &&
			openAIWSRawItemsHasFunctionCallOutput(currentTurnReplayInput)
		hasFunctionCallOutput = hasFunctionCallOutput || replayHasFunctionCallOutput
		if storeDisabled && turn > 1 && currentPreviousResponseID != "" {
			shouldKeepPreviousResponseID := false
			strictReason := ""
			var strictErr error
			if strictCindyContinuation && hasFunctionCallOutput &&
				strings.TrimSpace(currentPreviousResponseID) != expectedPrev {
				strictReason = "previous_response_id_mismatch"
			} else if lastTurnStrictState != nil {
				shouldKeepPreviousResponseID, strictReason, strictErr = shouldKeepIngressPreviousResponseIDWithStrictState(
					lastTurnStrictState,
					currentPayload,
					lastTurnResponseID,
					hasFunctionCallOutput,
				)
			} else {
				shouldKeepPreviousResponseID, strictReason, strictErr = shouldKeepIngressPreviousResponseID(
					lastTurnPayload,
					currentPayload,
					lastTurnResponseID,
					hasFunctionCallOutput,
				)
			}
			if strictErr != nil {
				logOpenAIWSModeInfo(
					"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=keep_previous_response_id reason=%s cause=%s previous_response_id=%s expected_previous_response_id=%s has_function_call_output=%v",
					account.ID,
					turn,
					truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
					normalizeOpenAIWSLogValue(strictReason),
					truncateOpenAIWSLogValue(strictErr.Error(), openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(currentPreviousResponseID, openAIWSIDValueMaxLen),
					truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
					hasFunctionCallOutput,
				)
			} else if !shouldKeepPreviousResponseID {
				if strictCindyContinuation {
					classification, replayable := prepareCindyFullReplay()
					if !replayable || classification.Mode != CindyContinuationAnchorPlusFull {
						return NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
					}
					logOpenAIWSModeInfo(
						"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=full_replay reason=%s continuation_mode=%s",
						account.ID,
						turn,
						truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
						normalizeOpenAIWSLogValue(strictReason),
						normalizeOpenAIWSLogValue(string(classification.Mode)),
					)
					currentPreviousResponseID = ""
				} else {
					updatedPayload, removed, dropErr := dropPreviousResponseIDFromRawPayload(currentPayload)
					if dropErr != nil || !removed {
						dropReason := "not_removed"
						if dropErr != nil {
							dropReason = "drop_error"
						}
						logOpenAIWSModeInfo(
							"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=keep_previous_response_id reason=%s drop_reason=%s previous_response_id=%s expected_previous_response_id=%s has_function_call_output=%v",
							account.ID,
							turn,
							truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
							normalizeOpenAIWSLogValue(strictReason),
							normalizeOpenAIWSLogValue(dropReason),
							truncateOpenAIWSLogValue(currentPreviousResponseID, openAIWSIDValueMaxLen),
							truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
							hasFunctionCallOutput,
						)
					} else {
						updatedWithInput, setInputErr := setOpenAIWSPayloadInputSequence(
							updatedPayload,
							currentTurnReplayInput,
							currentTurnReplayInputExists,
						)
						if setInputErr != nil {
							logOpenAIWSModeInfo(
								"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=keep_previous_response_id reason=%s drop_reason=set_full_input_error previous_response_id=%s expected_previous_response_id=%s cause=%s has_function_call_output=%v",
								account.ID,
								turn,
								truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
								normalizeOpenAIWSLogValue(strictReason),
								truncateOpenAIWSLogValue(currentPreviousResponseID, openAIWSIDValueMaxLen),
								truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
								truncateOpenAIWSLogValue(setInputErr.Error(), openAIWSLogValueMaxLen),
								hasFunctionCallOutput,
							)
						} else {
							currentPayload = updatedWithInput
							currentPayloadBytes = len(updatedWithInput)
							logOpenAIWSModeInfo(
								"ingress_ws_prev_response_strict_eval account_id=%d turn=%d conn_id=%s action=drop_previous_response_id_full_create reason=%s previous_response_id=%s expected_previous_response_id=%s has_function_call_output=%v",
								account.ID,
								turn,
								truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
								normalizeOpenAIWSLogValue(strictReason),
								truncateOpenAIWSLogValue(currentPreviousResponseID, openAIWSIDValueMaxLen),
								truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
								hasFunctionCallOutput,
							)
							currentPreviousResponseID = ""
						}
					}
				}
			}
		}
		forcePreferredConn := isStrictAffinityTurn(currentPayload) && strings.TrimSpace(preferredConnID) != ""
		if sessionLease == nil {
			acquiredLease, acquireErr := acquireTurnLease(turn, preferredConnID, forcePreferredConn)
			if acquireErr != nil {
				return normalizeOpenAIWSNonInitialTurnError(turn, fmt.Errorf("acquire upstream websocket: %w", acquireErr))
			}
			sessionLease = acquiredLease
			sessionConnID = strings.TrimSpace(sessionLease.ConnID())
			if storeDisabled {
				pinSessionConn(sessionConnID)
			} else {
				unpinSessionConn(sessionConnID)
			}
		}
		shouldPreflightPing := turn > 1 && sessionLease != nil && sessionLease.SupportsIdlePingWithoutReader() && turnRetry == 0
		if shouldPreflightPing && openAIWSIngressPreflightPingIdle > 0 && !lastTurnFinishedAt.IsZero() {
			if time.Since(lastTurnFinishedAt) < openAIWSIngressPreflightPingIdle {
				shouldPreflightPing = false
			}
		}
		if shouldPreflightPing {
			if pingErr := sessionLease.PingWithTimeout(openAIWSConnHealthCheckTO); pingErr != nil {
				logOpenAIWSModeInfo(
					"ingress_ws_upstream_preflight_ping_fail account_id=%d turn=%d conn_id=%s cause=%s",
					account.ID,
					turn,
					truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
					truncateOpenAIWSLogValue(pingErr.Error(), openAIWSLogValueMaxLen),
				)
				resetSessionLease(true)
				return NewOpenAIWSClientCloseError(
					coderws.StatusTryAgainLater,
					openAIWSNonInitialTurnRetryCloseReason,
					fmt.Errorf("upstream websocket preflight ping failed: %w", pingErr),
				)
			}
		}
		connID := sessionConnID
		if currentPreviousResponseID != "" {
			chainedFromLast := expectedPrev != "" && currentPreviousResponseID == expectedPrev
			currentPreviousResponseIDKind := ClassifyOpenAIPreviousResponseIDKind(currentPreviousResponseID)
			logOpenAIWSModeInfo(
				"ingress_ws_turn_chain account_id=%d turn=%d conn_id=%s previous_response_id=%s previous_response_id_kind=%s last_turn_response_id=%s chained_from_last=%v preferred_conn_id=%s header_session_id=%s header_conversation_id=%s has_turn_state=%v turn_state_len=%d has_prompt_cache_key=%v store_disabled=%v",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
				truncateOpenAIWSLogValue(currentPreviousResponseID, openAIWSIDValueMaxLen),
				normalizeOpenAIWSLogValue(currentPreviousResponseIDKind),
				truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
				chainedFromLast,
				truncateOpenAIWSLogValue(preferredConnID, openAIWSIDValueMaxLen),
				openAIWSHeaderValueForLog(baseAcquireReq.Headers, "session_id"),
				openAIWSHeaderValueForLog(baseAcquireReq.Headers, "conversation_id"),
				turnState != "",
				len(turnState),
				openAIWSPayloadStringFromRaw(currentPayload, "prompt_cache_key") != "",
				storeDisabled,
			)
		}

		result, relayErr := sendAndRelay(turn, sessionLease, currentPayload, currentPayloadBytes, currentOriginalModel, currentImageBillingModel, currentImageSizeTier, currentImageInputSize, currentRequestedReasoningEffort)
		if relayErr != nil {
			lastTurnClean = false
			if recoverIngressInvalidEncryptedContent(relayErr, turn, connID) {
				continue
			}
			if recoverIngressPrevResponseNotFound(relayErr, turn, connID) {
				continue
			}
			if retryIngressTurn(relayErr, turn, connID) {
				continue
			}
			finalErr := relayErr
			transportCause, transportFailure := openAIWSIngressTurnTransportCause(relayErr)
			if transportFailure {
				transportErr := s.handleOpenAIUpstreamTransportError(ctx, c, account, transportCause, false)
				if turn == 1 {
					finalErr = transportErr
				} else {
					var failoverErr *UpstreamFailoverError
					if errors.As(transportErr, &failoverErr) {
						s.CooldownOpenAIRetryExhausted(ctx, account, canonicalOpenAIAccountSchedulingModel(account, currentOriginalModel), failoverErr)
					}
					if unwrapped := errors.Unwrap(relayErr); unwrapped != nil {
						finalErr = unwrapped
					}
				}
			} else if unwrapped := errors.Unwrap(relayErr); unwrapped != nil {
				finalErr = unwrapped
			}
			finalErr = normalizeOpenAIWSNonInitialTurnError(turn, finalErr)
			if hooks != nil && hooks.AfterTurn != nil {
				hooks.AfterTurn(turn, nil, finalErr)
			}
			sessionLease.MarkBroken()
			return finalErr
		}
		turnRetry = 0
		turnPrevRecoveryTried = false
		turnInvalidEncryptedRecoveryTried = false
		lastTurnFinishedAt = time.Now()
		lastTurnClean = true
		if hooks != nil && hooks.AfterTurn != nil {
			hooks.AfterTurn(turn, result, nil)
		}
		if result == nil {
			return errors.New("websocket turn result is nil")
		}
		responseID := strings.TrimSpace(result.RequestID)
		lastTurnResponseID = responseID
		// 正文共享：currentPayload/currentTurnReplayInput 均不可变，历史直接引用；
		// collector 增量经 combine 合并（新头数组）。
		lastTurnReplayInput = currentTurnReplayInput
		lastTurnReplayInputExists = currentTurnReplayInputExists
		if result.wsReplayInputExists {
			lastTurnReplayInput = combineOpenAIWSReplayItems(lastTurnReplayInput, result.wsReplayInput)
			lastTurnReplayInputExists = true
			s.bindCindyOpaqueContinuationAccount(
				ctx, c, account, cindyOpaqueBindingIDsFromRawItems(result.wsReplayInput),
			)
		}
		nextStrictState, strictStateErr := buildOpenAIWSIngressPreviousTurnStrictState(currentPayload)
		if strictStateErr != nil {
			lastTurnStrictState = nil
			// strict 状态不可用时保留整份上一轮 payload 供慢路径比较。
			lastTurnPayload = currentPayload
			logOpenAIWSModeInfo(
				"ingress_ws_prev_response_strict_state_skip account_id=%d turn=%d conn_id=%s reason=build_error cause=%s",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
				truncateOpenAIWSLogValue(strictStateErr.Error(), openAIWSLogValueMaxLen),
			)
		} else {
			lastTurnStrictState = nextStrictState
			lastTurnPayload = nil
		}

		if responseID != "" && stateStore != nil {
			ttl := s.openAIWSResponseStickyTTL()
			logOpenAIWSBindResponseAccountWarn(groupID, account.ID, responseID, stateStore.BindResponseAccount(ctx, groupID, responseID, account.ID, ttl))
			stateStore.BindResponseConn(responseID, connID, ttl)
		}
		if stateStore != nil && storeDisabled && sessionHash != "" {
			stateStore.BindSessionConn(groupID, sessionHash, connID, s.openAIWSSessionStickyTTL())
		}
		if connID != "" {
			preferredConnID = connID
		}

		nextClientMessage, readErr := readClientMessage()
		if readErr != nil {
			if isOpenAIWSClientDisconnectError(readErr) {
				closeStatus, closeReason := summarizeOpenAIWSReadCloseError(readErr)
				logOpenAIWSModeInfo(
					"ingress_ws_client_closed account_id=%d conn_id=%s close_status=%s close_reason=%s",
					account.ID,
					truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
					closeStatus,
					truncateOpenAIWSLogValue(closeReason, openAIWSHeaderValueMaxLen),
				)
				return nil
			}
			return fmt.Errorf("read client websocket request: %w", readErr)
		}

		nextPayload, parseErr := parseClientPayload(turn+1, nextClientMessage)
		if parseErr != nil {
			return parseErr
		}
		nextRoutingFields := gjson.GetManyBytes(nextPayload.payloadRaw, "model", "service_tier")
		if nextPayload.promptCacheKey != "" {
			// ingress 会话在整个客户端 WS 生命周期内复用同一上游连接；
			// prompt_cache_key 对握手头的更新仅在未来需要重新建连时生效。
			updatedHeaders, _, updHdrErr := s.buildOpenAIWSHeaders(
				ctx,
				c,
				account,
				token,
				wsDecision,
				isCodexCLI,
				turnState,
				strings.TrimSpace(c.GetHeader(openAIWSTurnMetadataHeader)),
				nextPayload.promptCacheKey,
				nextRoutingFields[0].String(),
				nextRoutingFields[1].String(),
			)
			if updHdrErr != nil {
				logOpenAIWSModeInfo("ingress_ws_update_headers_failed account_id=%d err=%v", account.ID, updHdrErr)
			} else {
				baseAcquireReq.Headers = updatedHeaders
			}
		}
		setOpenAICodexRoutingHint(baseAcquireReq.Headers, account, nextRoutingFields[0].String(), nextRoutingFields[1].String())
		if nextPayload.previousResponseID != "" {
			expectedPrev := strings.TrimSpace(lastTurnResponseID)
			chainedFromLast := expectedPrev != "" && nextPayload.previousResponseID == expectedPrev
			nextPreviousResponseIDKind := ClassifyOpenAIPreviousResponseIDKind(nextPayload.previousResponseID)
			logOpenAIWSModeInfo(
				"ingress_ws_next_turn_chain account_id=%d turn=%d next_turn=%d conn_id=%s previous_response_id=%s previous_response_id_kind=%s last_turn_response_id=%s chained_from_last=%v has_prompt_cache_key=%v store_disabled=%v",
				account.ID,
				turn,
				turn+1,
				truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
				truncateOpenAIWSLogValue(nextPayload.previousResponseID, openAIWSIDValueMaxLen),
				normalizeOpenAIWSLogValue(nextPreviousResponseIDKind),
				truncateOpenAIWSLogValue(expectedPrev, openAIWSIDValueMaxLen),
				chainedFromLast,
				nextPayload.promptCacheKey != "",
				storeDisabled,
			)
		}
		if nextPayload.previousResponseID != "" {
			if strictCindyContinuation {
				boundConnID, resolveErr := resolveStrictCindyAnchorConn(nextPayload.previousResponseID, sessionConnID)
				if resolveErr != nil {
					return resolveErr
				}
				preferredConnID = boundConnID
			} else if stateStore != nil {
				if stickyConnID, ok := stateStore.GetResponseConn(nextPayload.previousResponseID); ok {
					if sessionConnID != "" && stickyConnID != "" && stickyConnID != sessionConnID {
						logOpenAIWSModeInfo(
							"ingress_ws_keep_session_conn account_id=%d turn=%d conn_id=%s sticky_conn_id=%s previous_response_id=%s",
							account.ID,
							turn,
							truncateOpenAIWSLogValue(sessionConnID, openAIWSIDValueMaxLen),
							truncateOpenAIWSLogValue(stickyConnID, openAIWSIDValueMaxLen),
							truncateOpenAIWSLogValue(nextPayload.previousResponseID, openAIWSIDValueMaxLen),
						)
					} else {
						preferredConnID = stickyConnID
					}
				}
			}
		}
		currentPayload = nextPayload.payloadRaw
		currentOriginalModel = nextPayload.originalModel
		currentImageBillingModel = nextPayload.imageBillingModel
		currentImageSizeTier = nextPayload.imageSizeTier
		currentImageInputSize = nextPayload.imageInputSize
		currentPayloadBytes = nextPayload.payloadBytes
		currentRequestedReasoningEffort = nextPayload.requestedReasoningEffort
		rejectedFieldRetryState = newOpenAIResponsesRejectedFieldRetryState(currentPayload)
		storeDisabled = s.isOpenAIWSStoreDisabledInRequestRaw(currentPayload, account)
		if !storeDisabled {
			unpinSessionConn(sessionConnID)
		}
		turn++
	}
}
