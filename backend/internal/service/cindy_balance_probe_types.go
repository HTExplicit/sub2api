package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	CindyBalanceProbeDefaultRateRPS = 0.5
	CindyBalanceProbeMinRateRPS     = 0.1
	CindyBalanceProbeMaxRateRPS     = 1.0

	cindyBalanceProbeLeaseDuration      = 60 * time.Second
	cindyBalanceProbeHeartbeatInterval  = 10 * time.Second
	cindyBalanceProbePollInterval       = time.Second
	cindyBalanceProbeTimeout            = 20 * time.Second
	cindyBalanceProbeConfirmationWindow = 5 * time.Minute
	cindyBalanceProbeHistoryRetention   = 30 * 24 * time.Hour
)

var (
	ErrCindyBalanceProbeChanged = infraerrors.Conflict(
		"CINDY_BALANCE_PROBE_CANDIDATES_CHANGED",
		"matching Cindy accounts changed; preview and confirm again",
	)
	ErrCindyBalanceProbeActive = infraerrors.Conflict(
		"CINDY_BALANCE_PROBE_ACTIVE",
		"another Cindy balance probe job is already active",
	)
	ErrCindyBalanceProbeNotFound = infraerrors.NotFound(
		"CINDY_BALANCE_PROBE_JOB_NOT_FOUND",
		"Cindy balance probe job not found",
	)
	ErrCindyBalanceProbeInvalidRate = infraerrors.BadRequest(
		"CINDY_BALANCE_PROBE_RATE_INVALID",
		"rate_rps must be between 0.1 and 1.0",
	)
	ErrCindyBalanceProbeNoCandidates = infraerrors.BadRequest(
		"CINDY_BALANCE_PROBE_NO_CANDIDATES",
		"no eligible Cindy accounts match this scope",
	)
)

type CindyBalanceProbeScope struct {
	Mode       string                `json:"mode"`
	AccountIDs []int64               `json:"account_ids,omitempty"`
	Filters    AccountConsoleFilters `json:"filters,omitempty"`
}

type CindyBalanceProbeCandidate struct {
	AccountID           int64
	IdentityFingerprint string
	AccountUpdatedAt    time.Time
	WasMarked           bool
}

type CindyBalanceProbePreview struct {
	Scope                CindyBalanceProbeScope       `json:"scope"`
	CandidateCount       int                          `json:"candidate_count"`
	MarkedCount          int                          `json:"marked_count"`
	UnmarkedCount        int                          `json:"unmarked_count"`
	CandidateFingerprint string                       `json:"candidate_fingerprint"`
	MinimumCalls         int                          `json:"minimum_calls"`
	MaximumCalls         int                          `json:"maximum_calls"`
	RateRPS              float64                      `json:"rate_rps"`
	MinimumETASeconds    int64                        `json:"minimum_eta_seconds"`
	MaximumETASeconds    int64                        `json:"maximum_eta_seconds"`
	Candidates           []CindyBalanceProbeCandidate `json:"-"`
}

type CindyBalanceProbeCounts struct {
	Pending      int `json:"pending"`
	Running      int `json:"running"`
	Healthy      int `json:"healthy"`
	Recovered    int `json:"recovered"`
	Exhausted    int `json:"exhausted"`
	Inconclusive int `json:"inconclusive"`
	Skipped      int `json:"skipped"`
}

