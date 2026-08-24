package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

var (
	ErrCindyAccountRequired           = infraerrors.BadRequest("CINDY_ACCOUNT_REQUIRED", "account is not a Cindy API key account")
	ErrCindyInsufficientDeleteChanged = infraerrors.Conflict(
		"CINDY_INSUFFICIENT_DELETE_CHANGED",
		"matching Cindy accounts changed; reload and confirm again",
	)
)

type CindyInsufficientDeletePreview struct {
	Count       int    `json:"count"`
	Fingerprint string `json:"fingerprint"`
}

type CindyInsufficientDeleteResult struct {
	DeletedCount          int     `json:"deleted_count"`
	DependentDeletedCount int     `json:"dependent_deleted_count"`
	DeletedAccountIDs     []int64 `json:"-"`
}

type CindyBannedAccountRepository interface {
	PreviewCindyBannedDeletion(ctx context.Context) (*CindyInsufficientDeletePreview, error)
	DeleteCindyBanned(ctx context.Context, expectedCount int, fingerprint string) (*CindyInsufficientDeleteResult, error)
}

// CindyBalanceSignal identifies the exact structured Cindy budget signal that
// was observed. The transport shape is part of the contract: an HTTP error is
// not interchangeable with an in-band Responses/WebSocket terminal event.
type CindyBalanceSignal uint8

const (
	CindyBalanceSignalNone CindyBalanceSignal = iota
	CindyBalanceSignalHTTP429
	CindyBalanceSignalResponseFailed
	CindyBalanceSignalErrorEvent
)

// CindyBalanceAccountRepository is intentionally separate from AccountRepository
// so focused gateway test doubles do not need to implement administrative cleanup.
type CindyBalanceAccountRepository interface {
	ClearCindyBalanceInsufficient(ctx context.Context, accountID int64) (bool, error)
	PreviewCindyInsufficientDeletion(ctx context.Context) (*CindyInsufficientDeletePreview, error)
	DeleteCindyInsufficient(ctx context.Context, expectedCount int, fingerprint string) (*CindyInsufficientDeleteResult, error)
}

// CindyBalancePendingStore exposes the Redis marker used by releases before
// v0.1.177 while a database marker was being committed. Durable admin probe
// jobs no longer create this cache authority; they clear matching leftovers
// after their lease-guarded database transaction. The interface remains for a
// one-version rolling-upgrade cleanup path and alternate cache adapters.
type CindyBalancePendingStore interface {
	GetCindyBalancePendingFingerprint(ctx context.Context, accountID int64) (string, error)
	HasCindyBalancePendingBatch(ctx context.Context, accountIDs []int64) (map[int64]bool, error)
	ClearCindyBalancePending(ctx context.Context, accountID int64) error
	ClearCindyBalancePendingIfFingerprintMatches(ctx context.Context, accountID int64, credentialsFingerprint string) error
}

type cindyBalancePendingSnapshotState uint8

const (
	cindyBalancePendingSnapshotClear cindyBalancePendingSnapshotState = iota + 1
	cindyBalancePendingSnapshotBlocked
	cindyBalancePendingSnapshotReadFailed
)

type cindyBalancePendingSnapshotContextKey struct{}

// cindyBalancePendingSnapshot is request-scoped so one request does not repeat
// best-effort cleanup reads for the same legacy marker.
type cindyBalancePendingSnapshot struct {
	mu     sync.RWMutex
	states map[int64]cindyBalancePendingSnapshotState
}

func ensureCindyBalancePendingSnapshotContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if snapshot, _ := ctx.Value(cindyBalancePendingSnapshotContextKey{}).(*cindyBalancePendingSnapshot); snapshot != nil {
		return ctx
	}
	return context.WithValue(ctx, cindyBalancePendingSnapshotContextKey{}, &cindyBalancePendingSnapshot{
		states: make(map[int64]cindyBalancePendingSnapshotState),
	})
}

func cindyBalancePendingSnapshotFromContext(ctx context.Context) *cindyBalancePendingSnapshot {
	if ctx == nil {
		return nil
	}
	snapshot, _ := ctx.Value(cindyBalancePendingSnapshotContextKey{}).(*cindyBalancePendingSnapshot)
	return snapshot
}

func (s *cindyBalancePendingSnapshot) state(accountID int64) (cindyBalancePendingSnapshotState, bool) {
	if s == nil {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[accountID]
	return state, ok
}

func (s *cindyBalancePendingSnapshot) unknownStrictCindyAccountIDs(accounts []Account) []int64 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]int64, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.ID <= 0 || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			continue
		}
		if _, loaded := s.states[account.ID]; loaded {
			continue
		}
		if _, duplicate := seen[account.ID]; duplicate {
			continue
		}
		seen[account.ID] = struct{}{}
		ids = append(ids, account.ID)
	}
	return ids
}

func (s *cindyBalancePendingSnapshot) record(accountIDs []int64, pending map[int64]bool, readErr error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, accountID := range accountIDs {
		state := cindyBalancePendingSnapshotClear
		if readErr != nil {
			state = cindyBalancePendingSnapshotReadFailed
		} else if pending[accountID] {
			state = cindyBalancePendingSnapshotBlocked
		}
		s.states[accountID] = state
	}
}

func (s *cindyBalancePendingSnapshot) recordState(accountID int64, state cindyBalancePendingSnapshotState) {
	if s == nil || accountID <= 0 {
		return
	}
	s.mu.Lock()
	s.states[accountID] = state
	s.mu.Unlock()
}

