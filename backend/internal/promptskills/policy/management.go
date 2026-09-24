package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/promptskills/source"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/pmezard/go-difflib/difflib"
)

const skillUpstreamRoot = "https://moxinggang.com/skills/security-research/current"
const skillPublicRoot = "https://codexrip.vip/skills/security-research/current"
const skillRoutingBegin = "<!-- BEGIN  SECURITY-RESEARCH ROUTING -->"
const skillRoutingEnd = "<!-- END  SECURITY-RESEARCH ROUTING -->"
const remoteSkillManagedSource = "remote_skill_registry"

func normalizeComposition(input extensionv1.PromptComposition) (extensionv1.PromptComposition, error) {
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	input.BundleID = strings.TrimSpace(input.BundleID)
	input.BundleManifestSHA256 = strings.ToLower(strings.TrimSpace(input.BundleManifestSHA256))
	if input.Mode == "" {
		input.Mode = "inline"
	}
	switch input.Mode {
	case "inline":
		if input.BundleID != "" || input.BundleManifestSHA256 != "" {
			return input, errors.New("inline composition cannot reference a bundle")
		}
	case BusinessSystemPromptCompositionCodexSkillHybrid:
		if input.BundleID != "codexrip-reverse-skill" {
			return input, errors.New("unknown CodexRip skill bundle")
		}
		if input.BundleManifestSHA256 != "" {
			return input, errors.New("registry-backed composition follows the published registry and cannot pin a manifest")
		}
	default:
		return input, fmt.Errorf("unsupported composition mode %q", input.Mode)
	}
	return input, nil
}

func captureSkillPrompt(raw []byte) (extensionv1.SkillPromptCapture, error) {
	rawHash, _, err := extensionv1.ValidateTextDocument(string(raw), BusinessSystemPromptMaxBytes)
	if err != nil {
		return extensionv1.SkillPromptCapture{}, err
	}
	begin, end := []byte(skillRoutingBegin), []byte(skillRoutingEnd)
	if bytes.Count(raw, begin) != 1 || bytes.Count(raw, end) != 1 {
		return extensionv1.SkillPromptCapture{}, errors.New("prompt routing marker must appear exactly once")
	}
	start, finish := bytes.Index(raw, begin), bytes.Index(raw, end)
	if finish < start+len(begin) {
		return extensionv1.SkillPromptCapture{}, errors.New("prompt routing block is malformed")
	}
	root := []byte(skillUpstreamRoot)
	if bytes.Count(raw, root) != 1 {
		return extensionv1.SkillPromptCapture{}, errors.New("upstream Skill root must appear exactly once")
	}
	position := bytes.Index(raw, root)
	if position < start || position+len(root) > finish+len(end) {
		return extensionv1.SkillPromptCapture{}, errors.New("upstream Skill root must be inside the routing block")
	}
	effective := bytes.Replace(raw, root, []byte(skillPublicRoot), 1)
	effectiveHash, _, err := extensionv1.ValidateTextDocument(string(effective), BusinessSystemPromptMaxBytes)
	if err != nil {
		return extensionv1.SkillPromptCapture{}, err
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(string(raw)), B: difflib.SplitLines(string(effective)), FromFile: "prompt_capture", ToFile: "effective_prompt", Context: 3})
	if err != nil {
		return extensionv1.SkillPromptCapture{}, errors.New("prompt diff failed")
	}
	return extensionv1.SkillPromptCapture{RawBody: bytes.Clone(raw), EffectiveBody: effective, RawSHA256: rawHash, EffectiveSHA256: effectiveHash, Diff: diff}, nil
}

func planTemplate(input extensionv1.PromptTemplatePolicyRequest) (extensionv1.PromptTemplatePolicyPlan, error) {
	out := extensionv1.PromptTemplatePolicyPlan{Slug: strings.TrimSpace(input.Slug)}
	if input.ManagedSource == remoteSkillManagedSource && input.Action != "create" {
		return out, errors.New("source_managed")
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		out.Name = &value
		if value == "" {
			return out, errors.New("name is required")
		}
	}
	if input.Description != nil {
		value := strings.TrimSpace(*input.Description)
		out.Description = &value
	}
	switch input.Action {
	case "create", "duplicate":
		if out.Slug == "" || out.Name == nil {
			return out, errors.New("slug and name are required")
		}
	case "update", "version", "delete", "sync":
	default:
		return out, errors.New("unsupported template operation")
	}
	return out, nil
}

