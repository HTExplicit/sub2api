// Package testextensions installs actual independent modules as deterministic
// contract fixtures. It is imported only by host tests; production composes
// native modules; the remaining plugin manager serves third-party packages.
package testextensions

import (
	"context"
	"strings"

	accounttools "github.com/Wei-Shaw/sub2api/internal/accounttools/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type operations struct{}

func (operations) InvokeOperation(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if strings.HasPrefix(in.Operation, "taxonomy.") || strings.HasPrefix(in.Operation, "test.") || strings.HasPrefix(in.Operation, "import.") || in.Operation == "tools.describe" {
		return accounttools.New().Invoke(ctx, in)
	}
	return extensionv1.Result{}, service.ErrExtensionOperationDisabled
}
func Install() {
	service.ConfigureNativePolicyOperations(operations{})
	service.ConfigureImageTools(nil)
	service.ConfigureAdminObservability(nil)
}

func InstallImageTools(config extensionv1.ImageToolsConfig) {
	service.ConfigureNativePolicyOperations(operations{})
	service.ConfigureImageTools(&config)
}
