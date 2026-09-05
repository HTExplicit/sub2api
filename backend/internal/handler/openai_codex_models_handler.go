package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexModels serves the Codex models manifest for Codex clients.
//
// Codex CLI and the Codex desktop app refresh their model picker from
// GET {base_url}/models?client_version=... (custom provider mode) or
// GET /backend-api/codex/models (chatgpt_base_url mode). Both routes land
// here. Groups with explicit account model mappings are generated locally;
// otherwise ChatGPT manifests are proxied and custom API key manifests receive
// provider-compatibility normalization plus short-lived caching.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	if c.Request.Context().Err() != nil {
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformCindy && apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for Cindy, OpenAI, and Composite groups")
		return
	}
	cindyScope, err := h.gatewayService.ResolveCindyCodexModelsScope(c.Request.Context(), apiKey.Group)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "Cindy Codex models catalog is temporarily unavailable")
		return
	}
	if cindyScope.CatalogOnly || cindyScope.MergeCatalog {
		if err := service.ValidateCindyCodexClientVersion(c.Query("client_version")); err != nil {
			h.errorResponse(c, http.StatusUpgradeRequired, "invalid_request_error", err.Error())
			return
		}
	}
	if cindyScope.CatalogOnly {
		manifest, buildErr := service.BuildCindyCodexModelsManifest(c.GetHeader("If-None-Match"))
		if buildErr != nil {
			h.errorResponse(c, http.StatusInternalServerError, "upstream_error", "Failed to build Codex models manifest")
			return
		}
		writeCodexModelsManifestResponse(c, manifest)
		return
	}

	ifNoneMatch := c.GetHeader("If-None-Match")
	if !cindyScope.MergeCatalog {
		configuredManifest, configured, configuredErr := h.gatewayService.BuildGroupConfiguredCodexModelsManifest(
			c.Request.Context(),
			apiKey.Group,
			ifNoneMatch,
		)
		if configuredErr != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
			return
		}
		if configured {
			writeCodexModelsManifestResponse(c, configuredManifest)
			return
		}
	}

	// 固定账号分支：开启后只用选定账号拉取 manifest，不经过调度器；
	// 全部不可用/全部失败时按 FallbackToScheduler 决定回退调度器或返回错误。
	if apiKey.Group.Platform == service.PlatformOpenAI &&
		apiKey.Group.CodexModelsManifestConfig.Enabled &&
		len(apiKey.Group.CodexModelsManifestConfig.AccountIDs) > 0 {
		pinnedManifest, pinnedAccount, pinnedErr := h.gatewayService.FetchPinnedCodexModelsManifest(
			c.Request.Context(),
			apiKey.Group,
			c.Query("client_version"),
		)
		if pinnedErr != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if !apiKey.Group.CodexModelsManifestConfig.FallbackToScheduler {
				if errors.Is(pinnedErr, service.ErrNoPinnedCodexModelsAccounts) {
					h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available pinned OpenAI accounts")
					return
				}
				h.errorResponse(c, infraerrors.Code(pinnedErr), "upstream_error", infraerrors.Message(pinnedErr))
				return
			}
			// 回退开启：跌入下方调度器循环。
		} else {
			// 让 ops 错误日志携带实际拉取成功的首个固定账号。
			setOpsSelectedAccount(c, pinnedAccount.ID, pinnedAccount.Platform)
			if err := h.gatewayService.MergeGroupConfiguredCodexModels(c.Request.Context(), apiKey.Group, pinnedManifest, ifNoneMatch); err != nil {
				h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
				return
			}
			if c.Request.Context().Err() != nil {
				return
			}
			writeCodexModelsManifestResponse(c, pinnedManifest)
			return
		}
	}

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	failedAccountIDs := make(map[int64]struct{})
	for _, accountID := range cindyScope.ExcludedAccountIDs {
		failedAccountIDs[accountID] = struct{}{}
	}
	mixedOrdinaryIndex := 0
	advanceMixedOrdinaryAccount := func() bool {
		if !cindyScope.MergeCatalog || mixedOrdinaryIndex+1 >= len(cindyScope.OrdinaryAccountIDs) {
			return false
		}
		mixedOrdinaryIndex++
		delete(failedAccountIDs, cindyScope.OrdinaryAccountIDs[mixedOrdinaryIndex])
		return true
	}
	switchCount := 0
	var lastUpstreamErr error
	var lastFailoverErr *service.UpstreamFailoverError
	retryState := newOpenAIFailoverRetryState()
	var sameAccountRetrySelection *service.AccountSelectionResult
	var oauth429FailoverState service.OpenAIOAuth429FailoverState

	for {
		retryingSameAccount := sameAccountRetrySelection != nil
		var selection *service.AccountSelectionResult
		var err error
		if retryingSameAccount {
			selection, err = h.gatewayService.ReacquireOpenAISameAccountSelection(c.Request.Context(), sameAccountRetrySelection)
			sameAccountRetrySelection = nil
		} else {
			selection, _, err = h.gatewayService.SelectAccountWithSchedulerForCapability(
				c.Request.Context(), apiKey.GroupID, "", "", "", failedAccountIDs,
				service.OpenAIUpstreamTransportHTTPSSE,
				service.OpenAIEndpointCapabilityChatCompletions,
				false, false, false,
			)
		}
		if err != nil {
			if c.Request.Context().Err() != nil {
				h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
				return
			}
			if lastUpstreamErr == nil && advanceMixedOrdinaryAccount() {
				continue
			}
			if retryingSameAccount && lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			return
		}
		if selection == nil || selection.Account == nil {
			if advanceMixedOrdinaryAccount() {
				continue
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
			} else {
				h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			}
			return
		}
		account := selection.Account
		if cindyScope.MergeCatalog && len(cindyScope.OrdinaryAccountIDs) > 0 &&
			account.ID != cindyScope.OrdinaryAccountIDs[mixedOrdinaryIndex] {
			failedAccountIDs[account.ID] = struct{}{}
			h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
			continue
		}
		// 让 ops 错误日志携带实际选中的上游账号，便于定位失效账号（#4544）。
		setOpsSelectedAccount(c, account.ID, account.Platform)
		var accountReleaseFunc func()
		var slotResult openAISlotAcquireResult
		if retryingSameAccount {
			accountReleaseFunc, slotResult = h.acquireResponsesAccountSlotForSameAccountRetry(c, apiKey.GroupID, "", selection, false, new(bool), zap.NewNop())
		} else {
			accountReleaseFunc, slotResult = h.acquireResponsesAccountSlot(c, apiKey.GroupID, "", selection, false, new(bool), zap.NewNop())
		}
		if slotResult != openAISlotAcquireOK {
			if retryingSameAccount && lastFailoverErr != nil && h.failoverAfterSameAccountSlotFailure(
				c, account, "", lastFailoverErr, failedAccountIDs,
				&switchCount, maxAccountSwitches, &oauth429FailoverState, false,
				"codex_models", nil, true,
				func() {
					h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				},
			) {
				advanceMixedOrdinaryAccount()
				continue
			}
			return
		}

		// The client ETag represents the final group-specific body rather than
		// the source manifest, so always fetch a complete source before applying
		// Cindy merging, provider normalization, or group-local filtering.
		upstreamIfNoneMatch := ""
		manifest, err := h.gatewayService.FetchCodexModelsManifest(c.Request.Context(), account, c.Query("client_version"), upstreamIfNoneMatch)
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}
		if err != nil {
			if c.Request.Context().Err() != nil {
				h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
				return
			}
			failoverErr := service.NormalizeCodexModelsManifestFailoverError(err, account)
			if failoverErr != nil {
				lastUpstreamErr = err
				lastFailoverErr = failoverErr
				retryAction := retryState.Handle(c.Request.Context(), h.gatewayService, account, "", failoverErr, true, sameAccountRetryDelay, "codex_models")
				finalizeOpenAIFailoverSelection(h.gatewayService, selection, account, "", failoverErr, retryAction)
				switch retryAction {
				case openAIFailoverRetrySameAccount:
					sameAccountRetrySelection = selection
					continue
				case openAIFailoverRetryCanceled:
					return
				case openAIFailoverRetryStop:
					h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
					return
				}
				failedAccountIDs[account.ID] = struct{}{}
				if switchCount >= maxAccountSwitches {
					h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
					return
				}
				switchCount++
				if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
					h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
					return
				}
				h.gatewayService.RecordOpenAIAccountSwitch()
				advanceMixedOrdinaryAccount()
				continue
			}
			h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
		if err := h.gatewayService.CompleteAPIKeyCodexModelsManifestForClient(manifest, account); err != nil {
			h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to complete Codex models manifest")
			return
		}
		if cindyScope.MergeCatalog {
			manifest, err = service.MergeCindyCodexModelsManifest(manifest, ifNoneMatch)
			if err != nil {
				h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to merge Codex models manifest")
				return
			}
		} else if err := h.gatewayService.MergeGroupConfiguredCodexModelsForAccount(
			c.Request.Context(), apiKey.Group, manifest, ifNoneMatch, account,
		); err != nil {
			h.gatewayService.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
			return
		}
		h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, "", true, nil)
		if c.Request.Context().Err() != nil {
			return
		}

		writeCodexModelsManifestResponse(c, manifest)
		return
	}
}

func writeCodexModelsManifestResponse(c *gin.Context, manifest *service.CodexModelsManifest) {
	if manifest.ETag != "" {
		c.Header("ETag", manifest.ETag)
	}
	if manifest.NotModified {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Data(http.StatusOK, "application/json", manifest.Body)
}
