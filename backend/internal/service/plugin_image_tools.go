package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func currentImageToolsConfig() (extensionv1.ImageToolsConfig, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(ctx, "*", "*", extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "image.features", Payload: json.RawMessage(`{}`)})
	var config extensionv1.ImageToolsConfig
	if err != nil || result.Code != "" || json.Unmarshal(result.Payload, &config) != nil {
		return config, false
	}
	return config, true
}

func EnsureImageStudioAvailable(ctx context.Context) error {
	result, err := invokeProcessExtensionCached(ctx, "*", "*", extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "image.features", Payload: json.RawMessage(`{}`)})
	var config extensionv1.ImageToolsConfig
	if err != nil || result.Code != "" || json.Unmarshal(result.Payload, &config) != nil {
		return newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	if !config.StudioEnabled {
		return newImageStudioError(404, "studio_disabled", "Image Studio is not enabled")
	}
	return nil
}

func planImageStudio(ctx context.Context, input ImageStudioCreateInput, hasReference, hasMask, execute bool) (extensionv1.ImageStudioPlan, error) {
	request := extensionv1.ImageStudioPlanRequest{APIKeySelected: input.APIKeyID > 0, Mode: string(input.Mode), Model: input.Model, PromptBytes: len(strings.TrimSpace(input.Prompt)), Count: input.Count, Size: input.Size, Quality: input.Quality, HasReference: hasReference, HasMask: hasMask}
	raw, _ := json.Marshal(request)
	operation := "image.studio.validate"
	if execute {
		operation = "image.studio.plan"
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtension(call, "*", "*", extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: operation, Payload: raw})
	var plan extensionv1.ImageStudioPlan
	if err != nil {
		return plan, newImageStudioError(503, "studio_unavailable", "Image Studio is unavailable")
	}
	if result.Code != "" {
		return plan, newImageStudioError(result.HTTPStatus, result.Code, result.Message)
	}
	if json.Unmarshal(result.Payload, &plan) != nil || plan.Model == "" || plan.OutputPerRequest != 1 || (plan.Endpoint != "/v1/images/generations" && plan.Endpoint != "/v1/images/edits") || plan.ResponseFormat != "b64_json" {
		return plan, newImageStudioError(503, "studio_unavailable", "Image Studio returned an invalid request plan")
	}
	return plan, nil
}

func PlanImageStudioExecution(ctx context.Context, request ImageStudioExecutionRequest) (extensionv1.ImageStudioPlan, error) {
	keyID := request.Job.APIKeyID
	if request.APIKey != nil {
		keyID = request.APIKey.ID
	}
	return planImageStudio(ctx, ImageStudioCreateInput{APIKeyID: keyID, Mode: request.Job.Mode, Model: request.Job.Model, Prompt: request.Job.Prompt, Count: 1, Size: request.Job.Size, Quality: request.Job.Quality}, len(request.Reference) > 0, len(request.Mask) > 0, true)
}
