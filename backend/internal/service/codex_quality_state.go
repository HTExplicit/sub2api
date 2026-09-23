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
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/google/uuid"
)

const (
	CodexQualityGrantHeader = "X-Sub2API-Quality-Grant"
	CodexQualityTrialHeader = "X-Sub2API-Quality-Trial"
	codexQualityModel       = "gpt-6-astra"
	codexQualityEffort      = "high"
	codexQualityMaxSends    = 60
)

// A diagnostic failure is deliberately small and never contains upstream text.
var ErrCodexQualityUnavailable = errors.New("codex_quality_unavailable")
var ErrCodexQualitySpent = errors.New("codex_quality_attempt_spent")

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
	ReasoningTokens          *int64     `json:"reasoning_tokens"`
	InputTokens              *int64     `json:"input_tokens"`
	OutputTokens             *int64     `json:"output_tokens"`
	ConnectionFingerprint    string     `json:"connection_fingerprint,omitempty"`
	QualificationFingerprint string     `json:"qualification_fingerprint,omitempty"`
}

type CodexQualityRunView struct {
	RunID                 string                `json:"run_id"`
	Grant                 string                `json:"grant,omitempty"`
	AccountID             int64                 `json:"account_id"`
	APIKeyID              int64                 `json:"api_key_id"`
	GroupID               int64                 `json:"group_id"`
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
	Attempts              []CodexQualityAttempt `json:"attempts"`
}

// Stored only in the existing host-private namespace. No grant, key, prompt,
// response text, Cookie or STATE value is stored in this ledger.
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

func readCodexQualityRun(ctx context.Context, store PluginExtensionStateStore, id string) (codexQualityRun, int64, error) {
	var run codexQualityRun
	canonical, valid := canonicalCodexQualityID(id)
	if !valid || store == nil {
		return run, 0, ErrCodexQualityUnavailable
	}
	id = canonical
	record, err := store.ReadExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: codexQualityKey(id)})
	if err != nil {
		return run, 0, ErrCodexQualityUnavailable
	}
	if !record.Found {
		return run, 0, nil
	}
	if json.Unmarshal(record.Value, &run) != nil || run.RunID != id || run.MaxSends < 1 || run.MaxSends > codexQualityMaxSends || run.UsedSends != len(run.Attempts) || run.UsedSends > run.MaxSends {
		return codexQualityRun{}, 0, ErrCodexQualityUnavailable
	}
	return run, record.Revision, nil
}

func mutateCodexQualityRun(ctx context.Context, store PluginExtensionStateStore, id string, change func(*codexQualityRun) error) (codexQualityRun, error) {
	canonical, valid := canonicalCodexQualityID(id)
	if !valid {
		return codexQualityRun{}, ErrCodexQualityUnavailable
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
			return run, ErrCodexQualityUnavailable
		}
		result, err := store.CompareSwapExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: codexQualityKey(id), ExpectedRevision: revision, Value: raw})
		if err != nil {
			return run, ErrCodexQualityUnavailable
		}
		if result.Applied {
			return run, nil
		}
	}
	return codexQualityRun{}, ErrCodexQualityUnavailable
}

func newCodexQualityGrant(id string) (string, string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", "", ErrCodexQualityUnavailable
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

func issueCodexQualityGrant(ctx context.Context, store PluginExtensionStateStore, wanted codexQualityRun, digest string, expiry time.Time) (codexQualityRun, error) {
	canonical, valid := canonicalCodexQualityID(wanted.RunID)
	if !valid {
		return codexQualityRun{}, ErrCodexQualityUnavailable
	}
	wanted.RunID = canonical
	return mutateCodexQualityRun(ctx, store, wanted.RunID, func(run *codexQualityRun) error {
		if run.RunID == "" {
			*run = wanted
		} else if run.Status == "closed" || run.ActorID != wanted.ActorID || run.APIKeyID != wanted.APIKeyID || run.AccountID != wanted.AccountID || run.GroupID != wanted.GroupID || run.ProxyID != wanted.ProxyID || run.PromptSHA256 != wanted.PromptSHA256 || run.MaxSends != wanted.MaxSends || !run.Scope.SameOwner(wanted.Scope) {
			return ErrCodexQualityUnavailable
		}
		run.GrantDigest, run.ExpiresAt = digest, expiry
		return nil
	})
}

func sameCodexQualityAttempt(a, b CodexQualityAttempt) bool {
	return a.Stage == b.Stage && a.TrialID == b.TrialID && a.OperationID == b.OperationID
}

func reserveCodexQualityAttempt(ctx context.Context, store PluginExtensionStateStore, id, grantDigest string, attempt CodexQualityAttempt) error {
	_, err := mutateCodexQualityRun(ctx, store, id, func(run *codexQualityRun) error {
		if !codexQualityActive(*run, time.Now()) || (grantDigest != "" && !codexQualityGrantMatches(*run, grantDigest)) || run.AccountID != attempt.AccountID {
			return ErrCodexQualityUnavailable
		}
		for _, previous := range run.Attempts {
			if sameCodexQualityAttempt(previous, attempt) {
				return ErrCodexQualitySpent
			}
		}
		if run.UsedSends >= run.MaxSends {
			return ErrCodexQualitySpent
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

func finishCodexQualityAttempt(ctx context.Context, store PluginExtensionStateStore, id string, finished CodexQualityAttempt) {
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
		return ErrCodexQualityUnavailable
	})
}

func codexQualityView(run codexQualityRun, generation int64) *CodexQualityRunView {
	v := &CodexQualityRunView{RunID: run.RunID, AccountID: run.AccountID, APIKeyID: run.APIKeyID, GroupID: run.GroupID, Model: codexQualityModel, ReasoningEffort: codexQualityEffort, PromptSHA256: run.PromptSHA256, Status: run.Status, MaxSends: run.MaxSends, UsedSends: run.UsedSends, ExpiresAt: run.ExpiresAt, RouteGeneration: run.RouteGeneration, Attempts: run.Attempts}
	if v.Attempts == nil {
		v.Attempts = []CodexQualityAttempt{}
	}
	if q := run.Qualification; q != nil {
		v.RouteExpiresAt = &q.ExpiresAt
		v.RouteReady = codexQualityActive(run, time.Now()) && generation == run.RouteRuntimeGeneration && q.Valid(time.Now(), run.AccountID, run.Scope.Identity, codexQualityModel)
		v.ConnectionFingerprint = codexQualityHash(q.Scope.ConnectionLeaseID)[:16]
	}
	return v
}

func validCodexQualityHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}
