// Package extensionv1 is the public, process-independent downstream extension
// contract. It deliberately contains no host service or database model types.
package extensionv1

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	Version              = 1
	CapabilityProvider   = "extensions.provider.v1"
	CapabilityCatalog    = "extensions.catalog.v1"
	CapabilityRequest    = "extensions.request.v1"
	CapabilityScheduling = "extensions.scheduling.v1"
	CapabilityJobs       = "extensions.jobs.v1"
	CapabilityAdmin      = "extensions.admin.v1"
	CapabilityUI         = "extensions.ui.v1"
)

var capabilities = map[string]bool{
	CapabilityProvider: true, CapabilityCatalog: true, CapabilityRequest: true,
	CapabilityScheduling: true, CapabilityJobs: true, CapabilityAdmin: true, CapabilityUI: true,
}

func IsCapability(id string) bool { return capabilities[id] }

type Dependency struct {
	Capability  string `json:"capability"`
	Platform    string `json:"platform,omitempty"`
	AccountType string `json:"account_type,omitempty"`
}

type Contribution struct {
	ID         string            `json:"id"`
	Slot       string            `json:"slot"`
	Label      map[string]string `json:"label"`
	Action     string            `json:"action,omitempty"`
	Entrypoint string            `json:"entrypoint,omitempty"`
	Permission string            `json:"permission"`
	Order      int               `json:"order,omitempty"`
}

var slots = map[string]bool{
	"admin.page": true, "admin.settings": true, "account.actions": true,
	"account.details": true, "account.columns": true, "account.test": true,
	"group.actions": true, "group.details": true, "navigation": true,
	"theme": true, "usage.details": true,
}

func ValidSlot(slot string) bool { return slots[slot] }

type Account struct {
	ID         int64                      `json:"id"`
	Platform   string                     `json:"platform"`
	Type       string                     `json:"type"`
	Status     string                     `json:"status"`
	Shadow     bool                       `json:"shadow"`
	Identity   string                     `json:"identity"`
	Revision   string                     `json:"revision"`
	Attributes map[string]json.RawMessage `json:"attributes,omitempty"`
}

type AccountQuery struct {
	AccountID          int64  `json:"account_id,omitempty"`
	Platform           string `json:"platform,omitempty"`
	AccountType        string `json:"account_type,omitempty"`
	IncludeInactive    bool   `json:"include_inactive,omitempty"`
	PrepareCredentials bool   `json:"prepare_credentials,omitempty"`
}

type OutboundIdentity struct {
	AccountID int64               `json:"account_id"`
	Identity  string              `json:"identity"`
	Token     string              `json:"token"`
	ProxyURL  string              `json:"proxy_url,omitempty"`
	Headers   map[string][]string `json:"headers"`
}

type SchedulingRequest struct {
	Account Account   `json:"account"`
	Model   string    `json:"model"`
	Now     time.Time `json:"now"`
	Compact bool      `json:"compact"`
}

type SchedulingDecision struct {
	Allowed bool       `json:"allowed"`
	Reason  string     `json:"reason,omitempty"`
	Scope   string     `json:"scope,omitempty"`
	Until   *time.Time `json:"until,omitempty"`
}

// Invocation carries one declared capability and a domain operation. The host
// validates the capability binding before crossing the process boundary.
type Invocation struct {
	Capability string          `json:"capability"`
	Operation  string          `json:"operation"`
	Payload    json.RawMessage `json:"payload"`
}

type Result struct {
	Payload json.RawMessage `json:"payload,omitempty"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
}

type HostOperation string

const (
	HostAccountRead      HostOperation = "account.read"
	HostAccountList      HostOperation = "account.list"
	HostResolveIdentity  HostOperation = "account.resolve_identity"
	HostUsageQuery       HostOperation = "usage.query"
	HostStateRead        HostOperation = "state.read"
	HostStateDue         HostOperation = "state.due"
	HostStateCompareSwap HostOperation = "state.compare_swap"
	HostLeaseAcquire     HostOperation = "lease.acquire"
	HostLeaseRelease     HostOperation = "lease.release"
	HostJobRead          HostOperation = "job.read"
	HostJobSubmit        HostOperation = "job.submit"
	HostJobComplete      HostOperation = "job.complete"
)

type HostInvocation struct {
	Operation HostOperation   `json:"operation"`
	Payload   json.RawMessage `json:"payload"`
}

type StateRequest struct {
	Namespace        string          `json:"namespace"`
	Key              string          `json:"key"`
	ExpectedRevision int64           `json:"expected_revision"`
	Value            json.RawMessage `json:"value,omitempty"`
	NextAt           *time.Time      `json:"next_at,omitempty"`
}

type DueStateRequest struct {
	Namespace string `json:"namespace"`
	Limit     int    `json:"limit"`
}
type DueState struct {
	Key      string          `json:"key"`
	Revision int64           `json:"revision"`
	Value    json.RawMessage `json:"value"`
}

type StateResult struct {
	Found    bool            `json:"found"`
	Applied  bool            `json:"applied"`
	Revision int64           `json:"revision"`
	Value    json.RawMessage `json:"value,omitempty"`
}

type LeaseRequest struct {
	Namespace  string `json:"namespace"`
	Key        string `json:"key"`
	Owner      string `json:"owner"`
	TTLSeconds int    `json:"ttl_seconds"`
	Generation int64  `json:"generation,omitempty"`
}

type LeaseResult struct {
	Acquired   bool       `json:"acquired"`
	Generation int64      `json:"generation"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// Handler is implemented by a domain plugin, never by a host forwarding shim.
type Handler interface {
	Invoke(context.Context, Invocation) (Result, error)
}

type HostHandler interface {
	Call(context.Context, HostInvocation) (Result, error)
}

func Encode(value any) (json.RawMessage, error) { return json.Marshal(value) }

func Decode(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return errors.New("empty extension payload")
	}
	return json.Unmarshal(raw, into)
}
