package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
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

func consumeCodexValidationBudget(ctx context.Context, store PluginExtensionStateStore, plugin string, query extensionv1.CodexRoutingQuery) error {
	validation, ok := codexValidationFromContext(ctx)
	if !ok {
		return nil
	}
	if validation.AccountID != query.AccountID || validation.Model != query.Model || validation.Transport != query.Transport || !slices.Contains(codexValidationModels, query.Model) {
		return errCodexRoutingUnavailable
	}
	stage := query.Model + ":" + query.Transport + ":" + query.Stage
	if query.Transport == "ws" && query.Stage != "verify" {
		return errCodexRoutingUnavailable
	}
	key := "validation." + validation.ID
	for range 3 {
		previous, err := store.ReadExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
		if err != nil {
			return errCodexRoutingUnavailable
		}
		budget := codexValidationBudget{AccountID: query.AccountID, Stages: map[string]bool{}}
		if previous.Found && (json.Unmarshal(previous.Value, &budget) != nil || budget.AccountID != query.AccountID || budget.Stages == nil) {
			return errCodexRoutingUnavailable
		}
		if budget.Used >= 7 || budget.Stages[stage] || (query.Transport == "ws" && budget.Stages["ws-spent"]) {
			return errCodexRoutingUnavailable
		}
		budget.Used++
		budget.Stages[stage] = true
		if query.Transport == "ws" {
			budget.Stages["ws-spent"] = true
		}
		raw, _ := json.Marshal(budget)
		next, err := store.CompareSwapExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: previous.Revision, Value: raw})
		if err != nil {
			return errCodexRoutingUnavailable
		}
		if next.Applied {
			return nil
		}
	}
	return errCodexRoutingUnavailable
}

func (s *OpenAIGatewayService) ValidateCodexRouting(ctx context.Context, id int64, request CodexRoutingValidationRequest) (*CodexRoutingValidationResult, error) {
	if uuid.Validate(request.ValidationID) != nil || !slices.Contains(codexValidationModels, request.Model) || (request.Transport != "http" && request.Transport != "ws") || s.pluginManager == nil {
		return nil, errCodexRoutingUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil || !isOpenAICodexTicketAccount(account) {
		return nil, errCodexRoutingUnavailable
	}
	installation, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	store, ok := s.pluginManager.repo.(PluginExtensionStateStore)
	if installation == nil || !ok {
		return nil, errCodexRoutingUnavailable
	}
	server, ok := s.pluginManager.buildHostServices(installation).(*pluginHostServiceServer)
	if !ok || server.extension == nil {
		return nil, errCodexRoutingUnavailable
	}
	ctx = WithPluginExecution(ctx, installation)
	ctx = context.WithValue(ctx, codexValidationContextKey{}, codexValidationContext{ID: request.ValidationID, AccountID: id, Model: request.Model, Transport: request.Transport})
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	lease := extensionv1.LeaseRequest{Namespace: "routing-account", Key: fmt.Sprint(id), Owner: uuid.NewString(), TTLSeconds: 75}
	locked, err := store.AcquireExtensionLease(ctx, installation.PluginKey, lease)
	if err != nil || !locked.Acquired {
		return nil, ErrCodexTicketBusy
	}
	lease.Generation = locked.Generation
	defer func() {
		release, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_, _ = store.ReleaseExtensionLease(release, installation.PluginKey, lease)
	}()
	result := &CodexRoutingValidationResult{ValidationID: request.ValidationID, AccountID: id, Model: request.Model, Transport: request.Transport, BudgetLimit: 7, Observations: []extensionv1.CodexRoutingObservation{}}
	call := func(query extensionv1.CodexRoutingQuery) (extensionv1.CodexRoutingProbeResult, error) {
		raw, _ := json.Marshal(query)
		response, err := server.extension.Call(ctx, extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingProbe, Payload: raw})
		var value extensionv1.CodexRoutingProbeResult
		if err != nil || response.Code != "" || json.Unmarshal(response.Payload, &value) != nil {
			return value, errCodexRoutingUnavailable
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
		previous, readErr := store.ReadExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: storedKey})
		if readErr != nil || !previous.Found || json.Unmarshal(previous.Value, &acquired) != nil {
			return nil, errCodexRoutingUnavailable
		}
	}
	if err == nil && acquired.Valid && acquired.Bundle != nil {
		query.Stage, query.Bundle, query.Scope = "verify", acquired.Bundle, &acquired.Scope
		var verified extensionv1.CodexRoutingProbeResult
		verified, err = call(query)
		result.Success = err == nil && verified.Valid
		if result.Success && request.Transport == "http" {
			raw, _ := json.Marshal(verified)
			_, _ = store.CompareSwapExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: storedKey, Value: raw})
		}
	}
	if err != nil {
		result.Observations = append(result.Observations, extensionv1.CodexRoutingObservation{Stage: query.Stage, Code: "routing_validation_unavailable", RequestedModel: request.Model, Transport: request.Transport, ObservedAt: time.Now().UTC()})
	}
	budgetRecord, readErr := store.ReadExtensionState(context.WithoutCancel(ctx), installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "validation." + request.ValidationID})
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
