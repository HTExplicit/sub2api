package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// CindyCatalogSnapshot holds one admitted provider reply. Callers pass this
// value through a complete projection instead of performing another lookup
// for its version, model IDs, descriptors or defaults halfway through.
type CindyCatalogSnapshot struct {
	extensionv1.CindyCatalogSnapshotV1
	PluginID  int64
	Namespace string
	byID      map[string]CindyCapability
}

func (s *CindyCatalogSnapshot) Capability(model string) (CindyCapability, bool) {
	if s == nil {
		return CindyCapability{}, false
	}
	model = strings.TrimSpace(model)
	if target, ok := s.CompatibilityAliases[model]; ok {
		model = target
	}
	value, ok := s.byID[model]
	return value, ok
}

// LoadCindyCatalogSnapshot keeps shared catalog reads account-free, but any
// caller with an actual account must carry its ID through fresh admission.
// There is deliberately no compiled-table or deprecated-constant fallback.
func LoadCindyCatalogSnapshot(ctx context.Context, account *Account) (*CindyCatalogSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	accountID := int64(0)
	if account != nil {
		if account.ID <= 0 || !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			return nil, errors.New("provider: Cindy catalog account identity is unavailable")
		}
		accountID = account.ID
	}
	captured := capturedCindyPolicyFromContext(ctx, account)
	var images extensionv1.ImageToolsConfig
	if captured != nil {
		if captured.catalog == nil {
			return nil, errors.New("captured Cindy catalog is unavailable")
		}
		images = captured.catalog.Images
	} else {
		images, _ = currentImageToolsConfig()
	}
	query, _ := json.Marshal(extensionv1.CindyCatalogQuery{Method: extensionv1.CindyCatalogSnapshotMethodV1, Images: images})
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{
		Capability: extensionv1.CapabilityProvider, Operation: "cindy.catalog", AccountID: accountID, Payload: query,
	})
	if err != nil {
		return nil, fmt.Errorf("provider: Cindy catalog snapshot is unavailable: %w", err)
	}
	if result.Code != "" || len(result.Payload) == 0 || len(result.Payload) > extensionv1.MaxPayloadBytes {
		return nil, errors.New("provider: Cindy catalog snapshot is unavailable")
	}
	if captured != nil && captured.owner != result.PluginID {
		return nil, errors.New("captured Cindy catalog owner changed")
	}
	var values []json.RawMessage
	if json.Unmarshal(result.Payload, &values) != nil || len(values) != 1 {
		return nil, errors.New("invalid Cindy catalog snapshot envelope")
	}
	var snapshot extensionv1.CindyCatalogSnapshotV1
	decoder := json.NewDecoder(bytes.NewReader(values[0]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(new(any)) != io.EOF || snapshot.Images != images {
		return nil, errors.New("invalid Cindy catalog snapshot")
	}
	if captured != nil {
		// Fresh admission above still fences current owner/scope/availability.
		// Validate the supported reply, but never splice its B data into A.
		if _, err := validateCindyCatalogSnapshot(snapshot); err != nil {
			return nil, err
		}
		return captured.catalog, nil
	}
	return newCindyCatalogSnapshot(snapshot, result.PluginID, images)
}

func newCindyCatalogSnapshot(snapshot extensionv1.CindyCatalogSnapshotV1, owner int64, images extensionv1.ImageToolsConfig) (*CindyCatalogSnapshot, error) {
	if snapshot.Images != images {
		return nil, errors.New("provider: Cindy catalog image facts changed")
	}
	index, err := validateCindyCatalogSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return nil, errors.New("invalid Cindy catalog snapshot encoding")
	}
	digest := sha256.Sum256(canonical)
	return &CindyCatalogSnapshot{CindyCatalogSnapshotV1: snapshot, PluginID: owner,
		Namespace: strconv.FormatInt(owner, 10) + ":" + hex.EncodeToString(digest[:]), byID: index}, nil
}

func boundedCindyCatalogID(id string) bool {
	return id != "" && strings.TrimSpace(id) == id && len(id) <= 256 &&
		!strings.Contains(id, "://") && !strings.ContainsAny(id, "\x00\r\n\t")
}

