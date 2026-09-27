package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/google/uuid"
)

const (
	CodexQualityGrantHeader = "X-Sub2API-Quality-Grant"
	CodexQualityTrialHeader = "X-Sub2API-Quality-Trial"
	codexQualityModel       = "gpt-6-astra"
	codexQualityEffort      = "high"
	codexQualityMaxSends    = 60
)

// Failures wrap these sentinels with the concrete precondition that failed;
// attempts record upstream error text separately (see CodexQualityAttempt).
var ErrCodexQualityUnavailable = errors.New("codex_quality_unavailable")
var ErrCodexQualitySpent = errors.New("codex_quality_attempt_spent")

func codexQualityUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCodexQualityUnavailable, fmt.Sprintf(format, args...))
}

type CodexQualityCreateRequest struct {
	RunID        string `json:"run_id"`
	APIKeyID     int64  `json:"api_key_id"`
	PromptSHA256 string `json:"prompt_sha256"`
	MaxSends     int    `json:"max_sends"`
	TTLSeconds   int    `json:"ttl_seconds"`
}

type CodexQualityAttempt struct {
	Stage                    string     `json:"stage"`
	TrialID                  string     `json:"trial_id,omitempty"`
	OperationID              string     `json:"operation_id,omitempty"`
	AccountID                int64      `json:"account_id"`
	State                    string     `json:"state"`
	ReservedAt               time.Time  `json:"reserved_at"`
	FinishedAt               *time.Time `json:"finished_at,omitempty"`
	RequestModel             string     `json:"request_model"`
	ReasoningEffort          string     `json:"reasoning_effort"`
	ResponseModels           []string   `json:"response_models"`
	CreatedModel             *string    `json:"created_model"`
	TerminalModel            *string    `json:"terminal_model"`
	HeaderModel              *string    `json:"header_model"`
	HeaderModels             []string   `json:"header_models"`
	Completed                bool       `json:"completed"`
	HTTPStatus               int        `json:"http_status,omitempty"`
	ErrorCode                string     `json:"error_code,omitempty"`
	ErrorMessage             string     `json:"error_message,omitempty"`
	UpstreamErrorType        string     `json:"upstream_error_type,omitempty"`
	UpstreamErrorCode        string     `json:"upstream_error_code,omitempty"`
	UpstreamErrorMessage     string     `json:"upstream_error_message,omitempty"`
	UpstreamErrorParam       string     `json:"upstream_error_param,omitempty"`
	UpstreamBody             string     `json:"upstream_body,omitempty"`
	RequestID                string     `json:"request_id,omitempty"`
	CFRay                    string     `json:"cf_ray,omitempty"`
	ReasoningTokens          *int64     `json:"reasoning_tokens"`
	InputTokens              *int64     `json:"input_tokens"`
	OutputTokens             *int64     `json:"output_tokens"`
	ConnectionFingerprint    string     `json:"connection_fingerprint,omitempty"`
	QualificationFingerprint string     `json:"qualification_fingerprint,omitempty"`
}

type CodexQualityRunView struct {
	RunID                 string                `json:"run_id"`
	Grant                 string                `json:"grant,omitempty"`
	ActorID               int64                 `json:"actor_id"`
	AccountID             int64                 `json:"account_id"`
	APIKeyID              int64                 `json:"api_key_id"`
	GroupID               int64                 `json:"group_id"`
	ProxyID               int64                 `json:"proxy_id"`
	Model                 string                `json:"model"`
	ReasoningEffort       string                `json:"reasoning_effort"`
	PromptSHA256          string                `json:"prompt_sha256"`
	Status                string                `json:"status"`
	MaxSends              int                   `json:"max_sends"`
	UsedSends             int                   `json:"used_sends"`
	ExpiresAt             time.Time             `json:"expires_at"`
	RouteReady            bool                  `json:"route_ready"`
	RouteExpiresAt        *time.Time            `json:"route_expires_at"`
	RouteGeneration       int64                 `json:"route_generation"`
	ConnectionFingerprint string                `json:"connection_fingerprint,omitempty"`
	RouteError            string                `json:"route_error,omitempty"`
	Attempts              []CodexQualityAttempt `json:"attempts"`
	// Set only on a renew-route reply whose renewal did not install a route.
	RenewalError        string                                `json:"renewal_error,omitempty"`
	RenewalObservations []extensionv1.CodexRoutingObservation `json:"renewal_observations,omitempty"`
}

