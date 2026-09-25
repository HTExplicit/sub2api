package service

import (
	"slices"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// officialCatalogQuery carries the real upstream model spellings and the
// account facts that product-scoped entries (Qwen Bailian, Doubao Ark, Kimi
// Coding) match on.
type officialCatalogQuery struct {
	Candidates        []string
	Platform          string
	AccountMode       string
	Scheme            string
	Host              string
	Port              string
	Path              string
	HasURLCredentials bool
}

// lookupOfficialModelCatalog matches candidates in order against the
// release-pinned reference catalog. Product scoping keeps a self-hosted
// look-alike ID from inheriting a vendor product's capacity; two applicable
// entries with different limits are ambiguous and match nothing.
func lookupOfficialModelCatalog(query officialCatalogQuery) *OfficialModelContextCapacity {
	if len(query.Candidates) == 0 || len(query.Candidates) > 16 {
		return nil
	}
	for _, candidate := range query.Candidates {
		if len(candidate) > 512 || strings.TrimSpace(candidate) == "" {
			return nil
		}
		key := modelReferenceKey(candidate)
		var found *OfficialModelContextCapacity
		for _, entry := range officialModelContextCapacityCatalog {
			if !officialCatalogEntryMatches(entry, key) || !officialCatalogApplies(query, entry) {
				continue
			}
			if !validOfficialCatalogCapacity(entry.ContextWindow) && !validOfficialCatalogCapacity(entry.MaxInputTokens) && !validOfficialCatalogCapacity(entry.MaxContextWindow) {
				continue
			}
			if found != nil && found.ModelContextCapacity != entry.ModelContextCapacity {
				return nil
			}
			copy := cloneOfficialModelContextCapacity(entry)
			found = &copy
		}
		if found != nil {
			return found
		}
	}
	return nil
}

func officialCatalogEntryMatches(entry OfficialModelContextCapacity, key string) bool {
	if modelReferenceKey(entry.ModelID) == key {
		return true
	}
	for _, alias := range entry.Aliases {
		if modelReferenceKey(alias) == key {
			return true
		}
	}
	return false
}

// modelReferenceKey is the vendor-neutral comparison key for reference lookups:
// case-insensitive, and a version separator between two digits may be written
// as "." or "-" ("claude-opus-4.6" matches "claude-opus-4-6").
func modelReferenceKey(id string) string {
	key := []byte(strings.ToLower(strings.TrimSpace(id)))
	for i := 1; i+1 < len(key); i++ {
		if key[i] == '.' && isASCIIDigitByte(key[i-1]) && isASCIIDigitByte(key[i+1]) {
			key[i] = '-'
		}
	}
	return string(key)
}

func isASCIIDigitByte(value byte) bool {
	return value >= '0' && value <= '9'
}

func cloneOfficialModelContextCapacity(entry OfficialModelContextCapacity) OfficialModelContextCapacity {
	entry.Aliases = slices.Clone(entry.Aliases)
	entry.SourceURLs = slices.Clone(entry.SourceURLs)
	entry.MatchHosts = slices.Clone(entry.MatchHosts)
	entry.MatchAccountModes = slices.Clone(entry.MatchAccountModes)
	if entry.Reference != nil {
		reference := *entry.Reference
		entry.Reference = &reference
	}
	return entry
}

// OfficialModelCatalogSnapshot returns a deep copy of the reference catalog for
// the admin catalog browser.
func OfficialModelCatalogSnapshot() []OfficialModelContextCapacity {
	result := make([]OfficialModelContextCapacity, len(officialModelContextCapacityCatalog))
	for i, entry := range officialModelContextCapacityCatalog {
		result[i] = cloneOfficialModelContextCapacity(entry)
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

func officialCatalogApplies(query officialCatalogQuery, entry OfficialModelContextCapacity) bool {
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