func planPromptPublication(input extensionv1.PromptPublicationPolicyRequest) (extensionv1.PromptPublicationPolicyPlan, error) {
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action != extensionv1.PublicationActionPublish && action != extensionv1.PublicationActionRollback {
		return extensionv1.PromptPublicationPolicyPlan{}, errors.New("unsupported publication action")
	}
	if input.ManagedSource == remoteSkillManagedSource {
		return extensionv1.PromptPublicationPolicyPlan{}, errors.New("source_managed")
	}
	if input.Target.ID > 0 && input.Target.Version <= 0 {
		return extensionv1.PromptPublicationPolicyPlan{}, errors.New("target version is invalid")
	}
	if action == extensionv1.PublicationActionRollback && input.Target.ID > 0 && input.Target.ID == input.CurrentVersionID {
		return extensionv1.PromptPublicationPolicyPlan{}, errors.New("current version cannot be rolled back")
	}
	composition, err := normalizeComposition(extensionv1.PromptComposition{
		Mode:                 input.Target.CompositionMode,
		BundleID:             input.Target.BundleID,
		BundleManifestSHA256: input.Target.BundleManifestSHA256,
	})
	if err != nil {
		return extensionv1.PromptPublicationPolicyPlan{}, err
	}
	return extensionv1.PromptPublicationPolicyPlan{Action: action, Allowed: true, Composition: composition}, nil
}

func invokeManagement(operation string, raw json.RawMessage) (extensionv1.Result, error) {
	var output any
	var err error
	switch operation {
	case "prompt.composition":
		var input extensionv1.PromptComposition
		if json.Unmarshal(raw, &input) != nil {
			return extensionv1.Result{}, errors.New("invalid composition request")
		}
		output, err = normalizeComposition(input)
	case "skills.capture":
		var input struct {
			Body []byte `json:"body"`
		}
		if json.Unmarshal(raw, &input) != nil {
			return extensionv1.Result{}, errors.New("invalid prompt capture request")
		}
		output, err = captureSkillPrompt(input.Body)
	case "prompt.template":
		var input extensionv1.PromptTemplatePolicyRequest
		if json.Unmarshal(raw, &input) != nil {
			return extensionv1.Result{}, errors.New("invalid template policy request")
		}
		output, err = planTemplate(input)
	case "prompt.publication.plan":
		var input extensionv1.PromptPublicationPolicyRequest
		if json.Unmarshal(raw, &input) != nil {
			return extensionv1.Result{}, errors.New("invalid prompt publication request")
		}
		output, err = planPromptPublication(input)
	default:
		return extensionv1.Result{}, errors.New("unsupported prompt management operation")
	}
	if err != nil {
		code := "prompt_invalid"
		if err.Error() == "source_managed" {
			code = "source_managed"
		}
		return extensionv1.Result{Code: code, Message: err.Error(), HTTPStatus: 400}, nil
	}
	encoded, err := json.Marshal(output)
	return extensionv1.Result{Payload: encoded}, err
}

func (m *Module) invokeSource(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	var envelope extensionv1.PromptSourceEnvelope
	var err error
	switch in.Operation {
	case "prompt.source.fetch":
		envelope.Candidate, err = m.source.Fetch(ctx)
		envelope.Body = envelope.Candidate.Body
	case "prompt.source.validate":
		if json.Unmarshal(in.Payload, &envelope) != nil {
			return extensionv1.Result{}, errors.New("invalid source validation request")
		}
		envelope.Candidate.Body = envelope.Body
		err = source.ValidateBusinessSystemPromptSourceCandidate(envelope.Candidate)
	default:
		return extensionv1.Result{}, errors.New("unsupported prompt source operation")
	}
	if err != nil {
		code, status := "source_unavailable", 503
		if errors.Is(err, source.ErrBusinessSystemPromptSourceInvalid) {
			code, status = "source_invalid", 400
		}
		if errors.Is(err, source.ErrBusinessSystemPromptSourceLicenseChanged) {
			code, status = "source_license_changed", 409
		}
		return extensionv1.Result{Code: code, HTTPStatus: status, Message: "Prompt source could not be verified"}, nil
	}
	raw, err := json.Marshal(envelope)
	return extensionv1.Result{Payload: raw}, err
}
