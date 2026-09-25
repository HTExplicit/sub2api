package config

import (
	"fmt"
	"os"
	"strings"
)

const ImageStudioEnabledEnv = "GATEWAY_IMAGE_STUDIO_ENABLED"

// ResolveImageStudioEnabledFromEnvironment resolves the Image Studio flag. An
// empty value is treated as absent because the bundled Compose files inject
// the variable with an empty default.
func ResolveImageStudioEnabledFromEnvironment() (bool, error) {
	enabled, _, err := optionalBooleanEnvironment(ImageStudioEnabledEnv)
	return enabled, err
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
