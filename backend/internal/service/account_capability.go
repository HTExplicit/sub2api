package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	AccountCapabilityKindDiscover   = "discover"
	AccountCapabilityKindProbe      = "probe"
	AccountCapabilityWorkers        = 2
	AccountCapabilityRequestTimeout = 60 * time.Second
)

var (
	ErrAccountCapabilityInvalid             = errors.New("invalid capability request")
	ErrAccountCapabilityScope               = errors.New("account is outside the frozen capability scope")
	ErrAccountCapabilityNotFound            = errors.New("capability record not found")
	ErrAccountCapabilityConflict            = errors.New("capability state has changed")
	ErrAccountCapabilityIdempotencyRequired = errors.New("capability idempotency key required")
	ErrAccountCapabilityIdempotencyConflict = errors.New("capability idempotency key conflict")
)

// Capability records contain only target identifiers and sanitized observations.
// Authentication material is read from the current account immediately before use.
type AccountCapabilityRun struct {
	ID                int64      `json:"id"`
	CreatedBy         int64      `json:"created_by"`
	Kind              string     `json:"kind"`
	IdempotencyKey    string     `json:"-"`
	RequestHash       string     `json:"-"`
	FolderIDs         []int64    `json:"folder_ids"`
	AccountIDs        []int64    `json:"account_ids"`
	Status            string     `json:"status"`
	TargetCount       int        `json:"target_count"`
	ProcessedCount    int        `json:"processed_count"`
	SucceededCount    int        `json:"succeeded_count"`
	FailedCount       int        `json:"failed_count"`
	RequestCount      int        `json:"request_count"`
	PossiblySentCount int        `json:"possibly_sent_count"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type AccountCapabilityItem struct {
	ID                    int64           `json:"id"`
	RunID                 int64           `json:"run_id"`
	Kind                  string          `json:"kind"`
	Ordinal               int             `json:"ordinal"`
	AccountID             int64           `json:"account_id"`
	AccountName           string          `json:"account_name"`
	FolderID              int64           `json:"folder_id"`
	ConfigFingerprint     string          `json:"config_fingerprint"`
	UpstreamModel         string          `json:"upstream_model"`
	Protocol              string          `json:"protocol"`
	Profile               string          `json:"profile"`
	Aliases               []string        `json:"aliases"`
	Status                string          `json:"status"`
	Result                json.RawMessage `json:"result"`
	RequestCount          int             `json:"request_count"`
	RequestCountUnknown   bool            `json:"request_count_unknown"`
	PublicationSuperseded bool            `json:"publication_superseded"`
	ClaimedAt             *time.Time      `json:"claimed_at,omitempty"`
	DispatchedAt          *time.Time      `json:"dispatched_at,omitempty"`
	FinishedAt            *time.Time      `json:"finished_at,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	StaleConfig           bool            `json:"stale_config"`
	IsCurrentScope        bool            `json:"is_current_scope"`
}

type AccountCapabilityProbeTarget struct {
	AccountID     int64    `json:"account_id"`
	UpstreamModel string   `json:"upstream_model"`
	Protocol      string   `json:"protocol"`
	Profile       string   `json:"profile"`
	Aliases       []string `json:"aliases,omitempty"`
}

type AccountCapabilityCreateRequest struct {
	Kind       string                         `json:"kind"`
	FolderIDs  []int64                        `json:"folder_ids"`
	AccountIDs []int64                        `json:"account_ids"`
	Items      []AccountCapabilityProbeTarget `json:"items,omitempty"`
}

type AccountCapabilityFilter struct {
	Kind       string
	Status     string
	FolderIDs  []int64
	AccountID  int64
	AccountIDs []int64
	Model      string
	Page       int
	PageSize   int
}

type AccountCapabilityRunPage struct {
	Items    []AccountCapabilityRun `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

type AccountCapabilityItemPage struct {
	Items    []AccountCapabilityItem `json:"items"`
	Total    int64                   `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
}

type AccountCapabilityExecutor interface {
	Discover(context.Context, *Account) AccountCapabilityDiscoveryResult
	Probe(context.Context, *Account, string, string, string) AccountCapabilityProbeResult
}

type AccountCapabilityRepository interface {
	Create(context.Context, *AccountCapabilityRun, []AccountCapabilityItem) (*AccountCapabilityRun, bool, error)
	FindIdempotent(context.Context, int64, string) (*AccountCapabilityRun, error)
	GetRun(context.Context, int64) (*AccountCapabilityRun, error)
	ListRuns(context.Context, AccountCapabilityFilter) (*AccountCapabilityRunPage, error)
	ListItems(context.Context, int64, AccountCapabilityFilter) (*AccountCapabilityItemPage, error)
	LatestItems(context.Context, AccountCapabilityFilter) (*AccountCapabilityItemPage, error)
	GetItemsByIDs(context.Context, []int64) ([]AccountCapabilityItem, error)
	ScopeItems(context.Context, int64) ([]AccountCapabilityItem, error)
	Claim(context.Context) (*AccountCapabilityItem, error)
	MarkDispatched(context.Context, int64) (bool, error)
	Release(context.Context, int64) error
	Complete(context.Context, int64, string, json.RawMessage, int) error
	Control(context.Context, int64, string) (*AccountCapabilityRun, error)
	RecoverInterrupted(context.Context) error
}

// Only the process holding this session-level PostgreSQL lock runs workers or
// performs restart recovery. It prevents a second server from interrupting live probes.
type AccountCapabilityRuntimeLeaseRepository interface {
	AcquireRuntimeLease(context.Context) (release func(), acquired bool, err error)
}

func AccountCapabilityFingerprint(account *Account) (string, error) {
	if account == nil || account.ID <= 0 {
		return "", ErrAccountCapabilityInvalid
	}
	fingerprint := ManagedModelAccountFingerprint(account)
	if len(fingerprint) != 64 {
		return "", ErrAccountCapabilityInvalid
	}
	return fingerprint, nil
}

func AccountCapabilityAccountInScope(account *Account, item *AccountCapabilityItem) bool {
	return account != nil && item != nil && account.ID == item.AccountID &&
		account.ManagementFolderID != nil && *account.ManagementFolderID == item.FolderID
}
