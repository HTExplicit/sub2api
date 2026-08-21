package handler

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const cindyFeatureOnHandlerTestMainEnv = "SUB2API_CINDY_FEATURE_ON_HANDLER_TEST_MAIN"

// TestMain restarts before service package initialization so ordinary handler
// tests explicitly exercise the feature-on rollout. Explicit rollback/default
// subprocess environments are left untouched.
func TestMain(m *testing.M) {
	if shouldReexecHandlerTestsWithCindyFeatures() {
		os.Exit(reexecHandlerTestsWithCindyFeatures())
	}
	os.Exit(m.Run())
}

func shouldReexecHandlerTestsWithCindyFeatures() bool {
	if os.Getenv(cindyFeatureOnHandlerTestMainEnv) == "1" || cindyHandlerTestHelperActive() {
		return false
	}
	for _, name := range []string{
		service.CindyBalanceDetectionEnabledEnv,
		service.CindyCapabilityCatalogEnabledEnv,
		service.CindySearchEnabledEnv,
		service.ImageStudioEnabledEnv,
		service.CindyImageStudioEnabledEnv,
		service.CindyResponsesImageBridgeEnabledEnv,
	} {
		if _, configured := os.LookupEnv(name); configured {
			return false
		}
	}
	return true
}

func cindyHandlerTestHelperActive() bool {
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		upperName := strings.ToUpper(name)
		if value != "" && strings.Contains(upperName, "CINDY") && strings.Contains(upperName, "HELPER") {
			return true
		}
	}
	return false
}

func reexecHandlerTestsWithCindyFeatures() int {
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(),
		service.CindyBalanceDetectionEnabledEnv+"=true",
		service.CindyCapabilityCatalogEnabledEnv+"=true",
		service.CindySearchEnabledEnv+"=true",
		service.ImageStudioEnabledEnv+"=true",
		service.CindyResponsesImageBridgeEnabledEnv+"=true",
		cindyFeatureOnHandlerTestMainEnv+"=1",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if cmd.ProcessState != nil {
			return cmd.ProcessState.ExitCode()
		}
		return 1
	}
	return 0
}
