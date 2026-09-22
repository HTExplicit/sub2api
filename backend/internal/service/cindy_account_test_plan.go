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
	"reflect"
	"strconv"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type CindyAccountTestPlan struct {
	Models         []CindyCatalogModel
	DefaultModelID string
	Namespace      string
}

// A management plan is only for the canonical provider identity. The broader
// runtime-compatible legacy identity must not activate management capabilities.
func LoadCindyAccountTestPlan(ctx context.Context, account *Account) (*CindyAccountTestPlan, error) {
	if account == nil || account.ID <= 0 || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return nil, errors.New("provider: Cindy account test plan identity is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	images, _ := currentImageToolsConfig()
	query, _ := json.Marshal(extensionv1.CindyCatalogQuery{Method: extensionv1.CindyAccountTestPlanMethodV1, Images: images})
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{
		Capability: extensionv1.CapabilityProvider, Operation: "cindy.catalog", AccountID: account.ID, Payload: query,
	})
	if err != nil {
		return nil, fmt.Errorf("provider: Cindy account test plan is unavailable: %w", err)
	}
	if result.PluginID <= 0 || result.Code != "" || len(result.Payload) == 0 || len(result.Payload) > extensionv1.MaxPayloadBytes {
		return nil, errors.New("provider: Cindy account test plan is unavailable")
	}
	var values []json.RawMessage
	if json.Unmarshal(result.Payload, &values) != nil || len(values) != 1 {
		return nil, errors.New("invalid Cindy account test plan envelope")
	}
	var plan extensionv1.CindyAccountTestPlanV1
	decoder := json.NewDecoder(bytes.NewReader(values[0]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(new(any)) != io.EOF || plan.SchemaVersion != 1 || plan.Models == nil {
		return nil, errors.New("invalid Cindy account test plan")
	}
	snapshot, err := newCindyCatalogSnapshot(plan.CatalogSnapshot, result.PluginID, images)
	if err != nil {
		return nil, err
	}
	if err := validateCindyAccountTestProjection(plan, snapshot); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, errors.New("invalid Cindy account test plan encoding")
	}
	digest := sha256.Sum256(canonical)
	return &CindyAccountTestPlan{Models: plan.Models, DefaultModelID: plan.DefaultModelID,
		Namespace: strconv.FormatInt(result.PluginID, 10) + ":" + strconv.FormatInt(account.ID, 10) + ":" + hex.EncodeToString(digest[:])}, nil
}

func validateCindyAccountTestProjection(plan extensionv1.CindyAccountTestPlanV1, snapshot *CindyCatalogSnapshot) error {
	invalid := errors.New("invalid Cindy account test projection")
	byID := make(map[string]CindyCatalogModel, len(snapshot.CatalogModels))
	for _, model := range snapshot.CatalogModels {
		byID[model.ID] = model
	}
	seen := make(map[string]bool, len(plan.Models))
	for _, model := range plan.Models {
		if !boundedCindyCatalogID(model.ID) || seen[model.ID] {
			return invalid
		}
		seen[model.ID] = true
		expected, found := byID[model.ID]
		if model.AliasTarget != "" {
			// Management aliases may remain visible with catalog publication off.
			// Check their facts against the same reply, not a fresh alias lookup.
			expected, found = byID[model.AliasTarget]
			expected.ID, expected.AliasTarget = model.ID, model.AliasTarget
			expected.SourceRevision = snapshot.Metadata.CompatibilityAliasRevision
		}
		if !found || !reflect.DeepEqual(model, expected) {
			return invalid
		}
	}
	if plan.DefaultModelID != "" && !seen[plan.DefaultModelID] {
		return invalid
	}
	return nil
}
