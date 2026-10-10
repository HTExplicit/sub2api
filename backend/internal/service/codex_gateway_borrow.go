package service

// Cookie scope, the 230-second lease and two-shot STATE comparison are adapted
// from ranxi2001/sub2api at 5ca3cca21eeaf4ca8a694a7f2f8f0ecd9575c549:
// backend/internal/repository/{codex_gateway_pin,astra_target_validation}.go
// and backend/internal/service/openai_codex_state_probe.go (LGPL-3.0).
// This native implementation owns no exit routing, account health or quota.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const SettingKeyCodexGatewayBorrowConfig = "codex_gateway_borrow_config"

const (
	codexGatewayBorrowTTL          = 230 * time.Second
	codexGatewayBorrowTimeout      = 90 * time.Second
	codexGatewayBorrowShotTimeout  = 45 * time.Second
	codexGatewayBorrowFailureWait  = 15 * time.Second
	codexGatewayBorrowStoreTimeout = 5 * time.Second
	codexGatewayBorrowSourceModel  = "gpt-6-astra"
)

var (
	ErrCodexGatewayBorrowUnavailable = infraerrors.ServiceUnavailable("CODEX_GATEWAY_BORROW_UNAVAILABLE", "no qualified Codex gateway borrow route is available")
	ErrCodexGatewayBorrowBusy        = infraerrors.Conflict("CODEX_GATEWAY_BORROW_BUSY", "target validation is already in progress")
	ErrCodexGatewayBorrowChanged     = infraerrors.Conflict("CODEX_GATEWAY_BORROW_CHANGED", "gateway borrow configuration or route changed")
)

// A local route qualification failure is distinct from an actual business
// request failing upstream. Callers must surface it before ordinary account
// failover, stream-timeout or health hooks. Unwrap preserves the specific cause.
type CodexGatewayBorrowFailure struct {
	Revision          uint64
	CookieFingerprint string
	Cause             error
	Stage             string
	Reason            string
	Detail            string
	RetryAfter        *time.Time
	Verification      *CodexGatewayBorrowVerification
}

func (e *CodexGatewayBorrowFailure) Error() string {
	if e == nil || e.Cause == nil {
		return "Codex gateway borrow qualification failed"
	}
	detail := e.Cause.Error()
	if e.Detail != "" {
		detail += ": " + e.Detail
	}
	if e.Reason != "" {
		detail = e.Stage + "/" + e.Reason + ": " + detail
	}
	return "Codex gateway borrow qualification failed: " + detail
}
func (e *CodexGatewayBorrowFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
func IsCodexGatewayBorrowFailure(err error) bool {
	var failure *CodexGatewayBorrowFailure
	return errors.As(err, &failure)
}

func borrowVerificationFailure(check codexGatewayBorrowTargetCheck, revision uint64) error {
	result := check.result
	failure := &CodexGatewayBorrowFailure{Revision: revision, Cause: ErrCodexGatewayBorrowUnavailable, Stage: "target_validation", Reason: result.Reason, Detail: result.Error, Verification: &result, CookieFingerprint: check.cookieKey}
	if !check.retryAfter.IsZero() {
		retry := check.retryAfter
		failure.RetryAfter = &retry
	}
	return failure
}

func codexBorrowFailureStage(err error) (string, string) {
	var failure *CodexGatewayBorrowFailure
	if errors.As(err, &failure) && failure.Reason != "" {
		return failure.Stage, failure.Reason
	}
	if errors.Is(err, ErrCodexGatewayBorrowChanged) {
		return "qualification", "configuration_changed"
	}
	if errors.Is(err, context.Canceled) {
		return "preparation", "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "preparation", "deadline_exceeded"
	}
	return "preparation", "borrow_unavailable"
}

// CodexGatewayBorrowProbeUpstream is deliberately a distinct injection type.
// Its implementation must own separate clients, pools and TLS caches, including
// separation between source acquisition and target observation.
type CodexGatewayBorrowProbeUpstream interface{ HTTPUpstream }

type CodexGatewayBorrowConfig struct {
	Enabled          bool     `json:"enabled"`
	SourceAccountIDs []int64  `json:"source_account_ids"`
	TargetAccountIDs []int64  `json:"target_account_ids"`
	Models           []string `json:"models"`
}

func DefaultCodexGatewayBorrowConfig() CodexGatewayBorrowConfig {
	return CodexGatewayBorrowConfig{SourceAccountIDs: []int64{}, TargetAccountIDs: []int64{}, Models: []string{"gpt-6-astra", "gpt-6.1-sol"}}
}

func DecodeCodexGatewayBorrowConfig(raw []byte) (CodexGatewayBorrowConfig, error) {
	cfg := DefaultCodexGatewayBorrowConfig()
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return cfg, errors.New("configuration must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return cfg, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return cfg, errors.New("configuration must contain one JSON object")
	}
	return normalizeCodexGatewayBorrowConfig(cfg)
}

func normalizeCodexGatewayBorrowConfig(cfg CodexGatewayBorrowConfig) (CodexGatewayBorrowConfig, error) {
	if cfg.SourceAccountIDs == nil {
		cfg.SourceAccountIDs = []int64{}
	}
	if cfg.TargetAccountIDs == nil {
		cfg.TargetAccountIDs = []int64{}
	}
	if cfg.Models == nil {
		cfg.Models = DefaultCodexGatewayBorrowConfig().Models
	}
	seen := map[int64]bool{}
	for _, ids := range [][]int64{cfg.SourceAccountIDs, cfg.TargetAccountIDs} {
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return cfg, errors.New("source and target account IDs must be positive, unique and disjoint")
			}
			seen[id] = true
		}
	}
	models := map[string]bool{}
	for _, model := range cfg.Models {
		if (model != "gpt-6-astra" && model != "gpt-6.1-sol") || models[model] {
			return cfg, errors.New("models must be a unique selection of gpt-6-astra and gpt-6.1-sol")
		}
		models[model] = true
	}
	if cfg.Enabled && (len(cfg.SourceAccountIDs) == 0 || len(cfg.TargetAccountIDs) == 0 || len(cfg.Models) == 0) {
		return cfg, errors.New("enabled gateway borrowing requires sources, targets and models")
	}
	return cloneCodexGatewayBorrowConfig(cfg), nil
}

