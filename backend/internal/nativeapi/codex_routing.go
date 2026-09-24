package nativeapi

import "time"

// Routing operations are named, account-scoped host IO. They never accept a
// destination URL, arbitrary request body, credential, or arbitrary Cookie.
const (
	HostCodexRoutingScope   HostOperation = "codex.routing.scope"
	HostCodexRoutingProbe   HostOperation = "codex.routing.probe"
	HostCodexRoutingCheck   HostOperation = "codex.routing.check"
	HostCodexRoutingCleanup HostOperation = "codex.routing.cleanup"
	CodexRoutingSchema                    = 2
	CodexRoutingMaxAge                    = 120 * time.Second
	CodexRoutingRefreshLead               = 20 * time.Second
)

type CodexRoutingScope struct {
	AccountID         int64  `json:"account_id"`
	Identity          string `json:"identity"`
	ProfileHash       string `json:"profile_hash"`
	RouteHash         string `json:"route_hash"`
	RouteEvidence     string `json:"route_evidence"`
	ConnectionLeaseID string `json:"connection_lease_id,omitempty"`
	Transport         string `json:"transport"`
}

// SameOwner intentionally excludes account.updated_at: writing a projection
// changes that timestamp without changing any outbound identity or route.
func (s CodexRoutingScope) SameOwner(other CodexRoutingScope) bool {
	return s.AccountID > 0 && s.AccountID == other.AccountID && s.Identity != "" && s.Identity == other.Identity &&
		s.ProfileHash != "" && s.ProfileHash == other.ProfileHash && s.RouteHash != "" && s.RouteHash == other.RouteHash
}

type CodexRoutingBundleRef struct {
	Key               string    `json:"key"`
	Revision          int64     `json:"revision"`
	ExpiresAt         time.Time `json:"expires_at"`
	ConnectionLeaseID string    `json:"connection_lease_id,omitempty"`
}

type CodexRoutingQuery struct {
	AccountID       int64                  `json:"account_id"`
	Model           string                 `json:"model,omitempty"`
	ReasoningEffort string                 `json:"reasoning_effort,omitempty"`
	Transport       string                 `json:"transport,omitempty"`
	Stage           string                 `json:"stage,omitempty"` // acquire or verify
	OperationID     string                 `json:"operation_id,omitempty"`
	Bundle          *CodexRoutingBundleRef `json:"bundle,omitempty"`
	Scope           *CodexRoutingScope     `json:"scope,omitempty"`
}

// All observation fields are safe for an authenticated read-only account view.
// Cookie and STATE values, tokens, proxy URLs and response text are excluded.
type CodexRoutingObservation struct {
	Stage           string    `json:"stage"`
	Code            string    `json:"code"`
	HTTPStatus      int       `json:"http_status,omitempty"`
	RequestedModel  string    `json:"requested_model,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	ResponseModel   string    `json:"response_model,omitempty"`
	Completed       bool      `json:"completed"`
	ModelMatched    bool      `json:"model_matched"`
	StateLength     int       `json:"state_length,omitempty"`
	CookieNames     []string  `json:"cookie_names,omitempty"`
	ObservedAt      time.Time `json:"observed_at"`
	DurationMS      int64     `json:"duration_ms,omitempty"`
	Transport       string    `json:"transport,omitempty"`
	CookieSent      bool      `json:"cookie_sent"`
}

type CodexRoutingProbeResult struct {
	Scope       CodexRoutingScope       `json:"scope"`
	Bundle      *CodexRoutingBundleRef  `json:"bundle,omitempty"`
	Observation CodexRoutingObservation `json:"observation"`
	Valid       bool                    `json:"valid"`
}

type CodexRoutingQualification struct {
	Scope      CodexRoutingScope     `json:"scope"`
	Bundle     CodexRoutingBundleRef `json:"bundle"`
	Model      string                `json:"model"`
	VerifiedAt time.Time             `json:"verified_at"`
	ExpiresAt  time.Time             `json:"expires_at"`
}

func (q *CodexRoutingQualification) Valid(now time.Time, id int64, identity, model string) bool {
	return q != nil && q.Scope.AccountID == id && q.Scope.Identity == identity && q.Model == model &&
		q.Bundle.Key != "" && q.Bundle.Revision > 0 && !q.VerifiedAt.IsZero() && !q.VerifiedAt.After(now) &&
		now.Before(q.ExpiresAt) && !q.ExpiresAt.After(q.Bundle.ExpiresAt) && !q.ExpiresAt.After(q.VerifiedAt.Add(CodexRoutingMaxAge))
}

// The host applies only a reference to a host-verified private bundle. Generic
// plugin header mutations continue to reject Cookie and Set-Cookie.
type CodexRoutingInjection struct {
	Headers       map[string]string          `json:"headers"`
	Qualification *CodexRoutingQualification `json:"routing_qualification,omitempty"`
}

type CodexRoutingDemand struct {
	AccountID int64  `json:"account_id"`
	Model     string `json:"model"`
	Transport string `json:"transport,omitempty"`
}

type CodexRoutingResponse struct {
	AccountID     int64                      `json:"account_id"`
	Identity      string                     `json:"identity"`
	Model         string                     `json:"model"`
	Qualification CodexRoutingQualification  `json:"qualification"`
	Observation   CodexRoutingObservation    `json:"observation"`
	Replacement   *CodexRoutingQualification `json:"replacement,omitempty"`
}
