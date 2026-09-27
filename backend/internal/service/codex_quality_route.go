package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/google/uuid"
)

func (s *OpenAIGatewayService) RenewCodexQualityRoute(ctx context.Context, actor, accountID int64, id, operation string, lookup CodexQualityKeyLookup) (*CodexQualityRunView, error) {
	canonical, valid := canonicalCodexQualityID(operation)
	if !valid {
		return nil, codexQualityUnavailable("operation_id %q is not a canonical UUID", operation)
	}
	if lookup == nil {
		return nil, codexQualityUnavailable("no API key lookup for the route renewal")
	}
	operation = canonical
	requested := id
	id, valid = canonicalCodexQualityID(id)
	if !valid {
		return nil, codexQualityUnavailable("run id %q is not a canonical UUID", requested)
	}
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return nil, err
	}
	ctx = rt.ctx(ctx)
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	run, _, err := readCodexQualityRun(ctx, rt.store, id)
	switch {
	case err != nil:
		return nil, err
	case run.RunID == "":
		return nil, codexQualityUnavailable("quality run %s not found", id)
	case run.ActorID != actor:
		return nil, codexQualityUnavailable("quality run %s was created by administrator %d; only its creator can renew its route with that administrator's API key", run.RunID, run.ActorID)
	case run.AccountID != accountID:
		return nil, codexQualityUnavailable("quality run %s belongs to account %d, not %d", run.RunID, run.AccountID, accountID)
	case !codexQualityActive(run, time.Now()):
		return nil, codexQualityUnavailable("%s", codexQualityInactiveReason(run, time.Now()))
	}
	key, err := lookup(ctx, run.APIKeyID)
	if err != nil {
		return nil, codexQualityUnavailable("look up API key %d: %v", run.APIKeyID, err)
	}
	if !codexQualityKeyUsable(key, actor, run.APIKeyID, run.GroupID) {
		return nil, codexQualityUnavailable("API key %d is no longer usable for quality run %s", run.APIKeyID, run.RunID)
	}
	for _, a := range run.Attempts {
		if a.OperationID == operation {
			return s.ReadCodexQualityRun(ctx, actor, accountID, id)
		}
	}
	account, err := rt.account(ctx, run)
	if err != nil {
		return nil, err
	}
	if s.concurrencyService == nil {
		return nil, codexQualityUnavailable("no concurrency service")
	}
	concurrency, err := s.concurrencyService.AcquireAccountSlot(ctx, accountID, account.Concurrency)
	switch {
	case err != nil:
		return nil, codexQualityUnavailable("acquire a concurrency slot of account %d: %v", accountID, err)
	case concurrency == nil || !concurrency.Acquired:
		return nil, codexQualityUnavailable("account %d has no free concurrency slot (limit %d)", accountID, account.Concurrency)
	}
	defer concurrency.ReleaseFunc()
	lease := extensionv1.LeaseRequest{Namespace: "routing-account", Key: fmt.Sprint(accountID), Owner: uuid.NewString(), TTLSeconds: 75}
	locked, err := rt.store.AcquireExtensionLease(ctx, codexRuntimePluginKey, lease)
	switch {
	case err != nil:
		return nil, codexQualityUnavailable("acquire the routing lease of account %d: %v", accountID, err)
	case !locked.Acquired:
		return nil, codexQualityUnavailable("another routing operation holds account %d", accountID)
	}
	lease.Generation = locked.Generation
	defer func() {
		release, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_, _ = rt.store.ReleaseExtensionLease(release, codexRuntimePluginKey, lease)
	}()
	query := codexQualityRenewQuery(run, operation)
	call := func() (extensionv1.CodexRoutingProbeResult, error) {
		e := &codexQualityExecution{runtime: rt, runID: id, operationID: operation, stage: query.Stage, accountID: accountID, keyLookup: lookup}
		stageCtx := context.WithValue(ctx, codexQualityExecutionKey{}, e)
		raw, _ := json.Marshal(query)
		result, callErr := rt.host.Call(stageCtx, extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingProbe, Payload: raw})
		var out extensionv1.CodexRoutingProbeResult
		switch {
		case callErr != nil:
			return out, codexQualityUnavailable("%s probe: %v", query.Stage, callErr)
		case result.Code != "":
			return out, codexQualityUnavailable("%s probe returned %s: %s", query.Stage, result.Code, result.Message)
		}
		if err := json.Unmarshal(result.Payload, &out); err != nil {
			return out, codexQualityUnavailable("decode %s probe result: %v", query.Stage, err)
		}
		return out, nil
	}
	acquired, err := call()
	if err != nil || !acquired.Valid || acquired.Bundle == nil {
		return s.codexQualityRenewalFailure(ctx, actor, accountID, id, codexQualityRenewalReason("acquire", err, acquired, run.Scope), acquired.Observation)
	}
	query.Stage, query.Bundle = "verify", acquired.Bundle
	verified, err := call()
	if err != nil || !verified.Valid || verified.Bundle == nil || !verified.Observation.Completed || !verified.Observation.ModelMatched || verified.Observation.ResponseModel != codexQualityModel || verified.Scope.ConnectionLeaseID == "" || !run.Scope.SameOwner(verified.Scope) {
		return s.codexQualityRenewalFailure(ctx, actor, accountID, id, codexQualityRenewalReason("verify", err, verified, run.Scope), acquired.Observation, verified.Observation)
	}
	q := &extensionv1.CodexRoutingQualification{Scope: verified.Scope, Bundle: *verified.Bundle, Model: codexQualityModel, VerifiedAt: time.Now().UTC(), ExpiresAt: verified.Bundle.ExpiresAt}
	saved, err := mutateCodexQualityRun(ctx, rt.store, id, func(current *codexQualityRun) error {
		switch {
		case !codexQualityActive(*current, time.Now()):
			return codexQualityUnavailable("%s", codexQualityInactiveReason(*current, time.Now()))
		case current.ActorID != actor:
			return codexQualityUnavailable("quality run %s was created by administrator %d", current.RunID, current.ActorID)
		case !current.Scope.SameOwner(q.Scope):
			return codexQualityUnavailable("verified route %s", codexRoutingScopeChange(current.Scope, q.Scope))
		}
		current.Qualification, current.RouteRuntimeGeneration = q, rt.installation.RuntimeGeneration
		current.RouteGeneration++
		return nil
	})
	if err != nil {
		s.closeCodexQualityConnection(q)
		return nil, err
	}
	// The old diagnostic connection has no ordinary qualification owners.
	if run.Qualification != nil && run.Qualification.Scope.ConnectionLeaseID != q.Scope.ConnectionLeaseID {
		s.closeCodexQualityConnection(run.Qualification)
	}
	return codexQualityView(saved, rt.installation.RuntimeGeneration), nil
}