func validateCindyCatalogSnapshot(s extensionv1.CindyCatalogSnapshotV1) (map[string]CindyCapability, error) {
	invalid := func() (map[string]CindyCapability, error) {
		return nil, errors.New("invalid Cindy catalog snapshot contract")
	}
	if s.SchemaVersion != 1 || len(s.Capabilities) == 0 || len(s.Capabilities) > 2048 {
		return invalid()
	}
	for _, value := range []string{s.Metadata.CatalogVersion, s.Metadata.ModelMetadataRevision,
		s.Metadata.CompatibilityAliasRevision, s.Metadata.InventoryRevision} {
		if strings.TrimSpace(value) == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
			return invalid()
		}
	}
	index := make(map[string]CindyCapability, len(s.Capabilities)*2)
	public := make(map[string]CindyCapability, len(s.Capabilities))
	live := make(map[string]bool, len(s.Capabilities))
	ids := make([]string, 0, len(s.Capabilities))
	for _, capability := range s.Capabilities {
		if !boundedCindyCatalogID(capability.PublicID) || !boundedCindyCatalogID(capability.LiveUpstreamID) ||
			len(capability.DisplayName) > 256 || len(capability.Description) > 4096 ||
			capability.MaxInputTokens < 0 || int64(capability.MaxInputTokens) > extensionv1.MaxModelContextTokens ||
			capability.MaxOutputTokens < 0 || int64(capability.MaxOutputTokens) > extensionv1.MaxModelContextTokens ||
			!slices.Contains([]CindyModelKind{CindyModelKindText, CindyModelKindImage, CindyModelKindSpecial}, capability.Kind) {
			return invalid()
		}
		if _, duplicate := public[capability.PublicID]; duplicate || live[capability.LiveUpstreamID] {
			return invalid()
		}
		for _, id := range []string{capability.PublicID, capability.LiveUpstreamID} {
			if previous, exists := index[id]; exists && previous.PublicID != capability.PublicID {
				return invalid()
			}
			index[id] = capability
		}
		public[capability.PublicID], live[capability.LiveUpstreamID] = capability, true
		ids = append(ids, capability.LiveUpstreamID)
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\n") + "\n"))
	if hex.EncodeToString(digest[:]) != s.Metadata.InventorySHA256 {
		return invalid()
	}
	for _, reference := range []extensionv1.CindyModelReference{s.DefaultTestModel, s.ResponsesImageController} {
		capability, ok := public[reference.PublicID]
		if !ok || reference.LiveUpstreamID != capability.LiveUpstreamID || !capability.PublicModel ||
			capability.Kind != CindyModelKindText || !slices.Contains(capability.VerifiedEndpoints, CindyEndpointResponses) {
			return invalid()
		}
	}
	if len(s.CompatibilityAliases) > 2048 || len(s.CatalogModels) != len(public) ||
		len(s.PublicModelIDs) > len(public) || len(s.CodexCapabilities) > len(public) || len(s.ModelCapabilities) > len(public) {
		return invalid()
	}
	for alias, target := range s.CompatibilityAliases {
		capability, ok := public[target]
		if !boundedCindyCatalogID(alias) || !ok || !capability.PublicModel {
			return invalid()
		}
		if existing, exists := index[alias]; exists && existing.PublicID != target {
			return invalid()
		}
	}
	for _, mapping := range []map[string]string{s.AvailableMappings, s.CompatibilityMappings, s.LegacyLiveMappings} {
		if len(mapping) > len(public)*3+len(s.CompatibilityAliases) {
			return invalid()
		}
		for id, target := range mapping {
			lookup := id
			if alias, ok := s.CompatibilityAliases[id]; ok {
				lookup = alias
			}
			capability, ok := index[lookup]
			if !boundedCindyCatalogID(id) || !ok || !capability.PublicModel || target != capability.LiveUpstreamID {
				return invalid()
			}
		}
	}
	seen := map[string]bool{}
	for _, model := range s.CatalogModels {
		capability, ok := public[model.ID]
		if !ok || seen[model.ID] || model.LiveUpstreamID != capability.LiveUpstreamID || model.PublicModel != capability.PublicModel {
			return invalid()
		}
		seen[model.ID] = true
	}
	seen = map[string]bool{}
	for _, id := range s.PublicModelIDs {
		capability, ok := public[id]
		if !ok || seen[id] || !capability.PublicModel || len(capability.VerifiedEndpoints) == 0 {
			return invalid()
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, capability := range s.CodexCapabilities {
		original, ok := public[capability.PublicID]
		left, _ := json.Marshal(capability)
		right, _ := json.Marshal(original)
		if !ok || seen[capability.PublicID] || !original.PublicModel || !bytes.Equal(left, right) {
			return invalid()
		}
		seen[capability.PublicID] = true
	}
	seen = map[string]bool{}
	for _, model := range s.ModelCapabilities {
		capability, ok := public[model.ID]
		if !ok || seen[model.ID] || !capability.PublicModel || model.Kind != capability.Kind || model.Object != "model_capability" {
			return invalid()
		}
		if model.MaxInputTokens != capability.MaxInputTokens || model.MaxOutputTokens != capability.MaxOutputTokens ||
			model.PricingSource != capability.PricingSource || model.ExplicitZeroPrice != capability.ExplicitZeroPrice ||
			!slices.Equal(model.InputModalities, capability.InputModalities) || !slices.Equal(model.OutputModalities, capability.OutputModalities) ||
			!slices.Equal(model.Endpoints, capability.VerifiedEndpoints) || !slices.Equal(model.ClientSurfaces, capability.ClientSurfaces) ||
			!maps.Equal(model.AgentWireProtocols, capability.AgentWireProtocols) {
			return invalid()
		}
		controls, _ := json.Marshal(model.Controls)
		originalControls, _ := json.Marshal(capability.Controls)
		if !bytes.Equal(controls, originalControls) {
			return invalid()
		}
		seen[model.ID] = true
	}
	return index, nil
}

func cindyLegacyLiveModel(ctx context.Context, account *Account, model string) (string, bool, error) {
	if account == nil || !IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return "", false, nil
	}
	snapshot, err := LoadCindyCatalogSnapshot(ctx, account)
	if err != nil {
		return "", false, err
	}
	mapped, ok := snapshot.LegacyLiveMappings[strings.TrimSpace(model)]
	return mapped, ok, nil
}

func cindyAccountMappedModel(snapshot *CindyCatalogSnapshot, account *Account, model string) string {
	for _, target := range snapshot.CompatibilityMappings {
		if model == target {
			return model
		}
	}
	if mapped, ok := snapshot.AvailableMappings[model]; ok {
		return mapped
	}
	mapping := account.GetModelMapping()
	if mapped, ok := mapping[model]; ok {
		return mapped
	}
	if mapped, ok := matchWildcardMappingResult(mapping, model); ok {
		return mapped
	}
	return model
}
