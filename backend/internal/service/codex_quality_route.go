package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/google/uuid"
)

func (s *OpenAIGatewayService) RenewCodexQualityRoute(ctx context.Context, actor, accountID int64, id, operation string, lookup CodexQualityKeyLookup) (*CodexQualityRunView, error) {
	canonical, valid := canonicalCodexQualityID(operation)
	if !valid || lookup == nil {
		return nil, ErrCodexQualityUnavailable
	}
	operation = canonical
	id, valid = canonicalCodexQualityID(id)
	if !valid {
		return nil, ErrCodexQualityUnavailable
	}
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return nil, err
	}
	ctx = rt.ctx(ctx)
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	run, _, err := readCodexQualityRun(ctx, rt.store, id)
	if err != nil || run.ActorID != actor || run.AccountID != accountID || !codexQualityActive(run, time.Now()) {
		return nil, ErrCodexQualityUnavailable
	}
	key, err := lookup(ctx, run.APIKeyID)
	if err != nil || !codexQualityKeyUsable(key, actor, run.APIKeyID, run.GroupID) {
		return nil, ErrCodexQualityUnavailable
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
		return nil, ErrCodexQualityUnavailable
	}
	concurrency, err := s.concurrencyService.AcquireAccountSlot(ctx, accountID, account.Concurrency)
	if err != nil || concurrency == nil || !concurrency.Acquired {
		return nil, ErrCodexQualityUnavailable
	}
	defer concurrency.ReleaseFunc()
	lease := extensionv1.LeaseRequest{Namespace: "routing-account", Key: fmt.Sprint(accountID), Owner: uuid.NewString(), TTLSeconds: 75}
	locked, err := rt.store.AcquireExtensionLease(ctx, codexRuntimePluginKey, lease)
	if err != nil || !locked.Acquired {
		return nil, ErrCodexQualityUnavailable
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
		if callErr != nil || result.Code != "" || json.Unmarshal(result.Payload, &out) != nil {
			return out, ErrCodexQualityUnavailable
		}
		return out, nil
	}
	acquired, err := call()
	if err != nil || !acquired.Valid || acquired.Bundle == nil {
		return s.ReadCodexQualityRun(ctx, actor, accountID, id)
	}
	query.Stage, query.Bundle = "verify", acquired.Bundle
	verified, err := call()
	if err != nil || !verified.Valid || verified.Bundle == nil || !verified.Observation.Completed || !verified.Observation.ModelMatched || verified.Observation.ResponseModel != codexQualityModel || verified.Scope.ConnectionLeaseID == "" || !run.Scope.SameOwner(verified.Scope) {
		return s.ReadCodexQualityRun(ctx, actor, accountID, id)
	}
	q := &extensionv1.CodexRoutingQualification{Scope: verified.Scope, Bundle: *verified.Bundle, Model: codexQualityModel, VerifiedAt: time.Now().UTC(), ExpiresAt: verified.Bundle.ExpiresAt}
	saved, err := mutateCodexQualityRun(ctx, rt.store, id, func(current *codexQualityRun) error {
		if !codexQualityActive(*current, time.Now()) || current.ActorID != actor || !current.Scope.SameOwner(q.Scope) {
			return ErrCodexQualityUnavailable
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

func codexQualityRenewQuery(run codexQualityRun, operation string) extensionv1.CodexRoutingQuery {
	return extensionv1.CodexRoutingQuery{
		AccountID: run.AccountID, Model: codexQualityModel, ReasoningEffort: codexQualityEffort,
		Transport: "http", Stage: "acquire", OperationID: "quality-" + run.RunID + "-" + operation, Scope: &run.Scope,
	}
}
