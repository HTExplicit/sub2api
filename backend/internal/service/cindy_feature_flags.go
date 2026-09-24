package service

import (
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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

// Immutable legacy environment snapshots seed plugin configuration once.
// Existing installations subsequently use their persisted plugin settings.
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
	value, _ := currentCindyProviderConfig()
	return value.BalanceDetection
}

func CindyCapabilityCatalogFeatureEnabled() bool {
	value, _ := currentCindyProviderConfig()
	return value.CatalogEnabled
}

func CindySearchFeatureEnabled() bool {
	value, _ := currentCindyProviderConfig()
	return value.SearchEnabled
}

// Legacy environment values are consulted only by initial migration and
// contract fixtures. Runtime policy reads the enabled provider configuration.
func LegacyCindyProviderConfig() extensionv1.CindyProviderConfig {
	return extensionv1.CindyProviderConfig{BalanceDetection: cindyRolloutFeatures.balanceDetection, CatalogEnabled: cindyRolloutFeatures.capabilityCatalog, SearchEnabled: cindyRolloutFeatures.search}
}

func LegacyImageToolsConfig() extensionv1.ImageToolsConfig {
	return extensionv1.ImageToolsConfig{StudioEnabled: cindyRolloutFeatures.imageStudio, ResponsesImageEnabled: cindyRolloutFeatures.responsesImage}
}

// ImageStudioFeatureEnabled is independent from the Cindy catalog rollout.
func ImageStudioFeatureEnabled() bool {
	value, _ := currentImageToolsConfig()
	return value.StudioEnabled
}

// CindyImageStudioFeatureEnabled is the one-release compatibility name for
// existing Cindy-specific callers.
func CindyImageStudioFeatureEnabled() bool {
	return ImageStudioFeatureEnabled()
}

func CindyResponsesImageBridgeFeatureEnabled() bool {
	value, _ := currentImageToolsConfig()
	return value.ResponsesImageEnabled
}
