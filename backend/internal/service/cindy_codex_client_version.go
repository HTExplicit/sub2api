package service

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// CindyCodexMinimumClientVersion is the first stable release whose custom
// provider ModelInfo contract is covered by the pinned Cindy projection.
const CindyCodexMinimumClientVersion = "0.147.0"

func ValidateCindyCodexClientVersion(clientVersion string) error {
	clientVersion = strings.TrimSpace(clientVersion)
	if clientVersion == "" {
		return nil
	}
	canonical := clientVersion
	if !strings.HasPrefix(canonical, "v") {
		canonical = "v" + canonical
	}
	minimum := "v" + CindyCodexMinimumClientVersion
	if !semver.IsValid(canonical) || semver.Canonical(canonical) != canonical {
		return fmt.Errorf("Cindy Codex catalog requires a valid client_version at or above %s", CindyCodexMinimumClientVersion)
	}
	if semver.Compare(canonical, minimum) < 0 {
		return fmt.Errorf("Cindy Codex catalog requires Codex %s or newer", CindyCodexMinimumClientVersion)
	}
	return nil
}