func cloneCodexGatewayBorrowConfig(cfg CodexGatewayBorrowConfig) CodexGatewayBorrowConfig {
	cfg.SourceAccountIDs = append([]int64{}, cfg.SourceAccountIDs...)
	cfg.TargetAccountIDs = append([]int64{}, cfg.TargetAccountIDs...)
	cfg.Models = append([]string{}, cfg.Models...)
	return cfg
}

type CodexGatewayBorrowCandidateStatus struct {
	SourceRequestEncoding string    `json:"source_request_encoding,omitempty"`
	SourceAccountID       int64     `json:"source_account_id"`
	ExpiresAt             time.Time `json:"expires_at"`
	RemainingSeconds      int64     `json:"remaining_seconds"`
	CookieFingerprint     string    `json:"cookie_fingerprint"`
}

type CodexGatewayBorrowSourceStatus struct {
	AccountID        int64      `json:"account_id"`
	State            string     `json:"state"`
	Reason           string     `json:"reason"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RemainingSeconds int64      `json:"remaining_seconds"`
	Error            string     `json:"error,omitempty"`
}

type CodexGatewayBorrowVerification struct {
	MintRequestEncoding     string     `json:"mint_request_encoding,omitempty"`
	ContinueRequestEncoding string     `json:"continue_request_encoding,omitempty"`
	RequestShape            string     `json:"request_shape,omitempty"`
	ServiceTier             string     `json:"service_tier,omitempty"`
	MintCompleted           bool       `json:"mint_completed"`
	ContinueCompleted       bool       `json:"continue_completed"`
	MintStateLength         int        `json:"mint_state_length"`
	ContinueStateLength     int        `json:"continue_state_length"`
	RouteChanged            bool       `json:"route_changed"`
	AccountID               int64      `json:"account_id"`
	Model                   string     `json:"model"`
	Success                 bool       `json:"success"`
	Reason                  string     `json:"reason"`
	Error                   string     `json:"error,omitempty"`
	MintStatus              int        `json:"mint_status"`
	ContinueStatus          int        `json:"continue_status"`
	Minted                  bool       `json:"minted"`
	NewTicket               bool       `json:"new_ticket"`
	ReportedModel           string     `json:"reported_model,omitempty"`
	CheckedAt               time.Time  `json:"checked_at"`
	ExpiresAt               *time.Time `json:"expires_at,omitempty"`
}

type CodexGatewayBorrowTargetStatus struct {
	CodexGatewayBorrowVerification
	State            string     `json:"state"`
	CacheValid       bool       `json:"cache_valid"`
	RemainingSeconds int64      `json:"remaining_seconds"`
	RetryAfter       *time.Time `json:"retry_after,omitempty"`
}

type CodexGatewayBorrowStatus struct {
	ObservedSince time.Time                          `json:"observed_since"`
	RecentUsage   []CodexGatewayBorrowUsage          `json:"recent_usage"`
	Setup         CodexGatewayBorrowSetup            `json:"setup"`
	Enabled       bool                               `json:"enabled"`
	Revision      uint64                             `json:"revision"`
	GeneratedAt   time.Time                          `json:"generated_at"`
	Preparing     bool                               `json:"preparing"`
	Config        CodexGatewayBorrowConfig           `json:"config"`
	Candidate     *CodexGatewayBorrowCandidateStatus `json:"candidate,omitempty"`
	Sources       []CodexGatewayBorrowSourceStatus   `json:"sources"`
	Targets       []CodexGatewayBorrowTargetStatus   `json:"targets"`
	ModelEfforts  map[string][]string                `json:"model_efforts"`
}

// Application contains only opaque identities. The selected cookie itself is
// available solely in the cloned outbound request's Cookie header.
type CodexGatewayBorrowApplication struct {
	Verification      CodexGatewayBorrowVerification
	Applied           bool
	SourceAccountID   int64
	ExpiresAt         time.Time
	Fingerprint       string
	CookieFingerprint string
}

type codexGatewayBorrowCandidate struct {
	sourceRequestEncoding string
	cookie                http.Cookie
	expires               time.Time
	sourceID              int64
}

type codexGatewayBorrowTargetKey struct {
	accountID int64
	model     string
}
type codexGatewayBorrowTargetCheck struct {
	validationID     uint64
	policyRevision   uint64
	identityRevision string
	key              string
	cookieKey        string
	expires          time.Time
	retryAfter       time.Time
	validating       bool
	result           CodexGatewayBorrowVerification
}

type CodexGatewayBorrowService struct {
	rejectedCookie     string
	rejectedCause      error
	rejectedRoutes     map[string]time.Time
	validatingRoutes   map[string]int
	validationSequence uint64
	sourceSequence     uint64
	qualifications     map[string]codexGatewayBorrowTargetCheck
	settings           SettingRepository
	accounts           AccountRepository
	gateway            *OpenAIGatewayService
	upstream           CodexGatewayBorrowProbeUpstream
	// A profile supplied by a business caller is reused verbatim. Current Codex
	// HTTP and WS business paths use standard TLS, so manual preparation uses nil.
	tlsProfiles        *TLSFingerprintProfileService
	storeMu            sync.Mutex
	mu                 sync.Mutex
	config             CodexGatewayBorrowConfig
	loaded             bool
	revision           uint64
	revisionCtx        context.Context
	cancel             context.CancelFunc
	candidate          *codexGatewayBorrowCandidate
	sources            map[int64]CodexGatewayBorrowSourceStatus
	targets            map[codexGatewayBorrowTargetKey]codexGatewayBorrowTargetCheck
	preparing          bool
	prepareRuns        int
	prepareFailedUntil time.Time
	prepareFailure     string
	sourceFlight       codexBorrowFlights
	targetFlight       codexBorrowFlights
	setupFlight        codexBorrowFlights
	setup              CodexGatewayBorrowSetup
	usage              map[codexBorrowUsageKey]CodexGatewayBorrowUsage
	observedSince      time.Time
	wsAnchors          codexGatewayBorrowWSAnchorStore
}

func NewCodexGatewayBorrowService(settings SettingRepository, accounts AccountRepository, gateway *OpenAIGatewayService, upstream CodexGatewayBorrowProbeUpstream, tlsProfiles *TLSFingerprintProfileService) *CodexGatewayBorrowService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &CodexGatewayBorrowService{settings: settings, accounts: accounts, gateway: gateway, upstream: upstream, tlsProfiles: tlsProfiles,
		config: DefaultCodexGatewayBorrowConfig(), revisionCtx: ctx, cancel: cancel,
		sources: map[int64]CodexGatewayBorrowSourceStatus{}, targets: map[codexGatewayBorrowTargetKey]codexGatewayBorrowTargetCheck{}}
	if gateway != nil {
		gateway.SetCodexGatewayBorrowService(s)
	}
	return s
}

func ProvideCodexGatewayBorrowService(settings SettingRepository, accounts AccountRepository, gateway *OpenAIGatewayService, upstream CodexGatewayBorrowProbeUpstream, tlsProfiles *TLSFingerprintProfileService) (*CodexGatewayBorrowService, error) {
	s := NewCodexGatewayBorrowService(settings, accounts, gateway, upstream, tlsProfiles)
	ctx, cancel := context.WithTimeout(context.Background(), codexGatewayBorrowStoreTimeout)
	defer cancel()
	if _, err := s.GetConfig(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *OpenAIGatewayService) SetCodexGatewayBorrowService(borrow *CodexGatewayBorrowService) {
	s.gatewayBorrow = borrow
}

func (s *CodexGatewayBorrowService) ConfigSnapshot() CodexGatewayBorrowConfig {
	if s == nil {
		return DefaultCodexGatewayBorrowConfig()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCodexGatewayBorrowConfig(s.config)
}

func (s *CodexGatewayBorrowService) GetConfig(ctx context.Context) (CodexGatewayBorrowConfig, error) {
	if s == nil || s.settings == nil {
		return DefaultCodexGatewayBorrowConfig(), ErrCodexGatewayBorrowUnavailable
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	dbCtx, cancel := context.WithTimeout(ctx, codexGatewayBorrowStoreTimeout)
	defer cancel()
	raw, err := s.settings.GetValue(dbCtx, SettingKeyCodexGatewayBorrowConfig)
	cfg := DefaultCodexGatewayBorrowConfig()
	if !errors.Is(err, ErrSettingNotFound) {
		if err != nil {
			return cfg, err
		}
		cfg, err = DecodeCodexGatewayBorrowConfig([]byte(raw))
		if err != nil {
			return cfg, fmt.Errorf("stored gateway borrow configuration: %w", err)
		}
	}
	s.publishConfig(cfg, false)
	return cfg, nil
}

func (s *CodexGatewayBorrowService) SaveConfig(ctx context.Context, cfg CodexGatewayBorrowConfig) (CodexGatewayBorrowConfig, error) {
	if s == nil || s.settings == nil || s.accounts == nil {
		return cfg, ErrCodexGatewayBorrowUnavailable
	}
	cfg, err := normalizeCodexGatewayBorrowConfig(cfg)
	if err != nil {
		return cfg, err
	}
	// Validation only loads local rows and resolves existing parent credentials.
	// It never discovers models, touches quota or changes any account fields.
	for _, id := range append(append([]int64{}, cfg.SourceAccountIDs...), cfg.TargetAccountIDs...) {
		a, err := s.accounts.GetByID(ctx, id)
		if err != nil || !codexGatewayBorrowAccountSupported(a) {
			return cfg, fmt.Errorf("account %d must be an existing OpenAI OAuth-like account", id)
		}
		if _, err := resolveCredentialAccount(ctx, s.accounts, a); err != nil {
			return cfg, fmt.Errorf("account %d has no valid credential parent", id)
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	dbCtx, cancel := context.WithTimeout(ctx, codexGatewayBorrowStoreTimeout)
	defer cancel()
	if err := s.settings.Set(dbCtx, SettingKeyCodexGatewayBorrowConfig, string(raw)); err != nil {
		return cfg, err
	}
	s.publishConfig(cfg, true)
	return cfg, nil
}

func (s *CodexGatewayBorrowService) publishConfig(cfg CodexGatewayBorrowConfig, saved bool) {
	s.mu.Lock()
	changed := !s.loaded || !slices.Equal(cfg.SourceAccountIDs, s.config.SourceAccountIDs) || !slices.Equal(cfg.TargetAccountIDs, s.config.TargetAccountIDs) || !slices.Equal(cfg.Models, s.config.Models) || cfg.Enabled != s.config.Enabled
	if !changed && !saved {
		s.mu.Unlock()
		return
	}
	s.cancel()
	s.revisionCtx, s.cancel = context.WithCancel(context.Background())
	s.revision++
	s.config, s.loaded = cloneCodexGatewayBorrowConfig(cfg), true
	s.candidate = nil
	s.rejectedCookie, s.rejectedCause = "", nil
	s.rejectedRoutes = nil
	s.validatingRoutes = nil
	s.setup = CodexGatewayBorrowSetup{}
	if saved && cfg.Enabled {
		s.setup = CodexGatewayBorrowSetup{State: "queued", Total: len(cfg.TargetAccountIDs) * len(cfg.Models), StartedAt: time.Now()}
	}
	s.usage = nil
	s.observedSince = time.Now()
	s.sources = map[int64]CodexGatewayBorrowSourceStatus{}
	s.targets = map[codexGatewayBorrowTargetKey]codexGatewayBorrowTargetCheck{}
	s.qualifications = map[string]codexGatewayBorrowTargetCheck{}
	s.prepareFailedUntil, s.prepareFailure, s.preparing, s.prepareRuns = time.Time{}, "", false, 0
	ctx := s.revisionCtx
	s.mu.Unlock()
	// Exactly one finite preparation per save. Loading settings or restarting
	// never calls a model. Individual source/target deadlines remain 90 seconds.
	if saved && cfg.Enabled {
		go func() {
			prepareCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			_, _ = s.Prepare(prepareCtx)
		}()
	}
}

func (s *CodexGatewayBorrowService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel()
}

func codexGatewayBorrowAccountSupported(a *Account) bool {
	return a != nil && a.IsOpenAIOAuthLike() && !a.IsOpenAIAgentIdentity() && !a.IsSyntheticUITest()
}

func CodexGatewayBorrowRequestEligible(req *http.Request) bool {
	return req != nil && req.URL != nil && req.Method == http.MethodPost && req.URL.Scheme == "https" && req.URL.Host == "chatgpt.com" && req.URL.User == nil &&
		(req.Host == "" || req.Host == "chatgpt.com") && (req.URL.Path == "/backend-api/codex/responses" || req.URL.Path == "/backend-api/codex/responses/lite")
}

func (s *CodexGatewayBorrowService) RequestEligible(req *http.Request) bool {
	return CodexGatewayBorrowRequestEligible(req)
}
func (s *CodexGatewayBorrowService) ModelEligible(model string) bool {
	cfg := s.ConfigSnapshot()
	return cfg.Enabled && slices.Contains(cfg.Models, model)
}

type codexGatewayBorrowModelContextKey struct{}
type codexGatewayBorrowObservationContextKey struct{}

// The token provider may refresh credentials normally, but must not attach an
// observation failure to account health, scheduling or quota.
func WithCodexGatewayBorrowObservation(ctx context.Context) context.Context {
	return context.WithValue(WithAccountObservation(ctx), codexGatewayBorrowObservationContextKey{}, true)
}
func IsCodexGatewayBorrowObservation(ctx context.Context) bool {
	return ctx != nil && ctx.Value(codexGatewayBorrowObservationContextKey{}) == true
}

// Builders stage the model they actually resolved. This gate does not inspect,
// consume, copy or limit the business body (which can be the full 100 MiB).
func WithCodexGatewayBorrowModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, codexGatewayBorrowModelContextKey{}, model)
}
func CodexGatewayBorrowModelFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	model, _ := ctx.Value(codexGatewayBorrowModelContextKey{}).(string)
	return model
}

func (s *CodexGatewayBorrowService) Status() CodexGatewayBorrowStatus {
	if s == nil {
		return CodexGatewayBorrowStatus{Config: DefaultCodexGatewayBorrowConfig(), Sources: []CodexGatewayBorrowSourceStatus{}, Targets: []CodexGatewayBorrowTargetStatus{}, ModelEfforts: codexGatewayBorrowModelEfforts()}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := CodexGatewayBorrowStatus{Enabled: s.config.Enabled, Revision: s.revision, GeneratedAt: now, Preparing: s.preparing || s.prepareRuns > 0 || s.setup.State == "queued",
		ObservedSince: s.observedSince, RecentUsage: []CodexGatewayBorrowUsage{}, Setup: s.setup, Config: cloneCodexGatewayBorrowConfig(s.config), Sources: []CodexGatewayBorrowSourceStatus{}, Targets: []CodexGatewayBorrowTargetStatus{}, ModelEfforts: codexGatewayBorrowModelEfforts()}
	if c := s.candidate; c != nil {
		out.Candidate = &CodexGatewayBorrowCandidateStatus{SourceAccountID: c.sourceID, SourceRequestEncoding: c.sourceRequestEncoding, ExpiresAt: c.expires, RemainingSeconds: borrowRemaining(c.expires, now), CookieFingerprint: borrowHash(c.cookie.Value)}
	}
	for _, usage := range s.usage {
		out.RecentUsage = append(out.RecentUsage, usage)
	}
	sort.Slice(out.RecentUsage, func(i, j int) bool { return out.RecentUsage[i].StartedAt.After(out.RecentUsage[j].StartedAt) })
	for _, id := range s.config.SourceAccountIDs {
		row, ok := s.sources[id]
		if !ok {
			row = CodexGatewayBorrowSourceStatus{AccountID: id, State: "waiting", Reason: "not_prepared"}
		}
		if !s.config.Enabled {
			row.State, row.Reason = "disabled", "disabled"
		}
		if row.ExpiresAt != nil {
			row.RemainingSeconds = borrowRemaining(*row.ExpiresAt, now)
			if row.State == "ready" && row.RemainingSeconds == 0 {
				row.State, row.Reason = "expired", "route_expired"
			}
		}
		if s.config.Enabled && s.candidate != nil && s.candidate.sourceID == id && s.candidateRejectedLocked() {
			row.State, row.Reason = "candidate", "source_passed_target_failed"
		}
		out.Sources = append(out.Sources, row)
	}
	for _, id := range s.config.TargetAccountIDs {
		for _, model := range s.config.Models {
			row := CodexGatewayBorrowTargetStatus{CodexGatewayBorrowVerification: CodexGatewayBorrowVerification{AccountID: id, Model: model, Reason: "not_verified"}, State: "waiting"}
			if c, ok := s.targets[codexGatewayBorrowTargetKey{id, model}]; ok {
				row.CodexGatewayBorrowVerification = c.result
				row.CacheValid = c.policyRevision == currentCodexFingerprintPolicyForAccount(&Account{ID: id}).revision && c.result.Success && s.candidate != nil && !s.candidateRejectedLocked() && c.cookieKey == borrowHash(s.candidate.cookie.Value) && now.Before(c.expires) && now.Before(s.candidate.expires)
				expires := c.expires
				if s.candidate != nil && c.cookieKey == borrowHash(s.candidate.cookie.Value) && s.candidate.expires.Before(expires) {
					expires = s.candidate.expires
				}
				if !expires.IsZero() {
					row.ExpiresAt = &expires
				}
				row.RemainingSeconds = borrowRemaining(expires, now)
				row.State = "rejected"
				if row.CacheValid {
					row.State = "ready"
				} else if c.validating {
					row.State = "validating"
				} else if c.result.Success {
					row.State, row.Reason = "expired", "route_expired"
				}
				if c.result.Success && s.candidate != nil && c.cookieKey != borrowHash(s.candidate.cookie.Value) && now.Before(c.expires) {
					row.State, row.Reason = "waiting", "target_route_changed"
				}
				if c.policyRevision != currentCodexFingerprintPolicyForAccount(&Account{ID: id}).revision && c.result.Success {
					row.State, row.Reason = "waiting", "identity_changed"
				}
				if now.Before(c.retryAfter) && !c.result.Success {
					retry := c.retryAfter
					row.RetryAfter = &retry
				}
			}
			if !s.config.Enabled {
				row.State, row.Reason, row.CacheValid = "disabled", "disabled", false
			}
			if s.config.Enabled && !row.CacheValid && s.candidateRejectedLocked() && now.Before(s.prepareFailedUntil) {
				retry := s.prepareFailedUntil
				row.RetryAfter = &retry
			}
			out.Targets = append(out.Targets, row)
		}
	}
	return out
}

func borrowRemaining(expires, now time.Time) int64 {
	remaining := int64(expires.Sub(now).Seconds())
	if remaining < 0 {
		return 0
	}
	return remaining
}
func borrowHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func borrowRevisionContext(parent, revision context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	stop := context.AfterFunc(revision, cancel)
	return ctx, func() { stop(); cancel() }
}

func (s *CodexGatewayBorrowService) Prepare(ctx context.Context) (CodexGatewayBorrowStatus, error) {
	if s == nil {
		return s.Status(), ErrCodexGatewayBorrowUnavailable
	}
	s.mu.Lock()
	cfg, rev, revisionCtx := cloneCodexGatewayBorrowConfig(s.config), s.revision, s.revisionCtx
	s.mu.Unlock()
	if !cfg.Enabled {
		return s.Status(), ErrCodexGatewayBorrowUnavailable
	}
	_, err := s.setupFlight.do(ctx, revisionCtx, fmt.Sprint(rev), 10*time.Minute, func(runCtx context.Context) (any, error) {
		progress := CodexGatewayBorrowSetup{State: "running", Phase: "source", Total: len(cfg.TargetAccountIDs) * len(cfg.Models), StartedAt: time.Now()}
		s.setSetup(rev, progress)
		s.mu.Lock()
		if s.revision != rev {
			s.mu.Unlock()
			return nil, ErrCodexGatewayBorrowChanged
		}
		s.prepareRuns++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			if s.revision == rev {
				s.prepareRuns--
			}
			s.mu.Unlock()
		}()
		finish := func(err error) {
			now := time.Now()
			progress.FinishedAt = &now
			progress.Phase = "complete"
			if err != nil {
				progress.Error = err.Error()
				progress.State = "failed"
			} else {
				ready := 0
				for _, row := range s.Status().Targets {
					if row.CacheValid {
						ready++
					}
				}
				progress.State = "failed"
				if ready == progress.Total {
					progress.State = "ready"
				} else if ready > 0 {
					progress.State = "partial"
				}
			}
			s.setSetup(rev, progress)
		}
		if err := s.prepareSource(runCtx, rev, revisionCtx); err != nil {
			finish(err)
			return nil, err
		}
		var lastErr error
		for _, id := range cfg.TargetAccountIDs {
			for _, model := range cfg.Models {
				if err := runCtx.Err(); err != nil {
					finish(err)
					return nil, err
				}
				progress.Phase, progress.AccountID, progress.Model = "target", id, model
				s.setSetup(rev, progress)
				a, req, proxy, itemErr := s.accountTemplate(runCtx, id, model)
				if itemErr == nil {
					_, _, itemErr = s.Apply(req, a, model, proxy, nil, false)
				} else {
					s.mu.Lock()
					if s.revision == rev {
						s.targets[codexGatewayBorrowTargetKey{id, model}] = codexGatewayBorrowTargetCheck{result: CodexGatewayBorrowVerification{AccountID: id, Model: model, CheckedAt: time.Now(), Reason: "account_unavailable", Error: itemErr.Error()}}
					}
					s.mu.Unlock()
				}
				progress.Completed++
				if itemErr != nil {
					progress.Failed++
					lastErr = itemErr
				}
				s.setSetup(rev, progress)
			}
		}
		if progress.Failed == progress.Total {
			finish(lastErr)
			return nil, lastErr
		}
		finish(nil)
		return nil, nil
	})
	return s.Status(), err
}

func (s *CodexGatewayBorrowService) prepareSource(ctx context.Context, rev uint64, revisionCtx context.Context) error {
	if s == nil {
		return ErrCodexGatewayBorrowUnavailable
	}
	_, err := s.sourceFlight.do(ctx, revisionCtx, fmt.Sprint(rev), codexGatewayBorrowTimeout, func(ctx context.Context) (any, error) {
		s.mu.Lock()
		if s.revision != rev || !s.config.Enabled {
			s.mu.Unlock()
			return nil, ErrCodexGatewayBorrowChanged
		}
		if s.candidate != nil && !s.candidateRejectedLocked() && time.Until(s.candidate.expires) > codexGatewayBorrowTimeout {
			s.mu.Unlock()
			return nil, nil
		}
		if s.accounts == nil || s.gateway == nil {
			s.mu.Unlock()
			return nil, ErrCodexGatewayBorrowUnavailable
		}
		if time.Now().Before(s.prepareFailedUntil) {
			detail := s.prepareFailure
			cause := s.rejectedCause
			s.mu.Unlock()
			if cause != nil {
				return nil, cause
			}
			if detail != "" {
				return nil, errors.New(detail)
			}
			return nil, ErrCodexGatewayBorrowUnavailable
		}
		ids := append([]int64{}, s.config.SourceAccountIDs...)
		if s.sources == nil {
			s.sources = make(map[int64]CodexGatewayBorrowSourceStatus)
		}
		s.preparing = true
		s.sourceSequence++
		sequence := s.sourceSequence
		s.mu.Unlock()
		probeCtx, cancel := borrowRevisionContext(ctx, revisionCtx, codexGatewayBorrowTimeout)
		defer func() {
			cancel()
			s.mu.Lock()
			if s.revision == rev && s.sourceSequence == sequence {
				s.preparing = false
			}
			s.mu.Unlock()
		}()
		var lastErr error = ErrCodexGatewayBorrowUnavailable
		for _, id := range ids {
			if errors.Is(probeCtx.Err(), context.Canceled) {
				lastErr = probeCtx.Err()
				break
			}
			candidate, detail, err := s.acquireSource(probeCtx, id)
			now := time.Now()
			s.mu.Lock()
			if s.revision != rev || revisionCtx.Err() != nil || s.sourceSequence != sequence || errors.Is(probeCtx.Err(), context.Canceled) {
				s.mu.Unlock()
				return nil, ErrCodexGatewayBorrowChanged
			}
			row := CodexGatewayBorrowSourceStatus{AccountID: id, State: "rejected", Reason: "source_probe_failed", CheckedAt: &now, Error: detail}
			previouslyRejected := false
			if candidate != nil {
				key := borrowHash(candidate.cookie.Value)
				if until, found := s.rejectedRoutes[key]; found {
					previouslyRejected = now.Before(until)
					if !previouslyRejected {
						delete(s.rejectedRoutes, key)
					}
				}
			}
			if err == nil && candidate != nil && previouslyRejected {
				// Keep the old candidate and expiry: reacquiring the identical
				// rejected credential must not renew its lease or qualify it.
				err = errors.New("source returned the same rejected route")
				row.Reason, row.Error = "source_returned_rejected_route", err.Error()
			}
			if err == nil && candidate != nil && now.Before(candidate.expires) {
				// Seeing the same value while its original lease is live never
				// renews it, even when a different source returns that value.
				if old := s.candidate; old != nil && now.Before(old.expires) && old.cookie.Value == candidate.cookie.Value && old.expires.Before(candidate.expires) {
					candidate.expires = old.expires
				}
				if s.candidate == nil || s.candidate.cookie.Value != candidate.cookie.Value || s.candidate.cookie.Path != candidate.cookie.Path {
					s.qualifications = map[string]codexGatewayBorrowTargetCheck{}
				}
				s.candidate = candidate
				s.rejectedCookie, s.rejectedCause = "", nil
				row.State, row.Reason, row.Error, row.ExpiresAt = "ready", "source_qualified", "", &candidate.expires
				s.sources[id] = row
				s.preparing = false
				s.prepareFailedUntil, s.prepareFailure = time.Time{}, ""
				s.mu.Unlock()
				return nil, nil
			}
			if err != nil {
				lastErr = err
				if detail != "" {
					lastErr = errors.New(detail)
				}
			}
			s.sources[id] = row
			s.mu.Unlock()
		}
		s.mu.Lock()
		if s.revision == rev && s.sourceSequence == sequence && !errors.Is(probeCtx.Err(), context.Canceled) && !errors.Is(lastErr, ErrCodexBorrowDiagnosticBudget) {
			s.preparing = false
			s.prepareFailedUntil = time.Now().Add(codexGatewayBorrowFailureWait)
			s.prepareFailure = lastErr.Error()
		}
		s.mu.Unlock()
		return nil, lastErr
	})
	return err
}

func borrowRequestFingerprint(req *http.Request, model, proxy string, profile *tlsfingerprint.Profile, cookie string) string {
	tlsJSON, _ := json.Marshal(profile)
	values := []string{cookie, model, proxy, req.URL.EscapedPath(), req.Header.Get("Authorization"), req.Header.Get("ChatGPT-Account-ID"), req.Header.Get("User-Agent"), req.Header.Get("Originator"), req.Header.Get("Version"), string(tlsJSON)}
	values = append(values, codexBorrowRequestServiceTier(req))
	// Probes clear business STATE and mint their own independent two-shot STATE.
	// A client's next turn must not discard that live route proof merely because
	// it echoes a new business STATE. Apply still preserves the client's exact
	// STATE; the candidate lease and native session identity remain part of reuse.
	// Include the native identity and routing policy preserved by the probe.
	// Per-request tracing IDs and the probe's random legacy session_id are not
	// qualification identities and must not defeat reuse or failure cooldowns.
	for _, name := range []string{"session-id", "thread-id", "x-codex-window-id", "x-codex-installation-id", "x-codex-beta-features", openAICodexRoutingHintHeader} {
		values = append(values, req.Header.Get(name))
	}
	identity, _ := json.Marshal(values)
	return borrowHash(string(identity))
}

func borrowTargetFingerprint(req *http.Request, account *Account, model, proxy string, profile *tlsfingerprint.Profile, cookie string) string {
	return borrowHash(borrowRequestFingerprint(req, model, proxy, profile, cookie) + fmt.Sprint(currentCodexFingerprintPolicyForAccount(account).revision))
}

// Apply changes only __oailb on a request clone. The target's auth, STATE,
// __cflb, other cookies, body and proxy stay exactly as supplied by its caller.
// cachedOnly is used by manual generation: it never acquires or validates.
func (s *CodexGatewayBorrowService) Apply(req *http.Request, account *Account, model, proxy string, profile *tlsfingerprint.Profile, cachedOnly bool) (*http.Request, *CodexGatewayBorrowApplication, error) {
	wire, application, err := s.applyWithCandidateRetry(req, account, model, proxy, profile, cachedOnly, false)
	if err != nil && !IsCodexGatewayBorrowFailure(err) {
		err = &CodexGatewayBorrowFailure{Cause: err}
	}
	var diagnostic *codexBorrowDiagnostic
	if req != nil {
		diagnostic = borrowDiagnosticFromContext(req.Context())
	}
	if d := diagnostic; d != nil {
		if application != nil {
			result := application.Verification
			d.verification.Store(&result)
		} else {
			var failure *CodexGatewayBorrowFailure
			if errors.As(err, &failure) {
				d.verification.Store(failure.Verification)
			}
		}
	}
	return wire, application, err
}

func (s *CodexGatewayBorrowService) apply(req *http.Request, account *Account, model, proxy string, profile *tlsfingerprint.Profile, cachedOnly, force bool) (*http.Request, *CodexGatewayBorrowApplication, error) {
	if s == nil || !CodexGatewayBorrowRequestEligible(req) || account == nil {
		return req, nil, nil
	}
	if d := borrowDiagnosticFromContext(req.Context()); d != nil && !d.borrow {
		return req, nil, nil
	}
	s.mu.Lock()
	if !s.config.Enabled || !slices.Contains(s.config.Models, model) || !slices.Contains(s.config.TargetAccountIDs, account.ID) {
		s.mu.Unlock()
		return req, nil, nil
	}
	rev, revisionCtx := s.revision, s.revisionCtx
	candidate := s.candidate
	rejected := s.candidateRejectedLocked()
	s.mu.Unlock()
	if !codexGatewayBorrowAccountSupported(account) {
		return req, nil, ErrCodexGatewayBorrowUnavailable
	}
	if candidate == nil || rejected || !time.Now().Before(candidate.expires) {
		if cachedOnly {
			return req, nil, ErrCodexGatewayBorrowUnavailable
		}
		if err := s.prepareSource(req.Context(), rev, revisionCtx); err != nil {
			if IsCodexGatewayBorrowFailure(err) {
				return req, nil, err
			}
			return req, nil, &CodexGatewayBorrowFailure{Cause: err, Stage: "source_preparation", Reason: "source_prepare_failed"}
		}
		s.mu.Lock()
		candidate = s.candidate
		s.mu.Unlock()
	}
	if candidate == nil || !time.Now().Before(candidate.expires) || !borrowCookiePathMatches(req.URL.Path, candidate.cookie.Path) {
		return req, nil, ErrCodexGatewayBorrowUnavailable
	}
	policyRevision := currentCodexFingerprintPolicyForAccount(account).revision
	key := borrowTargetFingerprint(req, account, model, proxy, profile, candidate.cookie.Value)
	targetKey := codexGatewayBorrowTargetKey{account.ID, model}
	// Cached proofs do not wait for preparation on another target or identity.
	if !force {
		if check, ok, err := s.cachedTarget(rev, targetKey, key, candidate); ok || err != nil {
			if err != nil {
				return req, nil, err
			}
			return borrowApplyCookie(req, candidate, check), borrowApplication(candidate, key, check.expires, check.result), nil
		}
	}
	if cachedOnly {
		return req, nil, ErrCodexGatewayBorrowUnavailable
	}
	value, err := s.targetFlight.do(req.Context(), revisionCtx, fmt.Sprintf("%d:%d:%s:%s", rev, account.ID, model, key), codexGatewayBorrowTimeout, func(operation context.Context) (any, error) {
		if !force {
			if check, ok, err := s.cachedTarget(rev, targetKey, key, candidate); ok || err != nil {
				if err != nil {
					return nil, err
				}
				return check, nil
			}
		}
		s.mu.Lock()
		if operation.Err() != nil || !s.candidateCurrentLocked(rev, candidate) {
			if s.revision == rev {
				delete(s.targets, targetKey)
			}
			s.mu.Unlock()
			return nil, ErrCodexGatewayBorrowChanged
		}
		s.validationSequence++
		validationID := s.validationSequence
		validationDone := s.trackCandidateValidationLocked(rev, borrowHash(candidate.cookie.Value))
		s.targets[targetKey] = codexGatewayBorrowTargetCheck{validationID: validationID, policyRevision: policyRevision, key: key, cookieKey: borrowHash(candidate.cookie.Value), validating: true,
			result: CodexGatewayBorrowVerification{AccountID: account.ID, Model: model, Reason: "validating", CheckedAt: time.Now()}}
		s.mu.Unlock()
		defer validationDone()
		probeCtx, cancel := borrowRevisionContext(operation, revisionCtx, codexGatewayBorrowTimeout)
		defer cancel()
		credential, credentialErr := resolveCredentialAccount(probeCtx, s.accounts, account)
		identityRevision := borrowAccountIdentityRevision(account, credential)
		var result CodexGatewayBorrowVerification
		if credentialErr != nil {
			result = CodexGatewayBorrowVerification{AccountID: account.ID, Model: model, CheckedAt: time.Now(), Reason: "account_unavailable", Error: credentialErr.Error()}
		} else {
			result = s.probeTarget(probeCtx, req, account, model, proxy, profile, candidate)
		}
		s.mu.Lock()
		if errors.Is(operation.Err(), context.Canceled) || !s.candidateCurrentLocked(rev, candidate) {
			if s.revision == rev && s.targets[targetKey].validationID == validationID {
				delete(s.targets, targetKey)
			}
			s.mu.Unlock()
			return nil, ErrCodexGatewayBorrowChanged
		}
		check := codexGatewayBorrowTargetCheck{validationID: validationID, identityRevision: identityRevision, policyRevision: policyRevision, key: key, cookieKey: borrowHash(candidate.cookie.Value), expires: candidate.expires, result: result}
		if !result.Success {
			check.retryAfter = time.Now().Add(codexGatewayBorrowFailureWait)
		}
		if s.targets[targetKey].validationID == validationID {
			s.targets[targetKey] = check
		}
		if s.qualifications == nil {
			s.qualifications = make(map[string]codexGatewayBorrowTargetCheck)
		}
		qualificationKey := fmt.Sprintf("%d:%s:%s", account.ID, model, key)
		// Bound request-identity variants; expired proofs are discarded.
		if len(s.qualifications) >= 1024 {
			oldestKey := ""
			var oldest time.Time
			for k, v := range s.qualifications {
				if !time.Now().Before(v.expires) {
					delete(s.qualifications, k)
				} else if oldestKey == "" || v.result.CheckedAt.Before(oldest) {
					oldestKey, oldest = k, v.result.CheckedAt
				}
			}
			if len(s.qualifications) >= 1024 {
				delete(s.qualifications, oldestKey)
			}
		}
		s.qualifications[qualificationKey] = check
		s.mu.Unlock()
		if !result.Success {
			return nil, borrowVerificationFailure(check, rev)
		}
		return check, nil
	})
	if err != nil {
		return req, nil, err
	}
	check, ok := value.(codexGatewayBorrowTargetCheck)
	if !ok {
		return req, nil, ErrCodexGatewayBorrowUnavailable
	}
	return borrowApplyCookie(req, candidate, check), borrowApplication(candidate, key, check.expires, check.result), nil
}

func (s *CodexGatewayBorrowService) candidateCurrentLocked(rev uint64, candidate *codexGatewayBorrowCandidate) bool {
	return s.revision == rev && s.config.Enabled && s.revisionCtx.Err() == nil && s.candidate != nil && !s.candidateRejectedLocked() && s.candidate.sourceID == candidate.sourceID && s.candidate.cookie.Value == candidate.cookie.Value && s.candidate.cookie.Path == candidate.cookie.Path && s.candidate.expires.Equal(candidate.expires) && time.Now().Before(s.candidate.expires) && time.Now().Before(candidate.expires)
}

func (s *CodexGatewayBorrowService) cachedTarget(rev uint64, key codexGatewayBorrowTargetKey, fingerprint string, candidate *codexGatewayBorrowCandidate) (codexGatewayBorrowTargetCheck, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.candidateCurrentLocked(rev, candidate) {
		return codexGatewayBorrowTargetCheck{}, false, ErrCodexGatewayBorrowChanged
	}
	old, ok := s.qualifications[fmt.Sprintf("%d:%s:%s", key.accountID, key.model, fingerprint)]
	if !ok {
		old, ok = s.targets[key]
	}
	if !ok || old.key != fingerprint || old.validating {
		return old, false, nil
	}
	now := time.Now()
	if !now.Before(old.expires) {
		return old, false, nil
	}
	if old.result.Success && now.Before(old.expires) {
		if candidate.expires.Before(old.expires) {
			old.expires = candidate.expires
		}
		return old, true, nil
	}
	if !old.result.Success && now.Before(old.retryAfter) {
		return old, false, borrowVerificationFailure(old, rev)
	}
	return old, false, nil
}

func borrowApplication(candidate *codexGatewayBorrowCandidate, fingerprint string, expires time.Time, verification ...CodexGatewayBorrowVerification) *CodexGatewayBorrowApplication {
	app := &CodexGatewayBorrowApplication{Applied: true, SourceAccountID: candidate.sourceID, ExpiresAt: expires, Fingerprint: fingerprint, CookieFingerprint: borrowHash(candidate.cookie.Value)}
	if len(verification) > 0 {
		app.Verification = verification[0]
	}
	return app
}

func borrowApplyCookie(req *http.Request, candidate *codexGatewayBorrowCandidate, check codexGatewayBorrowTargetCheck) *http.Request {
	clone := req.Clone(context.WithValue(req.Context(), codexBorrowAppliedContextKey{}, true))
	replaceCodexGatewayBorrowCookie(clone.Header, "__oailb", candidate.cookie.Value)
	return clone
}

func replaceCodexGatewayBorrowCookie(headers http.Header, name, value string) {
	var kept []string
	for _, header := range headers.Values("Cookie") {
		for _, part := range strings.Split(header, ";") {
			part = strings.TrimSpace(part)
			cookieName, _, ok := strings.Cut(part, "=")
			if part != "" && (!ok || strings.TrimSpace(cookieName) != name) {
				kept = append(kept, part)
			}
		}
	}
	if value != "" {
		kept = append(kept, (&http.Cookie{Name: name, Value: value}).String())
	}
	if len(kept) == 0 {
		headers.Del("Cookie")
	} else {
		headers.Set("Cookie", strings.Join(kept, "; "))
	}
}

func borrowCookiePathMatches(path, scope string) bool {
	return path == scope || (strings.HasPrefix(path, scope) && (strings.HasSuffix(scope, "/") || strings.HasPrefix(strings.TrimPrefix(path, scope), "/")))
}

func (s *CodexGatewayBorrowService) Verify(ctx context.Context, accountID int64, model string) (CodexGatewayBorrowVerification, error) {
	result := CodexGatewayBorrowVerification{AccountID: accountID, Model: model, CheckedAt: time.Now(), Reason: "not_configured"}
	cfg := s.ConfigSnapshot()
	if !cfg.Enabled || !slices.Contains(cfg.TargetAccountIDs, accountID) || !slices.Contains(cfg.Models, model) {
		return result, ErrCodexGatewayBorrowUnavailable
	}
	a, req, proxy, err := s.accountTemplate(ctx, accountID, model)
	if err != nil {
		return result, err
	}
	_, _, err = s.applyWithCandidateRetry(req, a, model, proxy, nil, false, true)
	s.mu.Lock()
	if check, ok := s.targets[codexGatewayBorrowTargetKey{accountID, model}]; ok {
		result = check.result
	}
	s.mu.Unlock()
	return result, err
}
