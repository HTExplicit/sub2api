package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func requireCindyBalanceProbePolicy(ctx context.Context) error {
	_, err := cindyBalanceProbePlan(ctx)
	if errors.Is(err, ErrExtensionOperationDisabled) {
		return infraerrors.NotFound("CINDY_BALANCE_PROBE_DISABLED", "Cindy balance probes are disabled").WithCause(err)
	}
	if err != nil {
		return infraerrors.New(503, "CINDY_BALANCE_PROBE_UNAVAILABLE", "Cindy balance probe policy is unavailable").WithCause(err)
	}
	return nil
}

func invokeCindyProbePolicy(ctx context.Context, accountID int64, operation string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeCindyProviderCached(call, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: operation, AccountID: accountID, Payload: raw})
	if err != nil {
		return err
	}
	if result.Code == "disabled" {
		return ErrExtensionOperationDisabled
	}
	if result.Code != "" || json.Unmarshal(result.Payload, output) != nil {
		return ErrExtensionOperationUnavailable
	}
	return nil
}

func cindyBalanceProbePlan(ctx context.Context) (extensionv1.CindyProbePlan, error) {
	return cindyBalanceProbePlanForAccount(ctx, 0)
}

func cindyBalanceProbePlanForAccount(ctx context.Context, accountID int64) (extensionv1.CindyProbePlan, error) {
	var plan extensionv1.CindyProbePlan
	if err := invokeCindyProbePolicy(ctx, accountID, "cindy.probe.plan", struct{}{}, &plan); err != nil {
		return plan, err
	}
	if strings.TrimSpace(plan.Models[0]) == "" || strings.TrimSpace(plan.Models[1]) == "" ||
		len(plan.Models[0]) > 256 || len(plan.Models[1]) > 256 || len(plan.Input) == 0 || len(plan.Input) > 1024 ||
		plan.MaxOutputTokens < 1 || plan.MaxOutputTokens > 16 {
		return extensionv1.CindyProbePlan{}, ErrExtensionOperationUnavailable
	}
	return plan, nil
}

func cindyBalanceProbeDecision(ctx context.Context, accountID int64, stage string, wasMarked bool, outcome cindyBalanceProbeOutcome) (extensionv1.CindyProbeDecision, error) {
	var decision extensionv1.CindyProbeDecision
	err := invokeCindyProbePolicy(ctx, accountID, "cindy.probe.decide", extensionv1.CindyProbeResult{
		Stage: stage, WasMarked: wasMarked, Outcome: outcome,
	}, &decision)
	return decision, err
}
