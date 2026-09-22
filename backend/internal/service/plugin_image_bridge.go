package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func imageBridgeText(value any) (string, bool) {
	text, ok := value.(string)
	if !ok || len(text) > 256 {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func imageBridgeControls(body map[string]any) extensionv1.ImageBridgeControls {
	stringFact := func(key string) extensionv1.ImageBridgeValue {
		value, present := body[key]
		text, valid := imageBridgeText(value)
		return extensionv1.ImageBridgeValue{Present: present, Valid: valid, Text: text}
	}
	facts := extensionv1.ImageBridgeControls{Size: stringFact("size"), Quality: stringFact("quality")}
	value, present := body["n"]
	facts.Count.Present = present
	switch count := value.(type) {
	case float64:
		facts.Count.Number, facts.Count.Valid = count, true
	case json.Number:
		parsed, err := count.Float64()
		facts.Count.Number, facts.Count.Valid = parsed, err == nil
	}
	if math.IsNaN(facts.Count.Number) || math.IsInf(facts.Count.Number, 0) {
		facts.Count.Number, facts.Count.Valid = 0, false
	}
	for _, key := range []string{"background", "output_format", "output_compression", "moderation", "style", "partial_images", "input_fidelity", "mask", "image"} {
		if _, exists := body[key]; exists {
			facts.Present = append(facts.Present, key)
		}
	}
	return facts
}

func imageBridgeFacts(body map[string]any, stage string) (extensionv1.ImageBridgeRequest, error) {
	model, valid := imageBridgeText(body["model"])
	if !valid && body["model"] != nil {
		return extensionv1.ImageBridgeRequest{}, errors.New("model must be a bounded string")
	}
	request := extensionv1.ImageBridgeRequest{Stage: stage, Model: model, Controls: imageBridgeControls(body)}
	tools, _ := body["tools"].([]any)
	for index, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(firstNonEmptyString(tool["type"])) != "image_generation" {
			continue
		}
		if len(request.Tools) >= 256 {
			return request, errors.New("too many image generation tools")
		}
		model, valid := imageBridgeText(tool["model"])
		request.Tools = append(request.Tools, extensionv1.ImageBridgeTool{Index: index, Model: model, InvalidModel: !valid && tool["model"] != nil, Controls: imageBridgeControls(tool)})
	}
	return request, nil
}

func queryImageBridge(ctx context.Context, account *Account, request extensionv1.ImageBridgeRequest) (extensionv1.ImageBridgePlan, error) {
	snapshot, err := LoadCindyCatalogSnapshot(ctx, account)
	if err != nil {
		return extensionv1.ImageBridgePlan{}, err
	}
	return queryImageBridgeWithSnapshot(ctx, account, request, snapshot)
}

func queryImageBridgeWithSnapshot(ctx context.Context, account *Account, request extensionv1.ImageBridgeRequest, snapshot *CindyCatalogSnapshot) (extensionv1.ImageBridgePlan, error) {
	request.Capabilities = snapshot.Capabilities
	request.Aliases = snapshot.CompatibilityAliases
	request.CatalogEnabled = snapshot.Config.CatalogEnabled
	raw, err := json.Marshal(request)
	var plan extensionv1.ImageBridgePlan
	if err != nil {
		return plan, err
	}
	in := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "image.responses.plan", Payload: raw}
	if account != nil {
		in.AccountID = account.ID
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformCindy, AccountTypeAPIKey, in)
	if err != nil {
		return plan, err
	}
	if result.Code == "image_model_not_found" {
		return plan, fmt.Errorf("%w: %s", ErrCindyResponsesImageToolModelNotFound, result.Message)
	}
	if result.Code != "" {
		return plan, errors.New(result.Message)
	}
	if json.Unmarshal(result.Payload, &plan) != nil || !validImageBridgePlan(request, plan) {
		return plan, ErrExtensionOperationUnavailable
	}
	return plan, nil
}

