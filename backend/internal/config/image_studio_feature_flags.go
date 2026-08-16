package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	ImageStudioEnabledEnv       = "GATEWAY_IMAGE_STUDIO_ENABLED"
	LegacyImageStudioEnabledEnv = "GATEWAY_CINDY_IMAGE_STUDIO_ENABLED"
)

// ResolveImageStudioEnabledFromEnvironment resolves the generic Image Studio
// flag and its one-release legacy fallback. Empty values are treated as absent
// because the bundled Compose files inject both variables with empty defaults.
func ResolveImageStudioEnabledFromEnvironment() (bool, error) {
	primary, primaryConfigured, err := optionalBooleanEnvironment(ImageStudioEnabledEnv)
	if err != nil {
		return false, err
	}
	legacy, legacyConfigured, err := optionalBooleanEnvironment(LegacyImageStudioEnabledEnv)
	if err != nil {
		return false, err
	}

	if primaryConfigured && legacyConfigured && primary != legacy {
		return false, fmt.Errorf(
			"%s conflicts with deprecated %s",
			ImageStudioEnabledEnv,
			LegacyImageStudioEnabledEnv,
		)
	}
	if primaryConfigured {
		return primary, nil
	}
	if legacyConfigured {
		return legacy, nil
	}
	return false, nil
}

func optionalBooleanEnvironment(name string) (value bool, configured bool, err error) {
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return false, false, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true, nil
	case "0", "false", "no", "off":
		return false, true, nil
	default:
		return false, true, fmt.Errorf("%s must be a boolean", name)
	}
}
