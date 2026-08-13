package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAIRetryCooldowner interface {
	CooldownOpenAIRetryExhausted(context.Context, *service.Account, string, *service.UpstreamFailoverError)
}

// failoverAfterSameAccountSlotFailure converts an exact-account reacquisition
// timeout into the same breaker-and-switch transition used after an exhausted
// upstream replay.  It returns true only when the caller may safely re-enter
// its account-selection loop.
func (h *OpenAIGatewayHandler) failoverAfterSameAccountSlotFailure(
	c *gin.Context,
	account *service.Account,
	canonicalModel string,
	failoverErr *service.UpstreamFailoverError,
	failedAccountIDs map[int64]struct{},
	switchCount *int,
	maxAccountSwitches int,
	oauth429State *service.OpenAIOAuth429FailoverState,
	streamStarted bool,
	logScope string,
	reqLog *zap.Logger,
	allowAccountSwitch bool,
	onExhausted func(),
) bool {
	if account == nil || failoverErr == nil || c == nil {
		return false
	}
	if failoverClientGone(c) || openAIResponseHasSemanticWrite(c) {
		return false
	}
	h.gatewayService.CooldownOpenAIRetryExhausted(c.Request.Context(), account, canonicalModel, failoverErr)
	if failedAccountIDs != nil {
		failedAccountIDs[account.ID] = struct{}{}
	}
	if !allowAccountSwitch {
		if onExhausted != nil {
			onExhausted()
		}
		return false
	}
	if switchCount == nil {
		return false
	}
	if *switchCount >= maxAccountSwitches {
		if onExhausted != nil {
			onExhausted()
		}
		return false
	}
	*switchCount++
	if oauth429State != nil && h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, *switchCount, oauth429State) {
		if onExhausted != nil {
			onExhausted()
		}
		return false
	}
	h.gatewayService.RecordOpenAIAccountSwitch()
	if reqLog != nil {
		reqLog.Warn("openai.same_account_reacquire_failed_switching",
			zap.String("scope", logScope),
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("switch_count", *switchCount),
		)
	}
	return true
}

func openAIResponseHasSemanticWrite(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if service.IsResponseCommitted(c) {
		return true
	}
	return service.OpenAICompactKeepaliveAdjustedWrittenSize(c) >= 0 &&
		service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) >= 0
}

type openAIFailoverRetryAction uint8

const (
	openAIFailoverRetrySwitchAccount openAIFailoverRetryAction = iota
	openAIFailoverRetrySameAccount
	openAIFailoverRetryCanceled
	openAIFailoverRetryStop
)

type openAIFailoverRetryState struct {
	sameAccountRetryCount map[int64]int
}

func newOpenAIFailoverRetryState() *openAIFailoverRetryState {
	return &openAIFailoverRetryState{sameAccountRetryCount: make(map[int64]int)}
}

// openAISameAccountRetryLimit keeps the official bounded transient retry for
// OAuth-style accounts. API keys use only their explicit pool-mode policy.
func openAISameAccountRetryLimit(account *service.Account, failoverErr *service.UpstreamFailoverError, allowTransportRetry bool) int {
	if account == nil || failoverErr == nil || !failoverErr.ShouldRetryNextAccount() {
		return 0
	}
	if service.IsCindyBalanceInsufficientResponse(account, failoverErr.StatusCode, failoverErr.ResponseBody) {
		return 0
	}
	if account.Type == service.AccountTypeAPIKey {
		if !account.IsPoolMode() || failoverErr.StatusCode <= 0 ||
			!account.IsPoolModeRetryableStatus(failoverErr.StatusCode) ||
			!failoverErr.RetryableOnSameAccount {
			return 0
		}
		return account.GetPoolModeRetryCount()
	}

	switch failoverErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return 0
	}
	switch failoverErr.Reason {
	case service.OpenAIPersistentTransportFailureReason, service.OpenAITransientTransportFailureReason:
		if allowTransportRetry {
			return 1
		}
		return 0
	}
	if failoverErr.StatusCode == http.StatusRequestTimeout || failoverErr.StatusCode >= http.StatusInternalServerError {
		return 1
	}
	return 0
}

func finalizeOpenAIFailoverSelection(
	gateway *service.OpenAIGatewayService,
	selection *service.AccountSelectionResult,
	account *service.Account,
	model string,
	failoverErr *service.UpstreamFailoverError,
	action openAIFailoverRetryAction,
) {
	if gateway == nil || selection == nil || account == nil || failoverErr == nil {
		return
	}
	if action == openAIFailoverRetrySameAccount {
		if failoverErr.ShouldReportAccountScheduleFailure() {
			gateway.ReportOpenAIAccountSameAccountRetry(selection, account.ID, model)
		}
		return
	}
	if failoverErr.ShouldReportAccountScheduleFailure() {
		gateway.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, model, false, nil)
		return
	}
	gateway.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection)
}

func (s *openAIFailoverRetryState) Handle(
	ctx context.Context,
	cooldowner openAIRetryCooldowner,
	account *service.Account,
	canonicalModel string,
	failoverErr *service.UpstreamFailoverError,
	allowTransportRetry bool,
	retryDelay time.Duration,
	logScope string,
) openAIFailoverRetryAction {
	if failoverErr == nil || !failoverErr.ShouldRetryNextAccount() {
		return openAIFailoverRetryStop
	}
	if s == nil {
		if cooldowner != nil {
			cooldowner.CooldownOpenAIRetryExhausted(ctx, account, canonicalModel, failoverErr)
		}
		return openAIFailoverRetrySwitchAccount
	}
	if s.sameAccountRetryCount == nil {
		s.sameAccountRetryCount = make(map[int64]int)
	}

	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	retryLimit := openAISameAccountRetryLimit(account, failoverErr, allowTransportRetry)
	if s.sameAccountRetryCount[accountID] < retryLimit {
		s.sameAccountRetryCount[accountID]++
		if retryDelay > 0 {
			retryDelay = sameAccountRetryDelayFor(failoverErr, s.sameAccountRetryCount[accountID])
		}
		logger.FromContext(ctx).Warn("openai.same_account_retry",
			zap.String("scope", logScope),
			zap.Int64("account_id", accountID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("retry_limit", retryLimit),
			zap.Int("retry_count", s.sameAccountRetryCount[accountID]),
		)
		if retryDelay <= 0 {
			return openAIFailoverRetrySameAccount
		}
		if ctx == nil {
			time.Sleep(retryDelay)
			return openAIFailoverRetrySameAccount
		}
		select {
		case <-ctx.Done():
			return openAIFailoverRetryCanceled
		case <-time.After(retryDelay):
			return openAIFailoverRetrySameAccount
		}
	}

	if cooldowner != nil {
		cooldowner.CooldownOpenAIRetryExhausted(ctx, account, canonicalModel, failoverErr)
	}
	return openAIFailoverRetrySwitchAccount
}