type CindyBalanceProbeJob struct {
	ID                   int64                   `json:"id"`
	Status               string                  `json:"status"`
	RequestedBy          *int64                  `json:"requested_by,omitempty"`
	Scope                CindyBalanceProbeScope  `json:"scope"`
	RateRPS              float64                 `json:"rate_rps"`
	CandidateCount       int                     `json:"candidate_count"`
	CandidateFingerprint string                  `json:"candidate_fingerprint"`
	RequestCount         int                     `json:"request_count"`
	ConsecutiveFailures  int                     `json:"consecutive_upstream_failures"`
	LastRequestStartedAt *time.Time              `json:"last_request_started_at,omitempty"`
	LeaseToken           string                  `json:"-"`
	LeaseUntil           *time.Time              `json:"-"`
	HeartbeatAt          *time.Time              `json:"heartbeat_at,omitempty"`
	CancelRequestedAt    *time.Time              `json:"cancel_requested_at,omitempty"`
	StartedAt            *time.Time              `json:"started_at,omitempty"`
	FinishedAt           *time.Time              `json:"finished_at,omitempty"`
	FailureReason        string                  `json:"failure_reason,omitempty"`
	CreatedAt            time.Time               `json:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at"`
	Counts               CindyBalanceProbeCounts `json:"counts"`
}

