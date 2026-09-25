package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type batchConnectionSnapshot struct {
	ModelID         string `json:"model_id"`
	MappedModelID   string `json:"mapped_model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	WirePlatform    string `json:"wire_platform"`
	APIProtocol     string `json:"api_protocol"`
	PlanStamp       string `json:"plan_stamp"`
}

func connectionPlanStamp(plan *accountTestPlanView, account *service.Account, modelID, effectiveModel string) string {
	var selected map[string]any
	for _, model := range plan.Models {
		if model["id"] == modelID {
			selected = model
			break
		}
	}
	// Only selected-model execution facts participate. Display names, catalog
	// order and unrelated models must not invalidate an accepted plan.
	facts := map[string]any{"purpose": "connection", "wire_platform": plan.WirePlatform,
		"mapped_model_id": effectiveModel, "api_protocol": account.GetAPIProtocol(),
		"policy_stamp": plan.PolicyStamp}
	for _, key := range []string{"endpoints", "input_modalities", "output_modalities", "reasoning_efforts", "default_reasoning_effort", "live_upstream_id"} {
		facts[key] = selected[key]
	}
	raw, _ := json.Marshal(facts)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (h *AccountHandler) executeBatchConnectionTest(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	startedAt := time.Now()
	id, ok := accountJobTarget(item)
	if !ok {
		return accountJobFailed(item.ID, "target_missing")
	}
	testCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	metadata := map[string]any{"account_id": id, "connection_status": "failed", "recovery_status": "not_attempted", "stage": "selection"}
	if len(item.Metadata) > 0 {
		_ = json.Unmarshal(item.Metadata, &metadata)
	}
	metadata["stage"], metadata["connection_status"], metadata["recovery_status"] = "selection", "failed", "not_attempted"
	metadata["message"] = ""
	fail := func(code string, causes ...error) service.AccountJobExecutionResult {
		metadata["latency_ms"] = time.Since(startedAt).Milliseconds()
		metadata["connection_status"] = "failed"
		if ctx.Err() != nil {
			metadata["message"] = ""
			metadata["connection_status"] = "canceled"
			encoded, _ := json.Marshal(metadata)
			return service.AccountJobExecutionResult{ItemID: item.ID, Status: service.AccountJobItemStatusCanceled, Metadata: encoded}
		}
		if errors.Is(testCtx.Err(), context.DeadlineExceeded) {
			code = "test_timeout"
		}
		result := accountJobFailed(item.ID, code)
		metadata["message"] = result.ErrorMessage
		if len(causes) > 0 && service.AccountTestFailureCode(causes[0]) == code {
			metadata["message"] = service.AccountTestSafeFailureMessage(causes[0])
		}
		result.Metadata, _ = json.Marshal(metadata)
		return result
	}
	var request batchTestJobPayload
	if json.Unmarshal(raw, &request) != nil || service.ValidateAccountTestPrompt(request.Prompt) != nil {
		return fail("payload_invalid")
	}
	models, prepared := ctx.Value(batchTestModelContextKey{}).(map[int64]string)
	if !prepared {
		var err error
		_, models, err = request.normalizeContext(testCtx)
		if err != nil {
			return fail("payload_invalid")
		}
	}
	model, exists := models[id]
	if !exists {
		return fail("target_missing")
	}
	effort := ""
	for _, selected := range request.Items {
		if selected.AccountID == id {
			effort = selected.ReasoningEffort
			break
		}
	}
	if h.accountTestService == nil || h.adminService == nil {
		return fail("test_unavailable")
	}
	account, err := h.adminService.GetAccount(testCtx, id)
	if err != nil || account == nil {
		return fail("account_not_found")
	}
	metadata["name"] = account.Name
	plan, err := h.accountTestPlan(testCtx, account)
	if err != nil {
		return fail("test_catalog_failed")
	}
	view := plan.ModeViews["connection"]
	var previous struct {
		Plan *batchConnectionSnapshot `json:"execution_plan"`
	}
	if json.Unmarshal(item.Metadata, &previous) != nil && len(item.Metadata) > 0 {
		return fail("payload_invalid")
	}
	if previous.Plan != nil {
		model, effort = previous.Plan.ModelID, previous.Plan.ReasoningEffort
		if !slices.Contains(view.ModelIDs, model) {
			return fail("test_plan_changed")
		}
	} else if model == "" {
		model = view.DefaultModelID
	}
	if model == "" {
		return fail("test_no_text_model")
	}
	if !slices.Contains(view.ModelIDs, model) {
		return fail("test_model_unsupported")
	}
	if previous.Plan == nil && effort == "" {
		levels, defaultEffort := service.AccountTestReasoningOptions(account, model)
		if defaultEffort != "" && slices.Contains(levels, defaultEffort) {
			effort = defaultEffort
		}
	}
	if err := service.ValidateAccountTestReasoningContext(testCtx, account, model, service.AccountTestModeDefault, effort); err != nil {
		if previous.Plan != nil {
			return fail("test_plan_changed")
		}
		return fail("test_reasoning_unsupported")
	}
	actualModel, mappingErr := service.ResolveAccountTestExecutionModel(testCtx, account, model)
	if mappingErr != nil {
		return fail("test_catalog_failed")
	}
	stamp := connectionPlanStamp(plan, account, model, actualModel)
	if actualModel == "" || (previous.Plan != nil && (previous.Plan.MappedModelID != actualModel || previous.Plan.PlanStamp != stamp)) {
		return fail("test_plan_changed")
	}
	snapshot := batchConnectionSnapshot{ModelID: model, MappedModelID: actualModel, ReasoningEffort: effort,
		WirePlatform: plan.WirePlatform, APIProtocol: account.GetAPIProtocol(), PlanStamp: stamp}
	metadata["execution_plan"], metadata["model_id"], metadata["reasoning_effort"] = snapshot, snapshot.MappedModelID, effort
	metadata["requested_model_id"], metadata["stage"] = model, "connection"
	metadata["connection_status"] = "running"
	encoded, _ := json.Marshal(metadata)
	if err := h.accountJobs.SaveExecutionSnapshot(testCtx, item.JobID, item.ID, encoded); err != nil {
		return fail("test_plan_persist_failed")
	}
	result, testErr := h.accountTestService.RunBatchTestBackgroundWithOptions(testCtx, id, model, request.Prompt,
		service.AccountTestOptions{ReasoningEffort: effort, ExpectedMappedModel: snapshot.MappedModelID,
			ExpectedWirePlatform: snapshot.WirePlatform, ExpectedAPIProtocol: snapshot.APIProtocol})
	if result != nil {
		metadata["latency_ms"], metadata["output_limited"] = result.LatencyMs, result.OutputLimited
		if result.ActualModel != "" {
			metadata["model_id"] = result.ActualModel
		}
		if result.EffectiveReasoningEffort != "" {
			metadata["effective_reasoning_effort"] = result.EffectiveReasoningEffort
		}
	}
	if testErr != nil || result == nil || result.Status != "success" {
		if ctx.Err() != nil {
			canceled := fail("test_failed")
			canceled.Status = service.AccountJobItemStatusCanceled
			return canceled
		}
		code := service.AccountTestFailureCode(testErr)
		if testCtx.Err() == context.DeadlineExceeded {
			code = "test_timeout"
		}
		return fail(code, testErr)
	}
	// A completed model call stays successful even when recovery fails, or Stop
	// arrives after the terminal event. Failed-item retry must not resend it.
	metadata["connection_status"], metadata["stage"] = "succeeded", "complete"
	recoveryCtx, release := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer release()
	metadata["recovery_status"] = "not_needed"
	if h.rateLimitService == nil {
		metadata["recovery_status"] = "warning"
	} else {
		recovered, recoverErr := h.rateLimitService.RecoverAccountAfterSuccessfulTest(recoveryCtx, id)
		switch {
		case recoverErr != nil:
			metadata["recovery_status"] = "warning"
		case recovered != nil && recovered.ManualStatePreserved:
			metadata["recovery_status"] = "manual_state_preserved"
		case recovered != nil && (recovered.ClearedError || recovered.ClearedRateLimit):
			metadata["recovery_status"] = "recovered"
		}
	}
	return accountJobSucceeded(item.ID, metadata)
}
