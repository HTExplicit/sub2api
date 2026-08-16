package service

import (
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	CindyBalanceDetectionEnabledEnv  = "GATEWAY_CINDY_BALANCE_DETECTION_ENABLED"
	CindyCapabilityCatalogEnabledEnv = "GATEWAY_CINDY_CAPABILITY_CATALOG_ENABLED"
	ImageStudioEnabledEnv            = config.ImageStudioEnabledEnv
	// CindyImageStudioEnabledEnv is retained for one release as a fallback
	// when ImageStudioEnabledEnv is not configured.
	CindyImageStudioEnabledEnv = config.LegacyImageStudioEnabledEnv
)

// Cindy rollout flags are immutable process snapshots. Each variable can be
// rolled back independently by changing the container environment and
// rebuilding only Sub2API. Existing balance markers are intentionally not
// cleared when detection is disabled.
var cindyRolloutFeatures = struct {
	balanceDetection  bool
	capabilityCatalog bool
	imageStudio       bool
}{
	balanceDetection:  envBoolWithDefault(CindyBalanceDetectionEnabledEnv, true),
	capabilityCatalog: envBoolWithDefault(CindyCapabilityCatalogEnabledEnv, false),
	imageStudio:       imageStudioEnabledFromEnvironment(),
}

func imageStudioEnabledFromEnvironment() bool {
	enabled, _ := config.ResolveImageStudioEnabledFromEnvironment()
	return enabled
}

func envBoolWithDefault(name string, defaultValue bool) bool {
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultValue
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func CindyBalanceDetectionFeatureEnabled() bool {
	return cindyRolloutFeatures.balanceDetection
}

func CindyCapabilityCatalogFeatureEnabled() bool {
	return cindyRolloutFeatures.capabilityCatalog
}

// ImageStudioFeatureEnabled reports whether the generic Image Studio surface
// is enabled. This release still requires the Cindy capability catalog.
func ImageStudioFeatureEnabled() bool {
	return cindyRolloutFeatures.capabilityCatalog && cindyRolloutFeatures.imageStudio
}

// CindyImageStudioFeatureEnabled is the one-release compatibility name for
// existing Cindy-specific callers.
func CindyImageStudioFeatureEnabled() bool {
	return ImageStudioFeatureEnabled()
}
