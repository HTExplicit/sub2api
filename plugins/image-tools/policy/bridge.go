package policy

import (
	"fmt"
	"math"
	"slices"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func planResponsesBridge(in extensionv1.ImageBridgeRequest, config extensionv1.ImageToolsConfig) (extensionv1.ImageBridgePlan, string, string) {
	plan := extensionv1.ImageBridgePlan{}
	resolve := func(model string) (extensionv1.CindyCapability, bool) {
		if target, ok := in.Aliases[model]; ok {
			model = target
		}
		for _, capability := range in.Capabilities {
			if model == capability.PublicID || model == capability.LiveUpstreamID {
				return capability, true
			}
		}
		return extensionv1.CindyCapability{}, false
	}
	available := func(c extensionv1.CindyCapability) bool {
		return c.PublicModel && (in.CatalogEnabled ||
			(c.Kind == extensionv1.CindyModelKindImage && config.StudioEnabled) ||
			(c.PublicID == "gpt-image-2" && config.ResponsesImageEnabled))
	}
	supports := func(c extensionv1.CindyCapability) bool {
		return config.ResponsesImageEnabled && c.PublicModel && c.PublicID == "gpt-image-2" &&
			c.Kind == extensionv1.CindyModelKindImage && slices.Contains(c.VerifiedEndpoints, extensionv1.CindyEndpointImagesGenerate)
	}
	top, known := resolve(in.Model)
	plan.Supported = known && supports(top)
	if in.Stage == "supports" {
		return plan, "", ""
	}
	if in.Stage == "map" {
		if len(in.Tools) > 0 && !config.ResponsesImageEnabled {
			return plan, "image_model_not_found", "Responses image bridge is disabled"
		}
		if known && available(top) {
			plan.Model = top.LiveUpstreamID
		}
		for _, tool := range in.Tools {
			if capability, ok := resolve(tool.Model); ok && available(capability) && capability.Kind == extensionv1.CindyModelKindImage {
				plan.Tools = append(plan.Tools, extensionv1.ImageBridgeModelPlan{Index: tool.Index, Model: capability.LiveUpstreamID})
			}
		}
		return plan, "", ""
	}
	if in.Stage != "validate" {
		return plan, "invalid_bridge_stage", "Invalid image bridge stage"
	}
	modelError := func(model string) (extensionv1.ImageBridgePlan, string, string) {
		return plan, "image_model_not_found", fmt.Sprintf("cindy Responses image tool model is not verified: %q", model)
	}
	if known && top.Kind == extensionv1.CindyModelKindImage && !plan.Supported {
		return modelError(in.Model)
	}
	if plan.Supported {
		if top.Controls == nil || top.Controls.Generation == nil {
			return modelError(in.Model)
		}
		if err := validateBridgeControls("request", top.PublicID, top.Controls.Generation, in.Controls); err != nil {
			return plan, "invalid_image_control", err.Error()
		}
		plan.Model, plan.StripCount = top.PublicID, in.Controls.Count.Present
	}
	for _, tool := range in.Tools {
		if tool.InvalidModel {
			return plan, "invalid_image_control", fmt.Sprintf("tools[%d].model must be a bounded string", tool.Index)
		}
		model := tool.Model
		if model == "" {
			model = "gpt-image-2"
		}
		capability, ok := resolve(model)
		if !ok || !available(capability) || !supports(capability) || capability.Controls == nil || capability.Controls.Generation == nil {
			return modelError(model)
		}
		if err := validateBridgeControls(fmt.Sprintf("tools[%d]", tool.Index), capability.PublicID, capability.Controls.Generation, tool.Controls); err != nil {
			return plan, "invalid_image_control", err.Error()
		}
		plan.Tools = append(plan.Tools, extensionv1.ImageBridgeModelPlan{Index: tool.Index, Model: capability.LiveUpstreamID, StripCount: tool.Controls.Count.Present})
	}
	return plan, "", ""
}

func validateBridgeControls(location, model string, controls *extensionv1.CindyImageRequestControls, facts extensionv1.ImageBridgeControls) error {
	for _, field := range []struct {
		name    string
		value   extensionv1.ImageBridgeValue
		allowed []string
	}{{"size", facts.Size, controls.Sizes}, {"quality", facts.Quality, controls.Qualities}} {
		if field.value.Present && (!field.value.Valid || !imageControlAllows(field.allowed, field.value.Text)) {
			return fmt.Errorf("%s.%s is not verified for model %q", location, field.name, model)
		}
	}
	count := facts.Count
	if count.Present && (!count.Valid || count.Number != math.Trunc(count.Number) || count.Number < 1 || count.Number > float64(controls.MaxOutputCount)) {
		return fmt.Errorf("%s.n must be between 1 and %d for model %q", location, controls.MaxOutputCount, model)
	}
	if len(facts.Present) > 0 {
		return fmt.Errorf("%s.%s is not verified for model %q", location, facts.Present[0], model)
	}
	return nil
}

func imageControlAllows(allowed []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), value) {
			return true
		}
	}
	return false
}
