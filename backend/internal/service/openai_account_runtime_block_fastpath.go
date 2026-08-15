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
	cindyBalancePendingReadTimeout        = 500 * time.Millisecond
	openAIOAuth429FallbackCooldown        = 5 * time.Second
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

func openAIAccountStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, openAIAccountStateUpdateTimeout)
}

func isOpenAIOAuthAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth
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
	cindyBalanceInsufficient := ClassifyCindyBalanceInsufficient(account, statusCode, responseBody) != CindyBalanceSignalNone
	if account != nil && account.Platform == PlatformGrok && isGrokContentPolicyRejection(statusCode, responseBody) {
		return false
	}
	// Any non-2xx upstream HTTP response means the model request was actually sent.
	if s != nil {
		scheduleOllamaCloudUsageActivity(s.deferredService, account)
	}
	if statusCode == http.StatusForbidden && account != nil &&
		IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		if hit, _, _ := detectOpenAICyberPolicy(responseBody); hit {
			return false
		}
		if s == nil || s.rateLimitService == nil {
			return false
		}
		stateCtx, cancel := openAIAccountStateContext(ctx)
		defer cancel()
		// handleOpenAI403 owns the exact 10-minute cooldown / 3-strike disable
		// policy and deliberately ignores HTML/WAF bodies. Call it directly so
		// pool-mode compatibility cannot bypass Cindy's evidence-based 403 policy.
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(responseBody)))
		return s.rateLimitService.handleOpenAI403(stateCtx, account, upstreamMsg, responseBody)
	}
	if isOpenAIAccount(account) {
		switch statusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
			// Authentication and quota responses are handled exclusively by the
			// request failover state: no same-account replay, runtime cooldown, then
			// switch. Do not persist legacy schedulable/error state here.
			if !cindyBalanceInsufficient {
				return false
			}
		}
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if cindyBalanceInsufficient {
		if s == nil || s.rateLimitService == nil {
			return false
		}
		shouldDisable := s.rateLimitService.HandleUpstreamError(stateCtx, account, statusCode, headers, responseBody)
		if shouldDisable && account.CindyBalanceInsufficientAt != nil {
			s.BlockAccountScheduling(account, time.Time{}, "cindy_balance_insufficient")
		}
		return shouldDisable
	}

	if account != nil && account.Platform == PlatformOpenAI && isOpenAIContextWindowError("", responseBody) {
		return false
	}

	if isOpenAIImageRateLimitError(statusCode, responseBody) {
		if s != nil && s.rateLimitService != nil {
			_ = s.rateLimitService.HandleOpenAIImageRateLimit(stateCtx, account, statusCode, headers, responseBody)
		}
		return false
	}

	if s == nil || account == nil {
		return false
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
	if statusCode == http.StatusTooManyRequests {
		s.markOpenAIOAuth429RateLimited(stateCtx, account, headers, responseBody)
	}
	if s.rateLimitService == nil {
		return false
	}
	shouldDisable := s.rateLimitService.HandleUpstreamError(stateCtx, account, statusCode, headers, responseBody)
	modelTempMatched := statusCode != http.StatusUnauthorized && tempUnschedulableModel(stateCtx, nil) != "" &&
		len(matchTempUnschedulableRules(account, statusCode, responseBody)) > 0
	if shouldDisable && !modelTempMatched {
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
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

// handleCindyBalanceTerminalEvent consumes an in-band HTTP-200 SSE/WS event
// before any generic error rewriting can discard the structured Cindy signal.
func (s *OpenAIGatewayService) handleCindyBalanceTerminalEvent(ctx context.Context, account *Account, headers http.Header, payload []byte, canonicalModel ...string) bool {
	if ClassifyCindyBalanceInsufficient(account, http.StatusOK, payload) == CindyBalanceSignalNone {
		if IsAmbiguousCindyBalanceTerminalEvent(account, payload) {
			s.scheduleAmbiguousCindyBalanceRecheck(account)
		}
		return false
	}
	if account != nil && account.CindyBalanceInsufficientAt != nil {
		s.BlockAccountScheduling(account, time.Time{}, "cindy_balance_insufficient")
		return true
	}
	// Preserve the event's real HTTP-200 transport status for the centralized
	// classifier. The client-facing failover is still normalized to 429, but
	// reclassifying response.failed as an HTTP 429 would discard its terminal
	// event shape and skip durable balance persistence.
	s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusOK, headers, payload, canonicalModel...)
	// Classification itself is authoritative. Even a partially constructed
	// service must emit a no-same-account failover instead of reinterpreting the
	// event as an ordinary transient error.
	return true
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

	cooldownUntil := time.Now().Add(openAIOAuth429FallbackCooldown)
	if s.rateLimitService != nil {
		if resetAt := s.rateLimitService.calculateOpenAI429ResetTime(headers); resetAt != nil && resetAt.After(time.Now()) {
			cooldownUntil = *resetAt
		} else if resetUnix := parseOpenAIRateLimitResetTime(responseBody); resetUnix != nil {
			if resetAt := time.Unix(*resetUnix, 0); resetAt.After(time.Now()) {
				cooldownUntil = resetAt
			}
		} else if cooldown, ok := s.rateLimitService.get429FallbackCooldown(ctx, account); ok && cooldown > 0 {
			cooldownUntil = time.Now().Add(cooldown)
		}
	}
	s.BlockAccountScheduling(account, cooldownUntil, "429")
}

func (s *OpenAIGatewayService) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	if s == nil || !isOpenAIAccount(account) {
		return
	}
	if until.IsZero() && reason == "cindy_balance_insufficient" {
		_ = s.blockCindyBalanceScheduling(account)
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	defer mu.Unlock()
	_, _ = s.blockAccountSchedulingLocked(account, until, reason)
	blockUntil := until
	if value, ok := s.openaiAccountRuntimeBlockUntil.Load(account.ID); ok {
		if current, valid := value.(time.Time); valid && current.After(blockUntil) {
			blockUntil = current
		}
	}
	if account.Type != AccountTypeAPIKey {
		s.persistOpenAIRuntimeBreaker(context.Background(), account.ID, "", reason, blockUntil)
	}
}

func (s *OpenAIGatewayService) blockCindyBalanceScheduling(account *Account) cindyBalanceRuntimeBlockToken {
	if s == nil || account == nil ||
		!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return cindyBalanceRuntimeBlockToken{}
	}
	fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	if err != nil {
		return cindyBalanceRuntimeBlockToken{}
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	defer mu.Unlock()
	token := cindyBalanceRuntimeBlockToken{}
	if previous, ok := s.openaiAccountRuntimeBlockUntil.Load(account.ID); ok {
		if previousUntil, valid := previous.(time.Time); valid {
			if previousUntil.IsZero() {
				storedFingerprint, _ := s.cindyBalanceRuntimeBlockFingerprint.Load(account.ID)
				if storedFingerprint != fingerprint {
					token.generation = s.openaiAccountRuntimeBlockSequence.Add(1)
					token.changed = true
					s.openaiAccountRuntimeBlockGeneration.Store(account.ID, token.generation)
					s.cindyBalanceRuntimeBlockFingerprint.Store(account.ID, fingerprint)
					return token
				}
				return token
			}
			token.hadPrevious = true
			token.previous = previousUntil
		}
	}
	token.generation, token.changed = s.blockAccountSchedulingLocked(account, time.Time{}, "cindy_balance_insufficient")
	if token.changed {
		s.cindyBalanceRuntimeBlockFingerprint.Store(account.ID, fingerprint)
	}
	return token
}

func (s *OpenAIGatewayService) clearCindyBalanceSchedulingBlock(accountID int64, token cindyBalanceRuntimeBlockToken) {
	if s == nil || accountID <= 0 || !token.changed {
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	generationValue, ok := s.openaiAccountRuntimeBlockGeneration.Load(accountID)
	currentGeneration, valid := generationValue.(uint64)
	if !ok || !valid || currentGeneration != token.generation {
		return
	}
	currentValue, ok := s.openaiAccountRuntimeBlockUntil.Load(accountID)
	current, valid := currentValue.(time.Time)
	if !ok || !valid || !current.IsZero() {
		return
	}
	if token.hadPrevious && token.previous.After(time.Now()) {
		s.openaiAccountRuntimeBlockUntil.Store(accountID, token.previous)
	} else {
		s.openaiAccountRuntimeBlockUntil.Delete(accountID)
	}
	s.cindyBalanceRuntimeBlockFingerprint.Delete(accountID)
	s.openaiAccountRuntimeBlockGeneration.Store(accountID, s.openaiAccountRuntimeBlockSequence.Add(1))
}

func (s *OpenAIGatewayService) openAIRuntimeBreakerStore() (OpenAIRuntimeBreakerStore, bool) {
	if s == nil || s.cache == nil {
		return nil, false
	}
	store, ok := s.cache.(OpenAIRuntimeBreakerStore)
	return store, ok && store != nil
}

func (s *OpenAIGatewayService) cindyBalancePendingStore() (CindyBalancePendingStore, bool) {
	if s == nil || s.cache == nil {
		return nil, false
	}
	store, ok := s.cache.(CindyBalancePendingStore)
	return store, ok && store != nil
}

// ClearCindyBalancePending is used by the explicit admin recovery path. It is
// deliberately separate from ClearAccountSchedulingBlock: ordinary runtime
// recovery must never erase a durable Cindy budget signal.
func (s *OpenAIGatewayService) ClearCindyBalancePending(ctx context.Context, accountID int64) error {
	store, ok := s.cindyBalancePendingStore()
	if !ok || accountID <= 0 {
		return nil
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	return store.ClearCindyBalancePending(stateCtx, accountID)
}

// CancelCindyBalancePersistenceRetry invalidates and drains any stale
// persistence retry before the admin recovery path clears durable state.
func (s *OpenAIGatewayService) CancelCindyBalancePersistenceRetry(accountID int64) {
	if s == nil || s.rateLimitService == nil {
		return
	}
	s.rateLimitService.CancelCindyBalancePersistenceRetry(accountID)
}

// withCindyBalancePendingSnapshot loads every not-yet-covered strict Cindy
// account with one Redis MGET and records the result in the request context.
// Initial filtering, compatibility checks and fresh DB rechecks all reuse it.
func (s *OpenAIGatewayService) withCindyBalancePendingSnapshot(ctx context.Context, accounts []Account) context.Context {
	ctx = ensureCindyBalancePendingSnapshotContext(ctx)
	snapshot := cindyBalancePendingSnapshotFromContext(ctx)
	accountIDs := snapshot.unknownStrictCindyAccountIDs(accounts)
	if len(accountIDs) == 0 {
		return ctx
	}
	store, ok := s.cindyBalancePendingStore()
	if !ok {
		// The store is an optional extension for alternate/test cache adapters.
		// Production GatewayCache always implements it.
		snapshot.record(accountIDs, nil, nil)
		return ctx
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cindyBalancePendingReadTimeout)
	pending, err := store.HasCindyBalancePendingBatch(stateCtx, accountIDs)
	cancel()
	snapshot.record(accountIDs, pending, err)
	if err != nil {
		slog.Error("cindy_balance_pending_batch_read_failed_closed", "account_count", len(accountIDs), "error", err)
	}
	return ctx
}

func (s *OpenAIGatewayService) isCindyBalancePendingBlocked(ctx context.Context, account *Account) bool {
	if s == nil || account == nil || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return false
	}
	ctx = ensureCindyBalancePendingSnapshotContext(ctx)
	snapshot := cindyBalancePendingSnapshotFromContext(ctx)
	state, loaded := snapshot.state(account.ID)
	if !loaded {
		ctx = s.withCindyBalancePendingSnapshot(ctx, []Account{*account})
		snapshot = cindyBalancePendingSnapshotFromContext(ctx)
		state, loaded = snapshot.state(account.ID)
	}
	if !loaded || state == cindyBalancePendingSnapshotClear {
		return false
	}
	if state == cindyBalancePendingSnapshotReadFailed {
		return true
	}
	if state == cindyBalancePendingSnapshotValidatedBlocked {
		return true
	}
	store, ok := s.cindyBalancePendingStore()
	if !ok {
		snapshot.recordState(account.ID, cindyBalancePendingSnapshotReadFailed)
		return true
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	pendingFingerprint, pendingErr := store.GetCindyBalancePendingFingerprint(stateCtx, account.ID)
	cancel()
	currentFingerprint, fingerprintErr := CindyAccountIdentityFingerprint(
		account.Platform,
		account.Type,
		account.Credentials,
	)
	if pendingErr != nil || fingerprintErr != nil {
		snapshot.recordState(account.ID, cindyBalancePendingSnapshotReadFailed)
		return true
	}
	if pendingFingerprint == "" {
		snapshot.recordState(account.ID, cindyBalancePendingSnapshotClear)
		return false
	}
	if pendingFingerprint != currentFingerprint {
		clearCtx, clearCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		_ = store.ClearCindyBalancePendingIfFingerprintMatches(clearCtx, account.ID, pendingFingerprint)
		clearCancel()
		snapshot.recordState(account.ID, cindyBalancePendingSnapshotClear)
		return false
	}
	snapshot.recordState(account.ID, cindyBalancePendingSnapshotValidatedBlocked)
	// Rebuild the in-memory DB retry after a service/process restart. The
	// request-scoped snapshot remains authoritative for the whole selection.
	if account.CindyBalanceInsufficientAt == nil && s.rateLimitService != nil {
		s.rateLimitService.scheduleCindyBalancePersistenceRetry(
			account,
			time.Now().UTC(),
			pendingFingerprint,
			cindyBalanceRuntimeBlockToken{},
		)
	}
	return true
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

func (s *OpenAIGatewayService) blockAccountSchedulingLocked(account *Account, until time.Time, reason string) (uint64, bool) {
	generation := s.openaiAccountRuntimeBlockSequence.Add(1)
	s.openaiAccountRuntimeBlockGeneration.Store(account.ID, generation)
	now := time.Now()
	blockUntil := until
	indefinite := blockUntil.IsZero() && reason == "cindy_balance_insufficient"
	if !indefinite && (blockUntil.IsZero() || !blockUntil.After(now)) {
		blockUntil = now.Add(openAIStopSchedulingBridgeCooldown)
	}

	for {
		current, loaded := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
		if !loaded {
			actual, stored := s.openaiAccountRuntimeBlockUntil.LoadOrStore(account.ID, blockUntil)
			if !stored {
				return generation, true
			}
			current = actual
		}

		currentUntil, ok := current.(time.Time)
		if !ok {
			if s.openaiAccountRuntimeBlockUntil.CompareAndSwap(account.ID, current, blockUntil) {
				return generation, true
			}
			continue
		}
		// A stored zero time is the Cindy balance fail-closed sentinel. It must
		// dominate every later finite cooldown until an explicit clear removes it.
		if currentUntil.IsZero() || (!blockUntil.IsZero() && !blockUntil.After(currentUntil)) {
			return generation, false
		}
		if s.openaiAccountRuntimeBlockUntil.CompareAndSwap(account.ID, current, blockUntil) {
			return generation, true
		}
	}
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlock(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	mu := s.openAIAccountRuntimeBlockLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	s.openaiAccountRuntimeBlockUntil.Delete(accountID)
	s.cindyBalanceRuntimeBlockFingerprint.Delete(accountID)
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
		if blockUntil, valid := value.(time.Time); valid && (blockUntil.IsZero() || time.Now().Before(blockUntil)) {
			mu.Unlock()
			return
		}
	}
	s.openaiAccountRuntimeBlockUntil.Delete(accountID)
	s.cindyBalanceRuntimeBlockFingerprint.Delete(accountID)
	s.openaiAccountRuntimeBlockGeneration.Store(accountID, s.openaiAccountRuntimeBlockSequence.Add(1))
	mu.Unlock()
	s.clearOpenAIRuntimeBreaker(context.Background(), accountID, "", owner)
}

func (s *OpenAIGatewayService) isOpenAIAccountRuntimeBlocked(account *Account) bool {
	if s == nil || account == nil || account.ID <= 0 {
		return false
	}
	isOpenAI := isOpenAIAccount(account)
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	value, ok := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	if !isOpenAI {
		if ok {
			cooldownUntil, valid := value.(time.Time)
			_, fingerprintLoaded := s.cindyBalanceRuntimeBlockFingerprint.Load(account.ID)
			if valid && cooldownUntil.IsZero() && fingerprintLoaded {
				s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
				s.cindyBalanceRuntimeBlockFingerprint.Delete(account.ID)
				s.openaiAccountRuntimeBlockGeneration.Store(account.ID, s.openaiAccountRuntimeBlockSequence.Add(1))
			}
		}
		mu.Unlock()
		return false
	}
	if ok {
		cooldownUntil, valid := value.(time.Time)
		if !valid {
			s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
			s.cindyBalanceRuntimeBlockFingerprint.Delete(account.ID)
			s.openaiAccountRuntimeBlockGeneration.Store(account.ID, s.openaiAccountRuntimeBlockSequence.Add(1))
		} else if cooldownUntil.IsZero() {
			storedFingerprint, fingerprintLoaded := s.cindyBalanceRuntimeBlockFingerprint.Load(account.ID)
			currentFingerprint, fingerprintErr := CindyAccountIdentityFingerprint(
				account.Platform,
				account.Type,
				account.Credentials,
			)
			if fingerprintLoaded && fingerprintErr == nil && storedFingerprint != currentFingerprint {
				s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
				s.cindyBalanceRuntimeBlockFingerprint.Delete(account.ID)
				s.openaiAccountRuntimeBlockGeneration.Store(account.ID, s.openaiAccountRuntimeBlockSequence.Add(1))
				mu.Unlock()
				return false
			}
			mu.Unlock()
			return true
		} else if time.Now().Before(cooldownUntil) {
			mu.Unlock()
			return true
		} else {
			s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
			s.cindyBalanceRuntimeBlockFingerprint.Delete(account.ID)
			s.openaiAccountRuntimeBlockGeneration.Store(account.ID, s.openaiAccountRuntimeBlockSequence.Add(1))
		}
	}
	mu.Unlock()
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

func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlocked(account *Account, requestedModel string) bool {
	return s.isOpenAIAccountRequestRuntimeBlockedContext(ensureOpenAIRuntimeBreakerProbeOwner(context.Background()), account, requestedModel)
}

func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlockedContext(ctx context.Context, account *Account, requestedModel string) bool {
	if s == nil || account == nil {
		return false
	}
	if s.isOpenAIAccountRuntimeBlocked(account) || s.isOpenAIAccountModelRuntimeBlocked(account, requestedModel) {
		return true
	}
	if s.isCindyBalancePendingBlocked(ctx, account) {
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
		until := now.Add(openAIOAuth429FallbackCooldown)
		if resetAt := parseRetryAfterResetTime(failoverErr.ResponseHeaders, now); resetAt != nil && resetAt.After(until) {
			until = *resetAt
		}
		if s.rateLimitService != nil {
			if resetAt := s.rateLimitService.calculateOpenAI429ResetTime(failoverErr.ResponseHeaders); resetAt != nil && resetAt.After(until) {
				until = *resetAt
			}
		}
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