type CindyBalanceProbeItem struct {
	ID                  int64      `json:"id"`
	JobID               int64      `json:"job_id"`
	AccountID           int64      `json:"account_id"`
	Ordinal             int        `json:"ordinal"`
	IdentityFingerprint string     `json:"-"`
	AccountUpdatedAt    time.Time  `json:"-"`
	WasMarked           bool       `json:"was_marked"`
	State               string     `json:"state"`
	LunaOutcome         string     `json:"luna_outcome,omitempty"`
	LunaAt              *time.Time `json:"luna_at,omitempty"`
	TerraOutcome        string     `json:"terra_outcome,omitempty"`
	TerraAt             *time.Time `json:"terra_at,omitempty"`
	RequestCount        int        `json:"request_count"`
	FinalOutcome        string     `json:"final_outcome,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CindyBalanceProbeLatest summarizes the newest durable probe item for one
// account. It is used to hydrate admin account list responses without exposing
// probe request payloads or account credentials.
type CindyBalanceProbeLatest struct {
	AccountID int64
	JobID     int64
	Outcome   string
	CheckedAt time.Time
}

type CindyBalanceProbePage struct {
	Items []CindyBalanceProbeItem `json:"items"`
	Total int64                   `json:"total"`
	Page  int                     `json:"page"`
	Size  int                     `json:"page_size"`
}

// CindyBalanceProbeJobList is the bounded recent-job view used by the admin UI.
type CindyBalanceProbeJobList struct {
	Items []CindyBalanceProbeJob `json:"items"`
	Total int64                  `json:"total"`
}

type CindyBalanceProbeReservation struct {
	JobID               int64
	ItemID              int64
	AccountID           int64
	Stage               string
	LeaseToken          string
	JobRequestCount     int
	RequestCount        int
	IdentityFingerprint string
	AccountUpdatedAt    time.Time
	WasMarked           bool
	LunaAt              *time.Time
}

type CindyBalanceProbeRepository interface {
	Preview(ctx context.Context, scope CindyBalanceProbeScope, rateRPS float64) (*CindyBalanceProbePreview, error)
	CreateJob(ctx context.Context, requestedBy *int64, scope CindyBalanceProbeScope, rateRPS float64, expectedCount int, expectedFingerprint string) (*CindyBalanceProbeJob, error)
	GetJob(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error)
	ListJobs(ctx context.Context, limit int) (*CindyBalanceProbeJobList, error)
	ListItems(ctx context.Context, jobID int64, state string, page, pageSize int) (*CindyBalanceProbePage, error)
	LatestByAccountIDs(ctx context.Context, accountIDs []int64) (map[int64]CindyBalanceProbeLatest, error)
	ClaimJob(ctx context.Context, leaseToken string, leaseUntil time.Time) (*CindyBalanceProbeJob, error)
	Heartbeat(ctx context.Context, jobID int64, leaseToken string, leaseUntil time.Time) (bool, error)
	RecoverInterruptedItems(ctx context.Context, jobID int64, leaseToken string) error
	ReserveNext(ctx context.Context, jobID int64, leaseToken string, now time.Time, confirmationCutoff time.Time) (*CindyBalanceProbeReservation, time.Duration, error)
	ValidateReservationForSend(ctx context.Context, reservation *CindyBalanceProbeReservation, account *Account, leaseToken string) (bool, error)
	CompleteStage(ctx context.Context, reservation *CindyBalanceProbeReservation, leaseToken, outcome, finalState string, networkFailure bool) (bool, error)
	FinalizeExhausted(ctx context.Context, reservation *CindyBalanceProbeReservation, leaseToken string, observedAt time.Time, confirmationWindow time.Duration) (string, error)
	FinalizeRecovery(ctx context.Context, reservation *CindyBalanceProbeReservation, leaseToken string, observedAt time.Time) (bool, error)
	FinishIfDone(ctx context.Context, jobID int64, leaseToken string) (bool, error)
	SetRate(ctx context.Context, jobID int64, rateRPS float64) (*CindyBalanceProbeJob, error)
	Pause(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error)
	Resume(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error)
	Cancel(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error)
	PruneFinished(ctx context.Context, before time.Time) error
}

func BuildCindyBalanceProbePreview(scope CindyBalanceProbeScope, accounts []Account, rateRPS float64) (*CindyBalanceProbePreview, error) {
	scope = CanonicalizeCindyBalanceProbeScope(scope)
	if rateRPS == 0 {
		rateRPS = CindyBalanceProbeDefaultRateRPS
	}
	if err := validateCindyBalanceProbeRate(rateRPS); err != nil {
		return nil, err
	}
	candidates := make([]CindyBalanceProbeCandidate, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	marked := 0
	for i := range accounts {
		account := &accounts[i]
		if account.ID <= 0 || account.Status != StatusActive || !account.Schedulable ||
			!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			continue
		}
		if _, exists := seen[account.ID]; exists {
			continue
		}
		fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
		if err != nil || fingerprint == "" {
			continue
		}
		seen[account.ID] = struct{}{}
		wasMarked := account.CindyBalanceInsufficientAt != nil
		if wasMarked {
			marked++
		}
		candidates = append(candidates, CindyBalanceProbeCandidate{
			AccountID: account.ID, IdentityFingerprint: fingerprint,
			AccountUpdatedAt: account.UpdatedAt.UTC().Truncate(time.Microsecond), WasMarked: wasMarked,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].AccountID < candidates[j].AccountID })
	minimumCalls := len(candidates)
	maximumCalls := marked + (len(candidates)-marked)*2
	return &CindyBalanceProbePreview{
		Scope: scope, CandidateCount: len(candidates), MarkedCount: marked,
		UnmarkedCount:        len(candidates) - marked,
		CandidateFingerprint: cindyBalanceProbeCandidateFingerprint(candidates),
		MinimumCalls:         minimumCalls, MaximumCalls: maximumCalls, RateRPS: rateRPS,
		MinimumETASeconds: cindyBalanceProbeETA(minimumCalls, rateRPS),
		MaximumETASeconds: cindyBalanceProbeETA(maximumCalls, rateRPS), Candidates: candidates,
	}, nil
}

// BuildCindyBalanceProbePreviewFromSnapshot applies the persisted scope to a
// transaction-consistent account snapshot before deriving the public preview.
// Preview and create both use this path so their candidate semantics cannot
// diverge between the admin console and the durable job transaction.
func BuildCindyBalanceProbePreviewFromSnapshot(
	scope CindyBalanceProbeScope,
	accounts []Account,
	rateRPS float64,
	now time.Time,
) (*CindyBalanceProbePreview, error) {
	scope = CanonicalizeCindyBalanceProbeScope(scope)
	if now.IsZero() {
		now = time.Now()
	}
	filters := scope.Filters
	if scope.Mode == "selected" {
		filters.AccountIDs = append([]int64(nil), scope.AccountIDs...)
	}
	matcher := newAccountFacetMatcher(filters)
	accountIDs := int64FilterSet(filters.AccountIDs)
	restrictAccountIDs := len(filters.AccountIDs) > 0
	search := strings.ToLower(strings.TrimSpace(filters.Search))

	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if search != "" && !strings.Contains(strings.ToLower(account.Name), search) {
			continue
		}
		if restrictAccountIDs {
			if _, ok := accountIDs[account.ID]; !ok {
				continue
			}
		}
		if filters.GroupID == AccountListGroupUngrouped {
			if len(account.GroupIDs) != 0 {
				continue
			}
		} else if filters.GroupID > 0 && !cindyBalanceProbeContainsID(account.GroupIDs, filters.GroupID) {
			continue
		}
		if filters.PrivacyMode != "" {
			privacyMode, hasPrivacyMode := account.Extra["privacy_mode"].(string)
			if filters.PrivacyMode == AccountPrivacyModeUnsetFilter {
				if hasPrivacyMode && privacyMode != "" {
					continue
				}
			} else if !hasPrivacyMode || privacyMode != filters.PrivacyMode {
				continue
			}
		}
		if !matcher.matches(account, accountFacetNone, now) {
			continue
		}
		filtered = append(filtered, *account)
	}
	return BuildCindyBalanceProbePreview(scope, filtered, rateRPS)
}

func cindyBalanceProbeContainsID(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cindyBalanceProbeCandidateFingerprint(candidates []CindyBalanceProbeCandidate) string {
	h := sha256.New()
	for _, candidate := range candidates {
		_, _ = fmt.Fprintf(h, "%d|%s|%d|%t\n", candidate.AccountID, candidate.IdentityFingerprint, candidate.AccountUpdatedAt.UnixNano(), candidate.WasMarked)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cindyBalanceProbeETA(calls int, rateRPS float64) int64 {
	if calls <= 1 || rateRPS <= 0 {
		return 0
	}
	return int64(float64(calls-1) / rateRPS)
}

func validateCindyBalanceProbeRate(rateRPS float64) error {
	if rateRPS < CindyBalanceProbeMinRateRPS || rateRPS > CindyBalanceProbeMaxRateRPS {
		return ErrCindyBalanceProbeInvalidRate
	}
	return nil
}

func EncodeCindyBalanceProbeScope(scope CindyBalanceProbeScope) []byte {
	scope = CanonicalizeCindyBalanceProbeScope(scope)
	data, _ := json.Marshal(scope)
	return data
}

func DecodeCindyBalanceProbeScope(data []byte) CindyBalanceProbeScope {
	var scope CindyBalanceProbeScope
	_ = json.Unmarshal(data, &scope)
	return CanonicalizeCindyBalanceProbeScope(scope)
}

// CanonicalizeCindyBalanceProbeScope keeps selected account IDs at the public
// top-level field while accepting scopes persisted by the earlier nested
// filters.account_ids representation.
func CanonicalizeCindyBalanceProbeScope(scope CindyBalanceProbeScope) CindyBalanceProbeScope {
	scope.Mode = strings.ToLower(strings.TrimSpace(scope.Mode))
	if scope.Mode != "selected" {
		return scope
	}
	accountIDs := scope.AccountIDs
	if len(accountIDs) == 0 {
		accountIDs = scope.Filters.AccountIDs
	}
	seen := make(map[int64]struct{}, len(accountIDs))
	canonicalIDs := make([]int64, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		canonicalIDs = append(canonicalIDs, accountID)
	}
	sort.Slice(canonicalIDs, func(i, j int) bool { return canonicalIDs[i] < canonicalIDs[j] })
	scope.AccountIDs = canonicalIDs
	scope.Filters.AccountIDs = nil
	return scope
}

func NormalizeCindyBalanceProbeState(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func WrapCindyBalanceProbeCreateError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "idx_cindy_balance_probe_jobs_one_active") || strings.Contains(message, "duplicate key") {
		return ErrCindyBalanceProbeActive
	}
	return err
}
