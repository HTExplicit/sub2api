package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/promptskills/registry"
	"github.com/Wei-Shaw/sub2api/internal/promptskills/source"
)

type BusinessSystemPromptSnapshot = extensionv1.BusinessSystemPromptSnapshot
type BusinessSystemPromptTarget = extensionv1.BusinessSystemPromptTarget
type BusinessSystemPromptApplication = extensionv1.BusinessSystemPromptApplication

const (
	BusinessSystemPromptMaxBytes                    = 64 << 10
	BusinessSystemPromptBundleMaxBytes              = 256 << 10
	BusinessSystemPromptProtocolResponses           = "responses"
	BusinessSystemPromptProtocolChat                = "chat"
	BusinessSystemPromptCarrierInstructions         = "instructions"
	BusinessSystemPromptCarrierSystemMessage        = "system_message"
	BusinessSystemPromptCompositionCodexSkillHybrid = "codex_skill_hybrid"
	PlatformOpenAI                                  = "openai"
)

var ErrBusinessSystemPromptUnavailable = errors.New("business system prompt unavailable")

func Plan(snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget, _ bool) (BusinessSystemPromptApplication, error) {
	return PlanRules(snapshot, target)
}

type Module struct {
	registry *registry.Controller
	source   *source.GitHubGPT56PromptSource
}

func New() *Module {
	return &Module{registry: registry.New(), source: source.NewGitHubGPT56PromptSource(nil)}
}
func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != 0 {
		return nil, errors.New("prompt application policy has no additional configuration")
	}
	return json.RawMessage(`{}`), nil
}
func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	_, err := m.ValidateConfig(ctx, raw)
	return err
}
func (m *Module) Status(context.Context) (json.RawMessage, error) {
	if err := registry.Ready(); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"document_limit": BusinessSystemPromptMaxBytes, "compiled_limit": BusinessSystemPromptBundleMaxBytes})
}
func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	if in.Capability == extensionv1.CapabilityAdmin && in.Operation == "prompt.describe" {
		raw, err := m.Status(ctx)
		return extensionv1.Result{Payload: raw}, err
	}
	if in.Capability != extensionv1.CapabilityRequest {
		return extensionv1.Result{}, errors.New("unsupported prompt policy operation")
	}
	if in.Operation == "prompt.availability" {
		if err := registry.Ready(); err != nil {
			return extensionv1.Result{Code: "skills_unavailable", HTTPStatus: 503, Message: "Skill registry is unavailable"}, nil
		}
		return extensionv1.Result{Payload: []byte(`{"ready":true}`)}, nil
	}
	if strings.HasPrefix(in.Operation, "skills.") && in.Operation != "skills.capture" {
		return m.registry.Invoke(ctx, in)
	}
	if in.Operation == "prompt.seeds" {
		raw, err := json.Marshal([]extensionv1.PromptSeed{source.DefaultSeed()})
		return extensionv1.Result{Payload: raw}, err
	}
	if strings.HasPrefix(in.Operation, "prompt.source.") {
		return m.invokeSource(ctx, in)
	}
	if in.Operation != "prompt.plan" {
		if in.Operation == "prompt.rules.validate" {
			var policy extensionv1.PromptRulePolicy
			if json.Unmarshal(in.Payload, &policy) != nil {
				return extensionv1.Result{}, errors.New("invalid prompt rule policy")
			}
			normalized, err := ValidateRulePolicy(policy)
			if err != nil {
				return extensionv1.Result{Code: "prompt_rules_invalid", HTTPStatus: 422, Message: err.Error()}, nil
			}
			raw, err := json.Marshal(normalized)
			return extensionv1.Result{Payload: raw}, err
		}
		return invokeManagement(in.Operation, in.Payload)
	}
	var request extensionv1.PromptPlanRequest
	if json.Unmarshal(in.Payload, &request) != nil {
		return extensionv1.Result{}, errors.New("invalid prompt policy request")
	}
	request.Snapshot.BaseSHA256, request.Snapshot.EffectiveSHA256, request.Snapshot.EffectiveByteLength = request.BaseSHA256, request.EffectiveSHA256, request.EffectiveByteLength
	application, err := Plan(request.Snapshot, request.Target, request.HasInstructions)
	if err != nil {
		if errors.Is(err, ErrPromptDeliveryUnsupported) {
			return extensionv1.Result{Code: "prompt_delivery_unsupported", HTTPStatus: 422, Message: "The selected prompt delivery is not supported by this destination"}, nil
		}
		return extensionv1.Result{Code: "prompt_unavailable", Message: "Configured prompt is unavailable"}, nil
	}
	raw, err := json.Marshal(application)
	return extensionv1.Result{Payload: raw}, err
}
