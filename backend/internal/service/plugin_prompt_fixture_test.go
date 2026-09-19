package service

import (
	"context"
	"encoding/json"
	"strings"

	cindy "github.com/HTExplicit/sub2api-plugins/cindyprovider/catalog"

	accounttools "github.com/HTExplicit/sub2api-plugins/accounttools/policy"
	imagetools "github.com/HTExplicit/sub2api-plugins/imagetools/policy"

	policy "github.com/HTExplicit/sub2api-plugins/promptskills/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type promptPolicyFixture struct{}

func (promptPolicyFixture) InvokeOperation(ctx context.Context, _ string, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if strings.HasPrefix(in.Operation, "image.") {
		module := imagetools.New()
		raw, _ := json.Marshal(LegacyImageToolsConfig())
		if err := module.ApplyConfig(ctx, raw); err != nil {
			return extensionv1.Result{}, err
		}
		return module.Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "cindy.") {
		module := cindy.New()
		raw, _ := json.Marshal(LegacyCindyProviderConfig())
		if err := module.ApplyConfig(ctx, raw); err != nil {
			return extensionv1.Result{}, err
		}
		return module.Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "taxonomy.") || strings.HasPrefix(in.Operation, "test.") || in.Operation == "tools.describe" {
		return accounttools.New().Invoke(ctx, in)
	}
	return policy.New().Invoke(ctx, in)
}
func init() {
	processExtensionOperations.Store(&extensionOperationProvider{invoker: promptPolicyFixture{}})
}
