package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	openAIAccountStateUpdateTimeout       = 5 * time.Second
	openAIOAuth429FallbackCooldown        = 5 * time.Second
	openAIOAuth429RetryWindow             = 2 * time.Minute
	openAIOAuth429RetryDelay              = 500 * time.Millisecond
	openAIOAuth429MaxRetryDelay           = 8 * time.Second
	openAIOAuth429MaxAccountAttempts      = 3
	openAIStopSchedulingBridgeCooldown    = 2 * time.Minute
	openAIOAuth429StormWindow             = 10 * time.Second
	openAIOAuth429StormThreshold          = 20
	openAIOAuth429StormMaxAccountSwitches = 1
	openAIRetryExhaustedTransientCooldown = 10 * time.Second
	openAIRetryExhaustedAuthCooldown      = 2 * time.Minute
	openAIRetryExhaustedTransportCooldown = 10 * time.Minute
	openAIRuntimeBreakerHalfOpenClaimTTL  = 2 * time.Minute
)

type openAIRuntimeBreakerProbeContextKey struct{}

type openAIRuntimeBreakerProbeDecisionKey struct {
	accountID int64
	model     string
}

type openAIRuntimeBreakerProbeContext struct {
	mu         sync.Mutex
	owner      string
	decisions  map[openAIRuntimeBreakerProbeDecisionKey]bool
	leaseStore OpenAIRuntimeBreakerLeaseStore
	claims     map[int64]map[string]struct{}
}

var openAIRuntimeBreakerProbeSequence atomic.Uint64

func withOpenAIRuntimeBreakerProbeOwner(ctx context.Context, owner string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, _ := ctx.Value(openAIRuntimeBreakerProbeContextKey{}).(*openAIRuntimeBreakerProbeContext); existing != nil {
		return ctx
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = fmt.Sprintf("%d-%d", time.Now().UnixNano(), openAIRuntimeBreakerProbeSequence.Add(1))
	}
	return context.WithValue(ctx, openAIRuntimeBreakerProbeContextKey{}, &openAIRuntimeBreakerProbeContext{
		owner:     owner,
		decisions: make(map[openAIRuntimeBreakerProbeDecisionKey]bool),
		claims:    make(map[int64]map[string]struct{}),
	})
}

func ensureOpenAIRuntimeBreakerProbeOwner(ctx context.Context) context.Context {
	return withOpenAIRuntimeBreakerProbeOwner(ctx, "")
}

func (p *openAIRuntimeBreakerProbeContext) rememberClaims(accountID int64, models []string, store OpenAIRuntimeBreakerLeaseStore) {
	if p == nil || accountID <= 0 || len(models) == 0 || store == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.leaseStore == nil {
		p.leaseStore = store
	}
	claims := p.claims[accountID]
	if claims == nil {
		claims = make(map[string]struct{}, len(models))
		p.claims[accountID] = claims
	}
	for _, model := range models {
		claims[normalizeOpenAIAccountModelTransientModel(model)] = struct{}{}
	}
}

func (p *openAIRuntimeBreakerProbeContext) claimedModels(accountID int64) []string {
	if p == nil || accountID <= 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	claims := p.claims[accountID]
	if len(claims) == 0 {
		return nil
	}
	models := make([]string, 0, len(claims))
	for model := range claims {
		models = append(models, model)
	}
	return models
}

func (p *openAIRuntimeBreakerProbeContext) releaseClaimsExcept(ctx context.Context, selectedAccountID int64) {
	if p == nil {
		return
	}
	type accountClaims struct {
		accountID int64
		models    []string
	}
	p.mu.Lock()
	store := p.leaseStore
	owner := strings.TrimSpace(p.owner)
	claims := make([]accountClaims, 0, len(p.claims))
	for accountID, modelSet := range p.claims {
		if accountID == selectedAccountID {
			continue
		}
		models := make([]string, 0, len(modelSet))
		for model := range modelSet {
			models = append(models, model)
		}
		claims = append(claims, accountClaims{accountID: accountID, models: models})
		delete(p.claims, accountID)
	}
	p.mu.Unlock()
	if store == nil || owner == "" || len(claims) == 0 {
		return
	}

	cacheCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	for _, claim := range claims {
		if _, err := store.ReleaseOpenAIRuntimeBreakerProbes(cacheCtx, claim.accountID, claim.models, owner); err != nil {
			slog.Warn("openai_runtime_breaker_unselected_probe_release_failed", "account_id", claim.accountID, "error", err)
		}
	}
}

// attachSelectionRuntimeBreakerProbe promotes the selected half-open claim to
// a renewable request-owned lease and releases claims for unselected accounts.
func attachSelectionRuntimeBreakerProbe(ctx context.Context, selection *AccountSelectionResult) *AccountSelectionResult {
	if selection == nil {
		return nil
	}
	probe, ok := ctx.Value(openAIRuntimeBreakerProbeContextKey{}).(*openAIRuntimeBreakerProbeContext)
	if !ok || probe == nil {
		return selection
	}

	selectedAccountID := int64(0)
	if selection.Account != nil {
		selectedAccountID = selection.Account.ID
	}
	probe.releaseClaimsExcept(ctx, selectedAccountID)
	// API-key accounts use the process-local account/model cooldown. Redis
	// half-open ownership is reserved for OAuth-style credentials, where a
	// probe must be coordinated across replicas.
	if selection.Account != nil && selection.Account.Type == AccountTypeAPIKey {
		return selection
	}
	selection.runtimeBreakerProbeOwner = strings.TrimSpace(probe.owner)
	if selection.Account == nil {
		return selection
	}

	models := probe.claimedModels(selection.Account.ID)
	probe.mu.Lock()
	leaseStore := probe.leaseStore
	probe.mu.Unlock()
	lease := newOpenAIRuntimeBreakerProbeLease(
		leaseStore,
		selection.Account.ID,
		models,
		selection.runtimeBreakerProbeOwner,
		openAIRuntimeBreakerHalfOpenClaimTTL,
	)
	if lease == nil {
		return selection
	}

	selection.runtimeBreakerProbeModels = append([]string(nil), models...)
	selection.runtimeBreakerProbeLease = lease
	originalRelease := selection.ReleaseFunc
	selection.ReleaseFunc = func() {
		lease.stopRenewal()
		if originalRelease != nil {
			originalRelease()
		}
	}
	if lease.start(ctx) {
		return selection
	}

	// The claim can be lost between candidate probing and promotion. Do not
	// forward a selection whose request-owned lease could not be renewed.
	lease.release(ctx)
	selection.runtimeBreakerProbeModels = nil
	selection.runtimeBreakerProbeLease = nil
	selection.ReleaseFunc = nil
	if originalRelease != nil {
		originalRelease()
	}
	probe.releaseClaimsExcept(ctx, 0)
	return nil
}

