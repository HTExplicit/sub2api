package catalog

import (
	"sort"
	"strings"
)

// Managed channels retain their stored binding; policy owns the catalog
// projection independently of the public catalog visibility switch.
func (r Registry) managedChannelProjection() (map[string]string, []string) {
	capabilities := r.CindyCapabilities()
	aliases := r.CindyManagedCompatibilityAliases()
	mapping := make(map[string]string, len(capabilities)*2+len(aliases))
	models := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if !capability.PublicModel || len(capability.VerifiedEndpoints) == 0 {
			continue
		}
		mapping[capability.PublicID] = capability.LiveUpstreamID
		mapping[capability.LiveUpstreamID] = capability.LiveUpstreamID
		models = append(models, capability.PublicID)
	}
	for alias, publicID := range aliases {
		if capability, ok := r.resolveKnownCindyCapability(publicID); ok && capability.PublicModel {
			mapping[alias] = capability.LiveUpstreamID
		}
	}
	sort.Strings(models)
	return mapping, models
}

func (r Registry) managedChannelModelAllowed(model string) bool {
	capability, ok := r.resolveKnownCindyCapability(strings.TrimSpace(model))
	return ok && capability.PublicModel && len(capability.VerifiedEndpoints) > 0
}
