package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func (m *Module) harvest(ctx, epoch context.Context, host *extensionv1.Client, cfg Config, req Operation, manual bool) (Outcome, error) {
	if !cfg.Enabled {
		return Outcome{Code: "ticket_disabled"}, nil
	}
	if !slices.Contains(cfg.Models, req.Model) {
		return Outcome{Code: "ticket_model_invalid"}, nil
	}
	if cfg.ProxyURL == "" {
		return Outcome{Code: "ticket_proxy_missing"}, nil
	}
	if manual && (req.OperationID == "" || len(req.OperationID) > 256) {
		return Outcome{Code: "ticket_operation_required"}, nil
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		return Outcome{Code: "ticket_canceled"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var account extensionv1.Account
	if err := hostCall(ctx, host, extensionv1.HostAccountRead, extensionv1.AccountQuery{AccountID: req.AccountID}, &account); err != nil {
		return Outcome{}, err
	}
	if !Eligible(account.Platform, account.Type, account.Shadow) {
		return Outcome{Code: "ticket_ineligible"}, nil
	}
	// Cookie jars are account-scoped, not model-scoped. Serialize all models of
	// one account while retaining independent per-model qualification records.
	lease := extensionv1.LeaseRequest{Namespace: "routing-account", Key: strconv.FormatInt(account.ID, 10), Owner: newSessionID(), TTLSeconds: 75}
	var acquired extensionv1.LeaseResult
	if err := hostCall(ctx, host, extensionv1.HostLeaseAcquire, lease, &acquired); err != nil {
		return Outcome{}, err
	}
	if !acquired.Acquired {
		return Outcome{Code: "ticket_busy"}, nil
	}
	lease.Generation = acquired.Generation
	defer func() {
		finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		var released extensionv1.LeaseResult
		_ = hostCall(finish, host, extensionv1.HostLeaseRelease, lease, &released)
	}()
	state, revision, err := readState(ctx, host, account.ID, req.Model)
	if err != nil {
		return Outcome{}, err
	}
	now := time.Now().UTC()
	if req.OperationID != "" && state.OperationID == req.OperationID {
		return Outcome{Code: "ticket_interrupted"}, nil
	}
	if state.Identity != "" && state.Identity != account.Identity {
		state = State{Schema: extensionv1.CodexRoutingSchema, Identity: account.Identity, Phase: "stopped"}
		if !manual {
			_, err = writeState(ctx, host, account.ID, req.Model, state, revision)
			return Outcome{Code: "ticket_stale"}, err
		}
	}
	if state.Phase == "manual_running" || state.Phase == "pre_running" || state.Phase == "post_running" {
		state.Stop()
		state.LastCode = "ticket_interrupted"
		revision, err = writeState(ctx, host, account.ID, req.Model, state, revision)
		if err != nil {
			return Outcome{}, err
		}
		if !manual {
			return Outcome{Code: "ticket_interrupted"}, nil
		}
	}
	if manual && !req.Force && state.Qualification.Valid(now, account.ID, account.Identity, req.Model) {
		var checked extensionv1.CodexRoutingProbeResult
		q := state.Qualification
		if hostCall(ctx, host, extensionv1.HostCodexRoutingCheck, extensionv1.CodexRoutingQuery{AccountID: account.ID, Model: req.Model, Scope: &q.Scope, Bundle: &q.Bundle, Transport: q.Scope.Transport}, &checked) == nil && checked.Valid {
			return Outcome{Success: true, Code: "ticket_skipped", ExpiresAt: &q.ExpiresAt}, nil
		}
	}
	state.Identity = account.Identity
	if err = state.beginRouting(now, manual); err != nil {
		return Outcome{Code: err.Error()}, nil
	}
	if req.OperationID == "" {
		req.OperationID = fmt.Sprintf("renew-%s", lease.Owner)
	}
	state.OperationID = req.OperationID
	revision, err = writeState(ctx, host, account.ID, req.Model, state, revision)
	if err != nil {
		return Outcome{}, err
	}
	observation := extensionv1.CodexRoutingObservation{Stage: "configuration", Code: "routing_unavailable", RequestedModel: req.Model, ObservedAt: now}
	var scope extensionv1.CodexRoutingScope
	var qualified *extensionv1.CodexRoutingQualification
	if scopeErr := hostCall(ctx, host, extensionv1.HostCodexRoutingScope, extensionv1.CodexRoutingQuery{AccountID: account.ID, Transport: "http"}, &scope); scopeErr == nil && scope.Identity == account.Identity {
		query := extensionv1.CodexRoutingQuery{AccountID: account.ID, Model: req.Model, Transport: "http", Stage: "acquire", OperationID: req.OperationID, Scope: &scope}
		var candidate extensionv1.CodexRoutingProbeResult
		if err := hostCall(ctx, host, extensionv1.HostCodexRoutingProbe, query, &candidate); err == nil {
			observation = candidate.Observation
			if candidate.Valid && candidate.Bundle != nil && candidate.Scope.SameOwner(scope) {
				query.Stage, query.Bundle = "verify", candidate.Bundle
				var result extensionv1.CodexRoutingProbeResult
				if err := hostCall(ctx, host, extensionv1.HostCodexRoutingProbe, query, &result); err == nil {
					observation = result.Observation
					if result.Valid && result.Bundle != nil && result.Scope.SameOwner(scope) && result.Observation.Completed && result.Observation.ModelMatched && result.Observation.ResponseModel == req.Model && result.Scope.ConnectionLeaseID != "" {
						verified := time.Now().UTC()
						qualified = &extensionv1.CodexRoutingQualification{Scope: result.Scope, Bundle: *result.Bundle, Model: req.Model, VerifiedAt: verified, ExpiresAt: result.Bundle.ExpiresAt}
					}
				} else {
					observation.Code = "routing_transport"
				}
			}
		} else {
			observation.Code = "routing_transport"
		}
	}
	finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	var current extensionv1.Account
	if err := hostCall(finish, host, extensionv1.HostAccountRead, extensionv1.AccountQuery{AccountID: account.ID}, &current); err != nil || current.Identity != account.Identity {
		return Outcome{Code: "ticket_stale"}, nil
	}
	if ctx.Err() != nil || epoch.Err() != nil {
		qualified = nil
		observation.Code = "ticket_canceled"
	}
	state.completeRouting(time.Now().UTC(), qualified, observation)
	if _, err := writeState(finish, host, account.ID, req.Model, state, revision); err != nil {
		return Outcome{Code: "ticket_persist"}, err
	}
	return Outcome{Success: qualified != nil, Code: observation.Code, HTTPStatus: observation.HTTPStatus, ObservedLength: observation.StateLength, ExpiresAt: state.ExpiresAt, Observation: &observation}, nil
}

func (m *Module) migrateRoutingStates(ctx context.Context, host *extensionv1.Client, cfg Config) {
	var accounts []extensionv1.Account
	if hostCall(ctx, host, extensionv1.HostAccountList, extensionv1.AccountQuery{Platform: "openai", IncludeInactive: true}, &accounts) != nil {
		return
	}
	for _, account := range accounts {
		if !Eligible(account.Platform, account.Type, account.Shadow) {
			continue
		}
		for _, model := range cfg.Models {
			var record extensionv1.StateResult
			if hostCall(ctx, host, extensionv1.HostStateRead, extensionv1.StateRequest{Namespace: "tickets", Key: stateKey(account.ID, model)}, &record) != nil {
				continue
			}
			if !record.Found {
				_, _ = writeState(ctx, host, account.ID, model, State{Schema: 2, Identity: account.Identity, Phase: "needs_cookie_verification"}, 0)
				continue
			}
			var state State
			if json.Unmarshal(record.Value, &state) != nil || state.Schema >= extensionv1.CodexRoutingSchema {
				continue
			}
			state.migrateRouting()
			_, _ = writeState(ctx, host, account.ID, model, state, record.Revision)
		}
	}
}

type routingDemand struct {
	At time.Time `json:"at"`
}

func hasRoutingDemand(ctx context.Context, host *extensionv1.Client, id int64, model string) bool {
	var record extensionv1.StateResult
	var demand routingDemand
	return hostCall(ctx, host, extensionv1.HostStateRead, extensionv1.StateRequest{Namespace: "routing-demand", Key: stateKey(id, model)}, &record) == nil && record.Found && json.Unmarshal(record.Value, &demand) == nil && time.Since(demand.At) >= 0 && time.Since(demand.At) <= extensionv1.CodexRoutingMaxAge
}

func (m *Module) recordRoutingDemand(ctx context.Context, host *extensionv1.Client, cfg Config, request extensionv1.CodexRoutingDemand) (any, error) {
	state, revision, err := readState(ctx, host, request.AccountID, request.Model)
	if err != nil || !cfg.Enabled || !state.Enrolled || state.Phase == "stopped" {
		return map[string]bool{"enqueued": false}, err
	}
	key := stateKey(request.AccountID, request.Model)
	var previous extensionv1.StateResult
	if err := hostCall(ctx, host, extensionv1.HostStateRead, extensionv1.StateRequest{Namespace: "routing-demand", Key: key}, &previous); err != nil {
		return nil, err
	}
	var old routingDemand
	if previous.Found && state.NextAt != nil && json.Unmarshal(previous.Value, &old) == nil && time.Since(old.At) < 10*time.Second {
		return map[string]bool{"enqueued": true}, nil
	}
	raw, _ := json.Marshal(routingDemand{At: time.Now().UTC()})
	var saved extensionv1.StateResult
	err = hostCall(ctx, host, extensionv1.HostStateCompareSwap, extensionv1.StateRequest{Namespace: "routing-demand", Key: key, ExpectedRevision: previous.Revision, Value: raw}, &saved)
	if err == nil && saved.Applied && state.NextAt == nil && state.Phase != "manual_running" && state.Phase != "pre_running" && state.Phase != "post_running" {
		due := time.Now().UTC()
		if state.ExpiresAt != nil && state.ExpiresAt.Add(-extensionv1.CodexRoutingRefreshLead).After(due) {
			due = state.ExpiresAt.Add(-extensionv1.CodexRoutingRefreshLead)
		}
		if state.Phase == "retry" && state.LastAttemptAt != nil && state.LastAttemptAt.Add(extensionv1.CodexRoutingRefreshLead).After(due) {
			due = state.LastAttemptAt.Add(extensionv1.CodexRoutingRefreshLead)
		}
		state.NextAt = &due
		_, err = writeState(ctx, host, request.AccountID, request.Model, state, revision)
	}
	return map[string]bool{"enqueued": err == nil && saved.Applied}, err
}

func observeRoutingResponse(ctx context.Context, host *extensionv1.Client, req extensionv1.CodexRoutingResponse) (any, error) {
	if req.AccountID <= 0 || req.Identity == "" || req.Model == "" {
		return nil, errors.New("invalid routing response")
	}
	state, revision, err := readState(ctx, host, req.AccountID, req.Model)
	if err != nil {
		return nil, err
	}
	q := state.Qualification
	if q == nil || state.Identity != req.Identity || q.Bundle.Key != req.Qualification.Bundle.Key || q.Bundle.Revision != req.Qualification.Bundle.Revision {
		return map[string]bool{"applied": false}, nil
	}
	if state.Phase == "manual_running" || state.Phase == "pre_running" || state.Phase == "post_running" {
		return map[string]bool{"applied": false}, nil
	}
	state.Observation, state.LastCode = &req.Observation, req.Observation.Code
	if replacement := req.Replacement; replacement != nil && req.Observation.Completed && req.Observation.ModelMatched && replacement.Valid(time.Now(), req.AccountID, req.Identity, req.Model) && replacement.Scope.SameOwner(q.Scope) {
		state.Qualification, state.ExpiresAt = replacement, &replacement.ExpiresAt
		if state.Phase == "ready" {
			due := replacement.ExpiresAt.Add(-extensionv1.CodexRoutingRefreshLead)
			state.NextAt = &due
		}
	}
	if !req.Observation.Completed || !req.Observation.ModelMatched || req.Observation.ResponseModel != req.Model || req.Observation.Code == "routing_cookie_deleted" {
		state.Qualification, state.ExpiresAt = nil, nil
		if state.Phase != "stopped" && state.Phase != "manual_running" && state.Phase != "pre_running" && state.Phase != "post_running" {
			state.Phase = "needs_cookie_verification"
			now := time.Now().UTC()
			state.NextAt = &now
		}
	}
	_, err = writeState(ctx, host, req.AccountID, req.Model, state, revision)
	return map[string]bool{"applied": err == nil}, err
}