func validImageBridgePlan(request extensionv1.ImageBridgeRequest, plan extensionv1.ImageBridgePlan) bool {
	validModel := func(original, model string, defaultImage bool) bool {
		if model == "" {
			return true
		}
		if alias, ok := request.Aliases[original]; ok {
			original = alias
		}
		for _, c := range request.Capabilities {
			sameIdentity := original == c.PublicID || original == c.LiveUpstreamID || (defaultImage && original == "" && c.Kind == CindyModelKindImage)
			if sameIdentity && c.PublicModel && (model == c.PublicID || model == c.LiveUpstreamID) {
				return true
			}
		}
		return false
	}
	if !validModel(request.Model, plan.Model, false) || (plan.StripCount && !request.Controls.Count.Present) {
		return false
	}
	targets := make(map[int]extensionv1.ImageBridgeTool, len(request.Tools))
	for _, tool := range request.Tools {
		targets[tool.Index] = tool
	}
	for _, change := range plan.Tools {
		tool, ok := targets[change.Index]
		if !ok || change.Model == "" || !validModel(tool.Model, change.Model, true) || (change.StripCount && !tool.Controls.Count.Present) {
			return false
		}
		delete(targets, change.Index)
	}
	return true
}

func applyImageBridgePlan(body map[string]any, plan extensionv1.ImageBridgePlan) (bool, error) {
	if body == nil {
		return false, ErrExtensionOperationUnavailable
	}
	tools, _ := body["tools"].([]any)
	targets := make([]map[string]any, len(plan.Tools))
	for index, change := range plan.Tools {
		if change.Index < 0 || change.Index >= len(tools) {
			return false, ErrExtensionOperationUnavailable
		}
		target, ok := tools[change.Index].(map[string]any)
		if !ok || target == nil {
			return false, ErrExtensionOperationUnavailable
		}
		targets[index] = target
	}
	changed := false
	apply := func(target map[string]any, model string, stripCount bool) {
		if model != "" && target["model"] != model {
			target["model"], changed = model, true
		}
		if stripCount {
			if _, exists := target["n"]; exists {
				delete(target, "n")
				changed = true
			}
		}
	}
	apply(body, plan.Model, plan.StripCount)
	for index, change := range plan.Tools {
		apply(targets[index], change.Model, change.StripCount)
	}
	return changed, nil
}

func CindyModelSupportsResponsesImageBridge(model string) bool {
	if len(model) > 256 {
		return false
	}
	plan, err := queryImageBridge(context.Background(), nil, extensionv1.ImageBridgeRequest{Stage: "supports", Model: strings.TrimSpace(model)})
	return err == nil && plan.Supported
}

// This is a read-only preselection decision. Support and its distinct text
// controller come from the same Cindy snapshot; a test-default update does
// not enable or retarget the image bridge by itself.
func CindyResponsesImageRoutingModel(ctx context.Context, model string) (string, bool) {
	if len(model) > 256 {
		return "", false
	}
	snapshot, err := LoadCindyCatalogSnapshot(ctx, nil)
	if err != nil || !snapshot.Images.ResponsesImageEnabled {
		return "", false
	}
	plan, err := queryImageBridgeWithSnapshot(ctx, nil, extensionv1.ImageBridgeRequest{Stage: "supports", Model: strings.TrimSpace(model)}, snapshot)
	if err != nil || !plan.Supported {
		return "", false
	}
	return snapshot.ResponsesImageController.PublicID, true
}

func mapCindyOpenAIResponsesImageModels(ctx context.Context, body map[string]any, account *Account) (bool, error) {
	if len(body) == 0 || account == nil || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return false, nil
	}
	request, err := imageBridgeFacts(body, "map")
	if err != nil {
		return false, err
	}
	plan, err := queryImageBridge(ctx, account, request)
	if err != nil {
		return false, err
	}
	return applyImageBridgePlan(body, plan)
}

// ResolveCindyResponsesImageTools is retained for preselection callers. The
// selected-account path repeats policy with the host-authorized account ID.
func ResolveCindyResponsesImageTools(body []byte) ([]byte, error) {
	return ResolveCindyResponsesImageToolsForAccount(context.Background(), nil, body)
}

func ResolveCindyResponsesImageToolsForAccount(ctx context.Context, account *Account, body []byte) ([]byte, error) {
	var requestBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &requestBody); err != nil {
		return nil, fmt.Errorf("decode Responses image tools: %w", err)
	}
	request, err := imageBridgeFacts(requestBody, "validate")
	if err != nil {
		return nil, err
	}
	// Ordinary text requests do not acquire an image capability dependency.
	if len(request.Tools) == 0 {
		capability, known := resolveKnownCindyCapability(request.Model)
		if !known || capability.Kind != CindyModelKindImage {
			return body, nil
		}
	}
	plan, err := queryImageBridge(ctx, account, request)
	if err != nil {
		return nil, err
	}
	changed, err := applyImageBridgePlan(requestBody, plan)
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(requestBody)
}
