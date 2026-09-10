package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// The organizer freezes the inputs behind its human-readable impact. Preview
// must reject a changed input, not silently show that old impact over a newly
// rebuilt plan. Legacy hand-written previews may omit this optional guard; the
// existing frozen-preview CAS remains mandatory for every Apply.
type CapabilityPublicationInputRevisions struct {
	Accounts map[int64]string `json:"accounts"`
	Groups   map[int64]string `json:"groups"`
}

func capabilityPublicationInputDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&normalized) != nil {
		return ""
	}
	raw, err = json.Marshal(capabilityPublicationNormalizeEmptyCollections(normalized))
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// SQL JSON aggregates return [] while ordinary repository list readers use
// nil for an empty collection. This normalization is digest-only and never
// changes the configuration or conflates an explicit zero price with no price.
func capabilityPublicationNormalizeEmptyCollections(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			return nil
		}
		for key, item := range typed {
			typed[key] = capabilityPublicationNormalizeEmptyCollections(item)
		}
	case []any:
		if len(typed) == 0 {
			return nil
		}
		for i, item := range typed {
			typed[i] = capabilityPublicationNormalizeEmptyCollections(item)
		}
	}
	return value
}

func capabilityPublicationPricingRevisionView(prices []ChannelModelPricing) []ChannelModelPricing {
	copy := publicationClone(prices)
	for i := range copy {
		price := &copy[i]
		price.ID, price.ChannelID = 0, 0
		price.CreatedAt, price.UpdatedAt = time.Time{}, time.Time{}
		if price.TimePricing != nil && price.TimePricing.Timezone == "" && !price.TimePricing.WeekdaysOnly && len(price.TimePricing.Periods) == 0 {
			price.TimePricing = nil
		}
		for j := range price.Intervals {
			interval := &price.Intervals[j]
			interval.ID, interval.PricingID = 0, 0
			interval.CreatedAt, interval.UpdatedAt = time.Time{}, time.Time{}
		}
	}
	return copy
}

func capabilityPublicationChannelRevisionView(channel *Channel) any {
	if channel == nil {
		return nil
	}
	copy := channel.Clone()
	copy.normalizeBillingModelSource()
	copy.ModelPricing = capabilityPublicationPricingRevisionView(copy.ModelPricing)
	for i := range copy.AccountStatsPricingRules {
		rule := &copy.AccountStatsPricingRules[i]
		rule.ID, rule.ChannelID = 0, 0
		rule.CreatedAt, rule.UpdatedAt = time.Time{}, time.Time{}
		rule.Pricing = capabilityPublicationPricingRevisionView(rule.Pricing)
	}
	view := publicationChannelFields(copy)
	view["status"] = copy.Status
	return view
}

func CapabilityPublicationAccountInputRevision(account *Account) string {
	if account == nil {
		return ""
	}
	return capabilityPublicationInputDigest(map[string]any{
		"id": account.ID, "name": account.Name, "folder_id": account.ManagementFolderID,
		"platform": account.Platform, "status": account.Status, "schedulable": account.Schedulable,
		"config_fingerprint":   ManagedModelAccountFingerprint(account),
		"publication_revision": capabilityAccountPublicationRevision(account),
	})
}

func CapabilityPublicationGroupInputRevision(group *Group, channel *Channel) string {
	if group == nil {
		return ""
	}
	return capabilityPublicationInputDigest(map[string]any{
		"id": group.ID, "name": group.Name, "platform": group.Platform, "wire_platform": group.WirePlatform,
		"provider_profile": group.ProviderProfile, "is_exclusive": group.IsExclusive,
		"rate_multiplier": group.RateMultiplier, "status": group.Status,
		"model_pricing": capabilityPublicationPricingRevisionView(group.ModelPricing), "long_context_pricing_enabled": group.LongContextPricingEnabled,
		"managed_model_routes": group.ManagedModelRoutes, "model_allowlist": group.ModelAllowlist,
		"messages_dispatch": group.MessagesDispatchModelConfig, "allow_messages_dispatch": group.AllowMessagesDispatch,
		"default_mapped_model": group.DefaultMappedModel, "channel": capabilityPublicationChannelRevisionView(channel),
	})
}

func capabilityPublicationSnapshotAccountRevision(snapshot *CapabilityPublicationAccountSnapshot) string {
	if snapshot == nil || snapshot.Account == nil {
		return ""
	}
	account := *snapshot.Account
	account.GroupIDs = nil
	account.AccountGroups = nil
	for groupID, priority := range snapshot.Bindings {
		account.GroupIDs = append(account.GroupIDs, groupID)
		account.AccountGroups = append(account.AccountGroups, AccountGroup{AccountID: account.ID, GroupID: groupID, Priority: priority})
	}
	return CapabilityPublicationAccountInputRevision(&account)
}

func validateCapabilityPublicationInputRevisions(snapshot *CapabilityPublicationSnapshot) error {
	expected := snapshot.Request.ExpectedConfigRevisions
	if expected == nil {
		return nil
	}
	if len(expected.Accounts) != len(snapshot.Request.Scope.AccountIDs) {
		return ErrCapabilityPublicationConflict
	}
	for _, id := range snapshot.Request.Scope.AccountIDs {
		if len(expected.Accounts[id]) != 64 || expected.Accounts[id] != capabilityPublicationSnapshotAccountRevision(snapshot.Accounts[id]) {
			return ErrCapabilityPublicationConflict
		}
	}
	existingGroups := 0
	for _, input := range snapshot.Request.Groups {
		current := snapshot.Groups[input.ID]
		if current == nil || current.Group == nil {
			return ErrCapabilityPublicationConflict
		}
		if current.IsNew {
			// New group names are already checked and reserved by the locked
			// repository snapshot, including an idempotent reserved-ID retry.
			continue
		}
		existingGroups++
		if len(expected.Groups[input.ID]) != 64 || expected.Groups[input.ID] != CapabilityPublicationGroupInputRevision(current.Group, current.Channel) {
			return ErrCapabilityPublicationConflict
		}
	}
	if len(expected.Groups) != existingGroups {
		return ErrCapabilityPublicationConflict
	}
	return nil
}
