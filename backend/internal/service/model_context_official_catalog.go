package service

import (
	"errors"
	"slices"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// resolveOfficialModelCatalog matches candidates against the release-pinned
// official catalog in process (formerly the model-policy plugin). Product
// scoping keeps a self-hosted look-alike ID from inheriting official capacity.
func resolveOfficialModelCatalog(query extensionv1.CatalogQuery) (extensionv1.CatalogMatch, error) {
	if len(query.Candidates) == 0 || len(query.Candidates) > 16 {
		return extensionv1.CatalogMatch{}, errors.New("invalid model candidates")
	}
	for _, candidate := range query.Candidates {
		// Keep the host's 512-byte model ID bound: an original namespace may
		// precede a short catalog leaf without invalidating the whole query.
		if len(candidate) > 512 || strings.TrimSpace(candidate) == "" {
			return extensionv1.CatalogMatch{}, errors.New("invalid model candidate")
		}
		var found *OfficialModelContextCapacity
		for _, entry := range officialModelContextCapacityCatalog {
			if !strings.EqualFold(entry.ModelID, candidate) && !officialCatalogContainsFold(entry.Aliases, candidate) {
				continue
			}
			if !officialCatalogApplies(query, entry) {
				continue
			}
			if !validOfficialCatalogCapacity(entry.ContextWindow) && !validOfficialCatalogCapacity(entry.MaxInputTokens) && !validOfficialCatalogCapacity(entry.MaxContextWindow) {
				continue
			}
			if found != nil && found.ModelContextCapacity != entry.ModelContextCapacity {
				return extensionv1.CatalogMatch{Matched: true}, nil
			}
			copy := entry
			copy.Aliases = slices.Clone(entry.Aliases)
			copy.SourceURLs = slices.Clone(entry.SourceURLs)
			if entry.Reference != nil {
				reference := *entry.Reference
				copy.Reference = &reference
			}
			found = &copy
		}
		if found != nil {
			return extensionv1.CatalogMatch{Matched: true, Entry: found}, nil
		}
	}
	return extensionv1.CatalogMatch{}, nil
}

// OfficialModelCatalogSnapshot returns a deep copy of the official catalog for
// the admin catalog browser.
func OfficialModelCatalogSnapshot() []OfficialModelContextCapacity {
	result := make([]OfficialModelContextCapacity, len(officialModelContextCapacityCatalog))
	for i, entry := range officialModelContextCapacityCatalog {
		entry.Aliases = slices.Clone(entry.Aliases)
		entry.SourceURLs = slices.Clone(entry.SourceURLs)
		entry.MatchHosts = slices.Clone(entry.MatchHosts)
		entry.MatchAccountModes = slices.Clone(entry.MatchAccountModes)
		if entry.Reference != nil {
			reference := *entry.Reference
			entry.Reference = &reference
		}
		result[i] = entry
	}
	return result
}

func validOfficialCatalogCapacity(value int64) bool {
	return value > 0 && value <= extensionv1.MaxModelContextTokens
}

func officialCatalogContainsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func officialCatalogApplies(query extensionv1.CatalogQuery, entry OfficialModelContextCapacity) bool {
	secure := query.Scheme == "https" && !query.HasURLCredentials && (query.Port == "" || query.Port == "443")
	kimiCoding := secure && strings.EqualFold(query.Host, "api.kimi.com") && (query.Path == "/coding" || strings.HasPrefix(query.Path, "/coding/"))
	if entry.Provider == "kimi" && entry.Product == "coding" && query.Platform != "kimi" && !kimiCoding {
		return false
	}
	if len(entry.MatchHosts) > 0 && (!secure || !officialCatalogContainsFold(entry.MatchHosts, query.Host)) {
		return false
	}
	if len(entry.MatchAccountModes) > 0 {
		mode := query.AccountMode
		if mode == "" && query.Scheme != "" {
			if kimiCoding {
				mode = AccountModeCoding
			} else {
				mode = AccountModePayG
			}
		}
		if !slices.Contains(entry.MatchAccountModes, mode) {
			return false
		}
	}
	return true
}
