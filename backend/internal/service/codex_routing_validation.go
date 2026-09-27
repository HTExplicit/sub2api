package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/google/uuid"
)

// This request is intentionally not a reusable arbitrary HTTP/model probe.
// One account, three exact models, six HTTP stages and one WS stage form a
// single durable validation run; no caller can raise or reset its budget.
type CodexRoutingValidationRequest struct {
	ValidationID string `json:"validation_id"`
	Model        string `json:"model"`
	Transport    string `json:"transport"`
}

type CodexRoutingValidationResult struct {
	ValidationID string                                `json:"validation_id"`
	AccountID    int64                                 `json:"account_id"`
	Model        string                                `json:"model"`
	Transport    string                                `json:"transport"`
	Success      bool                                  `json:"success"`
	Enrolled     bool                                  `json:"enrolled"`
	BudgetUsed   int                                   `json:"budget_used"`
	BudgetLimit  int                                   `json:"budget_limit"`
	Observations []extensionv1.CodexRoutingObservation `json:"observations"`
}

type codexValidationContextKey struct{}
type codexValidationContext struct {
	ID               string
	AccountID        int64
	Model, Transport string
}
type codexValidationBudget struct {
	AccountID int64           `json:"account_id"`
	Used      int             `json:"used"`
	Stages    map[string]bool `json:"stages"`
}

var codexValidationModels = []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna"}

func codexValidationFromContext(ctx context.Context) (codexValidationContext, bool) {
	value, ok := ctx.Value(codexValidationContextKey{}).(codexValidationContext)
	return value, ok
}

func consumeCodexValidationBudget(ctx context.Context, store NativeCodexStateStore, plugin string, query extensionv1.CodexRoutingQuery) error {
	validation, ok := codexValidationFromContext(ctx)
	if !ok {
		return nil
	}
	if validation.AccountID != query.AccountID || validation.Model != query.Model || validation.Transport != query.Transport || !slices.Contains(codexValidationModels, query.Model) {
		return codexRoutingUnavailable("validation %s is bound to account %d %s/%s, probe asked for account %d %s/%s", validation.ID, validation.AccountID, validation.Model, validation.Transport, query.AccountID, query.Model, query.Transport)
	}
	stage := query.Model + ":" + query.Transport + ":" + query.Stage
	if query.Transport == "ws" && query.Stage != "verify" {
		return codexRoutingUnavailable("WebSocket validation only has a verify stage, not %q", query.Stage)
	}
	key := "validation." + validation.ID
	for range 3 {
		previous, err := store.ReadExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
		if err != nil {
			return codexRoutingUnavailable("read validation budget %s: %v", validation.ID, err)
		}
		budget := codexValidationBudget{AccountID: query.AccountID, Stages: map[string]bool{}}
		if previous.Found && (json.Unmarshal(previous.Value, &budget) != nil || budget.AccountID != query.AccountID || budget.Stages == nil) {
			return codexRoutingUnavailable("validation budget %s belongs to account %d or is invalid: %s", validation.ID, budget.AccountID, string(previous.Value))
		}
		switch {
		case budget.Used >= 7:
			return codexRoutingUnavailable("validation %s already used %d of 7 upstream calls", validation.ID, budget.Used)
		case budget.Stages[stage]:
			return codexRoutingUnavailable("validation %s already spent stage %s", validation.ID, stage)
		case query.Transport == "ws" && budget.Stages["ws-spent"]:
			return codexRoutingUnavailable("validation %s already spent its WebSocket stage", validation.ID)
		}
		budget.Used++
		budget.Stages[stage] = true
		if query.Transport == "ws" {
			budget.Stages["ws-spent"] = true
		}
		raw, _ := json.Marshal(budget)
		next, err := store.CompareSwapExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: previous.Revision, Value: raw})
		if err != nil {
			return codexRoutingUnavailable("save validation budget %s: %v", validation.ID, err)
		}
		if next.Applied {
			return nil
		}
	}
	return codexRoutingUnavailable("validation budget %s kept changing concurrently", validation.ID)
}

