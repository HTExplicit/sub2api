package service

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const cindyIndependentFlagsHelperEnv = "SUB2API_CINDY_INDEPENDENT_FLAGS_HELPER"

func TestCindySurfaceFlagsAreIndependentAndDefaultOff(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		expected string
	}{
		{
			name:     "defaults",
			expected: "false,false,false,false",
		},
		{
			name: "catalog only",
			env: []string{
				CindyCapabilityCatalogEnabledEnv + "=true",
			},
			expected: "true,false,false,false",
		},
		{
			name: "search only",
			env: []string{
				CindySearchEnabledEnv + "=true",
			},
			expected: "false,true,false,false",
		},
		{
			name: "studio only",
			env: []string{
				ImageStudioEnabledEnv + "=true",
			},
			expected: "false,false,true,false",
		},
		{
			name: "responses image only",
			env: []string{
				CindyResponsesImageBridgeEnabledEnv + "=true",
			},
			expected: "false,false,false,true",
		},
		{
			name: "all enabled",
			env: []string{
				CindyCapabilityCatalogEnabledEnv + "=true",
				CindySearchEnabledEnv + "=true",
				ImageStudioEnabledEnv + "=true",
				CindyResponsesImageBridgeEnabledEnv + "=true",
			},
			expected: "true,true,true,true",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestCindyIndependentFlagsHelper$")
			cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
				CindyCapabilityCatalogEnabledEnv,
				CindySearchEnabledEnv,
				ImageStudioEnabledEnv,
				CindyImageStudioEnabledEnv,
				CindyResponsesImageBridgeEnabledEnv,
				cindyIndependentFlagsHelperEnv,
			), append(test.env, cindyIndependentFlagsHelperEnv+"="+test.expected)...)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated flag check failed: %v\n%s", err, output)
			}
		})
	}
}

func TestCindyIndependentFlagsHelper(t *testing.T) {
	expected := os.Getenv(cindyIndependentFlagsHelperEnv)
	if expected == "" {
		t.Skip("subprocess helper")
	}

	got := []string{
		strings.ToLower(boolString(CindyCapabilityCatalogFeatureEnabled())),
		strings.ToLower(boolString(CindySearchFeatureEnabled())),
		strings.ToLower(boolString(ImageStudioFeatureEnabled())),
		strings.ToLower(boolString(CindyResponsesImageBridgeFeatureEnabled())),
	}
	if strings.Join(got, ",") != expected {
		t.Fatalf("Cindy surface flags = %s, want %s", strings.Join(got, ","), expected)
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