// openAIRuntimeBreakerProbeLease keeps a selected half-open Redis claim alive
// for the duration of a long HTTP stream or WebSocket first turn. Its stop
// operation is intentionally separate from release: slot cleanup may happen
// before the final scheduling result is reported, while only the result owner
// may release a failed probe claim or close a successful breaker.
type openAIRuntimeBreakerProbeLease struct {
	store       OpenAIRuntimeBreakerLeaseStore
	accountID   int64
	models      []string
	owner       string
	claimTTL    time.Duration
	stop        chan struct{}
	stopOnce    sync.Once
	releaseOnce sync.Once
}

func newOpenAIRuntimeBreakerProbeLease(store OpenAIRuntimeBreakerLeaseStore, accountID int64, models []string, owner string, claimTTL time.Duration) *openAIRuntimeBreakerProbeLease {
	if store == nil || accountID <= 0 || strings.TrimSpace(owner) == "" || len(models) == 0 {
		return nil
	}
	copyModels := append([]string(nil), models...)
	return &openAIRuntimeBreakerProbeLease{
		store:     store,
		accountID: accountID,
		models:    copyModels,
		owner:     strings.TrimSpace(owner),
		claimTTL:  claimTTL,
		stop:      make(chan struct{}),
	}
}

func (l *openAIRuntimeBreakerProbeLease) renew(ctx context.Context) bool {
	if l == nil || l.store == nil {
		return false
	}
	cacheCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	renewed, err := l.store.RenewOpenAIRuntimeBreakerProbes(cacheCtx, l.accountID, l.models, l.owner, l.claimTTL)
	if err != nil {
		slog.Warn("openai_runtime_breaker_lease_renew_failed", "account_id", l.accountID, "error", err)
		return false
	}
	return renewed
}

func (l *openAIRuntimeBreakerProbeLease) start(ctx context.Context) bool {
	if l == nil {
		return false
	}
	// Renew synchronously while the selection is promoted. This closes the
	// claim-expiry race between candidate probing and forwarding immediately.
	if !l.renew(ctx) {
		return false
	}
	interval := l.claimTTL / 3
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !l.renew(ctx) {
					return
				}
			case <-l.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return true
}

func (l *openAIRuntimeBreakerProbeLease) stopRenewal() {
	if l != nil {
		l.stopOnce.Do(func() { close(l.stop) })
	}
}

func (l *openAIRuntimeBreakerProbeLease) release(ctx context.Context) {
	if l == nil || l.store == nil {
		return
	}
	l.releaseOnce.Do(func() {
		l.stopRenewal()
		cacheCtx, cancel := openAIAccountStateContext(ctx)
		defer cancel()
		if released, err := l.store.ReleaseOpenAIRuntimeBreakerProbes(cacheCtx, l.accountID, l.models, l.owner); err != nil {
			slog.Warn("openai_runtime_breaker_lease_release_failed", "account_id", l.accountID, "error", err)
		} else if !released {
			slog.Debug("openai_runtime_breaker_lease_release_not_owner", "account_id", l.accountID)
		}
	})
}

func normalizeOpenAIRuntimeBreakerProbeModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = normalizeOpenAIAccountModelTransientModel(model)
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		normalized = append(normalized, model)
	}
	return normalized
}

func (s *OpenAIGatewayService) reacquireOpenAIRuntimeBreakerProbe(
	ctx context.Context,
	previous *AccountSelectionResult,
	selection *AccountSelectionResult,
) error {
	if s == nil || previous == nil || selection == nil || selection.Account == nil {
		return nil
	}
	if selection.Account.Type == AccountTypeAPIKey {
		return nil
	}
	owner := strings.TrimSpace(previous.runtimeBreakerProbeOwner)
	models := normalizeOpenAIRuntimeBreakerProbeModels(previous.runtimeBreakerProbeModels)
	selection.runtimeBreakerProbeOwner = owner
	if owner == "" || len(models) == 0 {
		return nil
	}

	var store OpenAIRuntimeBreakerLeaseStore
	if previous.runtimeBreakerProbeLease != nil {
		store = previous.runtimeBreakerProbeLease.store
	}
	if store == nil {
		breakerStore, ok := s.openAIRuntimeBreakerStore()
		if !ok {
			return fmt.Errorf("openai runtime breaker store is unavailable for same-account retry")
		}
		store, ok = breakerStore.(OpenAIRuntimeBreakerLeaseStore)
		if !ok {
			return fmt.Errorf("openai runtime breaker lease store is unavailable for same-account retry")
		}
	}

	cacheCtx, cancel := openAIAccountStateContext(ctx)
	allowed, claimedModels, err := store.AllowOpenAIRuntimeBreakerProbes(
		cacheCtx,
		selection.Account.ID,
		models,
		owner,
		openAIRuntimeBreakerHalfOpenClaimTTL,
	)
	cancel()
	if err != nil {
		return fmt.Errorf("reclaim openai runtime breaker probe: %w", err)
	}
	if !allowed {
		return fmt.Errorf("openai runtime breaker probe is no longer owned by this request")
	}
	if len(claimedModels) == 0 {
		return nil
	}

	lease := newOpenAIRuntimeBreakerProbeLease(
		store,
		selection.Account.ID,
		claimedModels,
		owner,
		openAIRuntimeBreakerHalfOpenClaimTTL,
	)
	if lease == nil {
		return fmt.Errorf("create openai runtime breaker probe lease")
	}
	originalRelease := selection.ReleaseFunc
	selection.runtimeBreakerProbeModels = append([]string(nil), claimedModels...)
	selection.runtimeBreakerProbeLease = lease
	selection.ReleaseFunc = func() {
		lease.stopRenewal()
		if originalRelease != nil {
			originalRelease()
		}
	}
	if !lease.start(ctx) {
		selection.runtimeBreakerProbeModels = nil
		selection.runtimeBreakerProbeLease = nil
		selection.ReleaseFunc = originalRelease
		lease.release(ctx)
		return fmt.Errorf("renew openai runtime breaker probe lease")
	}
	return nil
}

// OpenAIOAuth429FailoverState tracks the request-local follow-up budget after
// the first Grok OAuth 429. Once that 429 occurs, exactly one different account
// may be attempted; any failure from that follow-up account ends failover.
type OpenAIOAuth429FailoverState struct {
	grokOAuth429FollowupPending bool
}

type openAIOAuth429Disposition uint8

const (
	openAIOAuth429Transient openAIOAuth429Disposition = iota
	openAIOAuth429Quota5h
	openAIOAuth429Quota7d
	openAIOAuth429QuotaReset
)

// classifyOpenAIOAuth429 区分账号配额耗尽信号与普通瞬时 429。只有窗口达到
// 100% 或响应体明确给出 reset 时间时，才视为配额限流信号。
func classifyOpenAIOAuth429(headers http.Header, responseBody []byte) (openAIOAuth429Disposition, *time.Time) {
	classification := classifyOpenAIOAuth429At(headers, responseBody, time.Now())
	return classification.Disposition, classification.ResetAt
}

func openAIAccountStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, openAIAccountStateUpdateTimeout)
}

func isOpenAIOAuthAccount(account *Account) bool {
	return account != nil && account.IsOpenAIOAuthLike()
}

func isGrokOAuthAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformGrok && account.Type == AccountTypeOAuth
}

func isOpenAIAccount(account *Account) bool {
	return account != nil && (account.Platform == PlatformOpenAI || account.Platform == PlatformGrok)
}

// handleOpenAIAccountUpstreamError expects canonicalModel to be the model used
// for scheduling after applying account mapping exactly once.
func (s *OpenAIGatewayService) handleOpenAIAccountUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, canonicalModel ...string) bool {
	// A scoped quality experiment records its own failures without changing
	// ordinary account health, cooldowns, linked accounts or scheduling state.
	if IsCodexQualityRequest(ctx) {
		return false
	}
	// Classify request-local failures before any policy can mutate account health.
	// The same predicates are used by the HTTP failover and error constructors.
	if isOpenAIRequestScopedSafetyRejection(responseBody) {
		return false
	}
	if isOpenAIRequestBudgetRejection(account, statusCode, responseBody) || isOpenAIReportedUpstreamFailure(statusCode, responseBody) {
		return false
	}
	if account != nil && account.Platform == PlatformGrok && isGrokContentPolicyRejection(statusCode, responseBody) {
		return false
	}
	// Any non-2xx upstream HTTP response means the model request was actually sent.
	if s != nil {
		scheduleOllamaCloudUsageActivity(s.deferredService, account)
		scheduleOpenCodeGoUsageActivity(s.deferredService, account)
	}
	// Capacity shedding describes this request, not account health. Keep the
	// account schedulable while the request-local retry budget handles recovery.
	if account != nil && account.Platform == PlatformOpenAI && isOpenAIRequestScopedCapacityShed("", responseBody) {
		return false
	}
	// An exhausted key budget stops scheduling completely: the account enters
	// the error state before the request-local API-key 429 handling below.
	if account != nil && account.IsOpenAICompatible() && isOpenAIBudgetExceededResponse(statusCode, responseBody) {
		s.handleOpenAIBudgetExceeded(ctx, account, responseBody)
		return true
	}
	if account != nil && account.Platform == PlatformOpenAI &&
		(isOpenAIHTTPUpstreamAccessStateError(statusCode, "", responseBody) ||
			(statusCode == http.StatusForbidden && openAIStreamCredentialAuthFailure(responseBody))) {
		stateCtx, cancel := openAIAccountStateContext(ctx)
		defer cancel()
		message := "OpenAI upstream account or workspace is unavailable"
		if upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(responseBody)); upstreamMsg != "" {
			message = upstreamMsg
		}
		if s != nil && s.rateLimitService != nil {
			s.rateLimitService.handleAuthError(stateCtx, account, message)
		}
		if s != nil {
			s.BlockAccountScheduling(account, time.Time{}, "openai_access_state")
		}
		return true
	}
	if isOpenAIAccount(account) && account.Type == AccountTypeAPIKey && !IsOpenAIOfficialHTTPFailover(ctx, account) {
		switch statusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
			// Authentication and quota responses are handled exclusively by the
			// request failover state: no same-account replay, runtime cooldown, then
			// switch. Do not persist legacy schedulable/error state here.
			return false
		}
	}
	if isOpenAIOAuthAccount(account) && (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) &&
		strings.EqualFold(strings.TrimSpace(extractUpstreamErrorMessage(responseBody)), "credential or quota failure") {
		return false
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()

	if account != nil && account.Platform == PlatformOpenAI && isOpenAIContextWindowError("", responseBody) {
		return false
	}

	if isOpenAIImageRateLimitError(statusCode, responseBody) {
		// Image rate-limit signals stay request-scoped here. Dedicated admin or
		// capability handlers may opt into model cooldown explicitly; the generic
		// gateway failover path must not persist it before the bounded retry runs.
		return false
	}

	// Self-built images requests always carry a matching image_generation tool, so a
	// "tool choice not found in 'tools'" 400 means upstream revoked this account's
	// image capability. Gated on the self-built marker: passthrough clients control
	// their own tools/tool_choice and could otherwise poison a healthy account.
	if isOpenAIImagesSelfBuiltRequest(ctx) && isOpenAIImageCapabilityLossError(statusCode, responseBody) {
		if s != nil && s.rateLimitService != nil {
			_ = s.rateLimitService.HandleOpenAIImageCapabilityLoss(stateCtx, account, statusCode, responseBody)
		}
		return false
	}

	if s == nil || account == nil {
		return false
	}
	// Team 联动熔断必须先于 model-not-found 与账户级临时不可调度规则的早退。
	if s.rateLimitService != nil {
		s.rateLimitService.maybeHandleOpenAITeamLinkedError(stateCtx, account, statusCode, responseBody)
	}
	stateCtx = withTempUnschedulableModel(stateCtx, canonicalModel)
	if s.rateLimitService != nil && len(canonicalModel) > 0 && s.rateLimitService.HandleUpstreamModelNotFound(stateCtx, account, canonicalModel[0], statusCode, responseBody) {
		return true
	}
	// Isolate a custom temporary-unschedulable match to the known upstream
	// model before entering the generic account error path. This keeps the
	// account available to other models and avoids the account runtime blocker.
	if s.rateLimitService != nil && statusCode != http.StatusUnauthorized && len(canonicalModel) > 0 && strings.TrimSpace(canonicalModel[0]) != "" &&
		s.rateLimitService.HandleTempUnschedulable(stateCtx, account, statusCode, responseBody, canonicalModel[0]) {
		return true
	}
	if statusCode == http.StatusTooManyRequests && s.rateLimitService != nil && len(canonicalModel) > 0 &&
		s.rateLimitService.HandleOpenAICodexSparkRateLimit(stateCtx, account, canonicalModel[0], statusCode, headers, responseBody) {
		return false
	}
	if statusCode == http.StatusTooManyRequests {
		s.markOpenAIOAuth429RateLimited(stateCtx, account, headers, responseBody)
		if isOpenAIOAuthAccount(account) {
			return false
		}
	}
	if s.rateLimitService == nil {
		return false
	}
	beforeGeneration, _ := s.openaiAccountRuntimeBlockGeneration.Load(account.ID)
	shouldDisable := s.rateLimitService.HandleUpstreamError(stateCtx, account, statusCode, headers, responseBody)
	modelTempMatched := statusCode != http.StatusUnauthorized && tempUnschedulableModel(stateCtx, nil) != "" &&
		len(matchTempUnschedulableRules(account, statusCode, responseBody)) > 0
	if shouldDisable && !modelTempMatched {
		// A finite DB-cooldown notification already supplies the bridge for this
		// error. Do not reclassify that notification as independent evidence.
		snapshot := s.peekOpenAIAccountRuntimeBlock(account)
		if !snapshot.blocked || !snapshot.sources.hasPersisted || snapshot.generation == beforeGeneration {
			s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		}
	}
	// Pool-mode retryable upstream errors are already bounded by the request-local
	// same-account retry budget. Recording the generic account+model transient
	// cooldown here would block the next approved retry before that budget is used.
	poolModeRetryable := account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode)
	if !shouldDisable && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey &&
		shouldCooldownOpenAITransientUpstreamError(statusCode, responseBody) && !poolModeRetryable {
		model := ""
		if len(canonicalModel) > 0 {
			model = canonicalModel[0]
		}
		decision := s.recordOpenAIAccountModelTransientFailure(account, model, time.Now())
		if decision.FailureStreak > 0 {
			slog.Warn("openai_model_transient_state",
				"account_id", account.ID,
				"model", openAIAccountModelTransientModel(model),
				"failure_streak", decision.FailureStreak,
				"cooldown_ms", decision.Cooldown.Milliseconds(),
				"block_scope", "account_model",
			)
		}
	}
	return shouldDisable
}

