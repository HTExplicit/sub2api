package service

import (
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	CindyBalanceDetectionEnabledEnv     = "GATEWAY_CINDY_BALANCE_DETECTION_ENABLED"
	CindyCapabilityCatalogEnabledEnv    = "GATEWAY_CINDY_CAPABILITY_CATALOG_ENABLED"
	CindySearchEnabledEnv               = "GATEWAY_CINDY_SEARCH_ENABLED"
	CindyResponsesImageBridgeEnabledEnv = "GATEWAY_CINDY_RESPONSES_IMAGE_BRIDGE_ENABLED"
	ImageStudioEnabledEnv               = config.ImageStudioEnabledEnv
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
	search            bool
	imageStudio       bool
	responsesImage    bool
}{
	balanceDetection:  envBoolWithDefault(CindyBalanceDetectionEnabledEnv, true),
	capabilityCatalog: envBoolWithDefault(CindyCapabilityCatalogEnabledEnv, false),
	search:            envBoolWithDefault(CindySearchEnabledEnv, false),
	imageStudio:       imageStudioEnabledFromEnvironment(),
	responsesImage:    envBoolWithDefault(CindyResponsesImageBridgeEnabledEnv, false),
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

func CindySearchFeatureEnabled() bool {
	return cindyRolloutFeatures.search
}

// ImageStudioFeatureEnabled is independent from the Cindy catalog rollout.
func ImageStudioFeatureEnabled() bool {
	return cindyRolloutFeatures.imageStudio
}

// CindyImageStudioFeatureEnabled is the one-release compatibility name for
// existing Cindy-specific callers.
func CindyImageStudioFeatureEnabled() bool {
	return ImageStudioFeatureEnabled()
}

func CindyResponsesImageBridgeFeatureEnabled() bool {
	return cindyRolloutFeatures.responsesImage
}
