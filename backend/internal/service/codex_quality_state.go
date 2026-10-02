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
	codexQualityMaxSends    = 60
	codexQualityMaxTTL      = 7200
	// Every attempt is an ordinary business send. Records written by the
	// retired route-qualified diagnostics also hold "acquire" and "verify".
	codexQualityStage = "business"
	// A record written before runs carried their own model and reasoning
	// effort was always bound to these.
	codexQualityLegacyModel  = "gpt-6-astra"
	codexQualityLegacyEffort = "high"
)

// codexPrivateStateNamespace is the host-private namespace of the native Codex
// runtime state (plugin key NativeCodexPluginKey). Quality-run ledgers and the
// observed final Codex request live there; the stored name is historical.
const codexPrivateStateNamespace = "codex-routing-private"

// Failures wrap these sentinels with the concrete precondition that failed;
// attempts record upstream error text separately (see CodexQualityAttempt).
var ErrCodexQualityUnavailable = errors.New("codex_quality_unavailable")
var ErrCodexQualitySpent = errors.New("codex_quality_attempt_spent")

func codexQualityUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCodexQualityUnavailable, fmt.Sprintf(format, args...))
}

type CodexQualityCreateRequest struct {
	RunID           string `json:"run_id"`
	APIKeyID        int64  `json:"api_key_id"`
	PromptSHA256    string `json:"prompt_sha256"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	MaxSends        int    `json:"max_sends"`
	TTLSeconds      int    `json:"ttl_seconds"`
}

type CodexQualityAttempt struct {
	Stage                string     `json:"stage"`
	TrialID              string     `json:"trial_id,omitempty"`
	OperationID          string     `json:"operation_id,omitempty"`
	AccountID            int64      `json:"account_id"`
	State                string     `json:"state"`
	ReservedAt           time.Time  `json:"reserved_at"`
	FinishedAt           *time.Time `json:"finished_at,omitempty"`
	RequestModel         string     `json:"request_model"`
	ReasoningEffort      string     `json:"reasoning_effort"`
	ResponseModels       []string   `json:"response_models"`
	CreatedModel         *string    `json:"created_model"`
	TerminalModel        *string    `json:"terminal_model"`
	HeaderModel          *string    `json:"header_model"`
	HeaderModels         []string   `json:"header_models"`
	Completed            bool       `json:"completed"`
	HTTPStatus           int        `json:"http_status,omitempty"`
	ErrorCode            string     `json:"error_code,omitempty"`
	ErrorMessage         string     `json:"error_message,omitempty"`
	UpstreamErrorType    string     `json:"upstream_error_type,omitempty"`
	UpstreamErrorCode    string     `json:"upstream_error_code,omitempty"`
	UpstreamErrorMessage string     `json:"upstream_error_message,omitempty"`
	UpstreamErrorParam   string     `json:"upstream_error_param,omitempty"`
	UpstreamBody         string     `json:"upstream_body,omitempty"`
	RequestID            string     `json:"request_id,omitempty"`
	CFRay                string     `json:"cf_ray,omitempty"`
	ReasoningTokens      *int64     `json:"reasoning_tokens"`
	InputTokens          *int64     `json:"input_tokens"`
	OutputTokens         *int64     `json:"output_tokens"`
	// Legacy only: attempts of the retired route-qualified diagnostics name
	// their connection and route. New attempts never set them.
	ConnectionFingerprint    string `json:"connection_fingerprint,omitempty"`
	QualificationFingerprint string `json:"qualification_fingerprint,omitempty"`
}

type CodexQualityRunView struct {
	RunID           string                `json:"run_id"`
	Grant           string                `json:"grant,omitempty"`
	ActorID         int64                 `json:"actor_id"`
	AccountID       int64                 `json:"account_id"`
	APIKeyID        int64                 `json:"api_key_id"`
	GroupID         int64                 `json:"group_id"`
	ProxyID         int64                 `json:"proxy_id"`
	Model           string                `json:"model"`
	ReasoningEffort string                `json:"reasoning_effort"`
	PromptSHA256    string                `json:"prompt_sha256"`
	Status          string                `json:"status"`
	MaxSends        int                   `json:"max_sends"`
	UsedSends       int                   `json:"used_sends"`
	ExpiresAt       time.Time             `json:"expires_at"`
	Attempts        []CodexQualityAttempt `json:"attempts"`
	// Read-only route facts of a record written by the retired
	// route-qualified diagnostics; absent from every other run.
	RouteGeneration       *int64     `json:"route_generation,omitempty"`
	RouteExpiresAt        *time.Time `json:"route_expires_at,omitempty"`
	ConnectionFingerprint string     `json:"connection_fingerprint,omitempty"`
}

// Stored only in the host-private namespace. No grant, API key, prompt, Cookie
// or STATE value is stored in this ledger. Attempts keep the upstream error
// object, transport/stream error text and the bounded failing event or body
// (which can quote response text) for administrators.
type codexQualityRun struct {
	RunID     string `json:"run_id"`
	ActorID   int64  `json:"actor_id"`
	APIKeyID  int64  `json:"api_key_id"`
	AccountID int64  `json:"account_id"`
	GroupID   int64  `json:"group_id"`
	ProxyID   int64  `json:"proxy_id"`
	// OwnerIdentity is CodexCredentialOwnerIdentity of the account when the
	// run was created; the credential owner is part of the binding.
	OwnerIdentity string `json:"owner_identity,omitempty"`
	// Model and ReasoningEffort are bound when the run is created. A record
	// without them is legacy (see legacy) and reads as gpt-6-astra/high.
	Model           string                `json:"model,omitempty"`
	ReasoningEffort string                `json:"reasoning_effort,omitempty"`
	PromptSHA256    string                `json:"prompt_sha256"`
	GrantDigest     string                `json:"grant_digest"`
	Status          string                `json:"status"`
	MaxSends        int                   `json:"max_sends"`
	UsedSends       int                   `json:"used_sends"`
	ExpiresAt       time.Time             `json:"expires_at"`
	Attempts        []CodexQualityAttempt `json:"attempts"`
	// Members only a legacy record holds. They are kept verbatim so a
	// rewritten record (for example a close) loses none of them, and are
	// read only for the view.
	Scope                  json.RawMessage `json:"scope,omitempty"`
	RouteGeneration        json.RawMessage `json:"route_generation,omitempty"`
	RouteRuntimeGeneration json.RawMessage `json:"route_runtime_generation,omitempty"`
	Qualification          json.RawMessage `json:"qualification,omitempty"`
}

// legacy reports a record written by the retired route-qualified diagnostics.
// Such a run stays readable and closable but takes no new grant or send.
func (r codexQualityRun) legacy() bool { return r.RunID != "" && r.Model == "" }

// binding returns the model and reasoning effort every send of the run uses.
func (r codexQualityRun) binding() (string, string) {
	if r.legacy() {
		return codexQualityLegacyModel, codexQualityLegacyEffort
	}
	return r.Model, r.ReasoningEffort
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
	record, err := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: codexQualityKey(id)})
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
		result, err := store.CompareSwapExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: codexQualityKey(id), ExpectedRevision: revision, Value: raw})
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
	return codexQualityInactiveReason(run, now) == ""
}

// codexQualityInactiveReason explains why a run takes no send, or returns "".
func codexQualityInactiveReason(run codexQualityRun, now time.Time) string {
	switch {
	case run.RunID == "":
		return "quality run not found"
	case run.legacy():
		return fmt.Sprintf("quality run %s was recorded by the retired route-qualified diagnostics; it is read-only, use a new run id", run.RunID)
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
	if wanted.legacy() {
		return codexQualityRun{}, codexQualityUnavailable("quality run %s names no model", wanted.RunID)
	}
	return mutateCodexQualityRun(ctx, store, wanted.RunID, func(run *codexQualityRun) error {
		if run.RunID == "" {
			*run = wanted
		} else if run.legacy() {
			return codexQualityUnavailable("%s", codexQualityInactiveReason(*run, time.Now()))
		} else if run.Status == "closed" {
			return codexQualityUnavailable("quality run %s is closed; use a new run id", run.RunID)
		} else if run.ActorID != wanted.ActorID || run.APIKeyID != wanted.APIKeyID || run.AccountID != wanted.AccountID || run.GroupID != wanted.GroupID || run.ProxyID != wanted.ProxyID || run.Model != wanted.Model || run.ReasoningEffort != wanted.ReasoningEffort || run.PromptSHA256 != wanted.PromptSHA256 || run.MaxSends != wanted.MaxSends || run.OwnerIdentity != wanted.OwnerIdentity {
			return codexQualityUnavailable("quality run %s exists with other parameters (actor %d/%d, api key %d/%d, account %d/%d, group %d/%d, proxy %d/%d, model %s/%s, reasoning_effort %s/%s, max_sends %d/%d, prompt matches %t, credential owner matches %t; stored/requested)", run.RunID, run.ActorID, wanted.ActorID, run.APIKeyID, wanted.APIKeyID, run.AccountID, wanted.AccountID, run.GroupID, wanted.GroupID, run.ProxyID, wanted.ProxyID, run.Model, wanted.Model, run.ReasoningEffort, wanted.ReasoningEffort, run.MaxSends, wanted.MaxSends, run.PromptSHA256 == wanted.PromptSHA256, run.OwnerIdentity == wanted.OwnerIdentity)
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

func codexQualityView(run codexQualityRun) *CodexQualityRunView {
	model, effort := run.binding()
	v := &CodexQualityRunView{RunID: run.RunID, ActorID: run.ActorID, AccountID: run.AccountID, APIKeyID: run.APIKeyID, GroupID: run.GroupID, ProxyID: run.ProxyID, Model: model, ReasoningEffort: effort, PromptSHA256: run.PromptSHA256, Status: run.Status, MaxSends: run.MaxSends, UsedSends: run.UsedSends, ExpiresAt: run.ExpiresAt, Attempts: run.Attempts}
	if v.Attempts == nil {
		v.Attempts = []CodexQualityAttempt{}
	}
	if !run.legacy() {
		return v
	}
	var generation int64
	if json.Unmarshal(run.RouteGeneration, &generation) == nil {
		v.RouteGeneration = &generation
	}
	var route struct {
		ExpiresAt time.Time `json:"expires_at"`
		Scope     struct {
			ConnectionLeaseID string `json:"connection_lease_id"`
		} `json:"scope"`
	}
	if len(run.Qualification) > 0 && json.Unmarshal(run.Qualification, &route) == nil {
		if !route.ExpiresAt.IsZero() {
			v.RouteExpiresAt = &route.ExpiresAt
		}
		if route.Scope.ConnectionLeaseID != "" {
			v.ConnectionFingerprint = codexQualityHash(route.Scope.ConnectionLeaseID)[:16]
		}
	}
	return v
}

func validCodexQualityHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}
