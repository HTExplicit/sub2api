package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"golang.org/x/net/http/httpguts"
)

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
	if result.Code != "" || json.Unmarshal(result.Payload, &changes) != nil {
		return ErrNativeCodexRuntimeUnavailable
	}
	if changes.Qualification != nil && !changes.Qualification.Valid(time.Now(), account.ID, CodexTicketAccountIdentity(account), model) {
		return ErrNativeCodexRuntimeUnavailable
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
			return errors.New("invalid native Codex request header")
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