// IsCindyBalanceInsufficientResponse trusts only Cindy's structured budget
// fields. Generic 402, ordinary 429, and message text retain their normal path.
func IsCindyBalanceInsufficientResponse(account *Account, statusCode int, responseBody []byte) bool {
	return ClassifyCindyBalanceInsufficient(account, statusCode, responseBody) == CindyBalanceSignalHTTP429
}

func newCindyBalanceTerminalFailover(headers http.Header) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:               http.StatusTooManyRequests,
		ResponseBody:             append([]byte(nil), openAITransportFailoverBody...),
		ResponseHeaders:          headers.Clone(),
		RetryableOnSameAccount:   false,
		Scope:                    GatewayFailureScopeAccount,
		NextAccountAction:        NextAccountRetry,
		CindyBalanceInsufficient: true,
	}
}

func (s *OpenAIGatewayService) cindyBalanceTerminalFailover(
	ctx context.Context,
	account *Account,
	headers http.Header,
	payload []byte,
	canonicalModel ...string,
) (*UpstreamFailoverError, bool) {
	if s == nil || !s.handleCindyBalanceTerminalEvent(ctx, account, headers, payload, canonicalModel...) {
		return nil, false
	}
	return newCindyBalanceTerminalFailover(headers), true
}

// HTTP terminal events are valid only on an exact 200 response. WebSocket
// callers use cindyBalanceTerminalFailover directly because they do not have
// an HTTP response status; HTTP callers must preserve the observed status here
// so a 201/202 body cannot masquerade as an in-band budget event.
func (s *OpenAIGatewayService) cindyBalanceHTTPResponseTerminalFailover(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	payload []byte,
	canonicalModel ...string,
) (*UpstreamFailoverError, bool) {
	if statusCode != http.StatusOK {
		return nil, false
	}
	return s.cindyBalanceTerminalFailover(ctx, account, headers, payload, canonicalModel...)
}

func (s *OpenAIGatewayService) handleCindyBalanceHTTPResponseTerminalEvent(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	payload []byte,
	canonicalModel ...string,
) bool {
	if statusCode != http.StatusOK {
		return false
	}
	return s.handleCindyBalanceTerminalEvent(ctx, account, headers, payload, canonicalModel...)
}

// ClassifyCindyBalanceInsufficient accepts only the three observed structured
// shapes. It deliberately rejects message text, numeric codes, generic 402s,
// malformed JSON, and the same payload on a non-Cindy account.
func ClassifyCindyBalanceInsufficient(account *Account, statusCode int, payload []byte) CindyBalanceSignal {
	if !CindyBalanceDetectionFeatureEnabled() || account == nil || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		!gjson.ValidBytes(payload) {
		return CindyBalanceSignalNone
	}

	if statusCode == http.StatusTooManyRequests && cindyBudgetErrorAtPath(payload, "error") {
		return CindyBalanceSignalHTTP429
	}

	eventType := gjson.GetBytes(payload, "type")
	if eventType.Type == gjson.String &&
		(eventType.Str == "response.failed" || eventType.Str == "error") &&
		statusCode != http.StatusOK {
		return CindyBalanceSignalNone
	}

	switch {
	case eventType.Type == gjson.String && eventType.Str == "response.failed" &&
		cindyBudgetErrorAtPath(payload, "response.error"):
		return CindyBalanceSignalResponseFailed
	case eventType.Type == gjson.String && eventType.Str == "error" &&
		cindyBudgetErrorAtPath(payload, "error"):
		return CindyBalanceSignalErrorEvent
	default:
		return CindyBalanceSignalNone
	}
}

func cindyBudgetErrorAtPath(payload []byte, path string) bool {
	errorType := gjson.GetBytes(payload, path+".type")
	errorCode := gjson.GetBytes(payload, path+".code")
	return errorType.Type == gjson.String && errorType.Str == "budget_exceeded" &&
		errorCode.Type == gjson.String && errorCode.Str == strconv.Itoa(http.StatusTooManyRequests)
}

func cindyBalanceReplayBufferEnabled(account *Account) bool {
	return CindyBalanceDetectionFeatureEnabled() && account != nil &&
		IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
}

func CindyInsufficientAccountFingerprint(accountIDs []int64) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("cindy-insufficient:v1\n"))
	for _, accountID := range accountIDs {
		_, _ = hash.Write([]byte(strconv.FormatInt(accountID, 10)))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func NormalizeCindyInsufficientFingerprint(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// CindyAccountIdentityFingerprint produces a deterministic, domain-separated
// digest of the platform, account type, and complete JSON credential object.
// Callers may persist the digest, but must never log or persist the raw
// identity snapshot.
func CindyAccountIdentityFingerprint(platform, accountType string, credentials map[string]any) (string, error) {
	payload := struct {
		Platform    string         `json:"platform"`
		AccountType string         `json:"account_type"`
		Credentials map[string]any `json:"credentials"`
	}{
		Platform:    platform,
		AccountType: accountType,
		Credentials: credentials,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("cindy-account-identity:v2\n"))
	_, _ = hash.Write(raw)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CindyCredentialsFingerprint returns the identity fingerprint for a strict
// Cindy OpenAI API-key account. It is kept for cache fixtures and adapters
// that already operate exclusively inside that identity boundary.
func CindyCredentialsFingerprint(credentials map[string]any) (string, error) {
	return CindyAccountIdentityFingerprint(PlatformOpenAI, AccountTypeAPIKey, credentials)
}

// NormalizeCindyCredentialsFingerprint accepts only a canonical SHA-256 hex digest.
func NormalizeCindyCredentialsFingerprint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}