// Stored only in the existing host-private namespace. No grant, API key,
// prompt, Cookie or STATE value is stored in this ledger. Attempts keep the
// upstream error object, transport/stream error text and the bounded failing
// event or body (which can quote response text) for administrators.
type codexQualityRun struct {
	RunID                  string                                 `json:"run_id"`
	ActorID                int64                                  `json:"actor_id"`
	APIKeyID               int64                                  `json:"api_key_id"`
	AccountID              int64                                  `json:"account_id"`
	GroupID                int64                                  `json:"group_id"`
	ProxyID                int64                                  `json:"proxy_id"`
	Scope                  extensionv1.CodexRoutingScope          `json:"scope"`
	PromptSHA256           string                                 `json:"prompt_sha256"`
	GrantDigest            string                                 `json:"grant_digest"`
	Status                 string                                 `json:"status"`
	MaxSends               int                                    `json:"max_sends"`
	UsedSends              int                                    `json:"used_sends"`
	ExpiresAt              time.Time                              `json:"expires_at"`
	RouteGeneration        int64                                  `json:"route_generation"`
	RouteRuntimeGeneration int64                                  `json:"route_runtime_generation"`
	Qualification          *extensionv1.CodexRoutingQualification `json:"qualification,omitempty"`
	Attempts               []CodexQualityAttempt                  `json:"attempts"`
}

func codexQualityKey(id string) string { return "quality-run." + id }

func canonicalCodexQualityID(text string) (string, bool) {
	if len(text) != 36 {
		return "", false
	}
	id, err := uuid.Parse(text)
	if err != nil {
		return "", false
	}
	return id.String(), true
}
func codexQualityHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func readCodexQualityRun(ctx context.Context, store NativeCodexStateStore, id string) (codexQualityRun, int64, error) {
	var run codexQualityRun
	canonical, valid := canonicalCodexQualityID(id)
	if !valid {
		return run, 0, codexQualityUnavailable("run id %q is not a canonical UUID", id)
	}
	if store == nil {
		return run, 0, codexQualityUnavailable("Codex runtime state store is unavailable")
	}
	id = canonical
	record, err := store.ReadExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: codexQualityKey(id)})
	if err != nil {
		return run, 0, codexQualityUnavailable("read quality run %s: %v", id, err)
	}
	if !record.Found {
		return run, 0, nil
	}
	if err := json.Unmarshal(record.Value, &run); err != nil {
		return codexQualityRun{}, 0, codexQualityUnavailable("decode quality run %s: %v", id, err)
	}
	if run.RunID != id || run.MaxSends < 1 || run.MaxSends > codexQualityMaxSends || run.UsedSends != len(run.Attempts) || run.UsedSends > run.MaxSends {
		return codexQualityRun{}, 0, codexQualityUnavailable("quality run record %s is inconsistent (run_id %q, max_sends %d, used_sends %d, attempts %d)", id, run.RunID, run.MaxSends, run.UsedSends, len(run.Attempts))
	}
	return run, record.Revision, nil
}

func mutateCodexQualityRun(ctx context.Context, store NativeCodexStateStore, id string, change func(*codexQualityRun) error) (codexQualityRun, error) {
	canonical, valid := canonicalCodexQualityID(id)
	if !valid {
		return codexQualityRun{}, codexQualityUnavailable("run id %q is not a canonical UUID", id)
	}
	id = canonical
	for attempt := 0; attempt < 12; attempt++ {
		run, revision, err := readCodexQualityRun(ctx, store, id)
		if err != nil {
			return run, err
		}
		if err = change(&run); err != nil {
			return run, err
		}
		raw, err := json.Marshal(run)
		if err != nil {
			return run, codexQualityUnavailable("encode quality run %s: %v", id, err)
		}
		result, err := store.CompareSwapExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: codexQualityKey(id), ExpectedRevision: revision, Value: raw})
		if err != nil {
			return run, codexQualityUnavailable("save quality run %s: %v", id, err)
		}
		if result.Applied {
			return run, nil
		}
	}
	return codexQualityRun{}, codexQualityUnavailable("quality run %s kept changing concurrently", id)
}