func shouldCooldownOpenAITransientUpstreamError(statusCode int, responseBody []byte) bool {
	switch statusCode {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 520, 521, 522, 523, 524:
		return true
	case http.StatusBadRequest:
		return isOpenAITransientProcessingError(statusCode, "", responseBody)
	default:
		return false
	}
}

func (s *OpenAIGatewayService) markOpenAIOAuth429RateLimited(ctx context.Context, account *Account, headers http.Header, responseBody []byte) {
	if s == nil || !isOpenAIOAuthAccount(account) {
		return
	}
	// Spark 影子：不按 /responses 429 的 global x-codex-* 信号做内存运行时熔断(同 handle429,外审第8轮 P1)。
	// 同时避免把 spark 的 429 计入全局 429 storm 计数(recordOpenAIOAuth429),否则会误伤母账号 failover 决策。
	if account.IsShadow() {
		return
	}
	s.recordOpenAIOAuth429()
	classification := classifyOpenAIOAuth429At(headers, responseBody, time.Now())
	disposition := classification.Disposition
	if disposition != openAIOAuth429Transient {
		if err := persistOpenAIQuotaClassification(ctx, s.accountRepo, account, classification, time.Now()); err != nil {
			slog.Warn("quota_state_write_failed", "account_id", account.ID)
		}
		s.openaiOAuth429RetryStartedAt.Delete(account.ID)
		return
	}
	slog.Info("codex_quota_429_classified",
		"account_id", account.ID,
		"classification", map[bool]string{true: "transient", false: "hard_quota"}[disposition == openAIOAuth429Transient],
		"window", classification.Window,
		"source", classification.Source,
		"code", classification.Code,
	)
	if disposition == openAIOAuth429Transient && s.openAIOAuth429RetryWindowActive(account) {
		return
	}
	now := time.Now()
	cooldownUntil := now.Add(openAIOAuth429FallbackCooldown)
	if s.rateLimitService != nil {
		cooldown, ok := s.rateLimitService.get429FallbackCooldown(ctx, account)
		if !ok || cooldown <= 0 {
			s.openaiOAuth429RetryStartedAt.Delete(account.ID)
			return
		}
		cooldownUntil = now.Add(cooldown)
	}
	s.BlockAccountScheduling(account, cooldownUntil, "429")
	s.openaiOAuth429RetryStartedAt.Delete(account.ID)
}

func (s *OpenAIGatewayService) shouldRetryOpenAIOAuth429OnSameAccountWithResponse(account *Account, statusCode int, shouldDisable bool, headers http.Header, responseBody []byte) bool {
	if shouldDisable || statusCode != http.StatusTooManyRequests || !isOpenAIOAuthAccount(account) || account.IsShadow() {
		return false
	}
	disposition, _ := classifyOpenAIOAuth429(headers, responseBody)
	if disposition != openAIOAuth429Transient || s.isOpenAIAccountRuntimeBlocked(account) {
		return false
	}
	return s.openAIOAuth429RetryWindowActive(account)
}

func (s *OpenAIGatewayService) ShouldRetryOpenAIOAuth429(account *Account, headers http.Header, responseBody []byte) bool {
	if s == nil || !isOpenAIOAuthAccount(account) || account.IsShadow() || s.isOpenAIAccountRuntimeBlocked(account) {
		return false
	}
	disposition, _ := classifyOpenAIOAuth429(headers, responseBody)
	return disposition == openAIOAuth429Transient && s.openAIOAuth429RetryWindowActive(account)
}

func (s *OpenAIGatewayService) openAIOAuth429RetryWindowActive(account *Account) bool {
	if s == nil || !isOpenAIOAuthAccount(account) || account.IsShadow() {
		return false
	}
	now := time.Now()
	value, _ := s.openaiOAuth429RetryStartedAt.LoadOrStore(account.ID, now)
	startedAt, ok := value.(time.Time)
	if !ok {
		s.openaiOAuth429RetryStartedAt.Store(account.ID, now)
		startedAt = now
	}
	return now.Before(startedAt.Add(openAIOAuth429RetryWindow))
}

func (s *OpenAIGatewayService) openAIOAuth429RetryDeadline(account *Account) time.Time {
	if s == nil || !isOpenAIOAuthAccount(account) || account.IsShadow() {
		return time.Time{}
	}
	value, ok := s.openaiOAuth429RetryStartedAt.Load(account.ID)
	if !ok {
		return time.Time{}
	}
	startedAt, ok := value.(time.Time)
	if !ok {
		return time.Time{}
	}
	return startedAt.Add(openAIOAuth429RetryWindow)
}

func openAIOAuth429SameAccountRetryDelay(headers http.Header, deadline time.Time) time.Duration {
	delay := openAIOAuth429RetryDelay
	now := time.Now()
	if resetAt := parseRetryAfterResetTime(headers, now); resetAt != nil && resetAt.After(now) {
		delay = resetAt.Sub(now)
	}
	if delay > openAIOAuth429MaxRetryDelay {
		delay = openAIOAuth429MaxRetryDelay
	}
	if remaining := time.Until(deadline); !deadline.IsZero() && delay > remaining {
		delay = remaining
	}
	if delay < 0 {
		return 0
	}
	return delay
}

func (s *OpenAIGatewayService) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	s.blockAccountScheduling(account, until, reason, false)
}

// BlockAccountSchedulingFromPersistedCooldown mirrors only the ordinary DB
// cooldown fields. Request-owned cooldowns and credential mutation guards
// continue to use BlockAccountScheduling instead.
func (s *OpenAIGatewayService) BlockAccountSchedulingFromPersistedCooldown(account *Account, until time.Time, reason string) {
	if until.IsZero() {
		s.BlockAccountScheduling(account, until, reason)
		return
	}
	s.blockAccountScheduling(account, until, reason, true)
}

