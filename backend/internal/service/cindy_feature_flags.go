package service

import (
	"os"
	"strings"
)

const (
	CindyBalanceDetectionEnabledEnv  = "GATEWAY_CINDY_BALANCE_DETECTION_ENABLED"
	CindyCapabilityCatalogEnabledEnv = "GATEWAY_CINDY_CAPABILITY_CATALOG_ENABLED"
	CindyImageStudioEnabledEnv       = "GATEWAY_CINDY_IMAGE_STUDIO_ENABLED"
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
	imageStudio:       envBoolWithDefault(CindyImageStudioEnabledEnv, false),
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

func CindyImageStudioFeatureEnabled() bool {
	return cindyRolloutFeatures.capabilityCatalog && cindyRolloutFeatures.imageStudio
}
