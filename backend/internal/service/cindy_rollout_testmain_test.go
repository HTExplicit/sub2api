package service

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const cindyFeatureOnTestMainEnv = "SUB2API_CINDY_FEATURE_ON_TEST_MAIN"

// TestMain keeps the ordinary service suite explicit about the feature-on
// behavior it exercises. Rollback/default subprocesses retain their own
// environment so they still cover the production-safe defaults.
func TestMain(m *testing.M) {
	if shouldReexecServiceTestsWithCindyFeatures() {
		os.Exit(reexecServiceTestsWithCindyFeatures())
	}
	os.Exit(m.Run())
}

func shouldReexecServiceTestsWithCindyFeatures() bool {
	if os.Getenv(cindyFeatureOnTestMainEnv) == "1" || cindyTestHelperActive() {
		return false
	}
	for _, name := range []string{
		CindyBalanceDetectionEnabledEnv,
		CindyCapabilityCatalogEnabledEnv,
		CindySearchEnabledEnv,
		ImageStudioEnabledEnv,
		CindyImageStudioEnabledEnv,
		CindyResponsesImageBridgeEnabledEnv,
	} {
		if _, configured := os.LookupEnv(name); configured {
			return false
		}
	}
	return true
}

func cindyTestHelperActive() bool {
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		upperName := strings.ToUpper(name)
		if value != "" && strings.Contains(upperName, "CINDY") && strings.Contains(upperName, "HELPER") {
			return true
		}
	}
	return false
}

func reexecServiceTestsWithCindyFeatures() int {
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(),
		CindyBalanceDetectionEnabledEnv+"=true",
		CindyCapabilityCatalogEnabledEnv+"=true",
		CindySearchEnabledEnv+"=true",
		ImageStudioEnabledEnv+"=true",
		CindyResponsesImageBridgeEnabledEnv+"=true",
		cindyFeatureOnTestMainEnv+"=1",
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
