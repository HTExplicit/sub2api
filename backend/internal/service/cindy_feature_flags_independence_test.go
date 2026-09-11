package service

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const cindyIndependentFlagsHelperEnv = "SUB2API_CINDY_INDEPENDENT_FLAGS_HELPER"
const cindySearchMatrixHelperEnv = "SUB2API_CINDY_SEARCH_MATRIX_HELPER"
const cindyFreePoolFlagHelperEnv = "SUB2API_CINDY_FREE_POOL_FLAG_HELPER"

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

func TestCindySearchAvailabilityIsIndependentFromCatalogPublication(t *testing.T) {
	tests := []struct {
		name    string
		catalog string
		search  string
		want    string
	}{
		{name: "both off", catalog: "false", search: "false", want: "false"},
		{name: "catalog only", catalog: "true", search: "false", want: "false"},
		{name: "search only", catalog: "false", search: "true", want: "true"},
		{name: "both on", catalog: "true", search: "true", want: "true"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestCindySearchAvailabilityHelper$")
			cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
				CindyCapabilityCatalogEnabledEnv,
				CindySearchEnabledEnv,
				cindySearchMatrixHelperEnv,
			),
				CindyCapabilityCatalogEnabledEnv+"="+test.catalog,
				CindySearchEnabledEnv+"="+test.search,
				cindySearchMatrixHelperEnv+"="+test.want,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated Search/catalog matrix check failed: %v\n%s", err, output)
			}
		})
	}
}

func TestCindySearchAvailabilityHelper(t *testing.T) {
	want := os.Getenv(cindySearchMatrixHelperEnv)
	if want == "" {
		t.Skip("subprocess helper")
	}
	got := boolString(CindyAlphaSearchModelAvailable("gpt-5.6-luna"))
	if got != want {
		t.Fatalf("Cindy Search availability = %s, want %s", got, want)
	}
	if want == "true" {
		models := CindyManagedCompatibilityModels()
		if len(models) != 9 {
			t.Fatalf("managed Cindy Search model count = %d, want 9", len(models))
		}
		for _, model := range models {
			if !CindyAlphaSearchModelAvailable(model) {
				t.Fatalf("managed Cindy Search model %q is unavailable", model)
			}
		}
		if !CindyAlphaSearchModelAvailable("gpt-5.4-mini") {
			t.Fatal("managed Cindy Search compatibility alias is unavailable")
		}
		if upstream, ok := CindyAlphaSearchUpstreamModel("gpt-5.4-mini"); !ok || upstream != "openai/gpt-5.6-luna" {
			t.Fatalf("managed Cindy Search alias target = %q, available=%v", upstream, ok)
		}
	}
}

func TestCindyFreePoolAllowlistSurvivesCatalogPublicationRollback(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCindyFreePoolAllowlistHelper$")
	cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
		CindyCapabilityCatalogEnabledEnv,
		cindyFreePoolFlagHelperEnv,
	),
		CindyCapabilityCatalogEnabledEnv+"=false",
		cindyFreePoolFlagHelperEnv+"=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated free-pool allowlist check failed: %v\n%s", err, output)
	}
}

func TestCindyFreePoolAllowlistHelper(t *testing.T) {
	if os.Getenv(cindyFreePoolFlagHelperEnv) != "1" {
		t.Skip("subprocess helper")
	}
	if CindyCapabilityCatalogFeatureEnabled() {
		t.Fatal("catalog publication must be disabled in helper")
	}
	if !CindyFreePoolModelSupportsEndpoint("gpt-5.6-luna", CindyEndpointResponses) {
		t.Fatal("free Luna must remain routable by the permanent pool allowlist")
	}
	if !CindyFreePoolModelAllowed("gpt-5.6-luna") {
		t.Fatal("free Luna must remain routable when scheduler capability is unspecified")
	}
	for _, removed := range []string{"gpt-5.6-sol", "openai/gpt-5.6-terra", "gpt-image-2", CindyWebSearchModel, CindyAutoReviewModel} {
		if CindyFreePoolModelSupportsEndpoint(removed, CindyEndpointResponses) {
			t.Fatalf("removed or special model %q bypassed the permanent pool allowlist", removed)
		}
		if CindyFreePoolModelAllowed(removed) {
			t.Fatalf("removed or special model %q bypassed the unspecified-capability allowlist", removed)
		}
	}
	if CindyModelSupportsEndpoint("gpt-5.6-luna", CindyEndpointResponses) {
		t.Fatal("public catalog gate must remain disabled independently")
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