// codexQualityRenewalFailure returns the unchanged run together with the reason
// the renewal did not install a route and the observations of its stages.
func (s *OpenAIGatewayService) codexQualityRenewalFailure(ctx context.Context, actor, accountID int64, id, reason string, observations ...extensionv1.CodexRoutingObservation) (*CodexQualityRunView, error) {
	view, err := s.ReadCodexQualityRun(ctx, actor, accountID, id)
	if err != nil {
		return nil, fmt.Errorf("%w (route renewal failed: %s)", err, reason)
	}
	view.RenewalError = reason
	for _, observation := range observations {
		if observation.Code != "" || !observation.ObservedAt.IsZero() {
			view.RenewalObservations = append(view.RenewalObservations, observation)
		}
	}
	return view, nil
}

func codexQualityRenewalReason(stage string, err error, result extensionv1.CodexRoutingProbeResult, owner extensionv1.CodexRoutingScope) string {
	if err != nil {
		return err.Error()
	}
	observation := result.Observation
	var reasons []string
	switch {
	case !result.Valid:
		reasons = append(reasons, "the probe result is not valid")
	case result.Bundle == nil:
		reasons = append(reasons, "the probe stored no route bundle")
	}
	if stage == "verify" {
		if !observation.Completed {
			reasons = append(reasons, "the response did not complete")
		}
		if !observation.ModelMatched || observation.ResponseModel != codexQualityModel {
			reasons = append(reasons, fmt.Sprintf("the upstream declared model %q instead of %s", observation.ResponseModel, codexQualityModel))
		}
		if result.Scope.ConnectionLeaseID == "" {
			reasons = append(reasons, "no connection lease was bound")
		}
		if !owner.SameOwner(result.Scope) {
			reasons = append(reasons, codexRoutingScopeChange(owner, result.Scope))
		}
	}
	reason := fmt.Sprintf("%s stage ended with %s: %s", stage, observation.Code, strings.Join(reasons, "; "))
	if summary := observation.Summary(); summary != "" {
		reason += " (" + summary + ")"
	}
	return reason
}

func codexQualityRenewQuery(run codexQualityRun, operation string) extensionv1.CodexRoutingQuery {
	return extensionv1.CodexRoutingQuery{
		AccountID: run.AccountID, Model: codexQualityModel, ReasoningEffort: codexQualityEffort,
		Transport: "http", Stage: "acquire", OperationID: "quality-" + run.RunID + "-" + operation, Scope: &run.Scope,
	}
}
