package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"golang.org/x/net/http/httpguts"
)

// NativeCodexResultError carries a result code returned by the native Codex
// runtime (routing_stale, ticket_missing, ...) and the runtime's own reason.
// It still matches ErrNativeCodexRuntimeUnavailable with errors.Is.
type NativeCodexResultError struct {
	Operation string
	Code      string
	Message   string
}

func (e *NativeCodexResultError) Error() string {
	text := fmt.Sprintf("native Codex %s returned %s (%s)", e.Operation, e.Code, CodexTicketFailure(e.Code).Message)
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

func (e *NativeCodexResultError) Unwrap() error { return ErrNativeCodexRuntimeUnavailable }

func (r *NativeCodexRuntime) ApplyRequestHeaders(ctx context.Context, account *Account, model string, headers http.Header) error {
	if account == nil || headers == nil || !account.IsOpenAIOAuthLike() {
		return nil
	}
	raw, err := json.Marshal(extensionv1.SchedulingRequest{Account: *extensionAccount(account), Model: model, Now: time.Now().UTC()})
	if err != nil {
		return err
	}
	result, err := r.Invoke(ctx, account.Platform, account.Type, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "inject", AccountID: account.ID, Payload: raw})
	if err != nil {
		return err
	}
	var changes extensionv1.CodexRoutingInjection
	if result.Code != "" {
		return &NativeCodexResultError{Operation: "inject", Code: result.Code, Message: result.Message}
	}
	if err := json.Unmarshal(result.Payload, &changes); err != nil {
		return fmt.Errorf("%w: decode the inject result: %v", ErrNativeCodexRuntimeUnavailable, err)
	}
	if changes.Qualification != nil {
		if problem := changes.Qualification.Problem(time.Now(), account.ID, CodexTicketAccountIdentity(account), model); problem != "" {
			return fmt.Errorf("%w: inject returned an unusable route qualification: %s", ErrNativeCodexRuntimeUnavailable, problem)
		}
	}
	projected, seen := headers.Clone(), map[string]bool{}
	for name, value := range changes.Headers {
		lower := strings.ToLower(name)
		blocked := isHeaderOverrideBlockedName(lower) && lower != "x-codex-turn-state"
		switch lower {
		case "openai-organization", "openai-project", "api-key", "set-cookie":
			blocked = true
		}
		if blocked || seen[lower] || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Errorf("invalid native Codex request header %q (blocked=%t, duplicate=%t)", name, blocked, seen[lower])
		}
		seen[lower] = true
		projected.Set(name, value)
	}
	for name, values := range projected {
		headers[name] = values
	}
	return nil
}

// Keep scheduling a projection-only operation. No repository IO is added to selection.
func (r *NativeCodexRuntime) SchedulingDecision(account *Account, model string, now time.Time) extensionv1.SchedulingDecision {
	allow := extensionv1.SchedulingDecision{Allowed: true}
	if account == nil || !isOpenAICodexTicketAccount(account) {
		return allow
	}
	snapshot := r.current()
	if snapshot == nil || snapshot.ctx.Err() != nil {
		return extensionv1.SchedulingDecision{Reason: "plugin_policy_unavailable", Scope: model}
	}
	if !snapshot.config.Enabled || !snapshot.config.FailClosed || !slices.Contains(snapshot.config.Models, model) {
		return allow
	}
	projection := nativeCodexAccountProjection(account)
	constraint, observed := projection.Scheduling[model]
	if !observed {
		constraint, observed = projection.Scheduling["*"]
	}
	if projection.Identity != CodexTicketAccountIdentity(account) || (constraint.Effect == "allow" && constraint.Reason != "routing_verified") || (constraint.Until != nil && !now.Before(*constraint.Until)) {
		observed = false
	}
	if observed {
		if constraint.Effect == "deny" {
			return extensionv1.SchedulingDecision{Reason: constraint.Reason, Until: constraint.Until, Scope: model}
		}
		return allow
	}
	return extensionv1.SchedulingDecision{Reason: "ticket_missing", Scope: model}
}

func (r *NativeCodexRuntime) bindMetadata(ctx context.Context, metadata *NativeCodexMetadata) (context.Context, context.CancelFunc, error) {
	snapshot := r.current()
	if snapshot == nil || metadata == nil || *snapshot.metadata != *metadata {
		return nil, nil, ErrNativeCodexRuntimeChanged
	}
	return snapshot.host.bind(ctx)
}

type nativeCodexLeaseBody struct {
	body    io.ReadCloser
	release context.CancelFunc
	once    sync.Once
}

func (b *nativeCodexLeaseBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err != nil {
		b.once.Do(b.release)
	}
	return n, err
}
func (b *nativeCodexLeaseBody) Close() error {
	err := b.body.Close()
	b.once.Do(b.release)
	return err
}
