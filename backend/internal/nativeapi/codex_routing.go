package nativeapi

import (
	"fmt"
	"strings"
	"time"
)

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
	// AccountProxyID records which account proxy the RouteHash was computed
	// with, so administrators can see the proxy behind a qualification. It is
	// informational only; SameOwner keeps comparing the hashes.
	AccountProxyID int64 `json:"account_proxy_id,omitempty"`
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

// CodexRoutingUpstreamBodyLimit bounds the upstream error body kept with one
// observation (latest attempt per account and model).
const CodexRoutingUpstreamBodyLimit = 8 << 10

// CodexRoutingHeaderLimit bounds the upstream response headers kept with one
// probe observation. Each kept value is complete; values that no longer fit
// are listed by name in ResponseHeadersOmitted.
const CodexRoutingHeaderLimit = 4 << 10

type CodexRoutingHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// An observation is shown to administrators as recorded: Error is the original
// Go error text, the upstream_* fields and UpstreamBody carry the upstream's own
// error object and (bounded) body, RequestID/CFRay identify the upstream request.
// Probe and validation observations also keep the raw STATE value and every
// upstream response header (Set-Cookie included) within CodexRoutingHeaderLimit.
type CodexRoutingObservation struct {
	Stage                string    `json:"stage"`
	Code                 string    `json:"code"`
	HTTPStatus           int       `json:"http_status,omitempty"`
	RequestedModel       string    `json:"requested_model,omitempty"`
	ReasoningEffort      string    `json:"reasoning_effort,omitempty"`
	ResponseModel        string    `json:"response_model,omitempty"`
	Completed            bool      `json:"completed"`
	ModelMatched         bool      `json:"model_matched"`
	StateLength          int       `json:"state_length,omitempty"`
	CookieNames          []string  `json:"cookie_names,omitempty"`
	ObservedAt           time.Time `json:"observed_at"`
	DurationMS           int64     `json:"duration_ms,omitempty"`
	Transport            string    `json:"transport,omitempty"`
	CookieSent           bool      `json:"cookie_sent"`
	Error                string    `json:"error,omitempty"`
	UpstreamErrorType    string    `json:"upstream_error_type,omitempty"`
	UpstreamErrorCode    string    `json:"upstream_error_code,omitempty"`
	UpstreamErrorMessage string    `json:"upstream_error_message,omitempty"`
	UpstreamErrorParam   string    `json:"upstream_error_param,omitempty"`
	UpstreamBody         string    `json:"upstream_body,omitempty"`
	RequestID            string    `json:"request_id,omitempty"`
	CFRay                string    `json:"cf_ray,omitempty"`
	// State is the raw x-codex-turn-state value returned by the upstream.
	State                  string               `json:"state,omitempty"`
	ResponseHeaders        []CodexRoutingHeader `json:"response_headers,omitempty"`
	ResponseHeadersOmitted []string             `json:"response_headers_omitted,omitempty"`
}

// Summary returns the original failure text of an observation: the transport or
// host error, otherwise the upstream error object. It never falls back to the
// upstream body, which stays in UpstreamBody only.
func (o CodexRoutingObservation) Summary() string {
	if o.Error != "" {
		return o.Error
	}
	var fields []string
	for _, field := range [][2]string{{"type", o.UpstreamErrorType}, {"code", o.UpstreamErrorCode}, {"param", o.UpstreamErrorParam}} {
		if field[1] != "" {
			fields = append(fields, field[0]+"="+field[1])
		}
	}
	switch {
	case o.UpstreamErrorMessage != "" && len(fields) > 0:
		return o.UpstreamErrorMessage + " (" + strings.Join(fields, ", ") + ")"
	case o.UpstreamErrorMessage != "":
		return o.UpstreamErrorMessage
	case len(fields) > 0:
		return strings.Join(fields, ", ")
	}
	return ""
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

// Problem names the first condition that makes Valid false, or returns "".
func (q *CodexRoutingQualification) Problem(now time.Time, id int64, identity, model string) string {
	format := func(at time.Time) string { return at.UTC().Format(time.RFC3339) }
	switch {
	case q == nil:
		return "no route qualification is recorded"
	case q.Scope.AccountID != id:
		return fmt.Sprintf("the route qualification belongs to account %d, not %d", q.Scope.AccountID, id)
	case q.Scope.Identity != identity:
		return fmt.Sprintf("the route qualification belongs to credential owner %s, the account is now %s", q.Scope.Identity, identity)
	case q.Model != model:
		return fmt.Sprintf("the route qualification is for model %q, not %q", q.Model, model)
	case q.Bundle.Key == "" || q.Bundle.Revision <= 0:
		return "the route qualification names no route bundle"
	case q.VerifiedAt.IsZero() || q.VerifiedAt.After(now):
		return fmt.Sprintf("the route qualification has an invalid verification time %s", format(q.VerifiedAt))
	case !now.Before(q.ExpiresAt):
		return fmt.Sprintf("the route qualification expired at %s", format(q.ExpiresAt))
	case q.ExpiresAt.After(q.Bundle.ExpiresAt):
		return fmt.Sprintf("the route qualification (until %s) outlives its bundle (until %s)", format(q.ExpiresAt), format(q.Bundle.ExpiresAt))
	case q.ExpiresAt.After(q.VerifiedAt.Add(CodexRoutingMaxAge)):
		return fmt.Sprintf("the route qualification (until %s) exceeds the %s cookie age after verification at %s", format(q.ExpiresAt), CodexRoutingMaxAge, format(q.VerifiedAt))
	}
	return ""
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