func (s *OpenAIGatewayService) blockAccountScheduling(account *Account, until time.Time, reason string, fromPersistedCooldown bool) {
	if s == nil || !isOpenAIAccount(account) {
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	defer mu.Unlock()
	_, _ = s.blockAccountSchedulingLockedWithSource(account, until, reason, fromPersistedCooldown)
	if fromPersistedCooldown {
		// A DB mirror is not an independent Redis breaker. Persisting its
		// deadline there would keep vetoing the account after DB recovery.
		return
	}
	if account.Type != AccountTypeAPIKey {
		sources := s.openAIAccountRuntimeBlockSourcesLocked(account.ID)
		if sources.hasIndependent {
			s.persistOpenAIRuntimeBreaker(context.Background(), account.ID, "", reason, sources.independentUntil)
		}
	}
}

func (s *OpenAIGatewayService) openAIRuntimeBreakerStore() (OpenAIRuntimeBreakerStore, bool) {
	if s == nil || s.cache == nil {
		return nil, false
	}
	store, ok := s.cache.(OpenAIRuntimeBreakerStore)
	return store, ok && store != nil
}

func (s *OpenAIGatewayService) persistOpenAIRuntimeBreaker(ctx context.Context, accountID int64, model, reason string, until time.Time) {
	store, ok := s.openAIRuntimeBreakerStore()
	if !ok || accountID <= 0 {
		return
	}
	ttl := time.Until(until)
	if ttl <= 0 {
		return
	}
	cacheCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if err := store.BlockOpenAIRuntimeBreaker(cacheCtx, accountID, model, reason, ttl); err != nil {
		slog.Warn("openai_runtime_breaker_persist_failed", "account_id", accountID, "model", model, "reason", reason, "error", err)
	}
}

func (s *OpenAIGatewayService) clearOpenAIRuntimeBreaker(ctx context.Context, accountID int64, model, owner string) {
	store, ok := s.openAIRuntimeBreakerStore()
	owner = strings.TrimSpace(owner)
	if !ok || accountID <= 0 || owner == "" {
		return
	}
	cacheCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if err := store.ClearOpenAIRuntimeBreaker(cacheCtx, accountID, model, owner); err != nil {
		slog.Warn("openai_runtime_breaker_clear_failed", "account_id", accountID, "model", model, "error", err)
	}
}

func (s *OpenAIGatewayService) clearOpenAIRuntimeBreakerProbeClaims(ctx context.Context, accountID int64, models []string, owner string) {
	for _, model := range normalizeOpenAIRuntimeBreakerProbeModels(models) {
		s.clearOpenAIRuntimeBreaker(ctx, accountID, model, owner)
	}
}

func (s *OpenAIGatewayService) releaseOpenAIRuntimeBreakerProbe(ctx context.Context, accountID int64, model, owner string) {
	store, ok := s.openAIRuntimeBreakerStore()
	leaseStore, supportsLease := store.(OpenAIRuntimeBreakerLeaseStore)
	owner = strings.TrimSpace(owner)
	if !ok || !supportsLease || accountID <= 0 || owner == "" {
		return
	}
	models := []string{""}
	if model = openAIAccountModelTransientModel(model); model != "" {
		models = append(models, model)
	}
	cacheCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if released, err := leaseStore.ReleaseOpenAIRuntimeBreakerProbes(cacheCtx, accountID, models, owner); err != nil {
		slog.Warn("openai_runtime_breaker_release_failed", "account_id", accountID, "model", model, "error", err)
	} else if !released {
		slog.Debug("openai_runtime_breaker_release_not_owner", "account_id", accountID, "model", model)
	}
}

func (s *OpenAIGatewayService) clearAllOpenAIRuntimeBreakers(ctx context.Context, accountID int64) {
	store, ok := s.openAIRuntimeBreakerStore()
	if !ok || accountID <= 0 {
		return
	}
	cacheCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if err := store.ClearAllOpenAIRuntimeBreakers(cacheCtx, accountID); err != nil {
		slog.Warn("openai_runtime_breaker_clear_all_failed", "account_id", accountID, "error", err)
	}
}

func (s *OpenAIGatewayService) openAIAccountRuntimeBlockLock(accountID int64) *sync.Mutex {
	actual, _ := s.openaiAccountRuntimeBlockLocks.LoadOrStore(accountID, &sync.Mutex{})
	mu, ok := actual.(*sync.Mutex)
	if !ok {
		mu = &sync.Mutex{}
		s.openaiAccountRuntimeBlockLocks.Store(accountID, mu)
	}
	return mu
}

type openAIAccountRuntimeBlockSources struct {
	hasIndependent   bool
	independentUntil time.Time
	independentOwner uint64
	hasPersisted     bool
	persistedUntil   time.Time
}

func (sources openAIAccountRuntimeBlockSources) effectiveUntil() (time.Time, bool) {
	if sources.hasIndependent {
		if sources.independentUntil.IsZero() || !sources.hasPersisted || !sources.persistedUntil.After(sources.independentUntil) {
			return sources.independentUntil, true
		}
	}
	return sources.persistedUntil, sources.hasPersisted
}

func (sources *openAIAccountRuntimeBlockSources) expire(now time.Time) {
	if sources.hasIndependent && !sources.independentUntil.IsZero() && !now.Before(sources.independentUntil) {
		sources.hasIndependent = false
		sources.independentUntil = time.Time{}
		sources.independentOwner = 0
	}
	if sources.hasPersisted && !now.Before(sources.persistedUntil) {
		sources.hasPersisted = false
		sources.persistedUntil = time.Time{}
	}
}

// Unknown entries retain the legacy independent meaning. Only an explicit
// persisted-cooldown writer may make a block eligible for DB-authority cleanup.
// Callers hold the per-account runtime block lock.
func (s *OpenAIGatewayService) openAIAccountRuntimeBlockSourcesLocked(accountID int64) openAIAccountRuntimeBlockSources {
	if raw, ok := s.openaiAccountRuntimeBlockSources.Load(accountID); ok {
		if sources, valid := raw.(openAIAccountRuntimeBlockSources); valid {
			return sources
		}
	}
	if raw, ok := s.openaiAccountRuntimeBlockUntil.Load(accountID); ok {
		if until, valid := raw.(time.Time); valid {
			generation, _ := s.openaiAccountRuntimeBlockGeneration.Load(accountID)
			owner, _ := generation.(uint64)
			return openAIAccountRuntimeBlockSources{hasIndependent: true, independentUntil: until, independentOwner: owner}
		}
	}
	return openAIAccountRuntimeBlockSources{}
}

func (s *OpenAIGatewayService) blockAccountSchedulingLocked(account *Account, until time.Time, reason string) (uint64, bool) {
	return s.blockAccountSchedulingLockedWithSource(account, until, reason, false)
}

func (s *OpenAIGatewayService) blockAccountSchedulingLockedWithSource(account *Account, until time.Time, reason string, fromPersistedCooldown bool) (uint64, bool) {
	now := time.Now()
	blockUntil := until
	if blockUntil.IsZero() || !blockUntil.After(now) {
		blockUntil = now.Add(openAIStopSchedulingBridgeCooldown)
	}

	current, loaded := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	currentUntil, validCurrent := current.(time.Time)
	sources := s.openAIAccountRuntimeBlockSourcesLocked(account.ID)
	sources.expire(now)
	generation := s.openaiAccountRuntimeBlockSequence.Add(1)
	if fromPersistedCooldown {
		if !sources.hasPersisted || blockUntil.After(sources.persistedUntil) {
			sources.persistedUntil = blockUntil
		}
		sources.hasPersisted = true
	} else {
		if !sources.hasIndependent || blockUntil.IsZero() || (!sources.independentUntil.IsZero() && blockUntil.After(sources.independentUntil)) {
			sources.independentUntil = blockUntil
		}
		sources.hasIndependent = true
		// Even a shorter independent write takes ownership of that contribution.
		// An unrelated DB mirror must not take over a tentative credential guard.
		sources.independentOwner = generation
	}
	effectiveUntil, _ := sources.effectiveUntil()
	s.openaiAccountRuntimeBlockUntil.Store(account.ID, effectiveUntil)
	s.openaiAccountRuntimeBlockSources.Store(account.ID, sources)
	s.openaiAccountRuntimeBlockGeneration.Store(account.ID, generation)
	return generation, !loaded || !validCurrent || !effectiveUntil.Equal(currentUntil)
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlock(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	s.openaiAccountRuntimeBlockUntil.Delete(accountID)
	s.openaiAccountRuntimeBlockSources.Delete(accountID)
	s.openaiAccountRuntimeBlockGeneration.Store(accountID, s.openaiAccountRuntimeBlockSequence.Add(1))
	state := s.getOpenAIAccountModelTransientState()
	if state != nil {
		state.clearAccount(accountID)
	}
	s.clearAllOpenAIRuntimeBreakers(context.Background(), accountID)
}

func (s *OpenAIGatewayService) clearOpenAIAccountSchedulingBlockScope(accountID int64, owner string) {
	if s == nil || accountID <= 0 {
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(accountID)
	mu.Lock()
	if value, ok := s.openaiAccountRuntimeBlockUntil.Load(accountID); ok {
		if blockUntil, valid := value.(time.Time); valid && time.Now().Before(blockUntil) {
			mu.Unlock()
			return
		}
	}
	s.openaiAccountRuntimeBlockUntil.Delete(accountID)
	s.openaiAccountRuntimeBlockSources.Delete(accountID)
	s.openaiAccountRuntimeBlockGeneration.Store(accountID, s.openaiAccountRuntimeBlockSequence.Add(1))
	mu.Unlock()
	s.clearOpenAIRuntimeBreaker(context.Background(), accountID, "", owner)
}

func (s *OpenAIGatewayService) isOpenAIAccountRuntimeBlocked(account *Account) bool {
	return s.isOpenAIAccountRuntimeBlockedContext(context.Background(), account)
}

func (s *OpenAIGatewayService) isOpenAIAccountRuntimeBlockedContext(_ context.Context, account *Account) bool {
	if s == nil || account == nil || account.ID <= 0 {
		return false
	}
	if quota := account.QuotaState(time.Now()); quota != nil && quota.Blocked {
		return true
	}
	if !isOpenAIAccount(account) {
		return false
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	defer mu.Unlock()
	value, ok := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	if !ok {
		return false
	}
	if cooldownUntil, valid := value.(time.Time); valid && time.Now().Before(cooldownUntil) {
		return true
	}
	s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
	s.openaiAccountRuntimeBlockSources.Delete(account.ID)
	s.openaiAccountRuntimeBlockGeneration.Store(account.ID, s.openaiAccountRuntimeBlockSequence.Add(1))
	return false
}

func (s *OpenAIGatewayService) getOpenAIAccountModelTransientState() *openAIAccountModelTransientState {
	if s == nil {
		return nil
	}
	s.openaiModelTransientOnce.Do(func() {
		if s.openaiModelTransient == nil {
			s.openaiModelTransient = newOpenAIAccountModelTransientState(openAIModelTransientDefaultMax)
		}
	})
	return s.openaiModelTransient
}

func canonicalOpenAIAccountSchedulingModel(account *Account, requestedModel string) string {
	model := strings.TrimSpace(requestedModel)
	if account == nil || model == "" {
		return model
	}
	if account.IsOpenAI() {
		return resolveOpenAIAccountUpstreamModelForRequest(account, model, false)
	}
	if mapped := strings.TrimSpace(account.GetMappedModel(model)); mapped != "" {
		return mapped
	}
	return model
}

func openAIAccountModelTransientModel(canonicalModel string) string {
	return normalizeOpenAIAccountModelTransientModel(canonicalModel)
}

func (s *OpenAIGatewayService) recordOpenAIAccountModelTransientFailure(account *Account, canonicalModel string, now time.Time) openAIAccountModelTransientDecision {
	if s == nil || account == nil {
		return openAIAccountModelTransientDecision{}
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return openAIAccountModelTransientDecision{}
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	defer mu.Unlock()
	model := openAIAccountModelTransientModel(canonicalModel)
	decision := state.recordFailure(account.ID, model, now)
	if account.Type != AccountTypeAPIKey && decision.Cooldown > 0 && !decision.BlockUntil.IsZero() {
		s.persistOpenAIRuntimeBreaker(context.Background(), account.ID, model, "transient_failures", decision.BlockUntil)
	}
	return decision
}

func (s *OpenAIGatewayService) clearOpenAIAccountModelTransientState(accountID int64, model, owner string) {
	if s == nil || accountID <= 0 {
		return
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	if !state.recordSuccess(accountID, model) {
		return
	}
	s.clearOpenAIRuntimeBreaker(context.Background(), accountID, model, owner)
}

func (s *OpenAIGatewayService) isOpenAIAccountModelRuntimeBlocked(account *Account, requestedModel string) bool {
	if s == nil || account == nil {
		return false
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return false
	}
	canonicalModel := canonicalOpenAIAccountSchedulingModel(account, requestedModel)
	return state.isBlocked(account.ID, openAIAccountModelTransientModel(canonicalModel), time.Now())
}

func accountPersistedSchedulingCooldownActive(account *Account) bool {
	if account == nil {
		return false
	}
	now := time.Now()
	if account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil) {
		return true
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		return true
	}
	if account.OverloadUntil != nil && now.Before(*account.OverloadUntil) {
		return true
	}
	return false
}

type openAIAccountRuntimeBlockSnapshot struct {
	until      time.Time
	generation uint64
	blocked    bool
	sources    openAIAccountRuntimeBlockSources
}

func (s *OpenAIGatewayService) peekOpenAIAccountRuntimeBlock(account *Account) openAIAccountRuntimeBlockSnapshot {
	if s == nil || !isOpenAIAccount(account) {
		return openAIAccountRuntimeBlockSnapshot{}
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	defer mu.Unlock()
	value, ok := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	if !ok {
		return openAIAccountRuntimeBlockSnapshot{}
	}
	until, isTime := value.(time.Time)
	if !isTime || until.IsZero() || !time.Now().Before(until) {
		s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
		s.openaiAccountRuntimeBlockSources.Delete(account.ID)
		s.openaiOAuth429RetryStartedAt.Delete(account.ID)
		s.openaiAccountRuntimeBlockGeneration.Store(account.ID, s.openaiAccountRuntimeBlockSequence.Add(1))
		return openAIAccountRuntimeBlockSnapshot{}
	}
	generation, _ := s.openaiAccountRuntimeBlockGeneration.Load(account.ID)
	gen, _ := generation.(uint64)
	return openAIAccountRuntimeBlockSnapshot{until: until, generation: gen, blocked: true, sources: s.openAIAccountRuntimeBlockSourcesLocked(account.ID)}
}

// clearOpenAIAccountRuntimeBlockIfUnchanged deletes the in-process account block
// only when generation and deadline are unchanged. A newer block installed after
// peek must be kept even if its deadline happens to match.
func (s *OpenAIGatewayService) clearOpenAIAccountRuntimeBlockIfUnchanged(accountID int64, snapshot openAIAccountRuntimeBlockSnapshot) {
	if s == nil || accountID <= 0 || !snapshot.blocked || !snapshot.sources.hasPersisted {
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	generation, ok := s.openaiAccountRuntimeBlockGeneration.Load(accountID)
	if !ok || generation != snapshot.generation {
		return
	}
	current, ok := s.openaiAccountRuntimeBlockUntil.Load(accountID)
	currentUntil, isTime := current.(time.Time)
	if !ok || !isTime || !currentUntil.Equal(snapshot.until) {
		return
	}
	sources := snapshot.sources
	sources.hasPersisted = false
	sources.persistedUntil = time.Time{}
	sources.expire(time.Now())
	if until, blocked := sources.effectiveUntil(); blocked {
		s.openaiAccountRuntimeBlockUntil.Store(accountID, until)
		s.openaiAccountRuntimeBlockSources.Store(accountID, sources)
	} else {
		s.openaiAccountRuntimeBlockUntil.Delete(accountID)
		s.openaiAccountRuntimeBlockSources.Delete(accountID)
		s.openaiOAuth429RetryStartedAt.Delete(accountID)
	}
	s.openaiAccountRuntimeBlockGeneration.Store(accountID, s.openaiAccountRuntimeBlockSequence.Add(1))
}

// isOpenAIAccountRequestRuntimeBlocked treats persisted cooldown fields as the
// source of truth only for explicitly mirrored DB cooldown contributions.
// Empty/inactive fields remove that contribution with generation+deadline CAS;
// an independent request/credential block keeps its own deadline and owner.
// Model scopes and OAuth Redis leases remain independent.
func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlocked(account *Account, requestedModel string, requireCompact ...bool) bool {
	return s.isOpenAIAccountRequestRuntimeBlockedContext(ensureOpenAIRuntimeBreakerProbeOwner(context.Background()), account, requestedModel, requireCompact...)
}

func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlockedContext(ctx context.Context, account *Account, requestedModel string, requireCompact ...bool) bool {
	return s.isOpenAIAccountRequestRuntimeBlockedContextInternal(ctx, account, requestedModel, true, requireCompact...)
}

// isOpenAIAccountCandidateRuntimeBlockedContext evaluates only gates that can
// be proven from the scheduler's partial candidate projection. Credential-bound
// gates, including Codex turn-state tickets, are intentionally deferred to the
// authoritative account returned by resolveFreshSchedulableOpenAIAccount or
// recheckSelectedOpenAIAccountFromDB. Running those gates on a projection whose
// credentials and Extra were filtered would turn a valid ticket into a
// fail-closed runtime block.
func (s *OpenAIGatewayService) isOpenAIAccountCandidateRuntimeBlockedContext(ctx context.Context, account *Account, requestedModel string, requireCompact ...bool) bool {
	return s.isOpenAIAccountRequestRuntimeBlockedContextInternal(ctx, account, requestedModel, false, requireCompact...)
}

func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlockedContextInternal(
	ctx context.Context,
	account *Account,
	requestedModel string,
	evaluateCodexTicket bool,
	requireCompact ...bool,
) bool {
	if s == nil || account == nil {
		return false
	}
	// Only OAuth/setup-token accounts can own tickets; API-key paths stay unchanged.
	// Scheduler candidate projections intentionally omit ticket material and the
	// identity fields needed to validate its owner, so that check is authoritative-only.
	if evaluateCodexTicket && isOpenAICodexTicketAccount(account) {
		compact := len(requireCompact) > 0 && requireCompact[0]
		outboundModel := s.openAICodexTicketOutboundModel(account, requestedModel, compact)
		if s.openAICodexTicketBlocksAccount(account, outboundModel) {
			return true
		}
	}
	snapshot := s.peekOpenAIAccountRuntimeBlock(account)
	if snapshot.blocked && snapshot.sources.hasPersisted && !accountPersistedSchedulingCooldownActive(account) {
		s.clearOpenAIAccountRuntimeBlockIfUnchanged(account.ID, snapshot)
	}
	// Re-read after the conditional clear so a concurrent replacement remains
	// blocked.
	if s.isOpenAIAccountRuntimeBlockedContext(ctx, account) || s.isOpenAIAccountModelRuntimeBlocked(account, requestedModel) {
		return true
	}
	if account.Type == AccountTypeAPIKey {
		return false
	}
	store, ok := s.openAIRuntimeBreakerStore()
	if !ok {
		return false
	}
	ctx = ensureOpenAIRuntimeBreakerProbeOwner(ctx)
	probe, _ := ctx.Value(openAIRuntimeBreakerProbeContextKey{}).(*openAIRuntimeBreakerProbeContext)
	if probe == nil {
		return false
	}
	models := []string{""}
	if model := openAIAccountModelTransientModel(canonicalOpenAIAccountSchedulingModel(account, requestedModel)); model != "" {
		models = append(models, model)
	}
	if batchStore, supportsBatch := store.(OpenAIRuntimeBreakerBatchProbeStore); supportsBatch {
		cacheCtx, cancel := openAIAccountStateContext(ctx)
		allowed, claimedModels, err := batchStore.AllowOpenAIRuntimeBreakerProbes(
			cacheCtx,
			account.ID,
			models,
			probe.owner,
			openAIRuntimeBreakerHalfOpenClaimTTL,
		)
		cancel()
		if err != nil {
			slog.Warn("openai_runtime_breaker_read_failed", "account_id", account.ID, "models", models, "error", err)
			return false
		}
		if allowed && len(claimedModels) > 0 {
			if leaseStore, supportsLease := store.(OpenAIRuntimeBreakerLeaseStore); supportsLease {
				probe.rememberClaims(account.ID, claimedModels, leaseStore)
			}
		}
		return !allowed
	}
	for _, model := range models {
		key := openAIRuntimeBreakerProbeDecisionKey{accountID: account.ID, model: model}
		cacheCtx, cancel := openAIAccountStateContext(ctx)
		allowed, err := store.AllowOpenAIRuntimeBreakerProbe(cacheCtx, account.ID, model, probe.owner, openAIRuntimeBreakerHalfOpenClaimTTL)
		cancel()
		if err != nil {
			slog.Warn("openai_runtime_breaker_read_failed", "account_id", account.ID, "model", model, "error", err)
			allowed = true
		}
		probe.mu.Lock()
		probe.decisions[key] = allowed
		probe.mu.Unlock()
		if !allowed {
			return true
		}
	}
	return false
}

// CooldownOpenAIRetryExhausted is the handler-to-scheduler circuit breaker used
// after the bounded same-account retry budget is exhausted. It reuses the
// existing in-memory account/model blockers and never shortens a stronger block.
func (s *OpenAIGatewayService) CooldownOpenAIRetryExhausted(
	ctx context.Context,
	account *Account,
	canonicalModel string,
	failoverErr *UpstreamFailoverError,
) {
	if s == nil || account == nil || failoverErr == nil || !isOpenAIAccount(account) {
		return
	}
	// The marked HTTP API-key path uses upstream's service-side transient
	// streaks and durable transport policy, not a second exhaustion penalty.
	if IsOpenAIOfficialHTTPFailover(ctx, account) {
		return
	}
	if failoverErr.RequestScopedTransient || failoverErr.SuppressAccountHealthPenalty ||
		failoverErr.Scope == GatewayFailureScopeRequest || failoverErr.Scope == GatewayFailureScopeProvider {
		return
	}

	now := time.Now()
	switch failoverErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		s.BlockAccountScheduling(account, now.Add(openAIRetryExhaustedAuthCooldown), "retry_exhausted_auth")
		return
	case http.StatusTooManyRequests:
		classification := classifyOpenAIOAuth429At(failoverErr.ResponseHeaders, failoverErr.ResponseBody, now)
		if isOpenAIOAuthAccount(account) && classification.Disposition != openAIOAuth429Transient {
			if err := persistOpenAIQuotaClassification(ctx, s.accountRepo, account, classification, now); err != nil {
				slog.Warn("quota_state_write_failed", "account_id", account.ID)
			}
			return
		}
		until := now.Add(openAIOAuth429FallbackCooldown)
		if classification.Disposition != openAIOAuth429Transient && classification.ResetAt != nil && classification.ResetAt.After(now) {
			until = *classification.ResetAt
		} else if resetAt := parseRetryAfterResetTime(failoverErr.ResponseHeaders, now); resetAt != nil && resetAt.After(now) {
			maxResetAt := now.Add(time.Duration(maxRateLimit429CooldownSeconds) * time.Second)
			until = *resetAt
			if until.After(maxResetAt) {
				until = maxResetAt
			}
		} else if s.rateLimitService != nil {
			// The retry owner must not reintroduce the fallback that the ordinary
			// 429 path explicitly disabled. Proven quota resets and Retry-After
			// above remain upstream-directed cooldowns, not fallback penalties.
			cooldown, enabled := s.rateLimitService.get429FallbackCooldown(ctx, account)
			if !enabled || cooldown <= 0 {
				return
			}
			until = now.Add(cooldown)
		}
		slog.Info("codex_quota_429_classified",
			"account_id", account.ID,
			"classification", map[bool]string{true: "transient", false: "hard_quota"}[classification.Disposition == openAIOAuth429Transient],
			"window", classification.Window,
			"source", classification.Source,
			"code", classification.Code,
			"path", "retry_exhausted",
		)
		s.BlockAccountScheduling(account, until, "retry_exhausted_429")
		return
	}

	shouldCool := failoverErr.StatusCode == http.StatusRequestTimeout ||
		failoverErr.StatusCode >= http.StatusInternalServerError ||
		failoverErr.RetryableOnSameAccount ||
		failoverErr.Reason == OpenAITransientTransportFailureReason ||
		failoverErr.Reason == OpenAIPersistentTransportFailureReason
	if !shouldCool {
		return
	}
	if failoverErr.Reason == OpenAIPersistentTransportFailureReason {
		until := now.Add(openAIRetryExhaustedTransportCooldown)
		if resetAt := parseRetryAfterResetTime(failoverErr.ResponseHeaders, now); resetAt != nil && resetAt.After(until) {
			until = *resetAt
		}
		s.BlockAccountScheduling(account, until, "retry_exhausted_transport")
		return
	}

	model := strings.TrimSpace(canonicalModel)
	transientUntil := now.Add(openAIRetryExhaustedTransientCooldown)
	if resetAt := parseRetryAfterResetTime(failoverErr.ResponseHeaders, now); resetAt != nil && resetAt.After(transientUntil) {
		transientUntil = *resetAt
	}
	if model == "" {
		s.BlockAccountScheduling(account, transientUntil, "retry_exhausted_transient")
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	decision := s.getOpenAIAccountModelTransientState().block(account.ID, model, now, time.Until(transientUntil))
	if account.Type != AccountTypeAPIKey {
		s.persistOpenAIRuntimeBreaker(ctx, account.ID, model, "retry_exhausted_transient", decision.BlockUntil)
	}
	mu.Unlock()
	slog.Warn("openai_model_retry_exhausted_cooldown",
		"account_id", account.ID,
		"model", openAIAccountModelTransientModel(model),
		"cooldown_ms", decision.Cooldown.Milliseconds(),
	)
}

func (s *OpenAIGatewayService) recordOpenAIOAuth429() {
	if s == nil {
		return
	}
	now := time.Now()
	windowStart := s.openaiOAuth429WindowStartUnixNano.Load()
	if windowStart == 0 || now.Sub(time.Unix(0, windowStart)) >= openAIOAuth429StormWindow {
		if s.openaiOAuth429WindowStartUnixNano.CompareAndSwap(windowStart, now.UnixNano()) {
			s.openaiOAuth429WindowCount.Store(1)
			return
		}
	}
	s.openaiOAuth429WindowCount.Add(1)
}

func (s *OpenAIGatewayService) isOpenAIOAuth429Storm() bool {
	if s == nil {
		return false
	}
	windowStart := s.openaiOAuth429WindowStartUnixNano.Load()
	if windowStart == 0 || time.Since(time.Unix(0, windowStart)) >= openAIOAuth429StormWindow {
		return false
	}
	return s.openaiOAuth429WindowCount.Load() >= openAIOAuth429StormThreshold
}

func (s *OpenAIGatewayService) ShouldStopOpenAIOAuth429Failover(account *Account, statusCode int, failedSwitches int, state *OpenAIOAuth429FailoverState) bool {
	if failedSwitches < openAIOAuth429StormMaxAccountSwitches {
		return false
	}
	if state != nil && state.grokOAuth429FollowupPending {
		// The follow-up budget was armed by a Grok OAuth 429. Consume it on
		// any failing follow-up account, even if a mixed pool selected an API-key
		// account next.
		return true
	}
	if isGrokOAuthAccount(account) {
		if state == nil {
			// Preserve the old threshold for callers that have not adopted the
			// request-local state contract yet.
			return statusCode == http.StatusTooManyRequests && failedSwitches >= 2
		}
		if statusCode == http.StatusTooManyRequests {
			state.grokOAuth429FollowupPending = true
		}
		return false
	}
	if statusCode != http.StatusTooManyRequests || !isOpenAIOAuthAccount(account) {
		return false
	}
	return s.isOpenAIOAuth429Storm()
}
