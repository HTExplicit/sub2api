package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/HTExplicit/sub2api-plugins/promptskills/registry"
	"github.com/HTExplicit/sub2api-plugins/promptskills/source"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
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

func validateBusinessSystemPromptBodyWithLimit(body string, limit int) (string, int, error) {
	return extensionv1.ValidateTextDocument(body, limit)
}
func Plan(snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget, hasInstructions bool) (BusinessSystemPromptApplication, error) {
	bundleManifestSHA256 := snapshot.BundleManifestSHA256
	if snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
		bundleManifestSHA256 = snapshot.RegistryEffectiveTreeSHA256
	}
	application := BusinessSystemPromptApplication{
		PreserveInstructionsEcho:    snapshot.ExposeServerPrompt || snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid,
		ExposeServerPrompt:          snapshot.ExposeServerPrompt,
		CompactEnabled:              snapshot.CompactEnabled,
		TemplateID:                  snapshot.TemplateID,
		VersionID:                   snapshot.VersionID,
		TemplateVersion:             snapshot.TemplateVersion,
		Revision:                    snapshot.Revision,
		SHA256:                      strings.ToLower(strings.TrimSpace(snapshot.SHA256)),
		BaseSHA256:                  strings.ToLower(strings.TrimSpace(snapshot.BaseSHA256)),
		EffectiveSHA256:             strings.ToLower(strings.TrimSpace(snapshot.EffectiveSHA256)),
		EffectiveByteLength:         snapshot.EffectiveByteLength,
		CompositionMode:             snapshot.CompositionMode,
		BundleID:                    snapshot.BundleID,
		BundleManifestSHA256:        strings.ToLower(strings.TrimSpace(bundleManifestSHA256)),
		BundleRevision:              snapshot.RegistryRevision,
		BundleRawTreeSHA256:         strings.ToLower(strings.TrimSpace(snapshot.RegistryRawTreeSHA256)),
		BundleEffectiveTreeSHA256:   strings.ToLower(strings.TrimSpace(snapshot.RegistryEffectiveTreeSHA256)),
		BundlePromptRawSHA256:       strings.ToLower(strings.TrimSpace(snapshot.RegistryPromptRawSHA256)),
		BundlePromptEffectiveSHA256: strings.ToLower(strings.TrimSpace(snapshot.RegistryPromptEffectiveSHA256)),
		BundleUpstreamSourceID:      snapshot.RegistryUpstreamSourceID,
		BundleUpstreamRoot:          snapshot.RegistryUpstreamRoot,
		BundlePublicRoot:            snapshot.RegistryPublicRoot,
		Degraded:                    snapshot.Degraded,
	}
	if !snapshot.Enabled || target.Platform != PlatformOpenAI || (target.Compact && !snapshot.CompactEnabled) {
		return application, nil
	}
	maxBytes := BusinessSystemPromptMaxBytes
	if snapshot.EffectiveSHA256 != "" {
		maxBytes = BusinessSystemPromptBundleMaxBytes
	}
	hash, byteLength, err := validateBusinessSystemPromptBodyWithLimit(snapshot.Body, maxBytes)
	if err != nil {
		return application, fmt.Errorf("%w: %v", ErrBusinessSystemPromptUnavailable, err)
	}
	expectedHash := snapshot.SHA256
	expectedLength := snapshot.ByteLength
	if snapshot.EffectiveSHA256 != "" {
		expectedHash = snapshot.EffectiveSHA256
		expectedLength = snapshot.EffectiveByteLength
	}
	if expectedHash != "" && !strings.EqualFold(expectedHash, hash) {
		return application, fmt.Errorf("%w: snapshot hash mismatch", ErrBusinessSystemPromptUnavailable)
	}
	if expectedLength > 0 && expectedLength != byteLength {
		return application, fmt.Errorf("%w: snapshot length mismatch", ErrBusinessSystemPromptUnavailable)
	}

	application.Applied = true
	application.ServerInstructions = strings.TrimSpace(snapshot.Body)
	application.SHA256 = hash
	if application.BaseSHA256 == "" {
		application.BaseSHA256 = hash
	}
	if application.EffectiveSHA256 == "" {
		application.EffectiveSHA256 = hash
	}
	if application.EffectiveByteLength == 0 {
		application.EffectiveByteLength = byteLength
	}

	switch target.Protocol {
	case BusinessSystemPromptProtocolResponses:
		application.Carrier = BusinessSystemPromptCarrierInstructions
	case BusinessSystemPromptProtocolChat:
		if hasInstructions {
			application.Carrier = BusinessSystemPromptCarrierInstructions
		} else {
			application.Carrier = BusinessSystemPromptCarrierSystemMessage
		}
	default:
		return BusinessSystemPromptApplication{}, fmt.Errorf("unsupported business system prompt protocol %q", target.Protocol)
	}
	return application, nil
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
		return invokeManagement(in.Operation, in.Payload)
	}
	var request extensionv1.PromptPlanRequest
	if json.Unmarshal(in.Payload, &request) != nil {
		return extensionv1.Result{}, errors.New("invalid prompt policy request")
	}
	request.Snapshot.BaseSHA256, request.Snapshot.EffectiveSHA256, request.Snapshot.EffectiveByteLength = request.BaseSHA256, request.EffectiveSHA256, request.EffectiveByteLength
	application, err := Plan(request.Snapshot, request.Target, request.HasInstructions)
	if err != nil {
		return extensionv1.Result{Code: "prompt_unavailable", Message: "Configured prompt is unavailable"}, nil
	}
	raw, err := json.Marshal(application)
	return extensionv1.Result{Payload: raw}, err
}