func (s *OpenAIGatewayService) ValidateCodexRouting(ctx context.Context, id int64, request CodexRoutingValidationRequest) (*CodexRoutingValidationResult, error) {
	if err := uuid.Validate(request.ValidationID); err != nil {
		return nil, codexRoutingUnavailable("validation_id %q is not a UUID: %v", request.ValidationID, err)
	}
	if !slices.Contains(codexValidationModels, request.Model) {
		return nil, codexRoutingUnavailable("model %q is not one of %v", request.Model, codexValidationModels)
	}
	if request.Transport != "http" && request.Transport != "ws" {
		return nil, codexRoutingUnavailable("transport must be http or ws, got %q", request.Transport)
	}
	if s.nativeCodexRuntime == nil {
		return nil, codexRoutingUnavailable("native Codex runtime is not configured")
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	switch {
	case err != nil:
		return nil, codexRoutingUnavailable("read account %d: %v", id, err)
	case !isOpenAICodexTicketAccount(account):
		return nil, codexRoutingUnavailable("account %d is not an OpenAI OAuth/setup-token Codex account", id)
	}
	installation := s.nativeCodexRuntime.metadata()
	store := s.nativeCodexRuntime.repo
	ok := store != nil
	if installation == nil || !ok {
		return nil, codexRoutingUnavailable("native Codex runtime is not loaded")
	}
	snapshot := s.nativeCodexRuntime.current()
	if snapshot == nil {
		return nil, codexRoutingUnavailable("native Codex runtime snapshot is not loaded")
	}

	ctx = WithNativeCodexExecution(ctx, installation)
	ctx = context.WithValue(ctx, codexValidationContextKey{}, codexValidationContext{ID: request.ValidationID, AccountID: id, Model: request.Model, Transport: request.Transport})
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	lease := extensionv1.LeaseRequest{Namespace: "routing-account", Key: fmt.Sprint(id), Owner: uuid.NewString(), TTLSeconds: 75}
	locked, err := store.AcquireExtensionLease(ctx, NativeCodexPluginKey, lease)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire account routing lease: %v", ErrCodexTicketBusy, err)
	}
	if !locked.Acquired {
		return nil, fmt.Errorf("%w: another routing operation holds account %d", ErrCodexTicketBusy, id)
	}
	lease.Generation = locked.Generation
	defer func() {
		release, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_, _ = store.ReleaseExtensionLease(release, NativeCodexPluginKey, lease)
	}()
	result := &CodexRoutingValidationResult{ValidationID: request.ValidationID, AccountID: id, Model: request.Model, Transport: request.Transport, BudgetLimit: 7, Observations: []extensionv1.CodexRoutingObservation{}}
	call := func(query extensionv1.CodexRoutingQuery) (extensionv1.CodexRoutingProbeResult, error) {
		raw, _ := json.Marshal(query)
		response, err := snapshot.host.Call(ctx, extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingProbe, Payload: raw})
		var value extensionv1.CodexRoutingProbeResult
		switch {
		case err != nil:
			return value, fmt.Errorf("%s probe: %w", query.Stage, err)
		case response.Code != "":
			return value, codexRoutingUnavailable("%s probe returned code %s: %s", query.Stage, response.Code, response.Message)
		}
		if err := json.Unmarshal(response.Payload, &value); err != nil {
			return value, codexRoutingUnavailable("decode %s probe result: %v", query.Stage, err)
		}
		result.Observations = append(result.Observations, value.Observation)
		return value, nil
	}
	query := extensionv1.CodexRoutingQuery{AccountID: id, Model: request.Model, Transport: request.Transport, OperationID: "validation-" + request.ValidationID}
	storedKey := "validation-result." + codexRoutingDigest(request.ValidationID, fmt.Sprint(id), request.Model)
	var acquired extensionv1.CodexRoutingProbeResult
	if request.Transport == "http" {
		query.Stage = "acquire"
		acquired, err = call(query)
	} else {
		previous, readErr := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: storedKey})
		switch {
		case readErr != nil:
			return nil, codexRoutingUnavailable("read the HTTP validation result of %s: %v", request.ValidationID, readErr)
		case !previous.Found:
			return nil, codexRoutingUnavailable("WebSocket validation needs a successful HTTP validation of model %s with the same validation_id first", request.Model)
		}
		if err := json.Unmarshal(previous.Value, &acquired); err != nil {
			return nil, codexRoutingUnavailable("decode the HTTP validation result of %s: %v", request.ValidationID, err)
		}
	}
	if err == nil && acquired.Valid && acquired.Bundle != nil {
		query.Stage, query.Bundle, query.Scope = "verify", acquired.Bundle, &acquired.Scope
		var verified extensionv1.CodexRoutingProbeResult
		verified, err = call(query)
		result.Success = err == nil && verified.Valid
		if result.Success && request.Transport == "http" {
			stored := verified
			stored.Observation = codexRoutingPrivateObservation(stored.Observation)
			raw, _ := json.Marshal(stored)
			_, _ = store.CompareSwapExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: storedKey, Value: raw})
		}
	}
	if err != nil {
		result.Observations = append(result.Observations, extensionv1.CodexRoutingObservation{Stage: query.Stage, Code: "routing_validation_unavailable", RequestedModel: request.Model, Transport: request.Transport, ObservedAt: time.Now().UTC(), Error: err.Error()})
	}
	budgetRecord, readErr := store.ReadExtensionState(context.WithoutCancel(ctx), NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "validation." + request.ValidationID})
	if readErr == nil {
		var budget codexValidationBudget
		if json.Unmarshal(budgetRecord.Value, &budget) == nil {
			result.BudgetUsed = budget.Used
		}
	}
	// No tickets-state, enrollment, failure counter or scheduling projection is
	// written by this operation. A verification run cannot start auto-renewal.
	return result, nil
}
