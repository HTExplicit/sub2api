// Package testextensions installs actual independent modules as deterministic
// contract fixtures. It is imported only by host tests; production composes
// signed plugin processes through ProvidePluginManager.
package testextensions

import (
	"context"
	"encoding/json"
	"strings"

	cindy "github.com/HTExplicit/sub2api-plugins/cindyprovider/catalog"
	codexprofile "github.com/HTExplicit/sub2api-plugins/codexruntime/profile"
	codexrecovery "github.com/HTExplicit/sub2api-plugins/codexruntime/recovery"

	accounttools "github.com/HTExplicit/sub2api-plugins/accounttools/policy"
	observability "github.com/HTExplicit/sub2api-plugins/adminobservability/policy"
	imagetools "github.com/HTExplicit/sub2api-plugins/imagetools/policy"
	prompt "github.com/HTExplicit/sub2api-plugins/promptskills/policy"
	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type operations struct{ imageConfig *extensionv1.ImageToolsConfig }

var promptFixtureModule = prompt.New()

func (fixture operations) InvokeOperation(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Capability == extensionv1.CapabilityRecovery {
		return codexrecovery.Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "codex.identity.") {
		return codexprofile.Invoke(ctx, in)
	}
	if in.Operation == "codex.transport.plan" {
		var query extensionv1.CodexTransportQuery
		if err := json.Unmarshal(in.Payload, &query); err != nil {
			return extensionv1.Result{}, err
		}
		raw, err := json.Marshal(codexprofile.TransportPlan(query, false))
		return extensionv1.Result{Payload: raw}, err
	}
	if strings.HasPrefix(in.Operation, "observability.") {
		return observability.New().Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "image.") {
		module := imagetools.New()
		config := service.LegacyImageToolsConfig()
		if fixture.imageConfig != nil {
			config = *fixture.imageConfig
		}
		raw, _ := json.Marshal(config)
		if err := module.ApplyConfig(ctx, raw); err != nil {
			return extensionv1.Result{}, err
		}
		return module.Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "cindy.") {
		module := cindy.New()
		raw, _ := json.Marshal(service.LegacyCindyProviderConfig())
		if err := module.ApplyConfig(ctx, raw); err != nil {
			return extensionv1.Result{}, err
		}
		return module.Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "taxonomy.") || strings.HasPrefix(in.Operation, "test.") || strings.HasPrefix(in.Operation, "import.") || in.Operation == "tools.describe" {
		return accounttools.New().Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "prompt.") || strings.HasPrefix(in.Operation, "skills.") {
		return promptFixtureModule.Invoke(ctx, in)
	}
	return extensionv1.Result{}, service.ErrExtensionOperationDisabled
}
func Install() { service.ConfigureProcessExtensionServices(nil, operations{}) }

func InstallImageTools(config extensionv1.ImageToolsConfig) {
	service.ConfigureProcessExtensionServices(nil, operations{imageConfig: &config})
}
