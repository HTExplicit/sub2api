package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func invokePromptManagement(ctx context.Context, operation string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if operation == "prompt.source.fetch" {
		call = ctx
	}
	result, err := invokePromptSkills(call, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: operation, Payload: raw})
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

func planPromptPublication(ctx context.Context, request extensionv1.PromptPublicationPolicyRequest) (extensionv1.PromptPublicationPolicyPlan, error) {
	var plan extensionv1.PromptPublicationPolicyPlan
	err := invokePromptManagement(ctx, "prompt.publication.plan", request, &plan)
	if err != nil {
		return plan, err
	}
	// Compare canonical spellings: EqualFold would additionally accept Unicode
	// folds such as long-s in an otherwise ASCII publication action.
	plannedAction := strings.ToLower(strings.TrimSpace(plan.Action))
	requestedAction := strings.ToLower(strings.TrimSpace(request.Action))
	if plannedAction != requestedAction || !plan.Allowed {
		return plan, ErrBusinessSystemPromptInvalid
	}
	return plan, nil
}

func planSkillPublication(ctx context.Context, request extensionv1.SkillPublicationPolicyRequest) error {
	var plan extensionv1.SkillPublicationPolicyPlan
	if err := invokePromptManagement(ctx, "skills.publication.plan", request, &plan); err != nil {
		return err
	}
	plannedAction := strings.ToLower(strings.TrimSpace(plan.Action))
	requestedAction := strings.ToLower(strings.TrimSpace(request.Action))
	if plannedAction != requestedAction || !plan.Allowed {
		return ErrBusinessSystemPromptBundleInvalid
	}
	return nil
}

func LoadRemoteSkillRegistryProfile(ctx context.Context) (extensionv1.SkillRegistryPolicyProfile, error) {
	var profile extensionv1.SkillRegistryPolicyProfile
	if err := invokePromptManagement(ctx, "skills.profile", struct{}{}, &profile); err != nil {
		return profile, err
	}
	if err := validateRemoteSkillRegistryProfile(profile); err != nil {
		return extensionv1.SkillRegistryPolicyProfile{}, err
	}
	return profile, nil
}

func validateRemoteSkillRegistryProfile(profile extensionv1.SkillRegistryPolicyProfile) error {
	// This named source has an existing network grant and persistent identity.
	// A policy profile may refine its contents/limits, not grant itself a new
	// URL or rewrite public links to a different origin.
	if profile.SourceID != RemoteSkillUpstreamSourceID || profile.UpstreamRoot != RemoteSkillUpstreamRoot || profile.PublicRoot != RemoteSkillPublicRoot {
		return ErrBusinessSystemPromptBundleInvalid
	}
	if strings.TrimSpace(profile.SourceID) == "" || profile.MaxFileCount < 1 || profile.MaxFileCount > remoteSkillMaxFileCount ||
		profile.MaxTotalBytes < 1 || profile.MaxTotalBytes > remoteSkillMaxTotalBytes || profile.StorageLayoutVersion != 1 {
		return ErrBusinessSystemPromptBundleInvalid
	}
	for _, raw := range []string{profile.UpstreamRoot, profile.PublicRoot} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || parsed.Path != path.Clean(parsed.Path) {
			return ErrBusinessSystemPromptBundleInvalid
		}
	}
	return nil
}

var remoteSkillStorageSlots = []string{"metadata", "raw_tree", "effective_tree", "raw_prompt", "effective_prompt", "prompt_diff"}

func planRemoteSkillStorage(ctx context.Context, candidate RemoteSkillCandidate) (extensionv1.SkillStorageLayoutPlan, error) {
	if !validRemoteSkillSHA256(candidate.Version.EffectiveTreeSHA256) || !validRemoteSkillSHA256(candidate.Prompt.EffectiveSHA256) {
		return extensionv1.SkillStorageLayoutPlan{}, ErrBusinessSystemPromptBundleInvalid
	}
	var plan extensionv1.SkillStorageLayoutPlan
	err := invokePromptManagement(ctx, "skills.storage.plan", extensionv1.SkillStorageLayoutRequest{
		BundleVersionID: candidate.Version.ID, PromptVersionID: candidate.Prompt.ID,
		EffectiveTreeSHA256: candidate.Version.EffectiveTreeSHA256, EffectivePromptSHA256: candidate.Prompt.EffectiveSHA256,
	}, &plan)
	if err != nil {
		return plan, err
	}
	if plan.LayoutVersion != 1 || plan.Namespace != "paired" ||
		plan.CandidateKey != candidate.Version.EffectiveTreeSHA256+"-"+candidate.Prompt.EffectiveSHA256 ||
		len(plan.Slots) != len(remoteSkillStorageSlots) {
		return extensionv1.SkillStorageLayoutPlan{}, ErrBusinessSystemPromptBundleInvalid
	}
	wanted := map[string]bool{}
	for _, slot := range remoteSkillStorageSlots {
		wanted[slot] = true
	}
	for _, slot := range plan.Slots {
		if !wanted[slot] {
			return extensionv1.SkillStorageLayoutPlan{}, ErrBusinessSystemPromptBundleInvalid
		}
		delete(wanted, slot)
	}
	if len(wanted) != 0 {
		return extensionv1.SkillStorageLayoutPlan{}, ErrBusinessSystemPromptBundleInvalid
	}
	return plan, nil
}

func remoteSkillFilePlan(ctx context.Context, name string, data []byte) (extensionv1.SkillFilePlan, error) {
	var plan extensionv1.SkillFilePlan
	err := invokePromptManagement(ctx, "skills.file.plan", extensionv1.SkillFileInspection{Path: name, Prefix: data[:min(2, len(data))], UTF8: utf8.Valid(data)}, &plan)
	if err == nil && (plan.Kind != "text" && plan.Kind != "script" && plan.Kind != "binary" || len(plan.ReplaceFrom) > 2048 || len(plan.ReplaceTo) > 2048) {
		err = ErrBusinessSystemPromptBundleInvalid
	}
	return plan, err
}