func newCodexQualityGrant(id string) (string, string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", "", codexQualityUnavailable("generate the grant: %v", err)
	}
	grant := id + "." + base64.RawURLEncoding.EncodeToString(secret[:])
	return grant, codexQualityHash(grant), nil
}

func codexQualityGrantMatches(run codexQualityRun, digest string) bool {
	return len(digest) == 64 && len(run.GrantDigest) == 64 && subtle.ConstantTimeCompare([]byte(digest), []byte(run.GrantDigest)) == 1
}

func codexQualityActive(run codexQualityRun, now time.Time) bool {
	return run.RunID != "" && run.Status == "open" && now.Before(run.ExpiresAt)
}

// codexQualityInactiveReason explains why codexQualityActive is false.
func codexQualityInactiveReason(run codexQualityRun, now time.Time) string {
	switch {
	case run.RunID == "":
		return "quality run not found"
	case run.Status != "open":
		return fmt.Sprintf("quality run %s is %s", run.RunID, run.Status)
	case !now.Before(run.ExpiresAt):
		return fmt.Sprintf("quality run %s expired at %s", run.RunID, run.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return ""
}

func issueCodexQualityGrant(ctx context.Context, store NativeCodexStateStore, wanted codexQualityRun, digest string, expiry time.Time) (codexQualityRun, error) {
	canonical, valid := canonicalCodexQualityID(wanted.RunID)
	if !valid {
		return codexQualityRun{}, codexQualityUnavailable("run id %q is not a canonical UUID", wanted.RunID)
	}
	wanted.RunID = canonical
	return mutateCodexQualityRun(ctx, store, wanted.RunID, func(run *codexQualityRun) error {
		if run.RunID == "" {
			*run = wanted
		} else if run.Status == "closed" {
			return codexQualityUnavailable("quality run %s is closed; use a new run id", run.RunID)
		} else if run.ActorID != wanted.ActorID || run.APIKeyID != wanted.APIKeyID || run.AccountID != wanted.AccountID || run.GroupID != wanted.GroupID || run.ProxyID != wanted.ProxyID || run.PromptSHA256 != wanted.PromptSHA256 || run.MaxSends != wanted.MaxSends || !run.Scope.SameOwner(wanted.Scope) {
			return codexQualityUnavailable("quality run %s exists with other parameters (actor %d/%d, api key %d/%d, account %d/%d, group %d/%d, proxy %d/%d, max_sends %d/%d, prompt matches %t, routing owner matches %t; stored/requested)", run.RunID, run.ActorID, wanted.ActorID, run.APIKeyID, wanted.APIKeyID, run.AccountID, wanted.AccountID, run.GroupID, wanted.GroupID, run.ProxyID, wanted.ProxyID, run.MaxSends, wanted.MaxSends, run.PromptSHA256 == wanted.PromptSHA256, run.Scope.SameOwner(wanted.Scope))
		}
		run.GrantDigest, run.ExpiresAt = digest, expiry
		return nil
	})
}

func sameCodexQualityAttempt(a, b CodexQualityAttempt) bool {
	return a.Stage == b.Stage && a.TrialID == b.TrialID && a.OperationID == b.OperationID
}

func reserveCodexQualityAttempt(ctx context.Context, store NativeCodexStateStore, id, grantDigest string, attempt CodexQualityAttempt) error {
	_, err := mutateCodexQualityRun(ctx, store, id, func(run *codexQualityRun) error {
		if reason := codexQualityInactiveReason(*run, time.Now()); reason != "" {
			return codexQualityUnavailable("%s", reason)
		}
		if grantDigest != "" && !codexQualityGrantMatches(*run, grantDigest) {
			return codexQualityUnavailable("the grant does not match quality run %s (it was reissued)", run.RunID)
		}
		if run.AccountID != attempt.AccountID {
			return codexQualityUnavailable("attempt account %d differs from run account %d", attempt.AccountID, run.AccountID)
		}
		for _, previous := range run.Attempts {
			if sameCodexQualityAttempt(previous, attempt) {
				return fmt.Errorf("%w: %s attempt (trial %q, operation %q) was already sent", ErrCodexQualitySpent, attempt.Stage, attempt.TrialID, attempt.OperationID)
			}
		}
		if run.UsedSends >= run.MaxSends {
			return fmt.Errorf("%w: %d of %d sends used", ErrCodexQualitySpent, run.UsedSends, run.MaxSends)
		}
		attempt.State, attempt.ReservedAt = "unknown", time.Now().UTC()
		attempt.ResponseModels = []string{}
		attempt.HeaderModels = []string{}
		run.Attempts = append(run.Attempts, attempt)
		run.UsedSends++
		return nil
	})
	return err
}

func finishCodexQualityAttempt(ctx context.Context, store NativeCodexStateStore, id string, finished CodexQualityAttempt) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, _ = mutateCodexQualityRun(ctx, store, id, func(run *codexQualityRun) error {
		for index, old := range run.Attempts {
			if sameCodexQualityAttempt(old, finished) {
				finished.ReservedAt = old.ReservedAt
				now := time.Now().UTC()
				finished.FinishedAt = &now
				run.Attempts[index] = finished
				return nil
			}
		}
		return codexQualityUnavailable("%s attempt (trial %q, operation %q) was never reserved", finished.Stage, finished.TrialID, finished.OperationID)
	})
}

func codexQualityView(run codexQualityRun, generation int64) *CodexQualityRunView {
	v := &CodexQualityRunView{RunID: run.RunID, ActorID: run.ActorID, AccountID: run.AccountID, APIKeyID: run.APIKeyID, GroupID: run.GroupID, ProxyID: run.ProxyID, Model: codexQualityModel, ReasoningEffort: codexQualityEffort, PromptSHA256: run.PromptSHA256, Status: run.Status, MaxSends: run.MaxSends, UsedSends: run.UsedSends, ExpiresAt: run.ExpiresAt, RouteGeneration: run.RouteGeneration, Attempts: run.Attempts}
	if v.Attempts == nil {
		v.Attempts = []CodexQualityAttempt{}
	}
	if q := run.Qualification; q != nil {
		v.RouteExpiresAt = &q.ExpiresAt
		v.RouteReady = codexQualityActive(run, time.Now()) && generation == run.RouteRuntimeGeneration && q.Valid(time.Now(), run.AccountID, run.Scope.Identity, codexQualityModel)
		v.ConnectionFingerprint = codexQualityHash(q.Scope.ConnectionLeaseID)[:16]
		if !v.RouteReady {
			v.RouteError = codexQualityRouteReason(run, generation, time.Now())
		}
	}
	return v
}

// codexQualityRouteReason explains why the installed route of a run cannot be
// used before the bundle and connection checks, or returns "" when it can.
func codexQualityRouteReason(run codexQualityRun, generation int64, now time.Time) string {
	q := run.Qualification
	switch {
	case !codexQualityActive(run, now):
		return codexQualityInactiveReason(run, now)
	case q == nil:
		return "no route is installed; renew the route first"
	case run.RouteRuntimeGeneration != generation:
		return fmt.Sprintf("the route was installed by runtime generation %d, the current generation is %d", run.RouteRuntimeGeneration, generation)
	case !q.Valid(now, run.AccountID, run.Scope.Identity, codexQualityModel):
		return fmt.Sprintf("the route qualification is not valid now (model %q, verified %s, expires %s)", q.Model, q.VerifiedAt.UTC().Format(time.RFC3339), q.ExpiresAt.UTC().Format(time.RFC3339))
	case !q.Scope.SameOwner(run.Scope):
		return "the route " + codexRoutingScopeChange(run.Scope, q.Scope)
	case q.Scope.Transport != "http" || q.Scope.ConnectionLeaseID == "":
		return fmt.Sprintf("the route is bound to transport %q connection %q; an HTTP connection lease is required", q.Scope.Transport, q.Scope.ConnectionLeaseID)
	}
	return ""
}

func validCodexQualityHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}
