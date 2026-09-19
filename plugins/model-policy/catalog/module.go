package catalog

import (
	"context"
	"encoding/json"
	"errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"slices"
	"strings"
)

const ReferenceRelease = GPTContextCapacityReferenceRelease
const ReferenceSource = gptContextCapacityReferenceSource
const ReferenceVerifiedAt = gptContextCapacityReferenceVerifiedAt

type Module struct{}

func New() *Module { return &Module{} }
func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != 0 {
		return nil, errors.New("model catalog has no configurable fields")
	}
	return json.RawMessage(`{}`), nil
}
func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	_, err := m.ValidateConfig(ctx, raw)
	return err
}
func (m *Module) Status(context.Context) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"entries": len(officialModelContextCapacityCatalog), "reference_release": ReferenceRelease})
}
func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	var value any
	switch {
	case in.Capability == extensionv1.CapabilityCatalog && in.Operation == "resolve":
		var request extensionv1.CatalogQuery
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid model reference query")
		}
		match, err := m.ResolveCatalog(ctx, request)
		if err != nil {
			return extensionv1.Result{}, err
		}
		value = match
	case in.Capability == extensionv1.CapabilityAdmin && in.Operation == "catalog.list":
		value = Snapshot()
	default:
		return extensionv1.Result{}, errors.New("unsupported catalog operation")
	}
	raw, err := json.Marshal(value)
	return extensionv1.Result{Payload: raw}, err
}

func Snapshot() []OfficialModelContextCapacity {
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
func (m *Module) ResolveCatalog(_ context.Context, query extensionv1.CatalogQuery) (extensionv1.CatalogMatch, error) {
	if len(query.Candidates) == 0 || len(query.Candidates) > 16 {
		return extensionv1.CatalogMatch{}, errors.New("invalid model candidates")
	}
	for _, candidate := range query.Candidates {
		if len(candidate) > 256 || strings.TrimSpace(candidate) == "" {
			return extensionv1.CatalogMatch{}, errors.New("invalid model candidate")
		}
		var found *OfficialModelContextCapacity
		for _, entry := range officialModelContextCapacityCatalog {
			if !strings.EqualFold(entry.ModelID, candidate) && !containsFold(entry.Aliases, candidate) {
				continue
			}
			if !applies(query, entry) {
				continue
			}
			if !validCapacity(entry.ContextWindow) && !validCapacity(entry.MaxInputTokens) && !validCapacity(entry.MaxContextWindow) {
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
func validCapacity(value int64) bool { return value > 0 && value <= extensionv1.MaxModelContextTokens }
func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
func applies(query extensionv1.CatalogQuery, entry OfficialModelContextCapacity) bool {
	secure := query.Scheme == "https" && !query.HasURLCredentials && (query.Port == "" || query.Port == "443")
	kimiCoding := secure && strings.EqualFold(query.Host, "api.kimi.com") && (query.Path == "/coding" || strings.HasPrefix(query.Path, "/coding/"))
	if entry.Provider == "kimi" && entry.Product == "coding" && query.Platform != "kimi" && !kimiCoding {
		return false
	}
	if len(entry.MatchHosts) > 0 && (!secure || !containsFold(entry.MatchHosts, query.Host)) {
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
