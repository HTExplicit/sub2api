package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func invokePromptManagement(ctx context.Context, operation string, input, output any) error {
	return invokePromptManagementPolicy(ctx, operation, input, output, false)
}

func invokePromptManagementPolicy(ctx context.Context, operation string, input, output any, cached bool) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if operation == "prompt.source.fetch" {
		call = ctx
	}
	invoke := invokeProcessExtension
	if cached {
		invoke = invokeProcessExtensionCached
	}
	result, err := invoke(call, PlatformOpenAI, "*", extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: operation, Payload: raw})
	if err != nil {
		if strings.HasPrefix(operation, "prompt.source.") {
			return ErrBusinessSystemPromptSourceUnavailable
		}
		if strings.HasPrefix(operation, "skills.") && operation != "skills.capture" {
			return ErrBusinessSystemPromptBundleUnavailable
		}
		return ErrBusinessSystemPromptUnavailable
	}
	if result.Code == "bundle_invalid" {
		return fmt.Errorf("%w: %s", ErrBusinessSystemPromptBundleInvalid, result.Message)
	}
	switch result.Code {
	case "source_unavailable":
		return ErrBusinessSystemPromptSourceUnavailable
	case "source_invalid":
		return ErrBusinessSystemPromptSourceInvalid
	case "source_license_changed":
		return ErrBusinessSystemPromptSourceLicenseChanged
	}
	if result.Code == "source_managed" {
		return ErrBusinessSystemPromptSourceNotManaged
	}
	if result.Code != "" {
		return fmt.Errorf("%w: %s", ErrBusinessSystemPromptInvalid, result.Message)
	}
	if output != nil && json.Unmarshal(result.Payload, output) != nil {
		return ErrBusinessSystemPromptUnavailable
	}
	return nil
}

func planPromptTemplate(ctx context.Context, request extensionv1.PromptTemplatePolicyRequest) (extensionv1.PromptTemplatePolicyPlan, error) {
	var plan extensionv1.PromptTemplatePolicyPlan
	err := invokePromptManagement(ctx, "prompt.template", request, &plan)
	if err == nil && ((request.Name != nil && plan.Name == nil) || (request.Description != nil && plan.Description == nil)) {
		err = ErrBusinessSystemPromptUnavailable
	}
	return plan, err
}

func remoteSkillFilePlan(ctx context.Context, name string, data []byte) (extensionv1.SkillFilePlan, error) {
	var plan extensionv1.SkillFilePlan
	err := invokePromptManagementPolicy(ctx, "skills.file.plan", extensionv1.SkillFileInspection{Path: name, Prefix: data[:min(2, len(data))], UTF8: utf8.Valid(data)}, &plan, true)
	if err == nil && (plan.Kind != "text" && plan.Kind != "script" && plan.Kind != "binary" || len(plan.ReplaceFrom) > 2048 || len(plan.ReplaceTo) > 2048) {
		err = ErrBusinessSystemPromptBundleInvalid
	}
	return plan, err
}
